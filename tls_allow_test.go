package lidza

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestTLSAllow: a terminator in front asks /tls-allow; the app's
// TLSHosts verdict answers it, cached, recorded for the admin pages, and
// a visitor's request through the proxy finds nothing there.
func TestTLSAllow(t *testing.T) {
	t.Setenv(EnvTLSDomains, "app.example.com")
	var calls atomic.Int32
	b, err := Boot(context.Background(), App{TLSHosts: func(ctx context.Context, host string) error {
		calls.Add(1)
		if host == "shop.customer.example" {
			return nil
		}
		return errors.New("not a customer domain")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	ask := func(domain string, header ...string) int {
		req := httptest.NewRequest("GET", TLSAllowPath+"?domain="+domain, nil)
		if len(header) == 2 {
			req.Header.Set(header[0], header[1])
		}
		rec := httptest.NewRecorder()
		b.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	for domain, want := range map[string]int{
		"app.example.com":        200, // listed
		"Shop.Customer.Example.": 200, // approved, case and root dot ignored
		"evil.example":           404,
		"":                       400,
		"127.0.0.1":              400,
		"a.example:443":          400,
		"*.example.com":          400,
	} {
		if got := ask(domain); got != want {
			t.Errorf("%q: %d, want %d", domain, got, want)
		}
	}
	// The verdicts are cached: asking again calls no hook.
	before := calls.Load()
	ask("shop.customer.example")
	ask("evil.example")
	if calls.Load() != before {
		t.Errorf("the hook was asked again within the cache")
	}
	// Through the proxy, a visitor gets nothing, approved name or not.
	if got := ask("shop.customer.example", "X-Forwarded-For", "203.0.113.9"); got != 404 {
		t.Errorf("forwarded request: %d", got)
	}
	if got := ask("shop.customer.example", "Forwarded", "for=203.0.113.9"); got != 404 {
		t.Errorf("Forwarded request: %d", got)
	}
	// The admin pages see the verdicts though the binary serves no TLS.
	rep, ok := Optional[TLSReporter](WithServices(context.Background(), b.Services))
	if !ok {
		t.Fatal("no TLSReporter")
	}
	snap := rep.Snapshot()
	if len(snap.Approved) != 1 || snap.Approved[0].Host != "shop.customer.example" || len(snap.Refused) == 0 || snap.Refused[0].Host != "evil.example" || len(snap.Domains) != 1 {
		t.Errorf("snapshot: %+v", snap)
	}
}

// TestTLSAllowAbsent: an app without TLSHosts or LIDZA_TLS_DOMAINS
// serves no such path.
func TestTLSAllowAbsent(t *testing.T) {
	t.Setenv(EnvTLSDomains, "")
	b, err := Boot(context.Background(), App{Frontend: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	rec := httptest.NewRecorder()
	b.Handler.ServeHTTP(rec, httptest.NewRequest("GET", TLSAllowPath+"?domain=a.example.com", nil))
	if rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
	if _, ok := Optional[TLSReporter](WithServices(context.Background(), b.Services)); ok {
		t.Fatal("a TLSReporter without hosts")
	}
}
