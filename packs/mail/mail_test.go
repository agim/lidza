package mail

import (
	"context"
	"encoding/json"
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
	"github.com/agim/lidza/packs/db"
)

// redirect sends every request to the test server, whatever host the
// provider asked for, the way a recorder would replay it.
type redirect struct{ to *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

func ctxWith(t *testing.T, srv *httptest.Server) context.Context {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	s := lidza.NewServices()
	lidza.Provide[http.RoundTripper](s, redirect{u})
	return lidza.WithServices(context.Background(), s)
}

func TestProviders(t *testing.T) {
	var got struct {
		path, auth, ctype string
		body              string
		headers           http.Header
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.path, got.auth, got.ctype, got.body, got.headers = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(body), r.Header.Clone()
		w.Header().Set("X-Message-Id", "sg-1")
		w.WriteHeader(202)
		io.WriteString(w, `{"id":"<mg-1@x>","MessageID":"pm-1"}`)
	}))
	defer srv.Close()
	ctx := ctxWith(t, srv)
	msg := Message{From: "App <app@example.com>", To: "Ada <ada@example.com>", Subject: "Hi", Text: "hello", HTML: "<p>hello</p>", ReplyTo: "reply@example.com"}

	for _, tc := range []struct {
		cfg     Config
		path    string
		wantID  string
		inBody  []string
		inAuth  string
		ctype   string
		hasHead string
	}{
		{Config{Provider: "mailgun", APIKey: "k", Domain: "example.com"}, "/v3/example.com/messages", "<mg-1@x>", []string{"to=Ada+%3Cada%40example.com%3E", "text=hello", "html=%3Cp%3Ehello", "h%3AReply-To=reply"}, "Basic YXBpOms=", "application/x-www-form-urlencoded", ""},
		{Config{Provider: "sendgrid", APIKey: "k"}, "/v3/mail/send", "sg-1", []string{`"email":"ada@example.com"`, `"type":"text/html"`, `"reply_to"`}, "Bearer k", "application/json", ""},
		{Config{Provider: "postmark", APIKey: "k"}, "/email", "pm-1", []string{`"HtmlBody":"<p>hello</p>"`, `"ReplyTo":"reply@example.com"`, `"MessageStream":"outbound"`}, "", "application/json", "X-Postmark-Server-Token"},
		{Config{Provider: "resend", APIKey: "k"}, "/emails", "<mg-1@x>", []string{`"to":["Ada <ada@example.com>"]`, `"reply_to":"reply@example.com"`}, "Bearer k", "application/json", ""},
	} {
		p, err := newProvider(tc.cfg)
		if err != nil {
			t.Fatal(tc.cfg.Provider, err)
		}
		id, err := p.Send(ctx, msg)
		if err != nil || id != tc.wantID {
			t.Fatalf("%s: id %q err %v", tc.cfg.Provider, id, err)
		}
		if got.path != tc.path || got.auth != tc.inAuth || !strings.HasPrefix(got.ctype, tc.ctype) {
			t.Fatalf("%s: path %s auth %q ctype %q", tc.cfg.Provider, got.path, got.auth, got.ctype)
		}
		for _, want := range tc.inBody {
			if !strings.Contains(got.body, want) {
				t.Errorf("%s: body lacks %s:\n%s", tc.cfg.Provider, want, got.body)
			}
		}
		if tc.hasHead != "" && got.headers.Get(tc.hasHead) != "k" {
			t.Errorf("%s: header %s = %q", tc.cfg.Provider, tc.hasHead, got.headers.Get(tc.hasHead))
		}
	}

	// A provider error carries the status and the provider's text.
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"errors":[{"message":"bad key"}]}`)
	}))
	defer fail.Close()
	p, _ := newProvider(Config{Provider: "resend", APIKey: "k"})
	if _, err := p.Send(ctxWith(t, fail), msg); err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("provider error: %v", err)
	}
	if _, err := newProvider(Config{Provider: "mailgun", APIKey: "k"}); err == nil || !strings.Contains(err.Error(), "MAIL_DOMAIN") {
		t.Fatalf("missing domain: %v", err)
	}
	if _, err := newProvider(Config{Provider: "carrier-pigeon"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestTemplatesAndValidation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "verify.txt.tmpl"), []byte("Hello {{.Name}}, open {{.Link}}"), 0o644)
	os.WriteFile(filepath.Join(dir, "verify.html.tmpl"), []byte("<p>Hello {{.Name}}, <a href=\"{{.Link}}\">verify</a></p>"), 0o644)
	m, err := New(Config{Provider: "outbox", From: "app@example.com", TemplatesDir: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := m.Templates(); len(names) != 1 || names[0] != "verify" {
		t.Fatalf("templates: %v", names)
	}
	msg := Message{To: "ada@example.com", Subject: "Verify", Template: "verify", Data: map[string]string{"Name": "Ada", "Link": "https://x/verify?token=1&a=<b>"}}
	if err := m.prepare(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Text != "Hello Ada, open https://x/verify?token=1&a=<b>" || !strings.Contains(msg.HTML, `href="https://x/verify?token=1&amp;a=%3cb%3e"`) || msg.From != "app@example.com" {
		t.Fatalf("rendered: %+v", msg)
	}
	for _, bad := range []Message{
		{To: "not an address", Subject: "x", Text: "y"},
		{To: "a@b.c", Subject: "", Text: "y"},
		{To: "a@b.c", Subject: "x"},
		{To: "a@b.c", Subject: "x", Template: "missing"},
	} {
		if err := m.prepare(&bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// Without an outbox, Send returns the provider's id (none for outbox).
	if id, err := m.Send(context.Background(), Message{To: "a@b.c", Subject: "x", Text: "y"}); err != nil || id != "" {
		t.Fatalf("send without outbox: %q %v", id, err)
	}
}

func TestMIME(t *testing.T) {
	msg := Message{Subject: "Hi there", Text: "plain ünïcode", HTML: "<p>hi</p>", ReplyTo: "r@example.com", Headers: map[string]string{"X-Tag": "welcome"}}
	from, _ := parseAddress("App <app@example.com>")
	to, _ := parseAddress("ada@example.com")
	raw, id, err := buildMIME(msg, from, to)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"From: \"App\" <app@example.com>\r\n", "To: <ada@example.com>\r\n", "Subject: Hi there\r\n", "Content-Type: multipart/alternative; boundary=", "Content-Type: text/plain; charset=utf-8", "Content-Type: text/html; charset=utf-8", "Reply-To: r@example.com", "X-Tag: welcome", "plain =C3=BCn=C3=AFcode", "Message-Id: " + id} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("message id: %s", id)
	}
	if _, err := newSMTP("smtps://u:p@mail.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := newSMTP("http://x"); err == nil {
		t.Fatal("bad scheme accepted")
	}
}

// TestOutbox needs Postgres: the message is a row, delivered at once
// without the jobs pack, with the outcome recorded.
func TestOutbox(t *testing.T) {
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
	pool.Exec(ctx, `DROP TABLE IF EXISTS mail_message`)
	if _, err := pool.Exec(ctx, OutboxTable); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["subject"] == "fail" {
			w.WriteHeader(500)
			io.WriteString(w, "down")
			return
		}
		io.WriteString(w, `{"id":"rs-1"}`)
	}))
	defer srv.Close()
	m, err := New(Config{Provider: "resend", APIKey: "k", From: "app@example.com"}, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	rctx := ctxWith(t, srv)
	id, err := m.Send(rctx, Message{To: "ada@example.com", Subject: "ok", Text: "hello"})
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	if _, err := m.Send(rctx, Message{To: "ada@example.com", Subject: "fail", Text: "hello"}); err == nil {
		t.Fatal("provider failure not returned")
	}
	rows, err := m.Outbox(rctx, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("outbox: %v %+v", err, rows)
	}
	failed, sent := rows[0], rows[1]
	if sent.Status != StatusSent || sent.ProviderID == nil || *sent.ProviderID != "rs-1" || sent.Attempts != 1 || sent.SentAt == nil {
		t.Fatalf("sent row: %+v", sent)
	}
	if failed.Status != StatusFailed || failed.Error == nil || !strings.Contains(*failed.Error, "500") || failed.Attempts != 1 {
		t.Fatalf("failed row: %+v", failed)
	}
	// Delivery can be retried by id.
	if err := m.Deliver(rctx, failed.ID); err == nil {
		t.Fatal("retry of a failing message succeeded")
	}
}
