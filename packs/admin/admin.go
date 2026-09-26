// Package admin serves the app's admin pages at /admin: an overview with
// a setup checklist, users, the credentials of the sign-in, mail,
// language-model and storage providers (saved sealed with the master key
// and applied without a restart) plus the app's own
// (Options.Sections), token usage, the mail outbox, jobs and stored
// files. Mount it from routes.go:
//
//	admin.Mount(r, admin.Options{})
//
// The first account and the users ADMIN_USERS lists (ids or emails,
// comma separated) get in, unless Options.Allow decides otherwise. The
// pages are server-rendered HTML on Tabler (https://tabler.io, MIT),
// light and dark, with their stylesheet, script and icons served from
// the binary. An app themes them with admin/theme.css (the --admin-*
// variables, or any Tabler variable) and replaces the frame with
// admin/layout.html.
package admin

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/credentials"
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
	// the first user ever to sign in is one, and so is anyone whose id
	// or email claim ADMIN_USERS lists (in .env, the credentials, or
	// saved from the Overview page).
	Allow func(ctx context.Context) bool
	// FirstUser returns the id of the app's first account, an admin by
	// default; the auth pack's FirstSubject unless set. Empty means no
	// such rule.
	FirstUser func(ctx context.Context) string
	// NoFirstUserAdmin turns that rule off: only listed users get in.
	NoFirstUserAdmin bool
	// CredentialsDir is where config/credentials.yml.enc lives when no
	// db pack holds the runtime values; "." by default.
	CredentialsDir string
	// Sections add the app's own settings to the Credentials page after
	// the packs': API keys of the services the app calls, feature
	// switches. Each field is an environment name the app reads with
	// pkg/env, saved sealed like the packs' and applied through
	// lidza.Reconfigure.
	Sections []Section
	// Pages add the app's own admin pages (orders to refund, reports,
	// moderation queues) to the sidebar, rendered in the same frame. See
	// Page.
	Pages []Page
	// Templates holds the app's page templates, usually embedded
	// (//go:embed admin/*.html) so they ship in the binary; nil reads
	// them from Dir on disk.
	Templates fs.FS
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
	if opt.FirstUser == nil && !opt.NoFirstUserAdmin {
		opt.FirstUser = firstSubject
	}
	if opt.Allow == nil {
		dir := opt.CredentialsDir
		first := opt.FirstUser
		opt.Allow = func(ctx context.Context) bool {
			u := auth.CurrentUser(ctx)
			if u == nil {
				return false
			}
			if first != nil && first(ctx) != "" && first(ctx) == u.ID {
				return true
			}
			return allowListed(u, adminList(dir))
		}
	}
	h := &Handler{opt: opt, path: strings.TrimSuffix(opt.Path, "/"), mux: http.NewServeMux()}
	p := h.path
	h.mux.HandleFunc("GET "+p+"/{$}", h.overview)
	h.mux.HandleFunc("GET "+p+"/theme.css", h.theme)
	h.mux.HandleFunc("GET "+p+"/assets/{file}", h.serveAsset)
	h.mux.HandleFunc("POST "+p+"/admins", h.saveAdmins)
	h.mux.HandleFunc("GET "+p+"/users", h.users)
	h.mux.HandleFunc("GET "+p+"/users/sign-in", h.packSettings("users", "Users"))
	h.mux.HandleFunc("POST "+p+"/users/{subject}/{action}", h.userAction)
	h.mux.HandleFunc("GET "+p+"/settings", h.appSettings)
	h.mux.HandleFunc("POST "+p+"/settings", h.saveSettings)
	h.mux.HandleFunc("GET "+p+"/credentials", h.credentialsRedirect)
	h.mux.HandleFunc("POST "+p+"/credentials", h.saveSettings)
	h.mux.HandleFunc("GET "+p+"/llm", h.llm)
	h.mux.HandleFunc("GET "+p+"/llm/settings", h.packSettings("llm", "Language model"))
	h.mux.HandleFunc("GET "+p+"/mail", h.mail)
	h.mux.HandleFunc("GET "+p+"/mail/settings", h.packSettings("mail", "Mail"))
	h.mux.HandleFunc("GET "+p+"/jobs", h.jobs)
	h.mux.HandleFunc("POST "+p+"/jobs/{id}/retry", h.retryJob)
	h.mux.HandleFunc("GET "+p+"/storage", h.storage)
	h.mux.HandleFunc("GET "+p+"/storage/settings", h.packSettings("storage", "Storage"))
	h.mountPages()
	h.chain = middleware.Chain(h.mux, opt.Auth, h.gate)
	return h
}

// ServeHTTP implements http.Handler: the stylesheets, the script and the
// theme are public (a sign-in page may use them), everything else is
// behind Auth and Allow.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == h.path+"/theme.css" {
		h.theme(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, h.path+"/assets/") {
		h.serveAsset(w, r)
		return
	}
	h.chain.ServeHTTP(w, r)
}

// gate refuses users Allow does not admit.
func (h *Handler) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.opt.Allow(r.Context()) {
			h.denied(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// firstSubject is the auth pack's first user, "" without the pack or
// before anyone signed in.
func firstSubject(ctx context.Context) string {
	a, ok := lidza.Optional[*auth.Auth](ctx)
	if !ok {
		return ""
	}
	first, _ := a.FirstSubject(ctx)
	return first
}

// adminList reads ADMIN_USERS the way the packs read their settings:
// the .env files, the credentials (the Overview page saves there), the
// process environment. Read on each request, cached for a few seconds.
func adminList(dir string) []string {
	listMu.Lock()
	defer listMu.Unlock()
	if time.Since(listAt) < 3*time.Second && listDir == dir {
		return listCache
	}
	// The union of what the environment names and what the Overview page
	// saved (the credentials), so a variable set at deploy time and an
	// admin added at runtime both count.
	values, _ := env.Values(dir)
	seen := map[string]bool{}
	var out []string
	for _, list := range []string{values[EnvAdminUsers], credentials.Values(dir)[EnvAdminUsers]} {
		for _, entry := range strings.Split(list, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" || seen[strings.ToLower(entry)] {
				continue
			}
			seen[strings.ToLower(entry)] = true
			out = append(out, entry)
		}
	}
	listCache, listAt, listDir = out, time.Now(), dir
	return out
}

var (
	listMu    sync.Mutex
	listCache []string
	listAt    time.Time
	listDir   string
)

// allowListed admits the users the list names, by id or email claim.
func allowListed(u *auth.User, listed []string) bool {
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
	// Problem is an app page's Data error, shown instead of its content.
	Problem string
	Tabs    []tab
	// User is the signed-in admin's email or id; SignOut the route that
	// ends the session, when the app mounted the auth pack's sign-in.
	User    string
	SignOut string
	Data    any
}

// navItem is one entry of the sidebar; Group starts a heading. Status
// colours a dot when the page's settings need a look.
type navItem struct {
	Name, Href, Icon, Group, Status string
}

// tab is one tab of a pack page.
type tab struct {
	Name, Href, Icon, Status string
	Active                   bool
}

// nav lists the pages for the packs that are enabled.
func (h *Handler) nav(ctx context.Context) []navItem {
	status := map[string]string{}
	appSections := false
	if views, err := h.settingsViews(ctx); err == nil {
		for _, v := range views {
			if v.Page == "" {
				appSections = true
				if v.Status == "partial" || v.Status == "off" {
					status[""] = "yellow"
				}
				continue
			}
			if v.Status == "partial" {
				status[v.Page] = "yellow"
			}
		}
	}
	items := []navItem{{Name: "Overview", Href: h.path + "/", Icon: "layout-dashboard"}}
	if _, ok := lidza.Optional[*auth.Auth](ctx); ok {
		items = append(items, navItem{Name: "Users", Href: h.path + "/users", Icon: "users", Status: status["users"]})
	}
	group := "Services"
	add := func(name, page, icon string) {
		items = append(items, navItem{Name: name, Href: h.path + "/" + page, Icon: icon, Group: group, Status: status[page]})
		group = ""
	}
	if _, ok := lidza.Optional[mailService](ctx); ok {
		add("Mail", "mail", "mail")
	}
	if _, ok := lidza.Optional[llmService](ctx); ok {
		add("Language model", "llm", "sparkles")
	}
	if _, ok := lidza.Optional[storageService](ctx); ok {
		add("Storage", "storage", "folder")
	}
	if _, ok := lidza.Optional[jobsService](ctx); ok {
		add("Jobs", "jobs", "list-check")
	}
	appGroup := "App"
	for _, it := range h.appNav(ctx) {
		it.Group, appGroup = appGroup, ""
		items = append(items, it)
	}
	if appSections {
		items = append(items, navItem{Name: "Settings", Href: h.path + "/settings", Icon: "adjustments-horizontal", Group: appGroup, Status: status[""]})
	}
	return items
}

// tabsFor returns the tabs of a pack page; name is the page template.
func (h *Handler) tabsFor(ctx context.Context, name string) []tab {
	pages := map[string][2]string{
		"mail": {"mail", "Outbox"}, "llm": {"llm", "Usage"}, "storage": {"storage", "Files"}, "users": {"users", "Accounts"},
	}
	var page string
	switch name {
	case "mail", "llm", "storage", "users":
		page = name
	case "packsettings":
		return nil // set by packSettings through the data
	default:
		return nil
	}
	sec, _ := h.sectionFor(ctx, page)
	return h.tabs(pages[page][0], pages[page][1], sec, false)
}

// tabs builds a pack page's two tabs: its data, and its settings when the
// pack's section applies.
func (h *Handler) tabs(page, first string, sec *sectionView, settingsActive bool) []tab {
	out := []tab{{Name: first, Href: h.path + "/" + page, Icon: map[string]string{"mail": "inbox", "llm": "chart-bar", "storage": "folder", "users": "users"}[page], Active: !settingsActive}}
	if sec != nil {
		name, href := "Settings", h.path+"/"+page+"/settings"
		if page == "users" {
			name, href = "Sign-in", h.path+"/users/sign-in"
		}
		t := tab{Name: name, Href: href, Icon: "settings", Active: settingsActive}
		if sec.Status == "partial" || sec.Status == "dev" || sec.Status == "off" {
			t.Status = statusColor(sec.Status)
		}
		out = append(out, t)
	}
	return out
}

// funcs are the template functions every page can use.
func (h *Handler) funcs() template.FuncMap {
	return template.FuncMap{
		"since": func(at time.Time) string { return humanSince(at) },
		"short": func(s string) string {
			if len(s) > 80 {
				return s[:80] + "…"
			}
			return s
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"deref": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"icon": icon,
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"asset":    h.assetURL,
		"bytes":    humanBytes,
		"num":      humanNumber,
		"initials": initials,
		"color":    avatarColor,
		"origin":   originLabel,
		"status":   statusColor,
		"float":    func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
		"int":      func(f float64) int64 { return int64(f) },
		"methodIcon": func(m string) string {
			switch m {
			case "password":
				return "password"
			case "google":
				return "brand-google"
			case "github":
				return "brand-github"
			case "microsoft":
				return "brand-windows"
			}
			return "key"
		},
		"fileIcon": func(contentType string) string {
			switch {
			case strings.HasPrefix(contentType, "image/"):
				return "photo"
			case strings.HasPrefix(contentType, "text/"), strings.Contains(contentType, "json"), strings.Contains(contentType, "pdf"):
				return "file-text"
			}
			return "file"
		},
	}
}

// frame builds the shared partials and the frame (the app's
// admin/layout.html when there is one, else the pack's); the page's
// content is added to it.
func (h *Handler) frame(name string) (*template.Template, error) {
	t, err := template.New("").Funcs(h.funcs()).ParseFS(files, "templates/partials.html")
	if err != nil {
		return nil, err
	}
	if custom, rerr := os.ReadFile(filepath.Join(h.opt.Dir, "layout.html")); rerr == nil && name != "denied" {
		t, err = t.Parse(string(custom))
	} else {
		t, err = t.ParseFS(files, "templates/layout.html")
	}
	return t, err
}

// render executes the page template inside the frame.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name, active string, data any) {
	h.renderStatus(w, r, http.StatusOK, name, active, data)
}

func (h *Handler) renderStatus(w http.ResponseWriter, r *http.Request, status int, name, active string, data any) {
	h.renderWith(w, r, status, name, active, data, func(t *template.Template) (*template.Template, error) {
		return t.ParseFS(files, "templates/"+name+".html")
	})
}

// renderWith renders a page whose "content" template addContent adds to
// the frame: the pack's own file, or an app page's.
func (h *Handler) renderWith(w http.ResponseWriter, r *http.Request, status int, name, active string, data any, addContent func(*template.Template) (*template.Template, error)) {
	ctx := r.Context()
	p := page{Title: h.opt.Title, App: h.opt.Title, Path: h.path, Version: version.String(), Active: active, Data: data,
		Flash: r.URL.Query().Get("saved"), Error: r.URL.Query().Get("error")}
	if d, ok := data.(appPageData); ok {
		p.Data = d.Data
		if d.Err != nil {
			p.Problem = d.Err.Error()
		}
	}
	if name != "denied" {
		p.Sections = h.nav(ctx)
		p.Tabs = h.tabsFor(ctx, name)
		if name == "packsettings" {
			if d, ok := data.(map[string]any); ok {
				if sec, ok := d["Section"].(*sectionView); ok && sec != nil {
					first := map[string]string{"mail": "Outbox", "llm": "Usage", "storage": "Files", "users": "Accounts"}[sec.Page]
					p.Tabs = h.tabs(sec.Page, first, sec, true)
				}
			}
		}
	}
	if u := auth.CurrentUser(ctx); u != nil {
		p.User = u.ID
		if email, _ := u.Claims["email"].(string); email != "" {
			p.User = email
		}
	}
	if auth.Mounted() {
		p.SignOut = auth.Prefix + "/logout"
	}
	t, err := h.frame(name)
	if err == nil {
		t, err = addContent(t)
	}
	if err != nil {
		http.Error(w, "admin: template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		lidza.Log(ctx).Error("admin: render", "page", name, "error", err)
		http.Error(w, "admin: render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// denied is the page a signed-in user who is not an admin gets.
func (h *Handler) denied(w http.ResponseWriter, r *http.Request) {
	h.renderStatus(w, r, http.StatusForbidden, "denied", "Not an admin", map[string]any{"Env": EnvAdminUsers})
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "B"
}

// humanNumber groups thousands: 12 345 678 as 12.3M.
func humanNumber(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + "B"
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 10_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// initials are an avatar's letters: "ana@example.com" gives "AN",
// "Ana Kraja" gives "AK".
func initials(s string) string {
	s = strings.TrimSpace(s)
	if at := strings.IndexByte(s, '@'); at > 0 {
		s = s[:at]
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '.' || r == '_' || r == '-' })
	var out []rune
	switch {
	case len(parts) >= 2:
		out = append([]rune(parts[0])[:1], []rune(parts[1])[:1]...)
	case len(parts) == 1:
		rs := []rune(parts[0])
		out = rs[:min(2, len(rs))]
	}
	return strings.ToUpper(string(out))
}

// avatarColor picks one of the palette's light colours for a name, the
// same every time (no blues: the accent is the palette's).
func avatarColor(s string) string {
	colors := []string{"primary", "pink", "red", "orange", "yellow", "lime", "green", "teal"}
	var h uint32
	for _, r := range s {
		h = h*31 + uint32(r)
	}
	return colors[h%uint32(len(colors))]
}

// statusColor is the colour of a settings status.
func statusColor(status string) string {
	switch status {
	case "ok":
		return "green"
	case "partial":
		return "yellow"
	case "dev":
		return "orange"
	}
	return "secondary"
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
