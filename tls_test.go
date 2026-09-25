package lidza

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/agim/lidza/pkg/router"
)

func TestTLSSettings(t *testing.T) {
	t.Setenv(EnvTLSDomains, "")
	if s, err := tlsFromEnv(); s != nil || err != nil {
		t.Fatalf("unset: %+v %v", s, err)
	}
	t.Setenv(EnvTLSDomains, " App.Example.com, www.app.example.com ,")
	t.Setenv(EnvTLSEmail, "ops@example.com")
	s, err := tlsFromEnv()
	if err != nil || strings.Join(s.domains, ",") != "app.example.com,www.app.example.com" || s.addr != ":443" || s.httpAddr != ":80" || s.email != "ops@example.com" || s.PublicURL() != "https://app.example.com" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"https://app.example.com", "app.example.com:443", "*.example.com", "localhost", " , "} {
		t.Setenv(EnvTLSDomains, bad)
		if _, err := tlsFromEnv(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	t.Setenv(EnvTLSDomains, "app.example.com")
	t.Setenv("APP_URL", "")
	t.Setenv("AUTH_COOKIE_SECURE", "")
	s, _ = tlsFromEnv()
	s.deriveEnv()
	if os.Getenv("APP_URL") != "https://app.example.com" || os.Getenv("AUTH_COOKIE_SECURE") != "true" {
		t.Fatalf("derived: APP_URL=%q AUTH_COOKIE_SECURE=%q", os.Getenv("APP_URL"), os.Getenv("AUTH_COOKIE_SECURE"))
	}
	t.Setenv("APP_URL", "https://other.example.com")
	s.deriveEnv()
	if os.Getenv("APP_URL") != "https://other.example.com" {
		t.Fatal("an explicit APP_URL was overwritten")
	}

	// The certificate store: the db pack's when provided, a directory
	// when named, else a clear error.
	t.Setenv(EnvTLSCacheDir, "")
	s, _ = tlsFromEnv()
	if _, err := s.cache(NewServices()); err == nil || !strings.Contains(err.Error(), "db pack") || !strings.Contains(err.Error(), EnvTLSCacheDir) {
		t.Fatalf("no store: %v", err)
	}
	dir := t.TempDir()
	t.Setenv(EnvTLSCacheDir, dir+"/certs")
	s, _ = tlsFromEnv()
	if c, err := s.cache(NewServices()); err != nil || c != autocert.DirCache(dir+"/certs") {
		t.Fatalf("dir cache: %v %v", c, err)
	}
	svc := NewServices()
	Provide[autocert.Cache](svc, autocert.DirCache("provided"))
	if c, err := s.cache(svc); err != nil || c != autocert.DirCache("provided") {
		t.Fatalf("provided cache: %v %v", c, err)
	}
	t.Setenv(EnvTLSDirectory, "https://acme-staging-v02.api.letsencrypt.org/directory")
	s, _ = tlsFromEnv()
	if m := s.manager(autocert.DirCache(dir)); m.Client == nil || m.Client.DirectoryURL == "" || m.Email != "ops@example.com" {
		t.Fatalf("manager: %+v", m)
	}
}

func TestRedirectToHTTPS(t *testing.T) {
	h := redirectToHTTPS("app.example.com")
	for _, c := range []struct{ host, path, want string }{
		{"app.example.com:80", "/notes?x=1", "https://app.example.com/notes?x=1"},
		{"www.app.example.com", "/", "https://www.app.example.com/"},
		{"", "/a", "https://app.example.com/a"},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://x"+c.path, nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.want {
			t.Errorf("%q %q: %d %s", c.host, c.path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// TestServeTLS starts the app with LIDZA_TLS_DOMAINS on free ports: the
// HTTP side redirects, the HTTPS side accepts a connection and asks for
// a certificate for the domain (issuance itself needs a CA and is not
// exercised), and shutdown is clean.
func TestServeTLS(t *testing.T) {
	// Port 0 on both listeners; the server reports what it bound.
	var httpsAddr, httpAddr string
	bound := make(chan struct{})
	tlsListening = func(https, http string) { httpsAddr, httpAddr = https, http; close(bound) }
	t.Cleanup(func() { tlsListening = nil })
	t.Setenv(EnvTLSDomains, "app.example.com")
	t.Setenv(EnvTLSAddr, "127.0.0.1:0")
	t.Setenv(EnvTLSHTTPAddr, "127.0.0.1:0")
	t.Setenv(EnvTLSCacheDir, t.TempDir())
	t.Setenv(EnvTLSDirectory, "https://127.0.0.1:1/directory") // never reached
	t.Setenv("APP_URL", "")
	ready := make(chan struct{})
	app := App{Name: "tlsapp", Routes: func(*router.Router) {}, OnReady: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, app) }()
	select {
	case <-ready:
		<-bound
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("not ready")
	}
	if os.Getenv("APP_URL") != "https://app.example.com" {
		t.Errorf("APP_URL not derived: %q", os.Getenv("APP_URL"))
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get("http://" + httpAddr + "/notes")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently || !strings.HasPrefix(res.Header.Get("Location"), "https://") {
		t.Fatalf("redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// A handshake for an unknown host is refused by the host policy; one
	// for the domain reaches the manager, which then needs the CA.
	conn, err := tls.Dial("tcp", httpsAddr, &tls.Config{ServerName: "other.example.com", InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
		t.Fatal("handshake for a host outside LIDZA_TLS_DOMAINS succeeded")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("did not stop")
	}
}
