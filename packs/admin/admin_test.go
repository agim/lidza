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
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/middleware"
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

// TestFirstUserIsAdmin: the first account gets in without being listed,
// a listed one gets in, anyone else does not; an admin adds another from
// the Overview page.
func TestFirstUserIsAdmin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv(EnvAdminUsers, "listed@example.com")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	s := lidza.NewServices()
	as := func(u *auth.User) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
			})
		}
	}
	page := func(u *auth.User) (int, string) {
		srv := serve(t, Options{Auth: as(u), FirstUser: func(context.Context) string { return "user-1" }, CredentialsDir: dir, Dir: filepath.Join(dir, "admin")}, s)
		return get(t, srv, "/admin/")
	}
	if code, body := page(&auth.User{ID: "user-1"}); code != 200 || !strings.Contains(body, "user-1") || !strings.Contains(body, "an admin by default") {
		t.Fatalf("first user: %d %s", code, body)
	}
	if code, _ := page(&auth.User{ID: "user-2", Claims: map[string]any{"email": "Listed@example.com"}}); code != 200 {
		t.Fatalf("listed user: %d", code)
	}
	if code, _ := page(&auth.User{ID: "user-3", Claims: map[string]any{"email": "other@example.com"}}); code != 403 {
		t.Fatalf("other user: %d", code)
	}
	// The first user adds user-3 by email; the list applies at once.
	srv := serve(t, Options{Auth: as(&auth.User{ID: "user-1"}), FirstUser: func(context.Context) string { return "user-1" }, CredentialsDir: dir, Dir: filepath.Join(dir, "admin")}, s)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.PostForm(srv.URL+"/admin/admins", url.Values{"admin": {"other@example.com"}})
	if err != nil || res.StatusCode != http.StatusSeeOther {
		t.Fatalf("add admin: %v %v", err, res)
	}
	res.Body.Close()
	if code, _ := page(&auth.User{ID: "user-3", Claims: map[string]any{"email": "other@example.com"}}); code != 200 {
		t.Fatalf("added admin: %d", code)
	}
	res, _ = client.PostForm(srv.URL+"/admin/admins", url.Values{"admin": {"other@example.com"}, "remove": {"on"}})
	res.Body.Close()
	if code, _ := page(&auth.User{ID: "user-3", Claims: map[string]any{"email": "other@example.com"}}); code != 403 {
		t.Fatalf("removed admin: %d", code)
	}
	if code, _ := page(nil); code != 403 {
		t.Fatalf("no user: %d", code)
	}
}

// TestUsers: the Users page lists accounts the auth pack saw, and its
// actions disable, enable, sign out and promote them.
func TestUsers(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, tbl := range []string{"auth_session", "auth_token", "auth_account"} {
		pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl)
	}
	if _, err := pool.Exec(ctx, auth.SessionTable+auth.TokenTable+auth.AccountTable); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{Secret: strings.Repeat("s", 32), AccessTTL: time.Minute, RefreshTTL: time.Hour}, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "owner", map[string]any{"email": "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "member", map[string]any{"email": "member@example.com"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv(EnvAdminUsers, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	s := lidza.NewServices()
	lidza.Provide(s, a)
	owner := &auth.User{ID: "owner", Claims: map[string]any{"email": "owner@example.com"}}
	asOwner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), owner)))
		})
	}
	srv := serve(t, Options{Auth: asOwner, CredentialsDir: dir, Dir: filepath.Join(dir, "admin")}, s)
	code, body := get(t, srv, "/admin/users")
	if code != 200 || !strings.Contains(body, "owner@example.com") || !strings.Contains(body, "member@example.com") || !strings.Contains(body, "first account, admin by default") || !strings.Contains(body, "2 account(s)") {
		t.Fatalf("users: %d %s", code, body)
	}
	if code, body := get(t, srv, "/admin/users?q=MEMBER"); code != 200 || strings.Contains(body, "owner@example.com") || !strings.Contains(body, "1 account(s)") {
		t.Fatalf("search: %d", code)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	act := func(subject, action string) string {
		t.Helper()
		res, err := client.PostForm(srv.URL+"/admin/users/"+subject+"/"+action, nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	if loc := act("member", "disable"); !strings.Contains(loc, "disabled") {
		t.Fatalf("disable: %s", loc)
	}
	if ac, _ := a.AccountOf(ctx, "member"); !ac.Disabled() || ac.Sessions != 0 {
		t.Fatalf("after disable: %+v", ac)
	}
	if code, body := get(t, srv, "/admin/users"); code != 200 || !strings.Contains(body, ">disabled<") {
		t.Fatal("disabled not shown")
	}
	act("member", "enable")
	if ac, _ := a.AccountOf(ctx, "member"); ac.Disabled() {
		t.Fatal("still disabled")
	}
	act("member", "admin")
	if list := adminList(dir); len(list) != 1 || list[0] != "member@example.com" {
		t.Fatalf("admin list: %v", list)
	}
	act("member", "unadmin")
	if list := adminList(dir); len(list) != 0 {
		t.Fatalf("admin list after removal: %v", list)
	}
	if loc := act("owner", "disable"); !strings.Contains(loc, "your+own") {
		t.Fatalf("self-disable allowed: %s", loc)
	}
	if loc := act("nobody", "revoke"); !strings.Contains(loc, "no+such") {
		t.Fatalf("unknown account: %s", loc)
	}
}
