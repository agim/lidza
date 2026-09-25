package lidza

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/router"
)

// TestServeTLSIssues obtains a real certificate from a local ACME test
// authority (Pebble, letsencrypt/pebble, with PEBBLE_VA_ALWAYS_VALID=1
// so the domain need not resolve) on LIDZA_PEBBLE_URL (default
// https://localhost:14000/dir) and serves it: the whole path from
// LIDZA_TLS_DOMAINS to a certificate in the handshake. Skipped when no
// Pebble answers.
func TestServeTLSIssues(t *testing.T) {
	directory := envOr("LIDZA_PEBBLE_URL", "https://localhost:14000/dir")
	insecure := &http.Client{Timeout: 10 * time.Second, Transport: pebbleTransport{&http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}} //nolint:gosec // a local test CA
	if res, err := insecure.Get(directory); err != nil {
		t.Skipf("no Pebble at %s: %v", directory, err)
	} else {
		res.Body.Close()
	}
	free := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().String()
	}
	httpsAddr, httpAddr := free(), free()
	cacheDir := t.TempDir()
	t.Setenv(EnvTLSDomains, "app.example.com")
	t.Setenv(EnvTLSAddr, httpsAddr)
	t.Setenv(EnvTLSHTTPAddr, httpAddr)
	t.Setenv(EnvTLSCacheDir, cacheDir)
	t.Setenv(EnvTLSDirectory, directory)
	t.Setenv(EnvTLSEmail, "ops@example.com")
	acmeHTTPClient = insecure
	t.Cleanup(func() { acmeHTTPClient = nil })

	ready := make(chan struct{})
	app := App{Name: "tlsapp", Routes: func(r *router.Router) {
		r.HandleFunc("GET /api/v1/hello", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hello over TLS")) })
	}, OnReady: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, app) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("not ready")
	}

	// The first handshake for the domain makes the manager order the
	// certificate; the server presents it in that same handshake.
	dial := func() *tls.Conn {
		t.Helper()
		var conn *tls.Conn
		var err error
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			conn, err = tls.Dial("tcp", httpsAddr, &tls.Config{ServerName: "app.example.com", InsecureSkipVerify: true}) //nolint:gosec // the test CA's chain
			if err == nil {
				return conn
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("no certificate within a minute: %v", err)
		return nil
	}
	conn := dial()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 || len(certs[0].DNSNames) == 0 || certs[0].DNSNames[0] != "app.example.com" {
		t.Fatalf("certificate: %+v", certs)
	}
	t.Logf("issued by %s for %v, valid until %s", certs[0].Issuer.CommonName, certs[0].DNSNames, certs[0].NotAfter.Format(time.RFC3339))
	conn.Close()

	// A request over it reaches the app.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{ServerName: "app.example.com", InsecureSkipVerify: true}}} //nolint:gosec // as above
	res, err := client.Get("https://" + httpsAddr + "/api/v1/hello")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 64)
	n, _ := res.Body.Read(body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body[:n]) != "hello over TLS" {
		t.Fatalf("request over TLS: %d %q", res.StatusCode, body[:n])
	}
	// The certificate is in the store, for the next start and the other
	// nodes.
	if _, err := os.Stat(filepath.Join(cacheDir, "app.example.com")); err != nil {
		entries, _ := os.ReadDir(cacheDir)
		t.Fatalf("certificate not cached: %v (%v)", err, entries)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// pebbleTransport trusts Pebble's self-signed certificate and adds the
// Location header to its finalize replies: RFC 8555 lets a CA omit it,
// Let's Encrypt sends it, and the ACME client polls the order at that
// URL. Pebble's order URL follows from its finalize URL.
type pebbleTransport struct{ next http.RoundTripper }

func (p pebbleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := p.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if id, ok := strings.CutPrefix(req.URL.Path, "/finalize-order/"); ok && res.Header.Get("Location") == "" {
		res.Header.Set("Location", req.URL.Scheme+"://"+req.URL.Host+"/my-order/"+id)
	}
	return res, nil
}
