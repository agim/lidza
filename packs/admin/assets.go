package admin

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
)

// The front end of the pages: Tabler's stylesheet and script (vendored
// gzipped by assets/vendor.sh), the pack's own admin.css and admin.js,
// and the Tabler icons in assets/icons. Everything is served from the
// binary: no CDN, no inline script or style, so a strict
// Content-Security-Policy ("default-src 'self'") holds.
//
//go:embed assets/*.gz assets/admin.css assets/admin.js assets/icons/*.svg
var assetFiles embed.FS

// asset is one servable file.
type asset struct {
	gz      []byte // gzip-compressed body
	plain   []byte // uncompressed body, for clients without gzip
	ctype   string
	version string // content hash, the ?v= of its URL
}

var (
	assetsOnce   sync.Once
	assetsByName map[string]*asset
	iconsByName  map[string]template.HTML
)

// loadAssets reads the embedded files once.
func loadAssets() {
	assetsOnce.Do(func() {
		assetsByName = map[string]*asset{}
		for _, name := range []string{"tabler.min.css", "tabler.min.js", "admin.css", "admin.js"} {
			a := &asset{ctype: "text/css; charset=utf-8"}
			if strings.HasSuffix(name, ".js") {
				a.ctype = "text/javascript; charset=utf-8"
			}
			if gz, err := assetFiles.ReadFile("assets/" + name + ".gz"); err == nil {
				a.gz = gz
				zr, err := gzip.NewReader(bytes.NewReader(gz))
				if err != nil {
					panic("admin: asset " + name + ": " + err.Error())
				}
				a.plain, _ = io.ReadAll(zr)
			} else {
				a.plain, _ = assetFiles.ReadFile("assets/" + name)
				var buf bytes.Buffer
				zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
				zw.Write(a.plain)
				zw.Close()
				a.gz = buf.Bytes()
			}
			sum := sha256.Sum256(a.plain)
			a.version = hex.EncodeToString(sum[:4])
			assetsByName[name] = a
		}
		iconsByName = map[string]template.HTML{}
		entries, _ := assetFiles.ReadDir("assets/icons")
		for _, e := range entries {
			data, _ := assetFiles.ReadFile("assets/icons/" + e.Name())
			iconsByName[strings.TrimSuffix(e.Name(), ".svg")] = template.HTML(cleanIcon(string(data)))
		}
	})
}

var (
	svgClass  = regexp.MustCompile(`class="[^"]*"`)
	svgSpaces = regexp.MustCompile(`\s+`)
)

// cleanIcon makes a vendored SVG inline-ready: one line, Tabler's icon
// class, hidden from screen readers (the text next to it names it).
func cleanIcon(svg string) string {
	svg = svgSpaces.ReplaceAllString(strings.TrimSpace(svg), " ")
	svg = strings.ReplaceAll(svg, "> <", "><")
	svg = svgClass.ReplaceAllString(svg, `class="icon" aria-hidden="true" focusable="false"`)
	return svg
}

// icon returns a vendored icon as inline SVG; an unknown name renders
// nothing (assets/icons.txt lists the vendored ones).
func icon(name string) template.HTML {
	loadAssets()
	return iconsByName[name]
}

// assetURL is a file's URL under the pages' path with its content hash,
// so browsers keep it for a year and fetch a new one after an upgrade.
func (h *Handler) assetURL(name string) string {
	loadAssets()
	a, ok := assetsByName[name]
	if !ok {
		return h.path + "/assets/" + name
	}
	return h.path + "/assets/" + name + "?v=" + a.version
}

// serveAsset serves an embedded file, compressed when the client takes
// gzip.
func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	loadAssets()
	a, ok := assetsByName[path.Base(r.URL.Path)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.ctype)
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == a.version {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("ETag", `"`+a.version+`"`)
	if r.Header.Get("If-None-Match") == `"`+a.version+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(a.gz)
		return
	}
	w.Write(a.plain)
}
