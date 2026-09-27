package devserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"
)

// The sidecar is asked to render in the request's locale when the build
// has a page per locale, and in none otherwise.
func TestSidecarLocale(t *testing.T) {
	var got map[string]any
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]string{"html": "<!doctype html><html></html>"})
	}))
	defer node.Close()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", node.Listener.Addr().String())
	}}}
	dist := fstest.MapFS{
		".server/index.html":     {Data: []byte(`<html><body><div id="root"></div></body></html>`)},
		".locales/manifest.json": {Data: []byte(`{"default":"en","locales":["en","sq"]}`)},
	}
	render := func(s *Sidecar, header, value string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/about", nil)
		req.Header.Set(header, value)
		rec := httptest.NewRecorder()
		s.Handler(http.NotFoundHandler()).ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("render: %d %s", rec.Code, rec.Body)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Sidecar{dist: dist, log: log, client: client, timeout: time.Second, locales: loadLocales(dist, nil)}
	render(s, "Accept-Language", "sq-AL")
	if got["locale"] != "sq" {
		t.Fatalf("locale sent: %v", got)
	}
	s.locales = loadLocales(dist, func(*http.Request) string { return "en" })
	render(s, "Accept-Language", "sq")
	if got["locale"] != "en" {
		t.Fatalf("WithLocale: %v", got)
	}
	delete(dist, ".locales/manifest.json")
	s.locales = loadLocales(dist, nil)
	render(s, "Accept-Language", "sq")
	if _, ok := got["locale"]; ok {
		t.Fatalf("locale sent without variants: %v", got)
	}
}
