// Package admin serves the app's admin pages at /admin: an overview of
// the packs, the credentials of the mail, language-model and storage
// providers (saved sealed with the master key and applied without a
// restart), token usage, the mail outbox, jobs and stored files. Mount it
// from routes.go:
//
//	admin.Mount(r, admin.Options{})
//
// Only signed-in users listed in ADMIN_USERS (ids or emails, comma
// separated) get in, unless Options.Allow decides otherwise. The pages
// are plain HTML from the pack's own templates; an app themes them with
// admin/theme.css (CSS variables) and replaces the frame with
// admin/layout.html.
package admin

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/version"
)

//go:embed templates/*.html templates/theme.css
var files embed.FS

// Options configure Mount.
type Options struct {
	// Path is where the pages live; "/admin" by default.
	Path string
	// Dir holds the app's theme: Dir/theme.css replaces the variables,
	// Dir/layout.html the frame. "admin" by default.
	Dir string
	// Title heads every page; the app's name is a good one. "Admin" by
	// default.
	Title string
	// Auth protects the pages; auth.Require() by default. Tests pass a
	// no-op.
	Auth middleware.Middleware
	// Allow decides whether the signed-in user is an admin. By default
	// the user's id or email claim must be listed in ADMIN_USERS.
	Allow func(ctx context.Context) bool
	// CredentialsDir is where config/credentials.yml.enc lives when no
	// db pack holds the runtime values; "." by default.
	CredentialsDir string
}

// EnvAdminUsers lists who may open the admin pages.
const EnvAdminUsers = "ADMIN_USERS"

// Mount registers the pages on r at Options.Path.
func Mount(r *router.Router, opt Options) {
	h := New(opt)
	r.Mount(h.path+"/", h)
}

// Handler serves the pages.
type Handler struct {
	opt   Options
	path  string
	mux   *http.ServeMux
	chain http.Handler
}

// New builds the handler without mounting it.
func New(opt Options) *Handler {
	if opt.Path == "" {
		opt.Path = "/admin"
	}
	if opt.Dir == "" {
		opt.Dir = "admin"
	}
	if opt.Title == "" {
		opt.Title = "Admin"
	}
	if opt.Auth == nil {
		opt.Auth = auth.Require()
	}
	if opt.CredentialsDir == "" {
		opt.CredentialsDir = "."
	}
	if opt.Allow == nil {
		listed := adminList(opt.CredentialsDir)
		opt.Allow = func(ctx context.Context) bool { return allowListed(ctx, listed) }
	}
	h := &Handler{opt: opt, path: strings.TrimSuffix(opt.Path, "/"), mux: http.NewServeMux()}
	p := h.path
	h.mux.HandleFunc("GET "+p+"/{$}", h.overview)
	h.mux.HandleFunc("GET "+p+"/theme.css", h.theme)
	h.mux.HandleFunc("GET "+p+"/credentials", h.credentials)
	h.mux.HandleFunc("POST "+p+"/credentials", h.saveCredentials)
	h.mux.HandleFunc("GET "+p+"/llm", h.llm)
	h.mux.HandleFunc("GET "+p+"/mail", h.mail)
	h.mux.HandleFunc("GET "+p+"/jobs", h.jobs)
	h.mux.HandleFunc("POST "+p+"/jobs/{id}/retry", h.retryJob)
	h.mux.HandleFunc("GET "+p+"/storage", h.storage)
	h.chain = middleware.Chain(h.mux, opt.Auth, h.gate)
	return h
}

// ServeHTTP implements http.Handler: the stylesheet is public (a login
// page may use it), everything else is behind Auth and Allow.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == h.path+"/theme.css" {
		h.theme(w, r)
		return
	}
	h.chain.ServeHTTP(w, r)
}

// gate refuses users Allow does not admit.
func (h *Handler) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.opt.Allow(r.Context()) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, "<!doctype html><title>Not an admin</title><p>This account is not an admin. List admins in <code>%s</code> (ids or emails), or set admin.Options.Allow.</p>", EnvAdminUsers)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminList reads ADMIN_USERS the way the packs read their settings:
// the .env files, the credentials, the process environment.
func adminList(dir string) []string {
	values, _ := env.Values(dir)
	var out []string
	for _, entry := range strings.Split(values[EnvAdminUsers], ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// allowListed admits the users the list names, by id or email claim.
func allowListed(ctx context.Context, listed []string) bool {
	u := auth.CurrentUser(ctx)
	if u == nil {
		return false
	}
	email, _ := u.Claims["email"].(string)
	for _, entry := range listed {
		if entry == u.ID || strings.EqualFold(entry, email) {
			return true
		}
	}
	return false
}

// theme serves the app's admin/theme.css, else the pack's.
func (h *Handler) theme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if data, err := os.ReadFile(filepath.Join(h.opt.Dir, "theme.css")); err == nil {
		w.Write(data)
		return
	}
	data, _ := files.ReadFile("templates/theme.css")
	w.Write(data)
}

// page is what every template receives.
type page struct {
	Title    string
	App      string
	Path     string
	Version  string
	Active   string
	Sections []navItem
	Flash    string
	Error    string
	Data     any
}

type navItem struct{ Name, Href string }

// nav lists the pages for the packs that are enabled.
func (h *Handler) nav(ctx context.Context) []navItem {
	items := []navItem{{"Overview", h.path + "/"}, {"Credentials", h.path + "/credentials"}}
	if _, ok := lidza.Optional[llmService](ctx); ok {
		items = append(items, navItem{"Language model", h.path + "/llm"})
	}
	if _, ok := lidza.Optional[mailService](ctx); ok {
		items = append(items, navItem{"Mail", h.path + "/mail"})
	}
	if _, ok := lidza.Optional[jobsService](ctx); ok {
		items = append(items, navItem{"Jobs", h.path + "/jobs"})
	}
	if _, ok := lidza.Optional[storageService](ctx); ok {
		items = append(items, navItem{"Storage", h.path + "/storage"})
	}
	return items
}

// render executes the page template inside the frame: the app's
// admin/layout.html when there is one, else the pack's.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name, active string, data any) {
	p := page{Title: h.opt.Title, App: h.opt.Title, Path: h.path, Version: version.String(), Active: active, Sections: h.nav(r.Context()), Data: data,
		Flash: r.URL.Query().Get("saved"), Error: r.URL.Query().Get("error")}
	t := template.New("").Funcs(template.FuncMap{
		"since": func(at time.Time) string { return humanSince(at) },
		"short": func(s string) string {
			if len(s) > 80 {
				return s[:80] + "…"
			}
			return s
		},
		"deref": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
	})
	var err error
	if custom, rerr := os.ReadFile(filepath.Join(h.opt.Dir, "layout.html")); rerr == nil {
		t, err = t.Parse(string(custom))
	} else {
		t, err = t.ParseFS(files, "templates/layout.html")
	}
	if err == nil {
		t, err = t.ParseFS(files, "templates/"+name+".html")
	}
	if err != nil {
		http.Error(w, "admin: template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := t.ExecuteTemplate(w, "layout", p); err != nil {
		lidza.Log(r.Context()).Error("admin: render", "page", name, "error", err)
	}
}

func humanSince(at time.Time) string {
	d := time.Since(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return at.Format("2006-01-02")
	}
}

// redirect sends the browser back to a page with a message.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, to, flash, errText string) {
	q := ""
	if flash != "" {
		q = "?saved=" + template.URLQueryEscaper(flash)
	} else if errText != "" {
		q = "?error=" + template.URLQueryEscaper(errText)
	}
	http.Redirect(w, r, h.path+to+q, http.StatusSeeOther)
}

// templateFS exposes the embedded templates, for an app that wants to
// start its layout.html from the pack's.
func TemplateFS() fs.FS { return files }
