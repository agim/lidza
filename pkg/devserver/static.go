package devserver

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Static serves a built single-page app from dist: files by path, hashed
// assets under /assets/ with a long cache lifetime, and index.html for every
// path that has no file (client-side routing).
func Static(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		// Dot directories (dist/.server) are build internals, never pages.
		if strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
			http.NotFound(w, r)
			return
		}
		// A prerendered page lives at <path>/index.html.
		if name != "" && exists(dist, name+"/index.html") {
			page, err := fs.ReadFile(dist, name+"/index.html")
			if err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(page)
				return
			}
		}
		// index.html is written directly: FileServer would redirect it to "/".
		if name != "" && name != "index.html" && exists(dist, name) {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			r.URL.Path = "/" + name
			files.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "no frontend build: run `lidza build` first", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
