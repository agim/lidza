package devserver

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// ShellFile is the unrendered index.html the react template's prerender
// keeps next to the server bundle; it serves every path that has no file
// of its own.
const ShellFile = ".server/index.html"

// Static serves a built single-page app from dist: files by path, hashed
// assets under /assets/ with a long cache lifetime, prerendered pages at
// <path>/index.html, and the shell for every other path (client-side
// routing).
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
		// Any other path is the app's to route in the browser. It gets the
		// bare shell the prerender kept (dist/.server/index.html), not the
		// prerendered home page: that one carries the home markup and its
		// hydration payload, which would mismatch here.
		index, err := fs.ReadFile(dist, ShellFile)
		if err != nil || name == "" {
			index, err = fs.ReadFile(dist, "index.html")
		}
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
