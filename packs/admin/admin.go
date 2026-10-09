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
	"math"
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
	// Dir holds the app's theme on disk: Dir/theme.css replaces the
	// variables, Dir/layout.html the frame. "admin" by default. Both are
	// read from Templates first, so an app that embeds them runs from the
	// binary alone.
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
	// OnAudit runs after every admin action, for a staff audit log: each
	// form sent on any page (the pack's own and the app's, with its
	// outcome) and each download. Audit.Form has the secrets redacted. It
	// cannot refuse anything and runs on the request: insert a row.
	OnAudit func(ctx context.Context, a Audit)
	// FirstUser returns the id of the app's first account, an admin by
	// default; the auth pack's FirstSubject unless set. Empty means no
	// such rule.
	FirstUser func(ctx context.Context) string
	// NoFirstUserAdmin turns that rule off: only listed users get in.
	NoFirstUserAdmin bool
	// FoldGroups folds the sidebar's labelled groups (Services and the
	// app's Page.Group sections) but the one holding the page shown; a
	// click opens one. For an app with many pages, so the list fits.
	FoldGroups bool
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
	// Templates holds the app's page templates, theme.css and
	// layout.html, usually the app's admin/ folder embedded
	// (//go:embed admin, then lidza.Sub(files, "admin")) so they ship in
	// the binary. A file it lacks is read from Dir on disk; the theme and
	// the frame then fall back to the pack's own.
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
	// The union of every layer, none hiding another: the environment and
	// .env (a variable set at deploy time), the sealed file deployed with
	// the app (`lidza admin add`), and the list the Users page saved in the
	// database. A saved list would otherwise hide the file's, and an admin
	// added with `lidza admin add` and a deploy would never arrive.
	values, _ := env.Values(dir)
	file, _ := credentials.Read(dir)
	seen := map[string]bool{}
	var out []string
	for _, list := range []string{values[EnvAdminUsers], file[EnvAdminUsers], credentials.Overrides()[EnvAdminUsers]} {
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

// appFile reads one of the app's theme files: from Options.Templates,
// else from Options.Dir on disk.
func (h *Handler) appFile(name string) ([]byte, error) {
	if h.opt.Templates != nil {
		if data, err := fs.ReadFile(h.opt.Templates, name); err == nil {
			return data, nil
		}
	}
	return os.ReadFile(filepath.Join(h.opt.Dir, name))
}

// theme serves the app's theme.css (Templates, then Dir), else the
// pack's.
func (h *Handler) theme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if data, err := h.appFile("theme.css"); err == nil {
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
	Sections []navGroup
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
	// Back is the view's query ("?order=1001"), for a form's hidden
	// "back" field: its action returns to this view.
	Back string
}

// navItem is one entry of the sidebar; Group starts a heading. Status
// colours a dot when the page's settings need a look.
type navItem struct {
	Name, Href, Icon, Group, Status string
}

// navGroup is one labelled section of the sidebar (none for the first:
// Overview and Users), folded with <details> when Options.FoldGroups
// asks and it does not hold the page shown.
type navGroup struct {
	Label string
	Items []navItem
	Open  bool
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
	// The app's pages, in their groups in first-seen order; those without
	// a group, and the app's settings, under "App".
	var order []string
	byGroup := map[string][]navItem{}
	for i, it := range h.appNav(ctx) {
		g := h.opt.Pages[i].Group
		if g == "" {
			g = "App"
		}
		if _, seen := byGroup[g]; !seen {
			order = append(order, g)
		}
		it.Group = g
		byGroup[g] = append(byGroup[g], it)
	}
	if appSections {
		if _, seen := byGroup["App"]; !seen {
			order = append(order, "App")
		}
		byGroup["App"] = append(byGroup["App"], navItem{Name: "Settings", Href: h.path + "/settings", Icon: "adjustments-horizontal", Group: "App", Status: status[""]})
	}
	for _, g := range order {
		items = append(items, byGroup[g]...)
	}
	return items
}

// groups lays the sidebar out in labelled sections, each open unless
// Options.FoldGroups folds it; the section of the active page is open.
func (h *Handler) groups(items []navItem, active string) []navGroup {
	var out []navGroup
	label := ""
	for i, it := range items {
		if it.Group != "" && it.Group != label || i == 0 {
			label = it.Group
			out = append(out, navGroup{Label: label, Open: !h.opt.FoldGroups || label == ""})
		}
		g := &out[len(out)-1]
		g.Items = append(g.Items, it)
		if it.Name == active {
			g.Open = true
		}
	}
	return out
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
	sec, more, _ := h.sectionFor(ctx, page)
	return h.tabs(pages[page][0], pages[page][1], sec, more, false)
}

// tabs builds a pack page's two tabs: its data, and its settings when the
// pack's section applies. The settings tab's dot is the first section's
// status, or a later section's when that one needs a look.
func (h *Handler) tabs(page, first string, sec *sectionView, more []sectionView, settingsActive bool) []tab {
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
		for _, m := range more {
			if m.Status == "partial" {
				t.Status = statusColor(m.Status)
			}
		}
		out = append(out, t)
	}
	return out
}

// funcs are the template functions every page can use.
func (h *Handler) funcs() template.FuncMap {
	return template.FuncMap{
		"since": func(at time.Time) string { return humanSince(at) },
		"until": humanUntil,
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
		// navItemOf pairs a sidebar entry with whether it is the page shown.
		"navItemOf": func(it navItem, active string) map[string]any {
			return map[string]any{"Name": it.Name, "Href": it.Href, "Icon": it.Icon, "Status": it.Status, "Active": it.Name == active}
		},
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

// frame builds the shared partials and the frame (the app's layout.html
// from Templates or Dir when there is one, else the pack's); the page's
// content is added to it.
func (h *Handler) frame(name string) (*template.Template, error) {
	t, err := template.New("").Funcs(h.funcs()).ParseFS(files, "templates/partials.html")
	if err != nil {
		return nil, err
	}
	if custom, rerr := h.appFile("layout.html"); rerr == nil && name != "denied" {
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
		Flash: r.URL.Query().Get("saved"), Error: r.URL.Query().Get("error"), Back: viewQuery("?" + r.URL.RawQuery)}
	if d, ok := data.(appPageData); ok {
		p.Data = d.Data
		if d.Err != nil {
			p.Problem = d.Err.Error()
		}
	}
	if name != "denied" {
		p.Sections = h.groups(h.nav(ctx), active)
		p.Tabs = h.tabsFor(ctx, name)
		if name == "packsettings" {
			if d, ok := data.(map[string]any); ok {
				if sec, ok := d["Section"].(*sectionView); ok && sec != nil {
					first := map[string]string{"mail": "Outbox", "llm": "Usage", "storage": "Files", "users": "Accounts"}[sec.Page]
					more, _ := d["More"].([]sectionView)
					p.Tabs = h.tabs(sec.Page, first, sec, more, true)
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
	// In development the first account is often a test's or a
	// screenshot script's, so the page says how to add yourself.
	h.renderStatus(w, r, http.StatusForbidden, "denied", "Not an admin", map[string]any{"Env": EnvAdminUsers, "Dev": env.Mode() == "dev"})
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

// humanNumber shortens a count: 12 345 678 as 12.3M. It takes any
// integer or float type, so a template can pass a query's int32 or a
// float64 average as it comes.
func humanNumber(v any) string {
	var n float64
	switch x := v.(type) {
	case int:
		n = float64(x)
	case int8:
		n = float64(x)
	case int16:
		n = float64(x)
	case int32:
		n = float64(x)
	case int64:
		n = float64(x)
	case uint:
		n = float64(x)
	case uint8:
		n = float64(x)
	case uint16:
		n = float64(x)
	case uint32:
		n = float64(x)
	case uint64:
		n = float64(x)
	case float32:
		n = float64(x)
	case float64:
		n = x
	case nil:
		return "0"
	default:
		return fmt.Sprint(v)
	}
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	switch {
	case n >= 1_000_000_000:
		return sign + strconv.FormatFloat(n/1e9, 'f', 1, 64) + "B"
	case n >= 1_000_000:
		return sign + strconv.FormatFloat(n/1e6, 'f', 1, 64) + "M"
	case n >= 10_000:
		return sign + strconv.FormatFloat(n/1e3, 'f', 1, 64) + "k"
	case n == math.Trunc(n):
		return sign + strconv.FormatFloat(n, 'f', 0, 64)
	}
	return sign + strconv.FormatFloat(n, 'f', 1, 64)
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

// humanUntil is a time ahead in the same terms: "in 5m", "in 3h", "due".
func humanUntil(at time.Time) string {
	d := time.Until(at)
	switch {
	case d <= 0:
		return "due"
	case d < time.Minute:
		return "in under a minute"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %d days", int(d.Hours()/24))
	}
}

// redirect sends the browser back to a page with a message.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, to, flash, errText string) {
	if r.Method != http.MethodGet {
		h.audit(r, Audit{Message: flash, Error: errText})
	}
	q, sep := "", "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	if flash != "" {
		q = sep + "saved=" + template.URLQueryEscaper(flash)
	} else if errText != "" {
		q = sep + "error=" + template.URLQueryEscaper(errText)
	}
	http.Redirect(w, r, h.path+to+q, http.StatusSeeOther)
}

// templateFS exposes the embedded templates, for an app that wants to
// start its layout.html from the pack's.
func TemplateFS() fs.FS { return files }
