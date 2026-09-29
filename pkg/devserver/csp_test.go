package devserver

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agim/lidza/pkg/middleware"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// TestStaticAllowsInline: a page served under a strict policy gets the
// hashes of its own inline scripts and styles, as the browser computes
// them (CRLF as LF, NUL as U+FFFD); data blocks and external scripts get
// none, and a response without a policy stays without one.
func TestStaticAllowsInline(t *testing.T) {
	page := "<html><head><script src=\"/assets/app.js\"></script><style>body{margin:0}</style>" +
		"<script type=\"application/ld+json\">{\"@type\":\"WebPage\"}</script>" +
		"<script type=\"application/json\" id=\"i18n\">{}</script></head>" +
		"<body></body></html>\n<script>self.x={i:\"root\x00\"};\r\nrun()</script>" +
		"<SCRIPT type=module>go()</SCRIPT>"
	dist := fstest.MapFS{"index.html": {Data: []byte(page)}, "about/index.html": {Data: []byte(page)}}
	static := Static(dist)
	serve := func(policy, path string) string {
		h := middleware.SecureHeaders(middleware.SecureHeadersOptions{CSP: policy})(static)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Body.String() != page {
			t.Fatalf("%s: page changed: %q", path, rec.Body.String())
		}
		return rec.Header().Get("Content-Security-Policy")
	}
	for _, path := range []string{"/", "/about", "/posts/1"} {
		got := serve(middleware.DefaultCSP, path)
		want := middleware.AddCSP(middleware.AddCSP(middleware.DefaultCSP,
			"script-src", sha("self.x={i:\"root\uFFFD\"};\nrun()"), sha("go()")),
			"style-src", sha("body{margin:0}"))
		if got != want {
			t.Errorf("%s:\n got %s\nwant %s", path, got, want)
		}
	}
	if got := serve("", "/"); got != "" {
		t.Errorf("no policy: sent %q", got)
	}
	// 'unsafe-inline' would be switched off by a hash, 'none' opened.
	for _, policy := range []string{
		"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'none'",
		"script-src 'self' 'unsafe-inline'",
	} {
		if got := serve(policy, "/"); got != policy {
			t.Errorf("policy %q changed to %q", policy, got)
		}
	}
	// A policy that only restricts images leaves scripts unrestricted.
	if got := serve("img-src 'self'", "/"); got != "img-src 'self'" {
		t.Errorf("image-only policy changed to %q", got)
	}
	// Falling back to default-src, the directive is created from it.
	if got := serve("default-src 'self'", "/"); !strings.Contains(got, "; script-src 'self' "+sha("self.x={i:\"root\uFFFD\"};\nrun()")+" "+sha("go()")+";") {
		t.Errorf("default-src fallback: %q", got)
	}
}
