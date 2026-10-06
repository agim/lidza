package devserver

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// compressible are the extensions whose files lidza build stores
// compressed beside the original (name.br, name.gz) and whose responses
// vary by Accept-Encoding. Images, fonts and media are compressed
// already.
var compressible = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true, ".json": true,
	".map": true, ".svg": true, ".txt": true, ".md": true, ".xml": true, ".webmanifest": true,
	".wasm": true, ".csv": true, ".ico": true,
}

// MinCompress is the smallest file or page worth compressing: below it
// the headers outweigh the saving.
const MinCompress = 1024

// encodings are the stored representations (lidza build writes them),
// the preferred first.
var encodings = []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}}

// Compressible reports whether files with this extension are stored
// compressed and served by Accept-Encoding.
func Compressible(ext string) bool { return compressible[strings.ToLower(ext)] }

// acceptable reports whether the Accept-Encoding header admits coding,
// by name or through "*", with a quality above zero.
func acceptable(header, coding string) bool {
	star := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		switch name {
		case coding:
			return q > 0
		case "*":
			star = q > 0
		}
	}
	return star
}

// sendFile serves file name of dist: the stored .br or .gz copy when the
// request accepts it, else the file itself, with the original's type,
// Vary: Accept-Encoding for a compressible file, and an ETag per
// representation, so HEAD, ranges and conditional requests work as
// http.ServeContent makes them.
func sendFile(w http.ResponseWriter, r *http.Request, dist fs.FS, name string, tags *etags) {
	ext := strings.ToLower(path.Ext(name))
	h := w.Header()
	if compressible[ext] {
		h.Add("Vary", "Accept-Encoding")
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		h.Set("Content-Type", ct)
	}
	file, coding := name, ""
	if compressible[ext] {
		for _, e := range encodings {
			if acceptable(r.Header.Get("Accept-Encoding"), e.name) && exists(dist, name+e.ext) {
				file, coding = name+e.ext, e.name
				break
			}
		}
	}
	data, err := fs.ReadFile(dist, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if coding != "" {
		// http.ServeContent sends no length for an encoded body, and a
		// range of one is a range of the compressed bytes no client
		// asks for: the whole copy goes out, with its length.
		h.Set("Content-Encoding", coding)
		h.Set("Content-Length", strconv.Itoa(len(data)))
		if r.Header.Get("Range") != "" {
			whole := *r
			whole.Header = r.Header.Clone()
			whole.Header.Del("Range")
			r = &whole
		}
	}
	h.Set("ETag", tags.of(file, data))
	var mod time.Time
	if info, err := fs.Stat(dist, file); err == nil {
		mod = info.ModTime()
	}
	http.ServeContent(w, r, name, mod, bytes.NewReader(data))
}

// etags remembers each file's ETag: a build's files never change while
// it runs, and there are as many as the build has.
type etags struct {
	mu sync.Mutex
	m  map[string]string
}

func (t *etags) of(name string, data []byte) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tag, ok := t.m[name]; ok {
		return tag
	}
	if t.m == nil {
		t.m = map[string]string{}
	}
	t.m[name] = etag(data)
	return t.m[name]
}

func etag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:12]) + `"`
}

// writePage writes an HTML page built for this request (its head set,
// its locale chosen): gzipped when the request accepts it and the page
// is large enough, with Vary: Accept-Encoding, and an ETag of the page
// so a revalidation of an unchanged page is a 304.
func writePage(w http.ResponseWriter, r *http.Request, status int, page []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Add("Vary", "Accept-Encoding")
	gz := len(page) >= MinCompress && acceptable(r.Header.Get("Accept-Encoding"), "gzip")
	tag := etag(page)
	if gz {
		tag = strings.TrimSuffix(tag, `"`) + `-gzip"`
	}
	if status == http.StatusOK {
		h.Set("ETag", tag)
		if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, tag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	body := page
	if gz {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write(page)
		zw.Close()
		body = buf.Bytes()
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// etagMatches reads If-None-Match: "*" or a list of tags, weak or not.
func etagMatches(header, tag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == tag {
			return true
		}
	}
	return false
}
