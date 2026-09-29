package lidza

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// Hostnames beyond LIDZA_TLS_DOMAINS: App.TLSHosts approves them while
// the app runs (a customer's own domain, added and removed from the
// app's data). One policy decides everywhere a certificate is at stake:
// the handshake, the ACME challenges on :80 and :443, and every new
// certificate order, renewals included, so a host the app stops
// approving gets no new certificate and no handshake.
//
// tlsApproveTTL and tlsRefuseTTL bound how long a verdict of the hook is
// reused: a revoked host is refused within tlsApproveTTL. Variables for
// the tests.
var (
	tlsApproveTTL = 5 * time.Minute
	tlsRefuseTTL  = time.Minute
)

const (
	// tlsVerdicts caps the cached verdicts; a flood of handshakes naming
	// random hosts costs at most one hook call per name per minute and
	// this much memory.
	tlsVerdicts = 10000
	// tlsHookTimeout bounds one call of the hook.
	tlsHookTimeout = 5 * time.Second
	// tlsRefusalsKept is how many refusals the admin pages show.
	tlsRefusalsKept = 50
	// tlsRefusalLogs caps refusal log lines per minute; the rest are
	// counted and reported in the next line.
	tlsRefusalLogs = 20
)

// errNotListed refuses a host outside LIDZA_TLS_DOMAINS when the app has
// no TLSHosts hook.
var errNotListed = errors.New("not in " + EnvTLSDomains)

// TLSReporter is what this node's TLS server knows about its hostnames,
// for the admin pages: the listed domains, the hosts App.TLSHosts
// approved, each with the expiry of the certificate last served, and the
// latest refusals. It is provided as a service while the binary serves
// TLS itself.
type TLSReporter interface {
	Snapshot() TLSSnapshot
}

// tlsStatus is the TLSReporter the TLS server keeps.
type tlsStatus struct {
	mu       sync.Mutex
	domains  []string
	expiry   map[string]time.Time
	approved map[string]time.Time
	refused  []TLSRefusal
}

// TLSHost is a hostname with the certificate expiry last seen (zero
// before the first handshake) and, for a host the hook approved, when.
type TLSHost struct {
	Host     string
	Approved time.Time
	Expires  time.Time
}

// TLSRefusal is a hostname refused a certificate, and why.
type TLSRefusal struct {
	Host, Reason string
	At           time.Time
}

// TLSSnapshot is a copy of the status.
type TLSSnapshot struct {
	Domains  []TLSHost
	Approved []TLSHost
	Refused  []TLSRefusal
}

func newTLSStatus(domains []string) *tlsStatus {
	return &tlsStatus{domains: domains, expiry: map[string]time.Time{}, approved: map[string]time.Time{}}
}

// Snapshot copies the status: domains in their listed order, approved
// hosts by name, refusals newest first.
func (s *tlsStatus) Snapshot() TLSSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out TLSSnapshot
	for _, d := range s.domains {
		out.Domains = append(out.Domains, TLSHost{Host: d, Expires: s.expiry[d]})
	}
	for h, at := range s.approved {
		out.Approved = append(out.Approved, TLSHost{Host: h, Approved: at, Expires: s.expiry[h]})
	}
	slices.SortFunc(out.Approved, func(a, b TLSHost) int { return strings.Compare(a.Host, b.Host) })
	for i := len(s.refused) - 1; i >= 0; i-- {
		out.Refused = append(out.Refused, s.refused[i])
	}
	return out
}

func (s *tlsStatus) approve(host string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.approved[host]; !ok && len(s.approved) >= tlsVerdicts {
		return
	}
	s.approved[host] = at
}

func (s *tlsStatus) refuse(host, reason string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.approved, host)
	delete(s.expiry, host)
	s.refused = append(s.refused, TLSRefusal{Host: host, Reason: reason, At: at})
	if len(s.refused) > tlsRefusalsKept {
		s.refused = s.refused[len(s.refused)-tlsRefusalsKept:]
	}
}

func (s *tlsStatus) served(host string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.approved[host]; ok || slices.Contains(s.domains, host) {
		s.expiry[host] = expires
	}
}

// tlsPolicy decides which hostnames get a certificate: the listed
// domains always, any other when the app's hook approves it, the
// verdict cached per node.
type tlsPolicy struct {
	listed   map[string]bool
	hook     func(ctx context.Context, host string) error
	services *Services
	status   *tlsStatus
	log      *slog.Logger
	now      func() time.Time

	mu       sync.Mutex
	verdicts map[string]tlsVerdict
	logSlot  time.Time
	logged   int
	dropped  int
}

type tlsVerdict struct {
	err     error
	expires time.Time
}

func newTLSPolicy(domains []string, hook func(context.Context, string) error, services *Services, log *slog.Logger) *tlsPolicy {
	p := &tlsPolicy{listed: map[string]bool{}, hook: hook, services: services, status: newTLSStatus(domains),
		log: log, now: time.Now, verdicts: map[string]tlsVerdict{}}
	for _, d := range domains {
		p.listed[d] = true
	}
	return p
}

// allow is the autocert HostPolicy: nil for a listed domain or a host
// the hook approves.
func (p *tlsPolicy) allow(ctx context.Context, host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if p.listed[host] {
		return nil
	}
	if p.hook == nil {
		return fmt.Errorf("lidza: %s: %w", host, errNotListed)
	}
	now := p.now()
	p.mu.Lock()
	if v, ok := p.verdicts[host]; ok && now.Before(v.expires) {
		p.mu.Unlock()
		return v.err
	}
	p.mu.Unlock()

	hctx, cancel := context.WithTimeout(WithServices(ctx, p.services), tlsHookTimeout)
	err := p.hook(hctx, host)
	cancel()
	ttl := tlsApproveTTL
	if err != nil {
		err = fmt.Errorf("lidza: %s not approved: %w", host, err)
		ttl = tlsRefuseTTL
	}
	p.mu.Lock()
	if len(p.verdicts) >= tlsVerdicts {
		for h, v := range p.verdicts {
			if !now.Before(v.expires) {
				delete(p.verdicts, h)
			}
		}
		for h := range p.verdicts {
			if len(p.verdicts) < tlsVerdicts {
				break
			}
			delete(p.verdicts, h)
		}
	}
	p.verdicts[host] = tlsVerdict{err: err, expires: now.Add(ttl)}
	p.mu.Unlock()

	if err == nil {
		p.status.approve(host, now)
		p.log.Info("tls: host approved", "host", host)
		return nil
	}
	p.status.refuse(host, errors.Unwrap(err).Error(), now)
	p.logRefusal(host, err, now)
	return err
}

// logRefusal logs at most tlsRefusalLogs refusals a minute: handshakes
// naming random hosts cost a counter, not a log line each.
func (p *tlsPolicy) logRefusal(host string, err error, now time.Time) {
	p.mu.Lock()
	slot := now.Truncate(time.Minute)
	dropped := 0
	if !slot.Equal(p.logSlot) {
		p.logSlot, p.logged, dropped, p.dropped = slot, 0, p.dropped, 0
	}
	if p.logged >= tlsRefusalLogs {
		p.dropped++
		p.mu.Unlock()
		return
	}
	p.logged++
	p.mu.Unlock()
	if dropped > 0 {
		p.log.Warn("tls: host refused", "host", host, "error", err, "unlogged_before", dropped)
		return
	}
	p.log.Warn("tls: host refused", "host", host, "error", err)
}

// getCertificate wraps the manager's: a TLS-ALPN challenge certificate,
// which autocert serves without its host policy, is refused to a host
// the policy refuses; the expiry of what is served goes to the status.
func (p *tlsPolicy) getCertificate(m *autocert.Manager) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acme.ALPNProto {
			if err := p.allow(hello.Context(), hello.ServerName); err != nil {
				return nil, err
			}
		}
		cert, err := m.GetCertificate(hello)
		if err == nil && cert != nil && cert.Leaf != nil {
			p.status.served(strings.TrimSuffix(strings.ToLower(hello.ServerName), "."), cert.Leaf.NotAfter)
		}
		return cert, err
	}
}

// orderGuard sits in the ACME client's transport and refuses a new
// certificate order naming a host the policy refuses, before it leaves
// the node: autocert renews a certificate it holds without asking its
// host policy again, so without this a host the app stopped approving
// would be renewed, and retried every half hour when the challenge
// fails.
type orderGuard struct {
	next  http.RoundTripper
	allow func(context.Context, string) error
}

func (g orderGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && req.Body != nil {
		body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		for _, host := range orderHosts(body) {
			if err := g.allow(req.Context(), host); err != nil {
				return nil, fmt.Errorf("certificate order refused: %w", err)
			}
		}
	}
	return g.next.RoundTrip(req)
}

// orderHosts returns the DNS identifiers of an ACME new-order request
// (a JWS whose payload lists "identifiers"), none for any other request.
func orderHosts(body []byte) []string {
	var jws struct {
		Payload string `json:"payload"`
	}
	if json.Unmarshal(body, &jws) != nil || jws.Payload == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(jws.Payload)
	if err != nil {
		return nil
	}
	var order struct {
		Identifiers []struct{ Type, Value string } `json:"identifiers"`
	}
	if json.Unmarshal(raw, &order) != nil {
		return nil
	}
	var hosts []string
	for _, id := range order.Identifiers {
		if id.Type == "dns" {
			hosts = append(hosts, id.Value)
		}
	}
	return hosts
}
