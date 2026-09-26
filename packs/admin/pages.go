package admin

import (
	"context"
	"html/template"
	"net/http"
	"slices"
	"sort"
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

// packCard is one pack's state on the Overview page.
type packCard struct {
	Title, Icon, Provider, Status, StatusText, Href, Hint string
}

// checkItem is one step of the setup checklist.
type checkItem struct {
	Title, Help, Href string
	Done              bool
}

type overviewData struct {
	FirstUser   string
	FirstLabel  string
	Admins      []string
	You         string
	Packs       []string
	Cards       []packCard
	Checklist   []checkItem
	Done        int
	Percent     int
	Users       int
	Mode        string
	TLSDomains  string
	AppURL      string
	Credentials int
	HasKey      bool
	Store       string
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	values, _ := env.Values(h.opt.CredentialsDir)
	d := overviewData{TLSDomains: values["LIDZA_TLS_DOMAINS"], AppURL: values["APP_URL"], Mode: env.Mode(), HasKey: credentials.HasKey(h.opt.CredentialsDir), Store: "credentials file", Admins: adminList(h.opt.CredentialsDir)}
	if h.opt.FirstUser != nil {
		d.FirstUser = h.opt.FirstUser(ctx)
		// Shown by its label (the email) when the auth pack knows it.
		if a, ok := lidza.Optional[*auth.Auth](ctx); ok && d.FirstUser != "" {
			if ac, err := a.AccountOf(ctx, d.FirstUser); err == nil && ac.Label != "" {
				d.FirstLabel = ac.Label
			}
		}
	}
	if u := auth.CurrentUser(ctx); u != nil {
		d.You = u.ID
		if email, _ := u.Claims["email"].(string); email != "" {
			d.You = email
		}
	}
	if s := lidza.ServicesFrom(ctx); s != nil {
		s.Each(func(v any) {
			if p, ok := v.(lidza.Pack); ok {
				d.Packs = append(d.Packs, strings.TrimPrefix(p.Name(), "lidza/"))
			}
		})
	}
	sort.Strings(d.Packs)
	if _, ok := lidza.Optional[credentials.Store](ctx); ok {
		d.Store = "database"
	}
	d.Credentials = len(credentials.Names(h.opt.CredentialsDir))
	if a, ok := lidza.Optional[*auth.Auth](ctx); ok {
		_, d.Users, _ = a.Accounts(ctx, "", 1, 0)
	}

	// The packs' cards and the checklist come from the settings.
	views, _ := h.settingsViews(ctx)
	byKey := map[string]sectionView{}
	for _, v := range views {
		byKey[v.Key] = v
	}
	for _, c := range []struct{ key, title, icon, pack string }{
		{"auth", "Sign-in", "user-shield", "auth"}, {"mail", "Mail", "mail", "mail"},
		{"llm", "Language model", "sparkles", "llm"}, {"storage", "Storage", "folder", "storage"},
	} {
		card := packCard{Title: c.title, Icon: c.icon}
		v, ok := byKey[c.key]
		switch {
		case ok:
			card.Href = h.settingsHref(v.Section)
			card.Status, card.StatusText = v.Status, v.StatusText
			if v.Selector != nil {
				for _, o := range v.Selector.Opts {
					if o.Checked {
						card.Provider = strings.TrimPrefix(card.Provider+", "+o.Value, ", ")
					}
				}
			}
		case c.key == "auth" && slices.Contains(d.Packs, "auth"):
			card.Status, card.StatusText, card.Provider, card.Href = "ok", "The app's own sign-in", "sessions", h.path+"/users"
		default:
			card.Status, card.StatusText, card.Href = "off", "Not enabled", ""
			card.Hint = "lidza pack add " + c.pack
		}
		if card.Provider == "" && card.Status != "off" {
			card.Provider = card.StatusText
		}
		d.Cards = append(d.Cards, card)
	}
	d.Checklist = append(d.Checklist, checkItem{Title: "Master key", Done: d.HasKey || values[credentials.EnvMasterKey] != "",
		Help: "Seals the credentials. On a server, set LIDZA_MASTER_KEY in the environment; never commit config/master.key."})
	d.Checklist = append(d.Checklist, checkItem{Title: "Public address", Done: d.AppURL != "" || d.TLSDomains != "",
		Help: "APP_URL, or LIDZA_TLS_DOMAINS when the binary serves TLS itself: links in emails and the sign-in callbacks use it."})
	for _, c := range []struct{ key, title, help string }{
		{"mail", "Mail is delivered", "A real provider instead of log or outbox, with its key."},
		{"llm", "A real language model", "A provider instead of fake, with its key."},
		{"storage", "Shared file storage", "An S3-compatible service, so every node sees the same files."},
	} {
		if v, ok := byKey[c.key]; ok {
			d.Checklist = append(d.Checklist, checkItem{Title: c.title, Help: c.help, Href: h.settingsHref(v.Section), Done: v.Status == "ok"})
		}
	}
	if d.FirstUser != "" {
		d.Checklist = append(d.Checklist, checkItem{Title: "A second admin", Done: len(d.Admins) > 0,
			Help: "Someone besides the first account, so losing one account does not lock everyone out."})
	}
	for _, c := range d.Checklist {
		if c.Done {
			d.Done++
		}
	}
	if len(d.Checklist) > 0 {
		d.Percent = (d.Done*100/len(d.Checklist) + 2) / 5 * 5
	}
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
	Methods []string
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
			row.Methods = methods
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

// bar is one day of the token chart, in the SVG's coordinates.
type bar struct {
	Day               string
	X, InY, InH, OutY float64
	OutH              float64
	Input, Output     int64
}

// chartHeight and chartWidth are the token chart's viewBox.
const chartHeight, chartWidth, chartDays = 140.0, 600.0, 30

// tokenBars turns the usage rows into one stacked bar per day of the
// last 30, oldest first.
func tokenBars(rows []llm.UsageRow, now time.Time) []bar {
	in, out := map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		in[r.Day] += r.Input
		out[r.Day] += r.Output
	}
	var max int64 = 1
	days := make([]string, chartDays)
	for i := range days {
		days[i] = now.AddDate(0, 0, i-chartDays+1).Format("2006-01-02")
		if t := in[days[i]] + out[days[i]]; t > max {
			max = t
		}
	}
	step := chartWidth / chartDays
	bars := make([]bar, chartDays)
	for i, d := range days {
		b := bar{Day: d, X: float64(i)*step + 2, Input: in[d], Output: out[d]}
		b.InH = float64(in[d]) / float64(max) * chartHeight
		b.OutH = float64(out[d]) / float64(max) * chartHeight
		b.InY = chartHeight - b.InH
		b.OutY = b.InY - b.OutH
		bars[i] = b
	}
	return bars
}

func (h *Handler) llm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, ok := lidza.Optional[llmService](ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Provider": l.Provider(), "Model": l.Model(), "Stats": l.TelemetryStats(), "Dev": l.Provider() == "fake",
		"Calls": int64(0), "Input": int64(0), "Output": int64(0), "Errors": int64(0)}
	if rows, err := l.Usage(ctx, time.Now().AddDate(0, 0, -30)); err == nil {
		var in, out, calls, errs int64
		for _, row := range rows {
			in, out, calls, errs = in+row.Input, out+row.Output, calls+row.Calls, errs+row.Errors
		}
		data["Rows"], data["Input"], data["Output"], data["Calls"], data["Errors"] = rows, in, out, calls, errs
		data["Bars"], data["BarWidth"] = tokenBars(rows, time.Now()), chartWidth/chartDays-4
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
	data := map[string]any{"Provider": m.Provider(), "Templates": m.Templates(), "Dev": m.Provider() == "log" || m.Provider() == "outbox", "Counts": map[string]int{}}
	if rows, err := m.Outbox(ctx, 50); err == nil {
		counts := map[string]int{}
		for _, row := range rows {
			counts[row.Status]++
		}
		data["Rows"], data["Counts"] = rows, counts
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
	if counts, err := q.Counts(ctx); err == nil {
		data["Counts"] = counts
	}
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
