package devserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticShell(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":         {Data: []byte("<html>home prerendered</html>")},
		".server/index.html": {Data: []byte("<html>shell</html>")},
		"about/index.html":   {Data: []byte("<html>about prerendered</html>")},
		"assets/app.js":      {Data: []byte("js")},
	}
	h := Static(dist)
	get := func(path string) (int, string, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control")
	}
	if code, body, _ := get("/"); code != 200 || body != "<html>home prerendered</html>" {
		t.Fatalf("home: %d %s", code, body)
	}
	if code, body, _ := get("/about"); code != 200 || body != "<html>about prerendered</html>" {
		t.Fatalf("prerendered page: %d %s", code, body)
	}
	if code, body, _ := get("/posts/42"); code != 200 || body != "<html>shell</html>" {
		t.Fatalf("client route gets the shell, not the prerendered home: %d %s", code, body)
	}
	if code, _, cc := get("/assets/app.js"); code != 200 || cc != "public, max-age=31536000, immutable" {
		t.Fatalf("asset: %d %q", code, cc)
	}
	if code, _, _ := get("/.server/index.html"); code != 404 {
		t.Fatalf("dot directory served: %d", code)
	}
	// A missing file is a 404; a route with a dot is still a route.
	for _, path := range []string{"/assets/app-old.js", "/assets/chunk", "/llms.txt", "/robots.txt", "/favicon.ico", "/.well-known/security.txt", "/missing.html"} {
		if code, body, _ := get(path); code != 404 {
			t.Errorf("missing file %s: %d %s", path, code, body)
		}
	}
	if code, body, _ := get("/u/jane.doe"); code != 200 || body != "<html>shell</html>" {
		t.Fatalf("route with a dot: %d %s", code, body)
	}
	// .well-known/ is served from the build; other dot directories are not.
	dist[".well-known/security.txt"] = &fstest.MapFile{Data: []byte("Contact: mailto:a@example.com")}
	if code, body, _ := get("/.well-known/security.txt"); code != 200 || body != "Contact: mailto:a@example.com" {
		t.Fatalf(".well-known file: %d %s", code, body)
	}
	// Without a kept shell, the prerendered home is the fallback.
	delete(dist, ".server/index.html")
	if code, body, _ := get("/posts/42"); code != 200 || body != "<html>home prerendered</html>" {
		t.Fatalf("fallback without shell: %d %s", code, body)
	}
}

// A build with a page per locale serves the visitor's: ?lang, the lang
// cookie, Accept-Language, then the manifest's default.
func TestStaticLocales(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":                   {Data: []byte("home en")},
		"about/index.html":             {Data: []byte("about en")},
		".server/index.html":           {Data: []byte("shell")},
		".locales/manifest.json":       {Data: []byte(`{"default":"en","locales":["en","sq","pt-BR"]}`)},
		".locales/en/index.html":       {Data: []byte("home en")},
		".locales/en/about/index.html": {Data: []byte("about en")},
		".locales/en/shell.html":       {Data: []byte("shell en")},
		".locales/sq/index.html":       {Data: []byte("home sq")},
		".locales/sq/about/index.html": {Data: []byte("about sq")},
		".locales/sq/shell.html":       {Data: []byte("shell sq")},
		".locales/pt-BR/index.html":    {Data: []byte("home pt-BR")},
		"assets/app.js":                {Data: []byte("js")},
	}
	get := func(h http.Handler, target string, header ...string) (string, string) {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", target, rec.Code)
		}
		// The language variation; every page varies by Accept-Encoding.
		var vary []string
		for _, v := range rec.Header().Values("Vary") {
			if v != "Accept-Encoding" {
				vary = append(vary, v)
			}
		}
		return rec.Body.String(), strings.Join(vary, ", ")
	}
	h := Static(dist)
	cases := []struct {
		target string
		header []string
		want   string
	}{
		{"/", nil, "home en"},
		{"/", []string{"Accept-Language", "sq-AL,en;q=0.5"}, "home sq"},
		{"/about", []string{"Accept-Language", "de, sq;q=0.9, en;q=0.8"}, "about sq"},
		{"/about", []string{"Accept-Language", "en;q=0.2, sq;q=0.9"}, "about sq"},
		{"/about", []string{"Accept-Language", "sq;q=0, en"}, "about en"},
		{"/about?lang=sq", []string{"Accept-Language", "en"}, "about sq"},
		{"/about", []string{"Cookie", "lang=sq", "Accept-Language", "en"}, "about sq"},
		{"/about", []string{"Accept-Language", "ja"}, "about en"},
		{"/", []string{"Accept-Language", "pt"}, "home pt-BR"},
		// No variant of the page in that locale: its shell.
		{"/posts/42", []string{"Accept-Language", "sq"}, "shell sq"},
		{"/posts/42", nil, "shell en"},
		// pt-BR has no shell: the plain one.
		{"/posts/42", []string{"Accept-Language", "pt-BR"}, "shell"},
	}
	for _, c := range cases {
		body, vary := get(h, c.target, c.header...)
		if body != c.want {
			t.Errorf("%s %v: %q, want %q", c.target, c.header, body, c.want)
		}
		if c.want != "shell" && vary != "Accept-Language, Cookie" {
			t.Errorf("%s: Vary %q", c.target, vary)
		}
	}
	if body, vary := get(h, "/assets/app.js", "Accept-Language", "sq"); body != "js" || vary != "" {
		t.Errorf("asset: %q, Vary %q", body, vary)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.locales/sq/index.html", nil))
	if rec.Code != 404 {
		t.Errorf("variant served by path: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if rec.Code != 404 {
		t.Errorf("missing file gets a locale's shell: %d %s", rec.Code, rec.Body)
	}

	// The i18n pack's negotiation wins; an empty answer (not started) falls
	// back to the built-in one.
	pack := Static(dist, WithLocale(func(*http.Request) string { return "sq" }))
	if body, _ := get(pack, "/about", "Accept-Language", "en"); body != "about sq" {
		t.Errorf("WithLocale: %q", body)
	}
	unstarted := Static(dist, WithLocale(func(*http.Request) string { return "" }))
	if body, _ := get(unstarted, "/about", "Accept-Language", "sq"); body != "about sq" {
		t.Errorf("WithLocale empty: %q", body)
	}

	// Without a manifest (an app without locales) the build is served as
	// before and nothing varies.
	delete(dist, ".locales/manifest.json")
	plain := Static(dist)
	if body, vary := get(plain, "/about", "Accept-Language", "sq"); body != "about en" || vary != "" {
		t.Errorf("no manifest: %q, Vary %q", body, vary)
	}
	if body, vary := get(plain, "/posts/42", "Accept-Language", "sq"); body != "shell" || vary != "" {
		t.Errorf("no manifest, shell: %q, Vary %q", body, vary)
	}

	// The svelte template keeps no shell: the locale's one page serves
	// every path.
	svelte := fstest.MapFS{
		"index.html":             {Data: []byte("app en")},
		".locales/manifest.json": {Data: []byte(`{"default":"en","locales":["en","sq"]}`)},
		".locales/en/index.html": {Data: []byte("app en")},
		".locales/sq/index.html": {Data: []byte("app sq")},
	}
	if body, _ := get(Static(svelte), "/anything", "Accept-Language", "sq"); body != "app sq" {
		t.Errorf("svelte: %q", body)
	}
}
