package admin

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/packs/storage"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/env"
)

// The pages carry no inline script or style, so a strict
// Content-Security-Policy ("default-src 'self'") holds; the assets come
// from the binary, gzipped, cached by content hash.
func TestAssetsAndCSP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	l, _ := llm.New(llm.Config{Provider: "fake"})
	s := lidza.NewServices()
	lidza.Provide(s, l)
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir}, s)
	for _, path := range []string{"/admin/", "/admin/llm/settings", "/admin/llm"} {
		code, body := get(t, srv, path)
		if code != 200 {
			t.Fatalf("%s: %d", path, code)
		}
		if strings.Contains(body, "<style") || regexp.MustCompile(`<script>|<script [^>]*>[^<]`).MatchString(body) || strings.Contains(body, ` style="`) {
			t.Errorf("%s has inline script or style", path)
		}
		if !strings.Contains(body, `data-bs-theme=`) || !strings.Contains(body, "tabler.min.css?v=") {
			t.Errorf("%s: not the Tabler frame", path)
		}
	}
	// The stylesheet: gzip for clients that take it, plain otherwise.
	_, body := get(t, srv, "/admin/")
	href := regexp.MustCompile(`/admin/assets/tabler\.min\.css\?v=[0-9a-f]+`).FindString(body)
	req, _ := http.NewRequest("GET", srv.URL+href, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset headers: %v", res.Header)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	css, _ := io.ReadAll(zr)
	res.Body.Close()
	if !strings.Contains(string(css), "Tabler") || len(css) < 100_000 {
		t.Fatalf("css: %d bytes", len(css))
	}
	req.Header.Del("Accept-Encoding")
	res, _ = http.DefaultTransport.RoundTrip(req)
	plain, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.Header.Get("Content-Encoding") != "" || len(plain) != len(css) {
		t.Fatalf("plain asset: %v %d", res.Header, len(plain))
	}
	if code, _ := get(t, srv, "/admin/assets/nope.css"); code != 404 {
		t.Fatalf("unknown asset: %d", code)
	}
	if icon("key") == "" || !strings.Contains(string(icon("key")), `aria-hidden="true"`) {
		t.Fatal("icon")
	}
}

func TestDeniedPage(t *testing.T) {
	s := lidza.NewServices()
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return false }, CredentialsDir: t.TempDir()}, s)
	code, body := get(t, srv, "/admin/mail/settings")
	if code != http.StatusForbidden || !strings.Contains(body, "not an admin") || !strings.Contains(body, EnvAdminUsers) || strings.Contains(body, `href="/admin/mail"`) {
		t.Fatalf("denied: %d %s", code, body)
	}
	if strings.Contains(body, "lidza admin add") {
		t.Fatal("the development hint outside development")
	}
	t.Setenv("LIDZA_MODE", "dev")
	if _, body := get(t, srv, "/admin/mail/settings"); !strings.Contains(body, "lidza admin add") {
		t.Fatalf("no development hint: %s", body)
	}
}

// The Credentials page: one section at a time, the fields for the
// chosen provider, where each value comes from, a value the process
// environment holds never saved, and the app's own sections.
func TestCredentialsPage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv("LIDZA_MODE", "test")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	m, err := mail.New(mail.Config{Provider: "log", TemplatesDir: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := llm.New(llm.Config{Provider: "fake"})
	rc := &fakeReconf{}
	s := lidza.NewServices()
	lidza.Provide(s, m)
	lidza.Provide(s, l)
	lidza.Provide(s, rc)
	app := Section{Key: "billing", Title: "Billing", Icon: "key", Fields: []Field{
		{Name: "STRIPE_SECRET_KEY", Label: "Secret key", Kind: "secret"},
		{Name: "BILLING_CURRENCY", Label: "Currency", Kind: "text", Default: "EUR"},
	}}
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir, Sections: []Section{app}}, s)

	// Each pack's settings are a tab of its page; the app's sections are
	// on Settings; the old address redirects.
	code, body := get(t, srv, "/admin/mail")
	if code != 200 || !strings.Contains(body, `href="/admin/mail/settings"`) || !strings.Contains(body, `href="/admin/settings"`) || strings.Contains(body, "/admin/storage") {
		t.Fatalf("mail page: %d", code)
	}
	code, body = get(t, srv, "/admin/settings")
	if code != 200 || !strings.Contains(body, `name="section" value="billing"`) || strings.Contains(body, `value="mail"`) {
		t.Fatalf("app settings: %d", code)
	}
	if code, body := get(t, srv, "/admin/credentials?section=llm"); code != 200 || !strings.Contains(body, `name="section" value="llm"`) {
		t.Fatalf("old address: %d", code)
	}
	if code, _ := get(t, srv, "/admin/storage/settings"); code != 404 {
		t.Fatalf("settings of a pack that is off: %d", code)
	}

	// Mail on log: development; the SMTP server's fields hidden until chosen.
	_, body = get(t, srv, "/admin/mail/settings")
	if !strings.Contains(body, "Development (log)") || !strings.Contains(body, `data-admin-selector="MAIL_PROVIDER"`) || !strings.Contains(body, "SMTP server") {
		t.Fatal("mail status or selector")
	}
	if !regexp.MustCompile(`d-none" data-for="smtp"`).MatchString(body) {
		t.Fatal("the SMTP fields should be hidden while the provider is log")
	}
	if !strings.Contains(body, `data-placeholders="mailgun=`) || !strings.Contains(body, "https://app.mailgun.com") {
		t.Fatal("placeholders and console links")
	}

	post := func(form url.Values) string {
		t.Helper()
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.PostForm(srv.URL+"/admin/settings", form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	// Switch to SMTP with its server: saved, the packs reloaded, back on
	// the Settings tab.
	if loc := post(url.Values{"section": {"mail"}, "MAIL_PROVIDER": {"smtp"}, "MAIL_SMTP_HOST": {"smtp.example.com"}, "MAIL_SMTP_SECURITY": {"tls"}, "MAIL_SMTP_PORT": {"465"}, "MAIL_SMTP_PASSWORD": {"pw-1"}, "MAIL_FROM": {"App <a@example.com>"}}); !strings.Contains(loc, "saved") || !strings.HasPrefix(loc, "/admin/mail/settings") {
		t.Fatalf("save: %s", loc)
	}
	vals := credentials.Values(dir)
	if vals["MAIL_PROVIDER"] != "smtp" || vals["MAIL_SMTP_HOST"] != "smtp.example.com" || vals["MAIL_SMTP_SECURITY"] != "tls" || vals["MAIL_SMTP_PASSWORD"] != "pw-1" || rc.calls != 1 {
		t.Fatalf("saved: %v, reconfigured %d", vals, rc.calls)
	}
	_, body = get(t, srv, "/admin/mail/settings")
	if strings.Contains(body, "pw-1") || regexp.MustCompile(`d-none" data-for="smtp"`).MatchString(body) || !regexp.MustCompile(`<option value="tls" selected>`).MatchString(body) {
		t.Fatal("the SMTP fields should show, the password withheld, the security selected")
	}
	if loc := post(url.Values{"section": {"mail"}, "MAIL_SMTP_PORT": {"4x5"}}); !strings.Contains(loc, "not+a+number") {
		t.Fatalf("port: %s", loc)
	}
	if !strings.Contains(body, "credentials file") && !strings.Contains(body, "saved here") {
		t.Fatal("the origin of the saved values")
	}
	// An unknown provider is refused; emptying a text removes it.
	if loc := post(url.Values{"section": {"mail"}, "MAIL_PROVIDER": {"pigeon"}}); !strings.Contains(loc, "error=") {
		t.Fatalf("unknown provider: %s", loc)
	}
	post(url.Values{"section": {"mail"}, "MAIL_FROM": {""}})
	if credentials.Values(dir)["MAIL_FROM"] != "" {
		t.Fatal("emptied text kept")
	}

	// A value the process environment holds is locked and never saved.
	t.Setenv("LLM_MODEL", "from-env")
	_, body = get(t, srv, "/admin/llm/settings")
	if !strings.Contains(body, "Set in the process environment") || !regexp.MustCompile(`name="LLM_MODEL" value="from-env"[^>]*disabled`).MatchString(body) {
		t.Fatal("locked field")
	}
	post(url.Values{"section": {"llm"}, "LLM_MODEL": {"other"}})
	if credentials.Values(dir)["LLM_MODEL"] != "" {
		t.Fatal("a value the environment overrides was saved")
	}

	// The app's section saves like the packs'.
	if loc := post(url.Values{"section": {"billing"}, "STRIPE_SECRET_KEY": {"sk_test_x"}}); !strings.Contains(loc, "Billing+saved") || !strings.Contains(loc, "/admin/settings?section=billing") {
		t.Fatalf("app section: %s", loc)
	}
	if credentials.Values(dir)["STRIPE_SECRET_KEY"] != "sk_test_x" {
		t.Fatal("app secret not saved")
	}
	if loc := post(url.Values{"section": {"nope"}}); !strings.Contains(loc, "unknown+section") {
		t.Fatalf("unknown section: %s", loc)
	}
}

// The llm page's Settings tab holds two forms, the model provider and
// Embeddings, each with its own selector; saving EMBED_PROVIDER and its
// key reaches the running pack without a restart.
func TestEmbedSettings(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // the llm pack reads the credentials from the working directory
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv("LIDZA_MODE", "test")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	// The chat provider comes from the environment, as a deployment's
	// would; Reconfigure reads it again with the saved EMBED_ settings.
	t.Setenv("LLM_PROVIDER", "anthropic")
	t.Setenv("LLM_API_KEY", "k")
	l, _ := llm.New(llm.Config{Provider: "anthropic", APIKey: "k"})
	s := lidza.NewServices()
	lidza.Provide(s, l)
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir}, s)

	_, body := get(t, srv, "/admin/llm/settings")
	for _, want := range []string{`name="section" value="llm"`, `name="section" value="embed"`, `data-admin-selector="LLM_PROVIDER"`, `data-admin-selector="EMBED_PROVIDER"`,
		"Same as the chat provider", `name="EMBED_PROVIDER" value="" class="form-selectgroup-input" checked`, "Same as chat", `data-for-selector="LLM_PROVIDER"`, `data-admin-nolinks="LLM_PROVIDER"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Same as chat: the model applies, the key and address wait for a
	// provider of their own.
	if !regexp.MustCompile(`<div class="mb-3" data-for=",openai,google,compatible,ollama">\s*<label[^>]*for="f-EMBED_MODEL"`).MatchString(body) {
		t.Error("EMBED_MODEL should show while embeddings follow the chat provider")
	}
	if !regexp.MustCompile(`d-none" data-for="openai,google,compatible">\s*<label[^>]*for="f-EMBED_API_KEY"`).MatchString(body) {
		t.Error("EMBED_API_KEY should be hidden until a provider is chosen")
	}
	if strings.Contains(body, `name="LLM_EMBED_MODEL"`) {
		t.Error("LLM_EMBED_MODEL is read, no longer offered")
	}

	post := func(form url.Values) string {
		t.Helper()
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.PostForm(srv.URL+"/admin/settings", form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	if loc := post(url.Values{"section": {"embed"}, "EMBED_PROVIDER": {"openai"}, "EMBED_API_KEY": {"sk-embed"}, "EMBED_MODEL": {""}}); !strings.Contains(loc, "Embeddings+saved") || !strings.HasPrefix(loc, "/admin/llm/settings") {
		t.Fatalf("save: %s", loc)
	}
	if vals := credentials.Values(dir); vals["EMBED_PROVIDER"] != "openai" || vals["EMBED_API_KEY"] != "sk-embed" {
		t.Fatalf("saved: %v", vals)
	}
	if l.EmbedProvider() != "openai" {
		t.Fatalf("not applied: %s", l.EmbedProvider())
	}
	_, body = get(t, srv, "/admin/llm/settings")
	if strings.Contains(body, "sk-embed") || !regexp.MustCompile(`<div class="mb-3" data-for="openai,google,compatible">`).MatchString(body) || !strings.Contains(body, `name="EMBED_PROVIDER" value="openai" class="form-selectgroup-input" checked`) {
		t.Error("the key should show as stored, never its value, with openai chosen")
	}
	// Off: Embed says so, chat is untouched.
	post(url.Values{"section": {"embed"}, "EMBED_PROVIDER": {"none"}})
	if _, err := l.Embed(context.Background(), []string{"x"}); !errors.Is(err, llm.ErrNoEmbeddings) || l.Provider() != "anthropic" {
		t.Fatalf("off: %v", err)
	}
	// A provider that cannot embed is refused.
	if loc := post(url.Values{"section": {"embed"}, "EMBED_PROVIDER": {"anthropic"}}); !strings.Contains(loc, "error=") {
		t.Fatalf("anthropic embeddings accepted: %s", loc)
	}
	// Settings the pack refuses (compatible without its address) are not
	// kept: the values before them are back, so the next start is not
	// stopped by them.
	if loc := post(url.Values{"section": {"embed"}, "EMBED_PROVIDER": {"compatible"}, "EMBED_BASE_URL": {""}}); !strings.Contains(loc, "error=not+saved") || !strings.Contains(loc, "EMBED_BASE_URL") {
		t.Fatalf("compatible without an address accepted: %s", loc)
	}
	if vals := credentials.Values(dir); vals["EMBED_PROVIDER"] != "none" {
		t.Fatalf("the refused value was kept: %q", vals["EMBED_PROVIDER"])
	}
	if l.EmbedProvider() != "none" {
		t.Fatalf("not applied back: %s", l.EmbedProvider())
	}
}

// A multi selector (the sign-in providers) saves the checked options and
// keeps names the page does not offer.
func TestMultiSelector(t *testing.T) {
	sec := Section{Key: "auth", Title: "Sign-in", Selector: "AUTH_PROVIDERS", Fields: []Field{
		{Name: "AUTH_PROVIDERS", Kind: "multi", Options: []string{"google", "github"}},
		{Name: "AUTH_GOOGLE_CLIENT_ID", Kind: "text", Group: "Google", For: []string{"google"}},
		{Name: "AUTH_GITHUB_CLIENT_ID", Kind: "text", Group: "GitHub", For: []string{"github"}},
	}}
	v := view(sec, map[string]string{"AUTH_PROVIDERS": "google, okta"}, map[string]string{"AUTH_PROVIDERS": env.OriginSaved})
	if v.Selector == nil || !v.Selector.Opts[0].Checked || v.Selector.Opts[1].Checked || !v.Groups[0].Applies || v.Groups[1].Applies {
		t.Fatalf("view: %+v", v)
	}
	if v.Status != "ok" || v.StatusText != "google, okta" {
		t.Fatalf("status: %s %s", v.Status, v.StatusText)
	}
	if off := view(sec, map[string]string{}, nil); off.Status != "off" {
		t.Fatalf("no provider: %s", off.Status)
	}

	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	credentials.Generate(dir)
	credentials.Set(dir, map[string]string{"AUTH_PROVIDERS": "google,okta"})
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir, Sections: []Section{sec}}, lidza.NewServices())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.PostForm(srv.URL+"/admin/settings", url.Values{"section": {"auth"}, "present_AUTH_PROVIDERS": {"1"}, "AUTH_PROVIDERS": {"github"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := credentials.Values(dir)["AUTH_PROVIDERS"]; got != "github,okta" {
		t.Fatalf("providers: %q", got)
	}
}

// A multi field that is not the section's selector renders as checkboxes
// too, and saves the checked options.
func TestMultiField(t *testing.T) {
	sec := Section{Key: "import", Title: "Import", Fields: []Field{
		{Name: "IMPORT_CATEGORIES", Label: "Categories", Kind: "multi", Options: []string{"1", "11"}, Labels: map[string]string{"1": "Books", "11": "Music"}},
		{Name: "IMPORT_BATCH", Label: "Batch size", Kind: "number"},
	}}
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	credentials.Generate(dir)
	credentials.Set(dir, map[string]string{"IMPORT_CATEGORIES": "11"})
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: dir, Sections: []Section{sec}}, lidza.NewServices())
	res, err := http.Get(srv.URL + "/admin/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	page := string(body)
	if !strings.Contains(page, `name="present_IMPORT_CATEGORIES"`) || !strings.Contains(page, `type="checkbox" name="IMPORT_CATEGORIES" value="11" checked`) || !strings.Contains(page, "Music") {
		t.Fatalf("multi field not rendered as checkboxes: %s", page)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err = client.PostForm(srv.URL+"/admin/settings", url.Values{"section": {"import"}, "present_IMPORT_CATEGORIES": {"1"}, "IMPORT_CATEGORIES": {"1", "11"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := credentials.Values(dir)["IMPORT_CATEGORIES"]; got != "1,11" {
		t.Fatalf("categories: %q", got)
	}
}

// Each built-in settings list offers every provider its pack knows, so a
// provider added to a pack cannot go missing from its page.
func TestSettingsCoverProviders(t *testing.T) {
	// EMBED_PROVIDER offers "same as chat" (empty) and every value but
	// fake, which only tests use.
	embed := []string{""}
	for _, p := range llm.EmbedProviders {
		if p != "fake" {
			embed = append(embed, p)
		}
	}
	want := map[string][]string{"MAIL_PROVIDER": mail.Providers, "LLM_PROVIDER": llm.Providers, "EMBED_PROVIDER": embed, "STORAGE_PROVIDER": storage.Providers, "MAIL_SMTP_SECURITY": mail.SMTPSecurities, "MAIL_REGION": mail.Regions}
	for _, sec := range builtinSections() {
		for _, f := range sec.Fields {
			if w, ok := want[f.Name]; ok {
				if !sameSet(f.Options, w) {
					t.Errorf("%s offers %v, the pack knows %v", f.Name, f.Options, w)
				}
				for _, o := range f.Options {
					if f.Labels[o] == "" {
						t.Errorf("%s: option %s has no label", f.Name, o)
					}
				}
				delete(want, f.Name)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("no field for %v", want)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, v := range a {
		m[v] = true
	}
	for _, v := range b {
		if !m[v] {
			return false
		}
	}
	return true
}

// An app's page: in the sidebar, rendered in the frame from an embedded
// template, its data loaded per request, its actions posting back with a
// message or an error, and its path checked at Mount.
func TestAppPages(t *testing.T) {
	tmpl := fstest.MapFS{"orders.html": {Data: []byte(`{{define "content"}}<div class="card"><ul>{{range .Data}}<li>{{.}}</li>{{end}}</ul><form method="post" action="{{.Path}}/orders/refund"><button>Refund</button></form>{{icon "inbox"}}</div>{{end}}`)}}
	refunded := ""
	pages := []Page{{Name: "Orders", Path: "orders", Icon: "inbox", Template: "orders.html",
		Data: func(r *http.Request) (any, error) {
			if r.URL.Query().Get("broken") != "" {
				return nil, errors.New("orders unavailable")
			}
			return []string{"#1001", "#1002"}, nil
		},
		Actions: map[string]Action{"refund": func(r *http.Request) (string, error) {
			if r.Form.Get("id") == "" {
				return "", errors.New("which order?")
			}
			refunded = r.Form.Get("id")
			return "order " + refunded + " refunded", nil
		}},
	}}
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: t.TempDir(), Templates: tmpl, Pages: pages}, lidza.NewServices())
	code, body := get(t, srv, "/admin/orders")
	if code != 200 || !strings.Contains(body, "<li>#1001</li>") || !strings.Contains(body, "<title>Orders · Admin</title>") || !strings.Contains(body, `aria-hidden="true"`) || !strings.Contains(body, `nav-item active`) {
		t.Fatalf("page: %d %s", code, body)
	}
	if _, body := get(t, srv, "/admin/"); !strings.Contains(body, `href="/admin/orders"`) {
		t.Fatal("not in the sidebar")
	}
	if _, body := get(t, srv, "/admin/orders?broken=1"); !strings.Contains(body, "orders unavailable") || strings.Contains(body, "#1001") {
		t.Fatal("a Data error should replace the content")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ := client.PostForm(srv.URL+"/admin/orders/refund", url.Values{"id": {"1001"}})
	res.Body.Close()
	if loc := res.Header.Get("Location"); refunded != "1001" || !strings.Contains(loc, "/admin/orders?saved=order+1001+refunded") {
		t.Fatalf("action: %q %q", refunded, loc)
	}
	res, _ = client.PostForm(srv.URL+"/admin/orders/refund", nil)
	res.Body.Close()
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "error=which+order") {
		t.Fatalf("action error: %q", loc)
	}
	res, _ = client.PostForm(srv.URL+"/admin/orders/nope", nil)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("unknown action: %d", res.StatusCode)
	}
	for _, bad := range []Page{{Name: "X", Path: "users", Template: "x.html"}, {Name: "X", Path: "a/b", Template: "x.html"}, {Name: "X", Path: "x"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("page %+v accepted", bad)
				}
			}()
			New(Options{Pages: []Page{bad}})
		}()
	}
}

// Every icon a template or the code names is vendored (assets/icons.txt).
func TestIconsVendored(t *testing.T) {
	loadAssets()
	names := map[string]bool{}
	entries, _ := files.ReadDir("templates")
	for _, e := range entries {
		data, _ := files.ReadFile("templates/" + e.Name())
		for _, m := range regexp.MustCompile(`icon "([a-z0-9-]+)"`).FindAllStringSubmatch(string(data), -1) {
			names[m[1]] = true
		}
	}
	for _, s := range builtinSections() {
		names[s.Icon] = true
	}
	for _, n := range groupIcons {
		names[n] = true
	}
	for _, n := range []string{"layout-dashboard", "users", "mail", "sparkles", "folder", "list-check", "adjustments-horizontal", "inbox", "chart-bar", "settings", "password", "brand-google", "brand-github", "brand-windows", "key", "photo", "file-text", "file"} {
		names[n] = true
	}
	for n := range names {
		if icon(n) == "" {
			t.Errorf("icon %q is not vendored: add it to assets/icons.txt and run assets/vendor.sh", n)
		}
	}
}

// num takes whatever number type a query returns.
func TestHumanNumber(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{{int32(42), "42"}, {int64(12_345_678), "12.3M"}, {12_500, "12.5k"}, {uint16(7), "7"}, {3.25, "3.2"}, {float32(2), "2"}, {-15_000, "-15.0k"}, {nil, "0"}} {
		if got := humanNumber(c.in); got != c.want {
			t.Errorf("num(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLongSidebar(t *testing.T) {
	pages := make([]Page, 30)
	templates := fstest.MapFS{"extra.html": {Data: []byte(`{{define "content"}}<p>Extra page</p>{{end}}`)}}
	for i := range pages {
		pages[i] = Page{Name: fmt.Sprintf("Extra %02d", i), Path: fmt.Sprintf("extra-%02d", i), Icon: "inbox", Template: "extra.html"}
	}
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: t.TempDir(), Templates: templates, Pages: pages}, lidza.NewServices())
	code, body := get(t, srv, "/admin/extra-29")
	if code != 200 || !strings.Contains(body, `href="/admin/extra-29" aria-current="page"`) {
		t.Fatalf("long menu current item: %d", code)
	}
	_, css := get(t, srv, "/admin/assets/admin.css")
	for _, rule := range []string{"position: sticky", "height: 100dvh", "max-height: calc(100dvh - 4rem)", "overflow-y: auto"} {
		if !strings.Contains(css, rule) {
			t.Fatalf("sidebar CSS missing %q", rule)
		}
	}
	_, script := get(t, srv, "/admin/assets/admin.js")
	if !strings.Contains(script, "shown.bs.collapse") || !strings.Contains(script, "nav.scrollTop") {
		t.Fatal("long menu does not reveal its active item")
	}
}

// TestRowGuttersFitPhones: on a phone the page's container has 8px of side
// padding, so a row's base gutter must be at most 1rem (g-3); a g-4 or g-5
// row pulls 12px or more out of it and the page scrolls sideways. Wider
// gutters go on a breakpoint (g-lg-4).
func TestRowGuttersFitPhones(t *testing.T) {
	entries, err := files.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`class="row\b[^"]*"`)
	wide := regexp.MustCompile(`(^|\s)(g|gx)-[45](\s|"|$)`)
	for _, e := range entries {
		data, _ := files.ReadFile("templates/" + e.Name())
		for _, m := range row.FindAllString(string(data), -1) {
			if wide.MatchString(strings.TrimPrefix(m, "class=")) {
				t.Errorf("%s: %s overflows a phone's 8px padding; use g-3 g-lg-4", e.Name(), m)
			}
		}
	}
}

// TestSaveBarAboveInputs: the sticky save bar covers the fields scrolling
// under it, the secret fields' Show buttons too: Bootstrap stacks an input
// group's controls up to z-index 5.
func TestSaveBarAboveInputs(t *testing.T) {
	m := regexp.MustCompile(`\.admin-savebar \{[^}]*z-index: (\d+)`).FindSubmatch(adminCSS(t))
	if m == nil {
		t.Fatal("no z-index on .admin-savebar")
	}
	if n, _ := strconv.Atoi(string(m[1])); n <= 5 {
		t.Fatalf(".admin-savebar z-index %d: an input group's Show button draws over it", n)
	}
}

func adminCSS(t *testing.T) []byte {
	t.Helper()
	b, err := assetFiles.ReadFile("assets/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
