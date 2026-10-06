package devserver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptable(t *testing.T) {
	for _, c := range []struct {
		header, coding string
		want           bool
	}{
		{"gzip, deflate, br", "br", true},
		{"gzip, deflate, br", "gzip", true},
		{"gzip", "br", false},
		{"br;q=0, gzip", "br", false},
		{"BR;Q=0.5", "br", true},
		{"*", "br", true},
		{"*;q=0", "gzip", false},
		{"gzip;q=0, *", "gzip", false},
		{"identity", "gzip", false},
		{"", "gzip", false},
	} {
		if got := acceptable(c.header, c.coding); got != c.want {
			t.Errorf("acceptable(%q, %q) = %v", c.header, c.coding, got)
		}
	}
}

// TestCompressedStatic: with the .br and .gz copies lidza build stores,
// the server sends the one the request accepts with
// the original's type, Vary and an ETag per representation, and keeps
// HEAD, ranges and 304s; a page built per request is compressed as it
// goes out and revalidates by its ETag.
func TestCompressedStatic(t *testing.T) {
	dir := t.TempDir()
	js := strings.Repeat("export const greeting = 'hello';\n", 200)
	html := "<!doctype html><html><head><title>App</title></head><body>" + strings.Repeat("<p>shell</p>", 300) + "</body></html>"
	for name, data := range map[string]string{
		"dist/index.html":         html,
		"dist/.server/index.html": html,
		"dist/assets/app.js":      js,
		"dist/assets/tiny.js":     "x()",
		"dist/photo.png":          strings.Repeat("\x89PNG", 600),
	} {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(data), 0o644)
	}
	// The copies lidza build stores (cmd/lidza compressDist): the br
	// one stands in for Brotli, which only the CLI writes.
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(js))
	zw.Close()
	os.WriteFile(filepath.Join(dir, "dist/assets/app.js.gz"), gz.Bytes(), 0o644)
	os.WriteFile(filepath.Join(dir, "dist/assets/app.js.br"), []byte("brotli:"+js[:100]), 0o644)

	h := Static(os.DirFS(filepath.Join(dir, "dist")))
	do := func(method, target string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder) string {
		var r io.Reader = rec.Body
		switch rec.Header().Get("Content-Encoding") {
		case "br":
			if b := rec.Body.String(); b == "brotli:"+js[:100] {
				return js
			}
			return "not the stored br copy"
		case "gzip":
			zr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			r = zr
		}
		b, _ := io.ReadAll(r)
		return string(b)
	}

	for _, c := range []struct{ accept, coding string }{
		{"gzip, deflate, br", "br"},
		{"gzip", "gzip"},
		{"br;q=0, gzip", "gzip"},
		{"", ""},
		{"identity", ""},
	} {
		rec := do("GET", "/assets/app.js", "Accept-Encoding", c.accept)
		if rec.Code != 200 || rec.Header().Get("Content-Encoding") != c.coding || decode(rec) != js {
			t.Errorf("Accept-Encoding %q: %d, coding %q", c.accept, rec.Code, rec.Header().Get("Content-Encoding"))
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("Accept-Encoding %q: Content-Type %q", c.accept, ct)
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("Accept-Encoding %q: Vary %q, Cache-Control %q", c.accept, rec.Header().Get("Vary"), rec.Header().Get("Cache-Control"))
		}
	}
	br := do("GET", "/assets/app.js", "Accept-Encoding", "br")
	plain := do("GET", "/assets/app.js")
	if br.Header().Get("ETag") == "" || br.Header().Get("ETag") == plain.Header().Get("ETag") {
		t.Errorf("ETags: br %q, plain %q", br.Header().Get("ETag"), plain.Header().Get("ETag"))
	}
	if br.Header().Get("Content-Length") == "" || br.Header().Get("Content-Length") == plain.Header().Get("Content-Length") {
		t.Errorf("Content-Length: br %q, plain %q", br.Header().Get("Content-Length"), plain.Header().Get("Content-Length"))
	}
	if rec := do("GET", "/assets/app.js", "Accept-Encoding", "br", "If-None-Match", br.Header().Get("ETag")); rec.Code != 304 {
		t.Errorf("If-None-Match: %d", rec.Code)
	}
	if rec := do("HEAD", "/assets/app.js", "Accept-Encoding", "br"); rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "br" {
		t.Errorf("HEAD: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if rec := do("GET", "/assets/app.js", "Range", "bytes=0-5"); rec.Code != 206 || rec.Body.String() != js[:6] {
		t.Errorf("Range: %d %q", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/photo.png", "Accept-Encoding", "gzip"); rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "" {
		t.Errorf("png: coding %q, Vary %q", rec.Header().Get("Content-Encoding"), rec.Header().Get("Vary"))
	}

	// Pages go out compressed as they are built, and revalidate.
	for _, target := range []string{"/", "/posts/42"} {
		rec := do("GET", target, "Accept-Encoding", "gzip")
		if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" || decode(rec) != html || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("%s: %d, coding %q, Vary %q", target, rec.Code, rec.Header().Get("Content-Encoding"), rec.Header().Get("Vary"))
		}
		tag := rec.Header().Get("ETag")
		if again := do("GET", target, "Accept-Encoding", "gzip", "If-None-Match", tag); again.Code != 304 || again.Body.Len() != 0 {
			t.Errorf("%s revalidated: %d", target, again.Code)
		}
		if other := do("GET", target, "If-None-Match", tag); other.Code != 200 || !bytes.Equal(other.Body.Bytes(), []byte(html)) {
			t.Errorf("%s: another representation's ETag matched: %d", target, other.Code)
		}
		if head := do("HEAD", target, "Accept-Encoding", "br"); head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
			t.Errorf("%s HEAD: %d %d", target, head.Code, head.Body.Len())
		}
	}
}
