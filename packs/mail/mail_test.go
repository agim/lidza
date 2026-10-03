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

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/jobs"
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
	if err := m.prepare(context.Background(), &msg); err != nil {
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
		if err := m.prepare(context.Background(), &bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// Without an outbox, Send returns the provider's id (none for outbox).
	if id, err := m.Send(context.Background(), Message{To: "a@b.c", Subject: "x", Text: "y"}); err != nil || id != "" {
		t.Fatalf("send without outbox: %q %v", id, err)
	}
}

// TestLocalizedTemplates: <name>.<lang> is chosen by the message's Lang,
// the i18n pack's locale or Accept-Language, with the base language and
// then <name> as the fallbacks; a template's "subject" is the subject.
func TestLocalizedTemplates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "welcome.txt.tmpl"), []byte("Welcome {{.Name}}"), 0o644)
	os.WriteFile(filepath.Join(dir, "welcome.de.txt.tmpl"), []byte(`{{define "subject"}}Willkommen, {{.Name}}
{{end}}Willkommen {{.Name}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "welcome.pt-BR.html.tmpl"), []byte("<p>Bem-vindo {{.Name}}</p>"), 0o644)
	m, err := New(Config{Provider: "outbox", From: "app@example.com", TemplatesDir: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	send := func(ctx context.Context, lang string) Message {
		t.Helper()
		msg := Message{To: "ada@example.com", Subject: "Welcome", Template: "welcome", Lang: lang, Data: map[string]string{"Name": "Ada"}}
		if err := m.prepare(ctx, &msg); err != nil {
			t.Fatal(err)
		}
		return msg
	}
	bg := context.Background()
	if msg := send(bg, "de"); msg.Template != "welcome.de" || msg.Text != "Willkommen Ada" || msg.Subject != "Willkommen, Ada" {
		t.Fatalf("de: %+v", msg)
	}
	// A regional tag falls back to its base language, any case.
	if msg := send(bg, "DE-at"); msg.Template != "welcome.de" {
		t.Fatalf("de-AT: %+v", msg)
	}
	if msg := send(bg, "pt-br"); msg.Template != "welcome.pt-BR" || msg.HTML != "<p>Bem-vindo Ada</p>" || msg.Subject != "Welcome" {
		t.Fatalf("pt-BR: %+v", msg)
	}
	// A language without templates, or none, gets the default.
	if msg := send(bg, "fr"); msg.Template != "welcome" || msg.Text != "Welcome Ada" || msg.Subject != "Welcome" {
		t.Fatalf("fr: %+v", msg)
	}
	if msg := send(bg, ""); msg.Template != "welcome" {
		t.Fatalf("no language: %+v", msg)
	}
	// The request's Accept-Language, recorded by the middleware.
	var seen context.Context
	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Context() }))
	req := httptest.NewRequest("POST", "/api/v1/x", nil)
	req.Header.Set("Accept-Language", "fr-CH, fr;q=0.9, de;q=0.8, *;q=0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got := strings.Join(Languages(seen), ","); got != "fr-CH,fr,de" {
		t.Fatalf("languages: %s", got)
	}
	if msg := send(seen, ""); msg.Template != "welcome.de" {
		t.Fatalf("accept-language: %+v", msg)
	}
	// An explicit Lang wins over the request.
	if msg := send(seen, "pt-BR"); msg.Template != "welcome.pt-BR" {
		t.Fatalf("explicit lang: %+v", msg)
	}
	// The i18n pack's locale (a Localizer service) wins over the header.
	s := lidza.NewServices()
	lidza.Provide[Localizer](s, fixedLocale("pt-BR"))
	if msg := send(lidza.WithServices(seen, s), ""); msg.Template != "welcome.pt-BR" {
		t.Fatalf("localizer: %+v", msg)
	}
	if got := ParseAcceptLanguage("en;q=0.5, de;q=bad, ja;q=0, it"); strings.Join(got, ",") != "it,en" {
		t.Fatalf("parse: %v", got)
	}
}

type fixedLocale string

func (f fixedLocale) Language(context.Context) string { return string(f) }

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
	ctx := context.Background()
	pool := schemaPool(ctx, t, "mail_outbox")
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
	// A re-run job never sends a delivered message again.
	if err := m.deliverQueued(rctx, sent.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := m.Outbox(rctx, 10); err != nil || again[1].Attempts != 1 {
		t.Fatalf("delivered message sent again: %v %+v", err, again)
	}
	// Delivery can be retried by id.
	if err := m.Deliver(rctx, failed.ID); err == nil {
		t.Fatal("retry of a failing message succeeded")
	}
	// A queued message must keep metadata through the outbox round trip,
	// including a retry after the sending configuration changes.
	if _, err := pool.Exec(ctx, jobs.JobTable); err != nil {
		t.Fatal(err)
	}
	delivered := make(chan map[string]any, 4)
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		delivered <- body
		io.WriteString(w, `{"id":"metadata-1"}`)
	}))
	defer metadataServer.Close()
	queued, err := New(Config{Provider: "resend", APIKey: "k", From: "default@example.com"}, pool, jobs.New(jobs.Config{}, pool))
	if err != nil {
		t.Fatal(err)
	}
	metadataContext := ctxWith(t, metadataServer)
	metadataID, err := queued.Send(metadataContext, Message{
		To: "ada@example.com", From: "sender@example.com", ReplyTo: "reply@example.com",
		Subject: "metadata", Text: "hello", Headers: map[string]string{"X-Tag": "notice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
		t.Fatal("queued mail was sent synchronously")
	default:
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := queued.Deliver(metadataContext, metadataID); err != nil {
			t.Fatal(err)
		}
		body := <-delivered
		if body["from"] != "sender@example.com" || body["reply_to"] != "reply@example.com" {
			t.Fatalf("metadata lost: %+v", body)
		}
		headers, ok := body["headers"].(map[string]any)
		if !ok || headers["X-Tag"] != "notice" {
			t.Fatalf("headers lost: %+v", body)
		}
	}
	// A legacy queued row has NULL metadata; its sender remains usable.
	var legacyID string
	if err := pool.QueryRow(ctx, `INSERT INTO mail_message (recipient, subject, text, status, attempts) VALUES ('ada@example.com', 'legacy', 'hello', 'queued', 0) RETURNING id`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if err := queued.Deliver(metadataContext, legacyID); err != nil {
		t.Fatal(err)
	}
	if body := <-delivered; body["from"] != "default@example.com" || body["reply_to"] != nil || body["headers"] != nil {
		t.Fatalf("legacy metadata: %+v", body)
	}

}
