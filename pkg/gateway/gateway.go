// Package gateway verifies original-client TLS evidence from explicitly trusted
// gateways over mutually authenticated TLS. It never modifies Request.TLS.
//
// This is a hop-by-hop trust protocol, not a portable attestation token. Every
// gateway is trusted to report its incoming transport accurately. All hops must
// use TLS 1.3 with certificate verification and explicit peer authorization.
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const Header = "Lidza-Gateway-Transport"
const maxHeader = 4096

var errEvidence = errors.New("gateway transport evidence unavailable or invalid")

type contextKey struct{}

// Evidence describes verified original-client transport, separately from the
// TLS connection of the current hop. Fields are read-only copies.
type Evidence struct {
	TLSVersion uint16
	Authority  string
	ServerName string
	// Gateways is the ordered list of authenticated gateway public-key pins.
	Gateways []string
}

type envelope struct {
	Nonce      string   `json:"nonce"`
	Version    int      `json:"version"`
	TLSVersion uint16   `json:"tls_version"`
	Authority  string   `json:"authority"`
	ServerName string   `json:"server_name"`
	Method     string   `json:"method"`
	Target     string   `json:"target"`
	Issued     int64    `json:"issued"`
	Gateways   []string `json:"gateways"`
}

// Policy authorizes both the immediate gateway and the original authority.
// Pins are SHA-256 SubjectPublicKeyInfo pins from Pin. Keep overlapping old/new
// pins during certificate-key rotation. Authorize must be concurrency-safe and
// return promptly using ctx; nil never authorizes an authority.
type Policy struct {
	// Claim atomically reserves an unused nonce for ttl across all verifier nodes.
	// Return false for a duplicate and an error on store failure; both fail closed.
	// A shared cache Incr with a TTL succeeds only when the result is 1.
	Claim func(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Peers []string
	// Delegates authorizes earlier gateways asserted by the immediate peer.
	// Empty accepts a direct edge only. Maximum four total protected gateways.
	Delegates []string
	// NativeEdges authorizes the native-terminator header format only for listed
	// immediate peer pins, each also present in Peers. Never enable globally.
	NativeEdges []string
	Authorize   func(ctx context.Context, authority string) error
}

// Pin identifies a certificate key independently of certificate renewal.
func Pin(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func validAuthority(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, "/\\@?#% \t\r\n") || strings.ToLower(s) != s {
		return false
	}
	host := s
	if strings.Contains(s, ":") {
		var err error
		host, _, err = net.SplitHostPort(s)
		if err != nil {
			return false
		}
	}
	return host != "" && !strings.ContainsAny(host, "[]")
}
func serverName(authority string) string {
	if h, _, err := net.SplitHostPort(authority); err == nil {
		return h
	}
	return authority
}
func bound(r *http.Request) bool {
	return r != nil && r.URL != nil && r.Method != "" && len(r.Method) <= 32 && len(r.URL.RequestURI()) <= 2048 && validAuthority(r.Host)
}

// From returns evidence installed by Edge or Verify. It never interprets
// forwarding headers or treats backend TLS as proof of original-client TLS.
func From(ctx context.Context) (Evidence, bool) {
	e, ok := ctx.Value(contextKey{}).(Evidence)
	e.Gateways = append([]string(nil), e.Gateways...)
	return e, ok
}

// RequireTLS13 checks verified original-client TLS. Use this after Edge or
// Verify; it deliberately does not fall back to r.TLS (which may be a backend
// connection). Application authentication and replay protection remain required.
func RequireTLS13(r *http.Request) bool {
	e, ok := From(r.Context())
	return ok && e.TLSVersion == tls.VersionTLS13
}

// Edge captures direct TLS at a public terminator, discarding client-supplied
// gateway evidence. SNI must match the canonical authority. This rejects domain
// fronting and cleartext; TLS 1.2 may be represented but fails RequireTLS13.
func Edge(authorize func(context.Context, string) error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.Clone(r.Context())
			stripHeaders(r.Header)
			if !bound(r) || authorize == nil || r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version < tls.VersionTLS12 || r.TLS.ServerName != serverName(r.Host) {
				http.Error(w, "transport not authorized", http.StatusForbidden)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			err := authorize(ctx, r.Host)
			cancel()
			if err != nil {
				http.Error(w, "transport not authorized", http.StatusForbidden)
				return
			}
			e := Evidence{TLSVersion: r.TLS.Version, Authority: r.Host, ServerName: r.TLS.ServerName}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, e)))
		})
	}
}

// Verify accepts the protocol only from a chain-verified, pinned mutual-TLS
// peer. Metadata is bound to this same protected HTTP request (method, target,
// authority), expires after 15 seconds and is never accepted as a bearer token.
// A shared atomic Claim store rejects reuse across connections and replicas.
// TLS record authentication, sequence numbers and fresh session keys prevent
// captured evidence/records being replayed by a network attacker. Go TLS has no
// server-side 0-RTT. Retrying an authenticated HTTP operation remains the app's
// idempotency/nonce responsibility, not a transport authentication guarantee.
func Verify(p Policy) (func(http.Handler) http.Handler, error) {
	if len(p.Peers) == 0 || len(p.Peers) > 16 || p.Authorize == nil || p.Claim == nil || len(p.Delegates) > 16 || len(p.NativeEdges) > 16 {
		return nil, errEvidence
	}
	pins := make(map[string]bool, len(p.Peers))
	for _, pin := range p.Peers {
		b, err := base64.RawURLEncoding.DecodeString(pin)
		if err != nil || len(b) != sha256.Size {
			return nil, errEvidence
		}
		pins[pin] = true
	}
	delegates := make(map[string]bool, len(p.Delegates))
	for _, pin := range p.Delegates {
		b, err := base64.RawURLEncoding.DecodeString(pin)
		if err != nil || len(b) != sha256.Size {
			return nil, errEvidence
		}
		delegates[pin] = true
	}
	native := map[string]bool{}
	for _, pin := range p.NativeEdges {
		if !pins[pin] {
			return nil, errEvidence
		}
		native[pin] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e, err := verify(r, pins, delegates, native, p.Authorize, p.Claim, time.Now())
			if err != nil {
				http.Error(w, "transport not authorized", http.StatusForbidden)
				return
			}
			r = r.Clone(context.WithValue(r.Context(), contextKey{}, e))
			stripHeaders(r.Header)
			next.ServeHTTP(w, r)
		})
	}, nil
}
func verify(r *http.Request, pins map[string]bool, delegates map[string]bool, native map[string]bool, authorize func(context.Context, string) error, claim func(context.Context, string, time.Duration) (bool, error), now time.Time) (Evidence, error) {
	if !bound(r) || r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version != tls.VersionTLS13 || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 || !pins[Pin(r.TLS.PeerCertificates[0])] {
		return Evidence{}, errEvidence
	}
	values := r.Header.Values(Header)
	if len(values) != 1 || len(values[0]) > maxHeader {
		return Evidence{}, errEvidence
	}
	wire := values[0]
	isNative := wire == NativeEdgeHeaders
	if isNative {
		if !native[Pin(r.TLS.PeerCertificates[0])] {
			return Evidence{}, errEvidence
		}
		var err error
		wire, err = nativeEnvelope(r)
		if err != nil {
			return Evidence{}, errEvidence
		}
	}
	data, err := base64.RawURLEncoding.DecodeString(wire)
	if err != nil {
		return Evidence{}, errEvidence
	}
	var env envelope
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if dec.Decode(&env) != nil {
		return Evidence{}, errEvidence
	}
	var extra any
	canonical, err := json.Marshal(env)
	if err != nil || !bytes.Equal(data, canonical) {
		return Evidence{}, errEvidence
	}
	if dec.Decode(&extra) != io.EOF {
		return Evidence{}, errEvidence
	}
	if env.Version != 1 || (env.TLSVersion != tls.VersionTLS12 && env.TLSVersion != tls.VersionTLS13) || env.Authority != r.Host || env.ServerName != serverName(r.Host) || env.Method != r.Method || env.Target != r.URL.RequestURI() || env.Issued < now.Add(-15*time.Second).Unix() || env.Issued > now.Add(time.Second).Unix() || len(env.Gateways) > 3 {
		return Evidence{}, errEvidence
	}
	seen := map[string]bool{Pin(r.TLS.PeerCertificates[0]): true}
	for _, pin := range env.Gateways {
		if !delegates[pin] || seen[pin] {
			return Evidence{}, errEvidence
		}
		seen[pin] = true
		b, err := base64.RawURLEncoding.DecodeString(pin)
		if err != nil || len(b) != sha256.Size {
			return Evidence{}, errEvidence
		}
	}
	nonce, err := base64.RawURLEncoding.DecodeString(env.Nonce)
	if err != nil || (len(nonce) != 24 && !(isNative && len(nonce) == 16)) {
		return Evidence{}, errEvidence
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if authorize(ctx, env.Authority) != nil {
		return Evidence{}, errEvidence
	}
	fresh, err := claim(ctx, "gateway:"+env.Nonce, 30*time.Second)
	if err != nil || !fresh {
		return Evidence{}, errEvidence
	}
	env.Gateways = append(env.Gateways, Pin(r.TLS.PeerCertificates[0]))
	return Evidence{TLSVersion: env.TLSVersion, Authority: env.Authority, ServerName: env.ServerName, Gateways: env.Gateways}, nil
}

// Forward replaces the outbound header using verified context only. Preserve
// method, target and public Host. Use ClientTransport for the outgoing hop;
// plaintext forwarding invalidates the security contract. This function alone
// does not secure a proxy. It must never take evidence from an unverified header.
func Forward(out, in *http.Request) error {
	stripHeaders(out.Header)
	e, ok := From(in.Context())
	if !ok || !bound(out) || out.Host != e.Authority || out.Method != in.Method || out.URL.RequestURI() != in.URL.RequestURI() || len(e.Gateways) > 3 {
		return errEvidence
	}
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data, err := json.Marshal(envelope{Nonce: base64.RawURLEncoding.EncodeToString(nonce), Version: 1, TLSVersion: e.TLSVersion, Authority: e.Authority, ServerName: e.ServerName, Method: out.Method, Target: out.URL.RequestURI(), Issued: time.Now().Unix(), Gateways: e.Gateways})
	if err != nil {
		return err
	}
	out.Header.Set(Header, base64.RawURLEncoding.EncodeToString(data))
	return nil
}

// ServerTLS builds a private TLS 1.3 listener config requiring client certificate
// chain verification. Verify additionally pins authorized gateway keys. Use a
// dedicated listener; do not expose a plaintext fallback for protected protocols.
func ServerTLS(cert tls.Certificate, clients *x509.CertPool) (*tls.Config, error) {
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil || clients == nil {
		return nil, errEvidence
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clients}, nil
}

// ClientTransport verifies the backend name and CA and authenticates the
// gateway. It has no environment proxy and never sends evidence over plaintext.
// CloseIdleConnections when retiring it. Redirects must be disabled by callers.
func ClientTransport(cert tls.Certificate, roots *x509.CertPool, name string) (*Transport, error) {
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil || roots == nil || name == "" {
		return nil, errEvidence
	}
	return &Transport{base: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: name, Certificates: []tls.Certificate{cert}}, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: time.Minute, MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 64}}, nil
}

// ValidateOutgoing is useful in proxy error handling before RoundTrip. A TLS
// transport does not prevent http:// requests by itself, so callers must check.
func ValidateOutgoing(r *http.Request) error {
	if r == nil || r.URL == nil || r.URL.Scheme != "https" || r.URL.User != nil {
		return fmt.Errorf("%w: HTTPS backend required", errEvidence)
	}
	return nil
}

// Transport rejects plaintext and credential-bearing backend URLs before any I/O.
// Its private TLS configuration cannot be weakened after construction.
type Transport struct{ base *http.Transport }

func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := ValidateOutgoing(r); err != nil {
		return nil, err
	}
	if r.Header.Get(Header) != "" {
		e, ok := From(r.Context())
		if !ok || e.Authority != r.Host {
			return nil, errEvidence
		}
	}
	return t.base.RoundTrip(r)
}
func (t *Transport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// Counter is satisfied by the cache pack. Production verifiers must use a
// shared atomic backend, not independent process-local memory stores.
type Counter interface {
	Incr(context.Context, string, int64, time.Duration) (int64, error)
}

// Claims adapts an atomic expiring counter into Policy.Claim. Use a stable
// application-specific prefix shared by all replicas and verifier key rotations.
func Claims(counter Counter, prefix string) func(context.Context, string, time.Duration) (bool, error) {
	return func(ctx context.Context, key string, ttl time.Duration) (bool, error) {
		if counter == nil || prefix == "" {
			return false, errEvidence
		}
		n, err := counter.Incr(ctx, prefix+key, 1, ttl)
		return n == 1, err
	}
}
