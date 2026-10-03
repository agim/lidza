package admin

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Audit is one admin action, as Options.OnAudit sees it: a form sent on
// any page (the pack's own and the app's) or a download.
type Audit struct {
	// Path is the action's address under the admin pages, without the
	// prefix: "/users/<subject>/disable", "/settings", "/orders/refund",
	// "/orders/export".
	Path string
	// Form is what the form sent, secrets redacted: a secret setting's
	// value, every credential value, and any field named like a password,
	// secret, token, key or code read "[redacted]"; long values are cut
	// to 500 characters; an uploaded file is "file: name (bytes)".
	Form url.Values
	// Message is the page's confirmation; Error its alert. One is set for
	// a form; neither for a download, whose Status says how it went.
	Message, Error string
	// Download reports a download; Status is its HTTP status.
	Download bool
	Status   int
	// Request is the request (the admin, the client address).
	Request *http.Request
}

var secretField = regexp.MustCompile(`(?i)pass|secret|token|key|otp|code|signature|credential`)

// audit reports a finished action to OnAudit; a panic there is logged.
func (h *Handler) audit(r *http.Request, e Audit) {
	if h.opt.OnAudit == nil {
		return
	}
	e.Path = strings.TrimPrefix(r.URL.Path, h.path)
	e.Request = r
	if !e.Download {
		e.Form = h.auditForm(r.Context(), r)
	}
	defer func() {
		if v := recover(); v != nil {
			slog.Error("admin: OnAudit panicked", "path", e.Path, "panic", v)
		}
	}()
	h.opt.OnAudit(r.Context(), e)
}

// auditForm is the posted form with its secrets redacted.
func (h *Handler) auditForm(ctx context.Context, r *http.Request) url.Values {
	secrets := map[string]bool{}
	all := strings.TrimPrefix(r.URL.Path, h.path) == "/credentials"
	for _, s := range h.enabledSections(ctx) {
		for _, f := range s.Fields {
			if f.Kind == "secret" {
				secrets[f.Name] = true
			}
		}
	}
	out := url.Values{}
	keep := map[string]bool{"section": true, "back": true}
	for k, vs := range r.PostForm {
		for _, v := range vs {
			switch {
			case v == "" || keep[k]:
			case all || secrets[k] || secretField.MatchString(k):
				v = "[redacted]"
			case len(v) > 500:
				v = v[:500] + "…"
			}
			out.Add(k, v)
		}
	}
	if r.MultipartForm != nil {
		names := make([]string, 0, len(r.MultipartForm.File))
		for k := range r.MultipartForm.File {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			for _, fh := range r.MultipartForm.File[k] {
				out.Add(k, "file: "+fh.Filename+" ("+strconv.FormatInt(fh.Size, 10)+" bytes)")
			}
		}
	}
	return out
}

// statusWriter keeps a download's status for the audit.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// audited serves a download and reports it.
func (h *Handler) audited(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		fn(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		h.audit(r, Audit{Download: true, Status: sw.status})
	}
}
