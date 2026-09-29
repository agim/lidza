package devserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const builtPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width">
<title>Shop</title>
<meta name="description" content="The shop.">
<meta property="og:title" content="Shop">
<link rel="canonical" href="https://example.com/">
<link rel="stylesheet" href="/assets/app.css">
</head>
<body><svg><title>icon</title></svg><div id="root"></div></body>
</html>`

func TestInjectHead(t *testing.T) {
	out := string(InjectHead([]byte(builtPage), Head{
		Title:       `Tea & "cups" </title><script>alert(1)</script>`,
		Description: `Loose leaf <b>tea</b>`,
		Canonical:   "https://example.com/products/7?a=1&b=2",
		Image:       "https://example.com/p/7.jpg",
		SiteName:    "Shop",
		NoIndex:     true,
		Meta:        []Meta{{Property: "og:locale", Content: "en_GB"}, {Name: "twitter:site", Content: "@shop"}},
		JSONLD: []any{
			map[string]any{"@context": "https://schema.org", "@type": "Product", "name": "</script><script>alert(1)</script>"},
			json.RawMessage(`{"@type":"BreadcrumbList","name":"a<b"}`),
		},
	}))
	for _, want := range []string{
		`<title>Tea &amp; &#34;cups&#34; &lt;/title&gt;&lt;script&gt;alert(1)&lt;/script&gt;</title>`,
		`<meta name="description" content="Loose leaf &lt;b&gt;tea&lt;/b&gt;">`,
		`<meta name="robots" content="noindex">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="Tea &amp; &#34;cups&#34;`,
		`<meta property="og:url" content="https://example.com/products/7?a=1&amp;b=2">`,
		`<meta property="og:image" content="https://example.com/p/7.jpg">`,
		`<meta property="og:site_name" content="Shop">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:description" content="Loose leaf &lt;b&gt;tea&lt;/b&gt;">`,
		`<meta property="og:locale" content="en_GB">`,
		`<meta name="twitter:site" content="@shop">`,
		`<link rel="canonical" href="https://example.com/products/7?a=1&amp;b=2">`,
		`<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"\u003c/script\u003e\u003cscript\u003ealert(1)\u003c/script\u003e"}</script>`,
		`<script type="application/ld+json">{"@type":"BreadcrumbList","name":"a\u003cb"}</script>`,
		// Kept: the tags the head does not set, and a title outside it.
		`<meta charset="utf-8">`, `<meta name="viewport" content="width=device-width">`, `<link rel="stylesheet" href="/assets/app.css">`,
		`<svg><title>icon</title></svg>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\n%s", want, out)
		}
	}
	for _, gone := range []string{"<title>Shop</title>", `content="The shop."`, `<meta property="og:title" content="Shop">`, `href="https://example.com/"`, "<script>alert"} {
		if strings.Contains(out, gone) {
			t.Errorf("still there: %s\n%s", gone, out)
		}
	}
	if strings.Count(out, "<title>") != 2 || strings.Count(out, `name="description"`) != 1 || strings.Count(out, `rel="canonical"`) != 1 {
		t.Errorf("duplicates:\n%s", out)
	}
	if i, j := strings.Index(out, "application/ld+json"), strings.Index(out, "</head>"); i < 0 || i > j {
		t.Errorf("not in the head:\n%s", out)
	}

	// Only a title: the rest of the page's head stays.
	out = string(InjectHead([]byte(builtPage), Head{Title: "Tea"}))
	if !strings.Contains(out, `content="The shop."`) || !strings.Contains(out, `<link rel="canonical" href="https://example.com/">`) ||
		!strings.Contains(out, "<title>Tea</title>") || !strings.Contains(out, `<meta name="twitter:card" content="summary">`) {
		t.Errorf("title only:\n%s", out)
	}
	// No head: unchanged.
	if out := string(InjectHead([]byte("<p>fragment</p>"), Head{Title: "x"})); out != "<p>fragment</p>" {
		t.Errorf("no head: %s", out)
	}
}

func TestStaticHead(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":         {Data: []byte("<html><head><title>Home</title></head><body>home</body></html>")},
		".server/index.html": {Data: []byte("<html><head><title>App</title></head><body>shell</body></html>")},
		"about/index.html":   {Data: []byte("<html><head><title>About</title></head><body>about</body></html>")},
		"assets/app.js":      {Data: []byte("js")},
	}
	head := func(r *http.Request) (Head, bool) {
		if id, ok := strings.CutPrefix(r.URL.Path, "/products/"); ok {
			if id == "404" {
				return Head{Title: "Not found", NoIndex: true, Status: http.StatusNotFound}, true
			}
			return Head{Title: "Product " + id, Canonical: "https://example.com/products/" + id}, true
		}
		if r.URL.Path == "/about" {
			return Head{Description: "Who we are"}, true
		}
		if r.URL.Path == "/assets/app.js" {
			t.Error("hook called for an asset")
		}
		return Head{}, false
	}
	h := Static(dist, WithHead(head))
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/products/7"); code != 200 || !strings.Contains(body, "<title>Product 7</title>") || strings.Contains(body, "<title>App</title>") ||
		!strings.Contains(body, `<link rel="canonical" href="https://example.com/products/7">`) || !strings.Contains(body, "shell") {
		t.Fatalf("shell: %d %s", code, body)
	}
	if code, body := get("/products/404"); code != 404 || !strings.Contains(body, `content="noindex"`) {
		t.Fatalf("status: %d %s", code, body)
	}
	if code, body := get("/about"); code != 200 || !strings.Contains(body, "<title>About</title>") || !strings.Contains(body, `<meta name="description" content="Who we are">`) {
		t.Fatalf("prerendered: %d %s", code, body)
	}
	if code, body := get("/"); code != 200 || body != "<html><head><title>Home</title></head><body>home</body></html>" {
		t.Fatalf("no head for the home: %d %s", code, body)
	}
	if code, body := get("/assets/app.js"); code != 200 || body != "js" {
		t.Fatalf("asset: %d %s", code, body)
	}
}

func TestProxyHead(t *testing.T) {
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("Content-Type", "text/javascript")
			io.WriteString(w, "js")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><head><title>Dev</title></head><body></body></html>")
	}))
	defer vite.Close()
	var host string
	p, err := NewProxy(vite.URL, WithHead(func(r *http.Request) (Head, bool) {
		host = r.Host
		return Head{Title: "Post " + strings.TrimPrefix(r.URL.Path, "/posts/")}, true
	}))
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(p)
	defer front.Close()
	res, err := http.Get(front.URL + "/posts/3")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "<title>Post 3</title>") || res.ContentLength != int64(len(body)) {
		t.Fatalf("proxied page: %s (length %d)", body, res.ContentLength)
	}
	if want := strings.TrimPrefix(front.URL, "http://"); host != want {
		t.Fatalf("hook saw host %q, want the visitor's %q", host, want)
	}
	res, _ = http.Get(front.URL + "/src/main.js")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "js" {
		t.Fatalf("asset changed: %s", body)
	}
}
