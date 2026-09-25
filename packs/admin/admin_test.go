package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/pkg/credentials"
)

type fakeReconf struct{ calls int }

func (f *fakeReconf) Reconfigure(context.Context) error { f.calls++; return nil }

func noAuth(next http.Handler) http.Handler { return next }

func serve(t *testing.T, opt Options, s *lidza.Services) *httptest.Server {
	t.Helper()
	h := New(opt)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(lidza.WithServices(r.Context(), s)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(body)
}

func TestGateAndPages(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	l, _ := llm.New(llm.Config{Provider: "fake", Model: "m1"})
	rc := &fakeReconf{}
	s := lidza.NewServices()
	lidza.Provide(s, l)
	lidza.Provide(s, rc)

	// Nobody is listed: refused, with the fix in the page.
	t.Setenv(EnvAdminUsers, "")
	denied := serve(t, Options{Auth: noAuth, CredentialsDir: dir, Dir: filepath.Join(dir, "admin")}, s)
	if code, body := get(t, denied, "/admin/"); code != http.StatusForbidden || !strings.Contains(body, EnvAdminUsers) {
		t.Fatalf("no admin: %d %s", code, body)
	}
	if code, body := get(t, denied, "/admin/theme.css"); code != 200 || !strings.Contains(body, "--admin-accent") {
		t.Fatalf("theme is public: %d", code)
	}

	allowed := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, Title: "notes", CredentialsDir: dir, Dir: filepath.Join(dir, "admin")}, s)
	code, body := get(t, allowed, "/admin/")
	if code != 200 || !strings.Contains(body, "<title>Overview · notes</title>") || !strings.Contains(body, "fake m1") || !strings.Contains(body, `href="/admin/llm"`) || strings.Contains(body, `href="/admin/mail"`) {
		t.Fatalf("overview: %d %s", code, body)
	}
	code, body = get(t, allowed, "/admin/credentials")
	if code != 200 || !strings.Contains(body, "Language model") || strings.Contains(body, "Storage") || !strings.Contains(body, `name="LLM_API_KEY"`) {
		t.Fatalf("credentials: %d %s", code, body)
	}
	if code, _ := get(t, allowed, "/admin/mail"); code != 404 {
		t.Fatalf("mail without the pack: %d", code)
	}
	if code, body := get(t, allowed, "/admin/llm"); code != 200 || !strings.Contains(body, "usage is recorded through the db pack") {
		t.Fatalf("llm page: %d %s", code, body)
	}

	// Saving seals the value into the credentials file (no db pack here)
	// and asks the packs to reconfigure; an empty secret keeps the stored
	// one; clear removes it.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(form url.Values) string {
		t.Helper()
		res, err := client.PostForm(allowed.URL+"/admin/credentials", form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("post: %d", res.StatusCode)
		}
		return res.Header.Get("Location")
	}
	if loc := post(url.Values{"section": {"llm"}, "LLM_API_KEY": {"sk-new"}, "LLM_MODEL": {"m2"}}); !strings.Contains(loc, "saved") {
		t.Fatalf("save: %s", loc)
	}
	vals, err := credentials.Read(dir)
	if err != nil || vals["LLM_API_KEY"] != "sk-new" || vals["LLM_MODEL"] != "m2" || rc.calls != 1 {
		t.Fatalf("stored: %v %v reconfigured=%d", vals, err, rc.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "config", "master.key")); err != nil {
		t.Fatal("no master key created")
	}
	if code, body := get(t, allowed, "/admin/credentials"); code != 200 || !strings.Contains(body, "stored; leave empty to keep") || strings.Contains(body, "sk-new") {
		t.Fatalf("secret shown: %d %s", code, body)
	}
	if loc := post(url.Values{"section": {"llm"}, "LLM_API_KEY": {""}}); !strings.Contains(loc, "nothing+changed") {
		t.Fatalf("empty secret: %s", loc)
	}
	post(url.Values{"section": {"llm"}, "clear_LLM_API_KEY": {"on"}})
	if vals, _ := credentials.Read(dir); vals["LLM_API_KEY"] != "" || vals["LLM_MODEL"] != "m2" {
		t.Fatalf("clear: %v", vals)
	}

	// Theming: the app's theme.css and layout.html replace the pack's.
	os.MkdirAll(filepath.Join(dir, "admin"), 0o755)
	os.WriteFile(filepath.Join(dir, "admin", "theme.css"), []byte(":root{--admin-accent:#ff0000}"), 0o644)
	os.WriteFile(filepath.Join(dir, "admin", "layout.html"), []byte(`{{define "layout"}}<html><body class="themed"><h1>{{.App}}</h1>{{template "content" .}}</body></html>{{end}}`), 0o644)
	if code, body := get(t, allowed, "/admin/theme.css"); code != 200 || !strings.Contains(body, "#ff0000") {
		t.Fatalf("theme override: %d %s", code, body)
	}
	if code, body := get(t, allowed, "/admin/"); code != 200 || !strings.Contains(body, `class="themed"`) || !strings.Contains(body, "Overview") {
		t.Fatalf("layout override: %d %s", code, body)
	}
}
