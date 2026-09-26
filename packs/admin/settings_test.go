package admin

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

// Each built-in settings list offers every provider its pack knows, so a
// provider added to a pack cannot go missing from its page.
func TestSettingsCoverProviders(t *testing.T) {
	want := map[string][]string{"MAIL_PROVIDER": mail.Providers, "LLM_PROVIDER": llm.Providers, "STORAGE_PROVIDER": storage.Providers, "MAIL_SMTP_SECURITY": mail.SMTPSecurities, "MAIL_REGION": mail.Regions}
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
