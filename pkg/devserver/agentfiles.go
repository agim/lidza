package devserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
)

// AgentFiles serves the files `lidza dev` keeps under .lidza for agents
// (the app's llms.txt, llms-full.txt and openapi.json) and passes every
// other request to next. Used in dev mode only.
//
// They are always at /_lidza/llms.txt, /_lidza/llms-full.txt and
// /_lidza/openapi.json. At the root (/llms.txt ...) they are served only
// when the app has no file of its own there: a product's own /llms.txt
// for crawlers wins, and the framework's copy stays under /_lidza/.
func AgentFiles(next http.Handler) http.Handler {
	files := map[string]string{
		"llms.txt":      filepath.Join(BuildDir, "llms.txt"),
		"llms-full.txt": filepath.Join(BuildDir, "llms-full.txt"),
		"openapi.json":  filepath.Join(BuildDir, "openapi.json"),
	}
	serve := func(w http.ResponseWriter, path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "not generated yet: lidza dev writes it after the first build", http.StatusNotFound)
			return
		}
		ct := "text/plain; charset=utf-8"
		if strings.HasSuffix(path, ".json") {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/_lidza/"); ok {
			if path, ok := files[name]; ok {
				serve(w, path)
				return
			}
		}
		path, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		// The app's own file wins: anything but a miss or the page shell
		// (HTML the frontend answers every unknown path with).
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		if rec.Code == http.StatusOK && !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		serve(w, path)
	})
}
