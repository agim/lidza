package lidza

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agim/lidza/pkg/devserver"
)

// TestSecurityTxt: SECURITY_CONTACT serves /.well-known/security.txt
// (RFC 9116) with its contacts, the policy, Canonical over HTTPS and an
// Expires ahead; without it the path is the build's, a 404 when the
// build has none; a contact that is not an address fails the start.
func TestSecurityTxt(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	t.Setenv(devserver.EnvFrontendURL, "")
	dist := fstest.MapFS{"index.html": {Data: []byte("app")}}
	get := func(h http.Handler, method string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, SecurityTxtPath, nil))
		return rec
	}

	t.Setenv("SECURITY_CONTACT", "security@example.com, https://example.com/report")
	t.Setenv("SECURITY_POLICY", "https://example.com/security")
	t.Setenv("APP_URL", "https://example.com/")
	h, err := Handler(App{Dist: dist})
	if err != nil {
		t.Fatal(err)
	}
	rec := get(h, http.MethodGet)
	body := rec.Body.String()
	for _, want := range []string{
		"Contact: mailto:security@example.com\n",
		"Contact: https://example.com/report\n",
		"Policy: https://example.com/security\n",
		"Canonical: https://example.com/.well-known/security.txt\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	_, exp, ok := strings.Cut(body, "Expires: ")
	if !ok {
		t.Fatalf("no Expires in\n%s", body)
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(exp))
	if err != nil || at.Before(time.Now().AddDate(0, 5, 0)) || at.After(time.Now().AddDate(1, 0, 0)) {
		t.Errorf("Expires %q: %v", exp, err)
	}
	if rec := get(h, http.MethodHead); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", rec.Code, rec.Body)
	}
	if rec := get(h, http.MethodPost); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}

	t.Setenv("APP_URL", "http://localhost:3000")
	h, _ = Handler(App{Dist: dist})
	if body := get(h, http.MethodGet).Body.String(); strings.Contains(body, "Canonical") {
		t.Errorf("Canonical over HTTP:\n%s", body)
	}

	t.Setenv("SECURITY_CONTACT", "")
	h, _ = Handler(App{Dist: dist})
	if rec := get(h, http.MethodGet); rec.Code != 404 {
		t.Errorf("no contact, no file: %d %s", rec.Code, rec.Body)
	}
	dist[".well-known/security.txt"] = &fstest.MapFile{Data: []byte("Contact: mailto:own@example.com\n")}
	h, _ = Handler(App{Dist: dist})
	if rec := get(h, http.MethodGet); rec.Code != 200 || !strings.Contains(rec.Body.String(), "own@example.com") {
		t.Errorf("the build's own file: %d %s", rec.Code, rec.Body)
	}

	for _, bad := range []string{"security team", "ftp://example.com"} {
		t.Setenv("SECURITY_CONTACT", bad)
		if _, err := Handler(App{Dist: dist}); err == nil || !strings.Contains(err.Error(), "SECURITY_CONTACT") {
			t.Errorf("contact %q: %v", bad, err)
		}
	}
	t.Setenv("SECURITY_CONTACT", "security@example.com")
	t.Setenv("SECURITY_POLICY", "http://example.com/policy")
	if _, err := Handler(App{Dist: dist}); err == nil || !strings.Contains(err.Error(), "SECURITY_POLICY") {
		t.Errorf("http policy: %v", err)
	}
}
