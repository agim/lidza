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
// routing). When the build has a page per locale (dist/.locales), pages
// and the shell come in the request's locale, negotiated as the i18n pack
// does (WithLocale passes the pack's own negotiation). WithHead sets each
// page's <head> per request.
func Static(dist fs.FS, opts ...Option) http.Handler {
	files := http.FileServerFS(dist)
	o := collect(opts)
	loc := loadLocales(dist, o.locale)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		// Dot directories (dist/.server, dist/.locales) are build
		// internals, never pages.
		if strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
			http.NotFound(w, r)
			return
		}
		// A page in the visitor's locale: the prerendered page (the home
		// page at "/"), else that locale's shell.
		if loc != nil && (name == "" || !exists(dist, name)) {
			dir := path.Join(LocalesDir, loc.pick(r))
			candidates := []string{path.Join(dir, name, "index.html")}
			if name != "" {
				candidates = append(candidates, path.Join(dir, "shell.html"))
				// Without a kept shell (the svelte template) the one page
				// serves every path, as below.
				if !exists(dist, ShellFile) {
					candidates = append(candidates, path.Join(dir, "index.html"))
				}
			}
			for _, file := range candidates {
				if exists(dist, file) && servePage(w, r, dist, file, true, o.head) {
					return
				}
			}
		}
		// A prerendered page lives at <path>/index.html.
		if name != "" && exists(dist, name+"/index.html") && servePage(w, r, dist, name+"/index.html", false, o.head) {
			return
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
		index, status := withHead(o.head, r, index)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(status)
		_, _ = w.Write(index)
	})
}

// servePage writes an HTML file of dist, its head set by head; false
// when it cannot be read. varies marks a page chosen by the request's
// language.
func servePage(w http.ResponseWriter, r *http.Request, dist fs.FS, file string, varies bool, head HeadFunc) bool {
	page, err := fs.ReadFile(dist, file)
	if err != nil {
		return false
	}
	page, status := withHead(head, r, page)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if varies {
		w.Header().Add("Vary", "Accept-Language, Cookie")
	}
	w.WriteHeader(status)
	_, _ = w.Write(page)
	return true
}

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
