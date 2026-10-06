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

// Static serves a built single-page app from dist: files by path (the
// .br or .gz copy lidza build stored when the request accepts it), hashed
// assets under /assets/ with a long cache lifetime, prerendered pages at
// <path>/index.html, and the shell for every other path (client-side
// routing). When the build has a page per locale (dist/.locales), pages
// and the shell come in the request's locale, negotiated as the i18n pack
// does (WithLocale passes the pack's own negotiation). WithHead sets each
// page's <head> per request.
func Static(dist fs.FS, opts ...Option) http.Handler {
	o := collect(opts)
	tags := &etags{}
	loc := loadLocales(dist, o.locale)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		// Dot directories (dist/.server, dist/.locales) are build
		// internals, never pages; .well-known/ (security.txt, app links)
		// is served as any file.
		if (strings.HasPrefix(name, ".") && !strings.HasPrefix(name, ".well-known/")) || strings.Contains(name, "/.") {
			http.NotFound(w, r)
			return
		}
		// A missing file is a 404, not the shell: a stale hashed asset
		// after a deploy, /robots.txt, an icon. Other paths are routes.
		if missingFile(name) && !exists(dist, name) {
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
		// Any other HTML file is a page too: its inline code needs the
		// policy's hashes.
		if strings.HasSuffix(name, ".html") && name != "index.html" && exists(dist, name) && servePage(w, r, dist, name, false, nil) {
			return
		}
		// index.html is written directly: FileServer would redirect it to "/".
		if name != "" && name != "index.html" && exists(dist, name) {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			sendFile(w, r, dist, name, tags)
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
		allowInline(w.Header(), index)
		writePage(w, r, status, index)
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
	allowInline(w.Header(), page)
	if varies {
		w.Header().Add("Vary", "Accept-Language, Cookie")
	}
	writePage(w, r, status, page)
	return true
}

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// fileExts are the extensions that name a file rather than a route, so
// a missing one is a 404. A route may hold a dot (/u/jane.doe); these
// endings are files.
var fileExts = map[string]bool{
	".avif": true, ".css": true, ".csv": true, ".gif": true, ".htm": true, ".html": true,
	".ico": true, ".jpeg": true, ".jpg": true, ".js": true, ".json": true, ".map": true,
	".md": true, ".mjs": true, ".mp3": true, ".mp4": true, ".otf": true, ".pdf": true,
	".png": true, ".svg": true, ".ttf": true, ".txt": true, ".wasm": true, ".webm": true,
	".webmanifest": true, ".webp": true, ".woff": true, ".woff2": true, ".xml": true, ".zip": true,
}

// missingFile tells a request for a file the build lacks (anything under
// assets/ or .well-known/, or a name with a file's extension) from a
// client-side route.
func missingFile(name string) bool {
	if strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, ".well-known/") {
		return true
	}
	return fileExts[strings.ToLower(path.Ext(name))]
}
