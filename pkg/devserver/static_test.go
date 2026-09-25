package devserver

import (
	"net/http"
	"net/http/httptest"
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
	// Without a kept shell, the prerendered home is the fallback.
	delete(dist, ".server/index.html")
	if code, body, _ := get("/posts/42"); code != 200 || body != "<html>home prerendered</html>" {
		t.Fatalf("fallback without shell: %d %s", code, body)
	}
}
