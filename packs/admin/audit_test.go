package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/audit"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/credentials"
)

// Every admin action reaches OnAudit: app forms with their outcome, the
// pack's own forms, uploads by name and downloads by status, with no
// secret value in it.
func TestAudit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv("LIDZA_MODE", "test")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	var mu sync.Mutex
	var got []Audit
	tmpl := fstest.MapFS{"posts.html": {Data: []byte(`{{define "content"}}posts{{end}}`)}}
	pages := []Page{{Name: "Posts", Path: "posts", Template: "posts.html", MaxUpload: 1 << 10,
		Actions: map[string]Action{
			"publish": func(*http.Request) (string, error) { return "published", nil },
			"fail":    func(*http.Request) (string, error) { return "", errors.New("no such post") },
		},
		Downloads: map[string]http.HandlerFunc{
			"export":  func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "id\n") },
			"missing": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		}}}
	app := Section{Key: "billing", Title: "Billing", Icon: "key", Fields: []Field{
		{Name: "STRIPE_SECRET", Label: "Secret", Kind: "secret"},
		{Name: "BILLING_CURRENCY", Label: "Currency", Kind: "text"},
	}}
	m, _ := mail.New(mail.Config{Provider: "log", TemplatesDir: dir}, nil, nil)
	s := lidza.NewServices()
	lidza.Provide(s, m)
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir, Templates: tmpl, Pages: pages,
		Sections: []Section{app}, OnAudit: func(_ context.Context, a Audit) {
			if a.Request == nil {
				t.Errorf("%s without the request", a.Path)
			}
			mu.Lock()
			got = append(got, a)
			mu.Unlock()
		}}, s)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	last := func() Audit {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(got) == 0 {
			t.Fatal("nothing audited")
		}
		return got[len(got)-1]
	}
	postForm := func(path string, v url.Values) {
		t.Helper()
		res, err := client.PostForm(srv.URL+path, v)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}

	postForm("/admin/posts/publish", url.Values{"post": {"7"}, "api_token": {"tok-123"}, "body": {strings.Repeat("x", 600)}})
	a := last()
	if a.Path != "/posts/publish" || a.Message != "published" || a.Error != "" || a.Form.Get("post") != "7" || a.Form.Get("api_token") != "[redacted]" || len(a.Form.Get("body")) > 510 {
		t.Fatalf("app action: %+v", a)
	}
	postForm("/admin/posts/fail", url.Values{"post": {"8"}})
	if a := last(); a.Error != "no such post" || a.Message != "" {
		t.Fatalf("failed action: %+v", a)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, _ := w.CreateFormFile("file", "a.csv")
	_, _ = fw.Write([]byte("abc"))
	_ = w.Close()
	res, err := client.Post(srv.URL+"/admin/posts/publish", w.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if a := last(); a.Form.Get("file") != "file: a.csv (3 bytes)" {
		t.Fatalf("upload: %+v", a.Form)
	}

	get(t, srv, "/admin/posts/export")
	if a := last(); !a.Download || a.Status != 200 || a.Path != "/posts/export" {
		t.Fatalf("download: %+v", a)
	}
	get(t, srv, "/admin/posts/missing")
	if a := last(); a.Status != 404 {
		t.Fatalf("failed download: %+v", a)
	}

	// The pack's own forms: a secret setting's value never reaches it.
	postForm("/admin/settings", url.Values{"section": {"billing"}, "STRIPE_SECRET": {"sk_live_123"}, "BILLING_CURRENCY": {"EUR"}})
	a = last()
	if a.Path != "/settings" || a.Form.Get("section") != "billing" || a.Form.Get("STRIPE_SECRET") != "[redacted]" || a.Form.Get("BILLING_CURRENCY") != "EUR" || a.Message == "" && a.Error == "" {
		t.Fatalf("settings: %+v", a)
	}
	postForm("/admin/admins", url.Values{"entry": {"ops@example.com"}})
	if a := last(); a.Path != "/admins" || a.Form.Get("entry") != "ops@example.com" {
		t.Fatalf("admins: %+v", a)
	}
	mu.Lock()
	for _, a := range got {
		for _, vs := range a.Form {
			for _, v := range vs {
				if strings.Contains(v, "sk_live") || strings.Contains(v, "tok-123") {
					t.Errorf("a secret reached the audit: %s %v", a.Path, a.Form)
				}
			}
		}
	}
	mu.Unlock()
}

// With the audit pack running, every admin action is in its log too:
// the admin as the actor, the path as the resource, secrets redacted.
func TestAuditToLog(t *testing.T) {
	ctx := context.Background()
	pool := auditPool(t)
	log := audit.New(audit.Config{}, pool)
	s := lidza.NewServices()
	lidza.Provide(s, log)
	asAdmin := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), &auth.User{ID: "staff-1"})))
		})
	}
	dir := t.TempDir()
	pages := []Page{{Name: "Posts", Path: "posts", Template: "posts.html",
		Actions: map[string]Action{"fail": func(*http.Request) (string, error) { return "", errors.New("no such post") }}}}
	srv := serve(t, Options{Auth: asAdmin, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir,
		Templates: fstest.MapFS{"posts.html": {Data: []byte(`{{define "content"}}posts{{end}}`)}}, Pages: pages}, s)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.PostForm(srv.URL+"/admin/posts/fail", url.Values{"post": {"8"}, "api_token": {"tok-123"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	p, err := log.List(ctx, audit.Query{})
	if err != nil || len(p.Records) != 1 {
		t.Fatalf("log: %+v %v", p.Records, err)
	}
	r := p.Records[0]
	if r.Actor != "staff-1" || r.Action != "admin.form" || r.Resource != "admin/posts/fail" || r.Outcome != audit.Failed || r.Meta["post"] != "8" || r.Meta["api_token"] != "[redacted]" {
		t.Fatalf("record: %+v", r)
	}
}

// auditPool is the test database with a schema of its own holding the
// audit table.
func auditPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err == nil {
		err = admin.Ping(ctx)
	}
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("admin_audit_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, audit.Table); err != nil {
		t.Fatal(err)
	}
	return pool
}
