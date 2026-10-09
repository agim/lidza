package devserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStaticProbes: the paths scanners send never read outside the build
// or a build-internal file, and the only redirects are Go's path
// cleaning to a same-origin path, never to another host. Served from a
// rooted directory (os.Root, as lidza.DirFS does), a symlink inside the
// build pointing outside it is refused.
func TestStaticProbes(t *testing.T) {
	base := t.TempDir()
	dist := filepath.Join(base, "dist")
	os.MkdirAll(filepath.Join(dist, "assets"), 0o755)
	os.MkdirAll(filepath.Join(dist, ".server"), 0o755)
	os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>shell</html>"), 0o644)
	os.WriteFile(filepath.Join(dist, ".server", "secret.js"), []byte("SECRET internal"), 0o644)
	os.WriteFile(filepath.Join(base, ".env"), []byte("SECRET=env"), 0o644)
	os.MkdirAll(filepath.Join(base, "outside"), 0o755)
	os.WriteFile(filepath.Join(base, "outside", "secret.txt"), []byte("SECRET outside"), 0o644)
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(dist, "assets", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, ".env"), filepath.Join(dist, "env.txt")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dist)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	mux := http.NewServeMux()
	mux.Handle("/", Static(root.FS()))
	for _, p := range []string{
		"/.env", "/../.env", "/..%2f.env", "/%2e%2e/.env", "/%2e%2e%2f.env", "/%252e%252e/.env", "/%252e%252e%252f.env",
		"/assets/..%2f..%2f.env", "/assets/..%5c..%5c.env", "/..\\.env", "/a%00b", "/.server/secret.js", "/%2eserver/secret.js",
		"/assets/escape/secret.txt", "/env.txt", "/proc/self/environ", "/.git/config", "//evil.example/x", "/\\evil.example/x",
		"/%2f%2fevil.example/x", "/assets/../../.env",
	} {
		req := httptest.NewRequest("GET", "http://app.example"+p, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("%s: read a file it must not (%d)", p, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" && (!strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\")) {
			t.Errorf("%s: redirect off the origin: %q", p, loc)
		}
	}
	// The same symlinks through os.DirFS do escape: why lidza.DirFS uses a root.
	escaped := httptest.NewRecorder()
	Static(os.DirFS(dist)).ServeHTTP(escaped, httptest.NewRequest("GET", "/assets/escape/secret.txt", nil))
	if !strings.Contains(escaped.Body.String(), "SECRET") {
		t.Log("os.DirFS no longer follows symlinks out of the directory")
	}
}
