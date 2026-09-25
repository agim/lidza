package devserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// AgentFiles serves /llms.txt, /llms-full.txt and /openapi.json from the files `lidza dev`
// keeps under .lidza in the working directory, and passes every other
// request to next. Used in dev mode only.
func AgentFiles(next http.Handler) http.Handler {
	files := map[string]string{
		"/llms.txt":      filepath.Join(BuildDir, "llms.txt"),
		"/llms-full.txt": filepath.Join(BuildDir, "llms-full.txt"),
		"/openapi.json":  filepath.Join(BuildDir, "openapi.json"),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ok := files[r.URL.Path]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
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
	})
}
