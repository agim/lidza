package devserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/agim/lidza/pkg/middleware"
)

// TestStaticHeadEveryPage: WithHead sets the head of every HTML page
// Static serves (a named .html file, the home page, a prerendered
// directory page, the shell), with its status, escaping, CSP hashes and
// ETag; files that are not pages, and missing HTML files, never call it.
func TestStaticHeadEveryPage(t *testing.T) {
	page := `<!doctype html><html><head><title>t</title><script>window.x=1</script></head><body></body></html>`
	dist := fstest.MapFS{
		"index.html":         {Data: []byte(page)},
		"console.html":       {Data: []byte(page)},
		"docs/index.html":    {Data: []byte(page)},
		".server/index.html": {Data: []byte(page)},
		"assets/app.js":      {Data: []byte("console.log(1)")},
		"assets/app.css":     {Data: []byte("body{}")},
	}
	var calls atomic.Int32
	version := "v1"
	h := Static(dist, WithHead(func(r *http.Request) (Head, bool) {
		calls.Add(1)
		st := 0
		if r.URL.Path == "/gone" {
			st = http.StatusNotFound
		}
		return Head{Status: st, Meta: []Meta{{Name: "app-version", Content: version + `"<x>`}}}, true
	}))
	serve := func(method, path string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if len(header) == 2 {
			req.Header.Set(header[0], header[1])
		}
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Security-Policy", middleware.DefaultCSP)
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, p := range []string{"/console.html", "/", "/docs", "/some/route"} {
		rec := serve("GET", p)
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, `name="app-version" content="v1&#34;&lt;x&gt;"`) {
			t.Errorf("%s: %d, no escaped app-version meta:\n%s", p, rec.Code, body)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, cspHash([]byte("window.x=1"))) {
			t.Errorf("%s: inline script not hashed: %s", p, csp)
		}
	}
	if rec := serve("GET", "/gone"); rec.Code != http.StatusNotFound {
		t.Errorf("hook status: %d", rec.Code)
	}

	// The ETag follows the head: a conditional GET or HEAD revalidates
	// until the hook's output changes.
	first := serve("GET", "/console.html")
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}
	if rec := serve("GET", "/console.html", "If-None-Match", tag); rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET: %d", rec.Code)
	}
	if rec := serve("HEAD", "/console.html"); rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("ETag") != tag {
		t.Errorf("HEAD: %d %d %s", rec.Code, rec.Body.Len(), rec.Header().Get("ETag"))
	}
	version = "v2"
	if rec := serve("GET", "/console.html", "If-None-Match", tag); rec.Code != 200 || rec.Header().Get("ETag") == tag || !strings.Contains(rec.Body.String(), "v2") {
		t.Errorf("changed head still matched: %d %s", rec.Code, rec.Header().Get("ETag"))
	}

	// Not pages: assets and missing HTML files never ask the hook.
	before := calls.Load()
	for _, p := range []string{"/assets/app.js", "/assets/app.css"} {
		if rec := serve("GET", p); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := serve("GET", "/missing.html"); rec.Code != http.StatusNotFound {
		t.Errorf("missing HTML file: %d", rec.Code)
	}
	if calls.Load() != before {
		t.Errorf("the page hook ran for an asset or a missing file")
	}
}
