package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newAuthority(t *testing.T) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return authority{cert, key, pool}
}
func (a authority) issue(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	cert.Leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
func claimStore() func(context.Context, string, time.Duration) (bool, error) {
	var mu sync.Mutex
	seen := map[string]time.Time{}
	return func(ctx context.Context, key string, ttl time.Duration) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		for k, v := range seen {
			if v.Before(now) {
				delete(seen, k)
			}
		}
		if _, ok := seen[key]; ok {
			return false, nil
		}
		seen[key] = now.Add(ttl)
		return true, nil
	}
}
func allow(_ context.Context, host string) error {
	if host != "public.example" {
		return errors.New("unapproved host")
	}
	return nil
}
func privateServer(t *testing.T, ca authority, cert tls.Certificate, h http.Handler) *httptest.Server {
	t.Helper()
	cfg, err := ServerTLS(cert, ca.pool)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(h)
	s.TLS = cfg
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
func secureProxy(t *testing.T, target string, ca authority, cert tls.Certificate, backendName string) http.Handler {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := ClientTransport(cert, ca.pool, backendName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.URL.Scheme = u.Scheme
		p.Out.URL.Host = u.Host
		if err := Forward(p.Out, p.In); err != nil {
			p.Out.URL.Scheme = "invalid"
		}
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "proxy refused", http.StatusBadGateway)
	}}
}
func publicClient(t *testing.T, ca authority, version uint16) *http.Client {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca.pool, ServerName: "public.example", MinVersion: version, MaxVersion: version}, TLSHandshakeTimeout: time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}
}
func TestRealProtectedGatewayChain(t *testing.T) {
	ca := newAuthority(t)
	edgeCert := ca.issue(t, "edge.internal")
	relayCert := ca.issue(t, "relay.internal")
	appCert := ca.issue(t, "app.internal")
	appVerify, err := Verify(Policy{Peers: []string{Pin(relayCert.Leaf)}, Delegates: []string{Pin(edgeCert.Leaf)}, Authorize: allow, Claim: claimStore()})
	if err != nil {
		t.Fatal(err)
	}
	app := privateServer(t, ca, appCert, appVerify(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e, ok := From(r.Context())
		if !ok || len(e.Gateways) != 2 || e.Gateways[0] != Pin(edgeCert.Leaf) || e.Gateways[1] != Pin(relayCert.Leaf) {
			t.Error("missing authenticated gateway chain")
		}
		if r.TLS == nil || r.TLS.Version != tls.VersionTLS13 {
			t.Error("backend TLS changed")
		}
		if r.Header.Get(Header) != "" {
			t.Error("wire evidence leaked to application")
		}
		if !RequireTLS13(r) {
			http.Error(w, "original TLS 1.3 required", http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	})))
	relayVerify, err := Verify(Policy{Peers: []string{Pin(edgeCert.Leaf)}, Authorize: allow, Claim: claimStore()})
	if err != nil {
		t.Fatal(err)
	}
	relay := privateServer(t, ca, relayCert, relayVerify(secureProxy(t, app.URL, ca, relayCert, "app.internal")))
	edge := httptest.NewUnstartedServer(Edge(allow)(secureProxy(t, relay.URL, ca, edgeCert, "relay.internal")))
	edge.TLS = &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "public.example")}, MinVersion: tls.VersionTLS12}
	edge.StartTLS()
	defer edge.Close()
	for _, version := range []uint16{tls.VersionTLS13, tls.VersionTLS12} {
		req, _ := http.NewRequest("POST", edge.URL+"/protected?x=1", strings.NewReader("private body"))
		req.Host = "public.example"
		req.Header.Set(Header, "forged client value")
		req.Header.Set("X-Forwarded-Proto", "https")
		response, err := publicClient(t, ca, version).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		expected := http.StatusOK
		if version == tls.VersionTLS12 {
			expected = http.StatusForbidden
		}
		if response.StatusCode != expected {
			t.Fatalf("TLS%x status=%d body=%s", version, response.StatusCode, body)
		}
		if version == tls.VersionTLS13 && string(body) != "private body" {
			t.Fatal("body changed")
		}
	}
}
func validWire(t *testing.T) string {
	t.Helper()
	req := httptest.NewRequest("POST", "https://public.example/protected?x=1", nil)
	req.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13, ServerName: "public.example"}
	var wire string
	Edge(allow)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		out := r.Clone(r.Context())
		out.URL.Scheme = "https"
		if err := Forward(out, r); err != nil {
			t.Fatal(err)
		}
		wire = out.Header.Get(Header)
	})).ServeHTTP(httptest.NewRecorder(), req)
	if wire == "" {
		t.Fatal("no wire evidence")
	}
	return wire
}
func changeWire(t *testing.T, wire string, change func(*envelope)) string {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	change(&env)
	data, err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}
func TestAdversarialEvidence(t *testing.T) {
	ca := newAuthority(t)
	peer := ca.issue(t, "edge.internal")
	wire := validWire(t)
	for _, name := range []string{"cleartext", "TLS12hop", "unverifiedCA", "wrongPeer", "wrongAuthority", "wrongSNI", "wrongMethod", "wrongTarget", "stale", "future", "unknownVersion", "unknownTLS", "longChain", "badNonce", "duplicateHeader", "oversize", "unknownField", "extraJSON", "duplicateJSON", "unapprovedDelegate", "replay", "storeFailure"} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "https://public.example/protected?x=1", nil)
			req.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{peer.Leaf}, VerifiedChains: [][]*x509.Certificate{{peer.Leaf, ca.cert}}}
			req.Header.Set(Header, wire)
			claim := claimStore()
			pins := map[string]bool{Pin(peer.Leaf): true}
			switch name {
			case "cleartext":
				req.TLS = nil
			case "TLS12hop":
				req.TLS.Version = tls.VersionTLS12
			case "unverifiedCA":
				req.TLS.VerifiedChains = nil
			case "wrongPeer":
				pins = map[string]bool{}
			case "wrongAuthority":
				req.Host = "attacker.example"
			case "wrongSNI":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.ServerName = "attacker.example" }))
			case "wrongMethod":
				req.Method = "DELETE"
			case "wrongTarget":
				req.URL.RawQuery = "x=2"
			case "stale":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.Issued = time.Now().Add(-time.Minute).Unix() }))
			case "future":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.Issued = time.Now().Add(time.Minute).Unix() }))
			case "unknownVersion":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.Version = 2 }))
			case "unknownTLS":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.TLSVersion = 0 }))
			case "longChain":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.Gateways = []string{"a", "b", "c", "d"} }))
			case "badNonce":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.Nonce = "a" }))
			case "duplicateHeader":
				req.Header.Add(Header, wire)
			case "oversize":
				req.Header.Set(Header, strings.Repeat("a", 4097))
			case "unknownField":
				data, _ := base64.RawURLEncoding.DecodeString(wire)
				data = append(data[:len(data)-1], []byte(`,"unknown":true}`)...)
				req.Header.Set(Header, base64.RawURLEncoding.EncodeToString(data))
			case "extraJSON":
				data, _ := base64.RawURLEncoding.DecodeString(wire)
				req.Header.Set(Header, base64.RawURLEncoding.EncodeToString(append(data, []byte(` {}`)...)))
			case "duplicateJSON":
				data, _ := base64.RawURLEncoding.DecodeString(wire)
				data = append(data[:len(data)-1], []byte(`,"version":1}`)...)
				req.Header.Set(Header, base64.RawURLEncoding.EncodeToString(data))
			case "unapprovedDelegate":
				req.Header.Set(Header, changeWire(t, wire, func(e *envelope) { e.Gateways = []string{Pin(peer.Leaf)} }))
			case "replay":
				if _, err := verify(req, pins, nil, nil, allow, claim, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "storeFailure":
				claim = func(context.Context, string, time.Duration) (bool, error) { return false, errors.New("unavailable") }
			}
			if _, err := verify(req, pins, nil, nil, allow, claim, time.Now()); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
func TestPeerRotationAndPlaintextRefusal(t *testing.T) {
	ca := newAuthority(t)
	old := ca.issue(t, "old.internal")
	next := ca.issue(t, "new.internal")
	backend := ca.issue(t, "backend.internal")
	verifyMiddleware, err := Verify(Policy{Peers: []string{Pin(old.Leaf), Pin(next.Leaf)}, Authorize: allow, Claim: claimStore()})
	if err != nil {
		t.Fatal(err)
	}
	server := privateServer(t, ca, backend, verifyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))
	for _, peer := range []tls.Certificate{old, next, ca.issue(t, "unapproved.internal")} {
		transport, err := ClientTransport(peer, ca.pool, "backend.internal")
		if err != nil {
			t.Fatal(err)
		}
		defer transport.CloseIdleConnections()
		req, _ := http.NewRequest("POST", server.URL+"/protected?x=1", nil)
		req.Host = "public.example"
		req = req.WithContext(context.WithValue(req.Context(), contextKey{}, Evidence{TLSVersion: tls.VersionTLS13, Authority: req.Host, ServerName: req.Host}))
		req.Header.Set(Header, validWire(t))
		resp, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		expected := 204
		if peer.Leaf.DNSNames[0] == "unapproved.internal" {
			expected = 403
		}
		if resp.StatusCode != expected {
			t.Fatal(resp.Status)
		}
		req.URL.Scheme = "http"
		if _, err := transport.RoundTrip(req); err == nil {
			t.Fatal("plaintext accepted")
		}
	}
}

func TestEvidenceCannotComeFromBackendTLSAlone(t *testing.T) {
	r := httptest.NewRequest("POST", "https://public.example/protected", nil)
	r.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13, ServerName: "public.example"}
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set(Header, validWire(t))
	if RequireTLS13(r) {
		t.Fatal("backend TLS/headers accepted as original-client proof")
	}
	out := r.Clone(r.Context())
	if Forward(out, r) == nil {
		t.Fatal("unverified header forwarded")
	}
	if out.Header.Get(Header) != "" {
		t.Fatal("unverified evidence retained")
	}
}
func TestPrivateTransportTrustFailures(t *testing.T) {
	ca := newAuthority(t)
	wrong := newAuthority(t)
	cert := ca.issue(t, "backend.internal")
	client := ca.issue(t, "edge.internal")
	backend := privateServer(t, ca, cert, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	cases := []struct {
		name       string
		cert       tls.Certificate
		roots      *x509.CertPool
		serverName string
	}{
		{"untrusted server CA", client, wrong.pool, "backend.internal"},
		{"wrong server name", client, ca.pool, "wrong.internal"},
		{"untrusted client CA", wrong.issue(t, "edge.internal"), ca.pool, "backend.internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport, err := ClientTransport(tc.cert, tc.roots, tc.serverName)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			resp, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get(backend.URL)
			if err == nil {
				resp.Body.Close()
				t.Fatal("untrusted protected hop accepted")
			}
		})
	}
}

func TestReplaySurvivesGatewayKeyRotation(t *testing.T) {
	ca := newAuthority(t)
	old := ca.issue(t, "old.internal")
	next := ca.issue(t, "new.internal")
	pins := map[string]bool{Pin(old.Leaf): true, Pin(next.Leaf): true}
	claim := claimStore()
	wire := validWire(t)
	request := func(cert tls.Certificate) *http.Request {
		r := httptest.NewRequest("POST", "https://public.example/protected?x=1", nil)
		r.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert.Leaf}, VerifiedChains: [][]*x509.Certificate{{cert.Leaf, ca.cert}}}
		r.Header.Set(Header, wire)
		return r
	}
	if _, err := verify(request(old), pins, nil, nil, allow, claim, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(request(next), pins, nil, nil, allow, claim, time.Now()); err == nil {
		t.Fatal("captured evidence replayed using rotated gateway key")
	}
	delete(pins, Pin(old.Leaf))
	r := request(old)
	r.Header.Set(Header, validWire(t))
	if _, err := verify(r, pins, nil, nil, allow, claim, time.Now()); err == nil {
		t.Fatal("retired key remained authorized")
	}
}
