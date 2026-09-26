package admin

import (
	"context"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/jobs"
	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/packs/storage"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/env"
)

// The packs the pages know, by the type they provide.
type (
	llmService     = *llm.LLM
	mailService    = *mail.Mail
	jobsService    = *jobs.Queue
	storageService = *storage.Storage
)

// field is one setting on the credentials page.
type field struct {
	Name    string
	Label   string
	Kind    string // text, secret, select
	Options []string
	Help    string
	Value   string // current, for text and select
	Set     bool   // a secret has a value
}

// section groups the fields of one pack.
type section struct {
	Title  string
	Pack   string
	Fields []field
}

var sections = []section{
	{Title: "Sign-in providers", Pack: "auth", Fields: []field{
		{Name: "AUTH_PROVIDERS", Label: "Providers", Kind: "text", Help: "Comma-separated: google, github, microsoft, or a name with AUTH_<NAME>_ISSUER (an OIDC issuer) set with lidza credentials set. Served by auth.Mount; the callback is <APP_URL>/api/v1/auth/<name>/callback."},
		{Name: "AUTH_GOOGLE_CLIENT_ID", Label: "Google client id", Kind: "text"},
		{Name: "AUTH_GOOGLE_CLIENT_SECRET", Label: "Google client secret", Kind: "secret"},
		{Name: "AUTH_GITHUB_CLIENT_ID", Label: "GitHub client id", Kind: "text"},
		{Name: "AUTH_GITHUB_CLIENT_SECRET", Label: "GitHub client secret", Kind: "secret"},
		{Name: "AUTH_MICROSOFT_CLIENT_ID", Label: "Microsoft client id", Kind: "text"},
		{Name: "AUTH_MICROSOFT_CLIENT_SECRET", Label: "Microsoft client secret", Kind: "secret"},
		{Name: "AUTH_MICROSOFT_TENANT", Label: "Microsoft tenant", Kind: "text", Help: "common, organizations, consumers or a tenant id."},
	}},
	{Title: "Mail", Pack: "mail", Fields: []field{
		{Name: "MAIL_PROVIDER", Label: "Provider", Kind: "select", Options: mail.Providers, Help: "log prints messages; outbox keeps them; the others deliver."},
		{Name: "MAIL_FROM", Label: "From", Kind: "text", Help: "Name <address> or an address."},
		{Name: "MAIL_API_KEY", Label: "API key", Kind: "secret", Help: "Mailgun, SendGrid, Postmark, Resend."},
		{Name: "MAIL_DOMAIN", Label: "Sending domain", Kind: "text", Help: "Mailgun."},
		{Name: "MAIL_SMTP_URL", Label: "SMTP URL", Kind: "secret", Help: "smtp://user:pass@host:587 or smtps://...:465."},
		{Name: "MAIL_BASE_URL", Label: "API base", Kind: "text", Help: "A provider's regional API (Mailgun EU)."},
	}},
	{Title: "Language model", Pack: "llm", Fields: []field{
		{Name: "LLM_PROVIDER", Label: "Provider", Kind: "select", Options: llm.Providers, Help: "fake echoes; ollama is local; the others need a key."},
		{Name: "LLM_MODEL", Label: "Model", Kind: "text", Help: "Empty picks the provider's default."},
		{Name: "LLM_API_KEY", Label: "API key", Kind: "secret", Help: "Anthropic, OpenAI, Google."},
		{Name: "LLM_BASE_URL", Label: "API base", Kind: "text", Help: "A proxy or region; Ollama elsewhere than 127.0.0.1:11434."},
		{Name: "LLM_EMBED_MODEL", Label: "Embedding model", Kind: "text", Help: "Empty picks the provider's default."},
	}},
	{Title: "Storage", Pack: "storage", Fields: []field{
		{Name: "STORAGE_PROVIDER", Label: "Provider", Kind: "select", Options: storage.Providers, Help: "local keeps files in a directory; s3 is any S3-compatible service."},
		{Name: "STORAGE_BUCKET", Label: "Bucket", Kind: "text"},
		{Name: "STORAGE_ENDPOINT", Label: "Endpoint", Kind: "text", Help: "https://s3.amazonaws.com, https://<account>.r2.cloudflarestorage.com, http://127.0.0.1:9000."},
		{Name: "STORAGE_REGION", Label: "Region", Kind: "text", Help: "us-east-1 unless the service says otherwise."},
		{Name: "STORAGE_ACCESS_KEY", Label: "Access key", Kind: "secret"},
		{Name: "STORAGE_SECRET_KEY", Label: "Secret key", Kind: "secret"},
		{Name: "STORAGE_PUBLIC_URL", Label: "Public URL", Kind: "text", Help: "A CDN or public bucket for URL(key)."},
	}},
}

// enabled reports whether a section's pack runs.
func enabled(ctx context.Context, pack string) bool {
	switch pack {
	case "auth":
		// The section matters once the app mounts the pack's sign-in.
		_, ok := lidza.Optional[*auth.Auth](ctx)
		return ok && auth.Mounted()
	case "mail":
		_, ok := lidza.Optional[mailService](ctx)
		return ok
	case "llm":
		_, ok := lidza.Optional[llmService](ctx)
		return ok
	case "storage":
		_, ok := lidza.Optional[storageService](ctx)
		return ok
	}
	return false
}

// store is where saved values go: the db pack's sealed table when it
// runs, else the credentials file.
func (h *Handler) store(ctx context.Context) credentials.Store {
	if s, ok := lidza.Optional[credentials.Store](ctx); ok {
		return s
	}
	return fileStore{dir: h.opt.CredentialsDir}
}

type fileStore struct{ dir string }

func (f fileStore) Save(ctx context.Context, name, value string) error {
	if !credentials.HasKey(f.dir) {
		if _, err := credentials.Generate(f.dir); err != nil {
			return err
		}
	}
	return credentials.Set(f.dir, map[string]string{name: value})
}
func (f fileStore) Delete(ctx context.Context, name string) error {
	return credentials.Unset(f.dir, name)
}
func (f fileStore) Names(ctx context.Context) ([]string, error) {
	return credentials.Names(f.dir), nil
}

type overviewData struct {
	FirstUser   string
	Admins      []string
	You         string
	Packs       []string
	Mail        string
	LLM         string
	Storage     string
	TLSDomains  string
	AppURL      string
	Credentials int
	HasKey      bool
	Store       string
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := overviewData{TLSDomains: os.Getenv("LIDZA_TLS_DOMAINS"), AppURL: os.Getenv("APP_URL"), HasKey: credentials.HasKey(h.opt.CredentialsDir), Store: "file", Admins: adminList(h.opt.CredentialsDir)}
	if h.opt.FirstUser != nil {
		d.FirstUser = h.opt.FirstUser(ctx)
	}
	if u := auth.CurrentUser(ctx); u != nil {
		d.You = u.ID
		if email, _ := u.Claims["email"].(string); email != "" {
			d.You = email + " (" + u.ID + ")"
		}
	}
	if s := lidza.ServicesFrom(ctx); s != nil {
		s.Each(func(v any) {
			if p, ok := v.(lidza.Pack); ok {
				d.Packs = append(d.Packs, p.Name())
			}
		})
	}
	if m, ok := lidza.Optional[mailService](ctx); ok {
		d.Mail = m.Provider()
	}
	if l, ok := lidza.Optional[llmService](ctx); ok {
		d.LLM = l.Provider() + " " + l.Model()
	}
	if st, ok := lidza.Optional[storageService](ctx); ok {
		d.Storage = st.Provider()
	}
	if _, ok := lidza.Optional[credentials.Store](ctx); ok {
		d.Store = "database"
	}
	d.Credentials = len(credentials.Names(h.opt.CredentialsDir)) + len(credentials.Overrides())
	h.render(w, r, "overview", "Overview", d)
}

// saveAdmins adds or removes an admin: ADMIN_USERS in the credentials
// store, applied at once (the list is read on each request).
func (h *Handler) saveAdmins(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		h.redirect(w, r, "/", "", "bad form")
		return
	}
	entry := strings.TrimSpace(r.Form.Get("admin"))
	if entry == "" {
		h.redirect(w, r, "/", "", "an email or a user id is needed")
		return
	}
	// Only the saved list changes; a name the environment sets stays.
	remove := r.Form.Get("remove") == "on"
	if err := h.setAdmin(ctx, entry, !remove); err != nil {
		h.redirect(w, r, "/", "", err.Error())
		return
	}
	if remove {
		h.redirect(w, r, "/", entry+" is no longer an admin", "")
		return
	}
	h.redirect(w, r, "/", entry+" is an admin", "")
}

// userRow is an account with what the page adds: whether it is an
// admin, and the entry the admin list would use for it.
type userRow struct {
	auth.Account
	Admin bool
	Entry string
	First bool
	You   bool
	// Methods are how the account signs in (password, google), for an
	// account of the pack's own sign-in.
	Methods string
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := lidza.Optional[*auth.Auth](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query().Get("q")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const per = 50
	accounts, total, err := a.Accounts(ctx, q, per, (page-1)*per)
	data := map[string]any{"Query": q, "Page": page, "Total": total, "Pages": (total + per - 1) / per}
	if err != nil {
		data["Error"] = err.Error()
	}
	admins := adminList(h.opt.CredentialsDir)
	first := ""
	if h.opt.FirstUser != nil {
		first = h.opt.FirstUser(ctx)
	}
	you := ""
	if u := auth.CurrentUser(ctx); u != nil {
		you = u.ID
	}
	var rows []userRow
	for _, ac := range accounts {
		row := userRow{Account: ac, Entry: ac.Subject, First: ac.Subject == first, You: ac.Subject == you}
		if ac.Label != "" {
			row.Entry = ac.Label
		}
		for _, e := range admins {
			if e == ac.Subject || strings.EqualFold(e, ac.Label) {
				row.Admin = true
			}
		}
		if methods, err := a.SignInMethods(ctx, ac.Subject); err == nil {
			row.Methods = strings.Join(methods, ", ")
		}
		rows = append(rows, row)
	}
	data["Rows"] = rows
	h.render(w, r, "users", "Users", data)
}

// userAction is one of revoke (sign out everywhere), disable, enable,
// admin and unadmin on an account.
func (h *Handler) userAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := lidza.Optional[*auth.Auth](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	subject, action := r.PathValue("subject"), r.PathValue("action")
	back := "/users"
	if q := r.URL.Query().Get("q"); q != "" {
		back += "?q=" + template.URLQueryEscaper(q)
	}
	if u := auth.CurrentUser(ctx); u != nil && u.ID == subject && (action == "disable" || action == "unadmin") {
		h.redirect(w, r, back, "", "not on your own account")
		return
	}
	account, err := a.AccountOf(ctx, subject)
	if err != nil {
		h.redirect(w, r, back, "", "no such account")
		return
	}
	entry := account.Subject
	if account.Label != "" {
		entry = account.Label
	}
	var msg string
	switch action {
	case "revoke":
		err, msg = a.RevokeAll(ctx, subject), entry+" signed out everywhere"
	case "disable":
		err, msg = a.Disable(ctx, subject), entry+" disabled: signed out, cannot sign in"
	case "enable":
		err, msg = a.Enable(ctx, subject), entry+" enabled"
	case "admin", "unadmin":
		err, msg = h.setAdmin(ctx, entry, action == "admin"), entry+" is an admin"
		if action == "unadmin" {
			msg = entry + " is no longer an admin"
		}
	default:
		h.redirect(w, r, back, "", "unknown action")
		return
	}
	if err != nil {
		h.redirect(w, r, back, "", err.Error())
		return
	}
	h.redirect(w, r, back, msg, "")
}

// setAdmin adds or removes an entry of the saved admin list.
func (h *Handler) setAdmin(ctx context.Context, entry string, add bool) error {
	var next []string
	for _, a := range strings.Split(credentials.Values(h.opt.CredentialsDir)[EnvAdminUsers], ",") {
		if a = strings.TrimSpace(a); a != "" && !strings.EqualFold(a, entry) {
			next = append(next, a)
		}
	}
	if add {
		next = append(next, entry)
	}
	if err := h.store(ctx).Save(ctx, EnvAdminUsers, strings.Join(next, ",")); err != nil {
		return err
	}
	listMu.Lock()
	listAt = time.Time{}
	listMu.Unlock()
	return nil
}

func (h *Handler) credentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	values, _ := env.Values(h.opt.CredentialsDir)
	var out []section
	for _, s := range sections {
		if !enabled(ctx, s.Pack) {
			continue
		}
		sec := section{Title: s.Title, Pack: s.Pack}
		for _, f := range s.Fields {
			f.Value = values[f.Name]
			if f.Kind == "secret" {
				f.Set = f.Value != ""
				f.Value = ""
			}
			sec.Fields = append(sec.Fields, f)
		}
		out = append(out, sec)
	}
	h.render(w, r, "credentials", "Credentials", map[string]any{"Sections": out, "HasKey": credentials.HasKey(h.opt.CredentialsDir)})
}

// saveCredentials stores every submitted value that changed (an empty
// secret keeps the stored one; "clear" removes it), then asks the packs
// to read their configuration again.
func (h *Handler) saveCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		h.redirect(w, r, "/credentials", "", "bad form")
		return
	}
	store := h.store(ctx)
	current, _ := env.Values(h.opt.CredentialsDir)
	saved := 0
	for _, s := range sections {
		if !enabled(ctx, s.Pack) || r.Form.Get("section") != s.Pack {
			continue
		}
		for _, f := range s.Fields {
			if r.Form.Get("clear_"+f.Name) == "on" {
				if err := store.Delete(ctx, f.Name); err != nil {
					h.redirect(w, r, "/credentials", "", err.Error())
					return
				}
				saved++
				continue
			}
			v := strings.TrimSpace(r.Form.Get(f.Name))
			if v == "" || v == current[f.Name] {
				continue
			}
			if err := store.Save(ctx, f.Name, v); err != nil {
				h.redirect(w, r, "/credentials", "", err.Error())
				return
			}
			saved++
		}
	}
	if saved == 0 {
		h.redirect(w, r, "/credentials", "nothing changed", "")
		return
	}
	if s := lidza.ServicesFrom(ctx); s != nil {
		if err := lidza.Reconfigure(ctx, s); err != nil {
			h.redirect(w, r, "/credentials", "", "saved, but a pack refused the new settings: "+err.Error())
			return
		}
	}
	h.redirect(w, r, "/credentials", "saved and applied", "")
}

func (h *Handler) llm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, ok := lidza.Optional[llmService](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Provider": l.Provider(), "Model": l.Model(), "Stats": l.TelemetryStats()}
	if rows, err := l.Usage(ctx, time.Now().AddDate(0, 0, -30)); err == nil {
		var in, out, calls int64
		for _, row := range rows {
			in, out, calls = in+row.Input, out+row.Output, calls+row.Calls
		}
		data["Rows"], data["Input"], data["Output"], data["Calls"] = rows, in, out, calls
		if recent, err := l.RecentCalls(ctx, 30); err == nil {
			data["Recent"] = recent
		}
	} else {
		data["NoUsage"] = err.Error()
	}
	h.render(w, r, "llm", "Language model", data)
}

func (h *Handler) mail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, ok := lidza.Optional[mailService](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Provider": m.Provider(), "Templates": m.Templates()}
	if rows, err := m.Outbox(ctx, 50); err == nil {
		data["Rows"] = rows
	} else {
		data["NoOutbox"] = err.Error()
	}
	h.render(w, r, "mail", "Mail", data)
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q, ok := lidza.Optional[jobsService](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Stats": q.TelemetryStats()}
	if rows, err := q.Recent(ctx, 50); err == nil {
		data["Rows"] = rows
	} else {
		data["Error"] = err.Error()
	}
	h.render(w, r, "jobs", "Jobs", data)
}

func (h *Handler) retryJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q, ok := lidza.Optional[jobsService](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := q.Retry(ctx, r.PathValue("id")); err != nil {
		h.redirect(w, r, "/jobs", "", err.Error())
		return
	}
	h.redirect(w, r, "/jobs", "job queued again", "")
}

func (h *Handler) storage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, ok := lidza.Optional[storageService](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	data := map[string]any{"Provider": st.Provider(), "Prefix": prefix}
	if rows, err := st.List(ctx, prefix, 200); err == nil {
		data["Rows"] = rows
	} else {
		data["Error"] = err.Error()
	}
	h.render(w, r, "storage", "Storage", data)
}
