package admin

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/env"
)

// Section is one group of settings on the Credentials page: a pack's
// provider and keys, or the app's own (Options.Sections). Every field is
// an environment name the app reads through pkg/env; the page saves it
// sealed with the master key and asks the packs to reload.
type Section struct {
	// Key names the section in the page's URL (?section=mail).
	Key string
	// Page is the pack page the section sits on (mail, llm, storage,
	// users); an app's sections (empty Page) are on the Settings page.
	Page string
	// Links point at the providers' consoles, per selector value.
	Links []Link
	// Title and Description head the section; Icon is a Tabler icon
	// name among the vendored ones (assets/icons.txt).
	Title, Description, Icon string
	// Selector names the field whose value decides which other fields
	// apply (MAIL_PROVIDER, AUTH_PROVIDERS); see Field.For.
	Selector string
	Fields   []Field
	// Enabled reports whether the section applies to the running app;
	// nil means always.
	Enabled func(ctx context.Context) bool
	// None, when set, makes an empty selector a working state with this
	// status text (sign-in with email and password alone), instead of
	// "Not set up".
	None string
}

// Field is one setting.
type Field struct {
	// Name is the environment name (MAIL_API_KEY).
	Name  string
	Label string
	// Kind is "text", "number", "secret" (never shown again once saved),
	// "select" (one of Options) or "multi" (any of Options, saved
	// comma-separated).
	Kind    string
	Options []string
	// Labels name options for people ("Cloudflare R2" for r2).
	Labels map[string]string
	// Dev lists the options meant for development and tests (log, fake,
	// local); the page flags a section that runs on one.
	Dev []string
	// Default is what the pack uses when the setting is empty.
	Default     string
	Help        string
	Placeholder string
	// Placeholders vary the placeholder with the selector's value
	// (the default model of each provider).
	Placeholders map[string]string
	// Group gathers fields under a heading within the section (Google,
	// SMTP server).
	Group string
	// For lists the values of the section's selector under which the
	// field applies (mailgun, sendgrid); empty means always.
	For []string
	// Advanced folds the field away under "Advanced" (a proxy address, a
	// token limit): rarely needed, never required.
	Advanced bool
}

// Link is a pointer to a provider's console, shown next to the
// section's fields while that provider is chosen.
type Link struct {
	// For is the selector value it belongs to.
	For, Title, URL string
}

// builtinSections are the packs' settings, in page order. Each belongs
// to its pack's page (Section.Page).
func builtinSections() []Section {
	return []Section{
		{Key: "auth", Page: "users", Title: "Sign-in providers", Icon: "user-shield", Selector: "AUTH_PROVIDERS", None: "Email and password only",
			Description: "Buttons on the sign-in page served by auth.Mount, next to email and password. Register <APP_URL>/api/v1/auth/<provider>/callback as the redirect URI with each provider.",
			Enabled:     func(ctx context.Context) bool { _, ok := lidza.Optional[*auth.Auth](ctx); return ok && auth.Mounted() },
			Links: []Link{
				{For: "google", Title: "Google Cloud console: OAuth clients", URL: "https://console.cloud.google.com/apis/credentials"},
				{For: "github", Title: "GitHub: OAuth Apps", URL: "https://github.com/settings/developers"},
				{For: "microsoft", Title: "Microsoft Entra: App registrations", URL: "https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade"},
			},
			Fields: []Field{
				{Name: "AUTH_PROVIDERS", Label: "Providers", Kind: "multi", Options: []string{"google", "github", "microsoft"}, Labels: map[string]string{"google": "Google", "github": "GitHub", "microsoft": "Microsoft"},
					Help: "Another OpenID Connect issuer (Okta, Keycloak, Auth0) is added by name with AUTH_<NAME>_ISSUER, AUTH_<NAME>_CLIENT_ID and AUTH_<NAME>_CLIENT_SECRET in the credentials."},
				{Name: "AUTH_GOOGLE_CLIENT_ID", Label: "Client ID", Kind: "text", Group: "Google", For: []string{"google"}, Placeholder: "1234-abc.apps.googleusercontent.com", Help: "An OAuth client of type Web application."},
				{Name: "AUTH_GOOGLE_CLIENT_SECRET", Label: "Client secret", Kind: "secret", Group: "Google", For: []string{"google"}},
				{Name: "AUTH_GITHUB_CLIENT_ID", Label: "Client ID", Kind: "text", Group: "GitHub", For: []string{"github"}},
				{Name: "AUTH_GITHUB_CLIENT_SECRET", Label: "Client secret", Kind: "secret", Group: "GitHub", For: []string{"github"}},
				{Name: "AUTH_MICROSOFT_CLIENT_ID", Label: "Application (client) ID", Kind: "text", Group: "Microsoft", For: []string{"microsoft"}},
				{Name: "AUTH_MICROSOFT_CLIENT_SECRET", Label: "Client secret", Kind: "secret", Group: "Microsoft", For: []string{"microsoft"}},
				{Name: "AUTH_MICROSOFT_TENANT", Label: "Accounts", Kind: "select", Group: "Microsoft", For: []string{"microsoft"}, Default: "common",
					Options: []string{"common", "organizations", "consumers"}, Labels: map[string]string{"common": "Any Microsoft account", "organizations": "Work and school accounts", "consumers": "Personal accounts"},
					Help: "A single organization's tenant ID can be set with lidza credentials set AUTH_MICROSOFT_TENANT=<id>."},
			}},
		{Key: "mail", Page: "mail", Title: "Mail delivery", Icon: "mail", Selector: "MAIL_PROVIDER",
			Description: "How the mail pack delivers verification links, resets and notifications.",
			Enabled:     func(ctx context.Context) bool { _, ok := lidza.Optional[mailService](ctx); return ok },
			Links: []Link{
				{For: "mailgun", Title: "Mailgun: sending keys and domains", URL: "https://app.mailgun.com/mg/sending/domains"},
				{For: "sendgrid", Title: "SendGrid: API keys", URL: "https://app.sendgrid.com/settings/api_keys"},
				{For: "postmark", Title: "Postmark: server API tokens", URL: "https://account.postmarkapp.com/servers"},
				{For: "resend", Title: "Resend: API keys", URL: "https://resend.com/api-keys"},
			},
			Fields: []Field{
				{Name: "MAIL_PROVIDER", Label: "Provider", Kind: "select", Options: []string{"smtp", "mailgun", "sendgrid", "postmark", "resend", "log", "outbox"}, Dev: []string{"log", "outbox"}, Default: "log",
					Labels: map[string]string{"smtp": "SMTP server", "mailgun": "Mailgun", "sendgrid": "SendGrid", "postmark": "Postmark", "resend": "Resend", "log": "Log only", "outbox": "Outbox only"},
					Help:   "Log only prints messages and Outbox only keeps them: for development and tests."},
				{Name: "MAIL_FROM", Label: "From", Kind: "text", Placeholder: "App <hello@example.com>", Help: "A name and address, or an address, on a domain the provider may send for."},
				{Name: "MAIL_SMTP_HOST", Label: "Server", Kind: "text", Group: "SMTP server", For: []string{"smtp"}, Placeholder: "smtp.example.com"},
				{Name: "MAIL_SMTP_SECURITY", Label: "Security", Kind: "select", Group: "SMTP server", For: []string{"smtp"}, Default: "starttls", Options: mail.SMTPSecurities,
					Labels: map[string]string{"starttls": "STARTTLS (port 587)", "tls": "TLS/SSL (port 465)", "none": "None (a local relay only)"}},
				{Name: "MAIL_SMTP_PORT", Label: "Port", Kind: "number", Group: "SMTP server", For: []string{"smtp"}, Help: "Empty uses the security's port: 587, 465 or 25."},
				{Name: "MAIL_SMTP_USERNAME", Label: "Username", Kind: "text", Group: "SMTP server", For: []string{"smtp"}},
				{Name: "MAIL_SMTP_PASSWORD", Label: "Password", Kind: "secret", Group: "SMTP server", For: []string{"smtp"}},
				{Name: "MAIL_API_KEY", Label: "API key", Kind: "secret", For: []string{"mailgun", "sendgrid", "postmark", "resend"},
					Placeholders: map[string]string{"mailgun": "key-… or a sending key", "sendgrid": "SG.…", "postmark": "server API token", "resend": "re_…"}},
				{Name: "MAIL_DOMAIN", Label: "Sending domain", Kind: "text", For: []string{"mailgun"}, Placeholder: "mg.example.com"},
				{Name: "MAIL_REGION", Label: "Region", Kind: "select", For: []string{"mailgun", "sendgrid"}, Default: "us", Options: mail.Regions,
					Labels: map[string]string{"us": "United States", "eu": "European Union"}, Help: "Where the account's data lives."},
				{Name: "MAIL_SMTP_URL", Label: "SMTP URL", Kind: "secret", For: []string{"smtp"}, Advanced: true, Placeholder: "smtp://user:pass@host:587",
					Help: "The server in one URL, instead of the fields above; it wins when set."},
				{Name: "MAIL_BASE_URL", Label: "API address", Kind: "text", For: []string{"mailgun", "sendgrid", "postmark", "resend"}, Advanced: true, Help: "Only for a proxy in front of the provider's API."},
			}},
		{Key: "llm", Page: "llm", Title: "Model provider", Icon: "sparkles", Selector: "LLM_PROVIDER",
			Description: "The model behind the llm pack: chat, structured output and embeddings.",
			Enabled:     func(ctx context.Context) bool { _, ok := lidza.Optional[llmService](ctx); return ok },
			Links: []Link{
				{For: "anthropic", Title: "Anthropic console: API keys", URL: "https://console.anthropic.com/settings/keys"},
				{For: "openai", Title: "OpenAI: API keys", URL: "https://platform.openai.com/api-keys"},
				{For: "google", Title: "Google AI Studio: API keys", URL: "https://aistudio.google.com/apikey"},
				{For: "compatible", Title: "llama.cpp: running llama-server", URL: "https://github.com/ggml-org/llama.cpp/tree/master/tools/server"},
				{For: "ollama", Title: "Ollama: download", URL: "https://ollama.com/download"},
			},
			Fields: []Field{
				{Name: "LLM_PROVIDER", Label: "Provider", Kind: "select", Options: []string{"anthropic", "openai", "google", "compatible", "ollama", "fake"}, Dev: []string{"fake"}, Default: "fake",
					Labels: map[string]string{"anthropic": "Anthropic", "openai": "OpenAI", "google": "Google Gemini", "compatible": "Custom server", "ollama": "Ollama", "fake": "Fake"},
					Help:   "Custom server is any server speaking the OpenAI API: llama.cpp's llama-server, vLLM, LM Studio. Fake answers with scripted replies, for tests."},
				{Name: "LLM_BASE_URL", Label: "Server address", Kind: "text", For: []string{"compatible", "ollama"},
					Placeholders: map[string]string{"compatible": "http://127.0.0.1:8080", "ollama": "http://127.0.0.1:11434"}, Help: "Where the server listens; /v1 may be left off."},
				{Name: "LLM_API_KEY", Label: "API key", Kind: "secret", For: []string{"anthropic", "openai", "google", "compatible"},
					Placeholders: map[string]string{"anthropic": "sk-ant-…", "openai": "sk-…", "google": "AIza…", "compatible": "only if the server asks for one"}},
				{Name: "LLM_MODEL", Label: "Model", Kind: "text", For: []string{"anthropic", "openai", "google", "compatible", "ollama"},
					Placeholders: map[string]string{"anthropic": "claude-sonnet-5", "openai": "gpt-5-mini", "google": "gemini-2.5-flash", "compatible": "the model the server loaded", "ollama": "llama3.2"},
					Help:         "Empty uses the placeholder's model."},
				{Name: "LLM_EMBED_MODEL", Label: "Embedding model", Kind: "text", For: []string{"openai", "google", "compatible", "ollama"}, Advanced: true,
					Placeholders: map[string]string{"openai": "text-embedding-3-small", "google": "text-embedding-004", "ollama": "nomic-embed-text"}},
				{Name: "LLM_MAX_TOKENS", Label: "Longest reply (tokens)", Kind: "number", For: []string{"anthropic", "openai", "google", "compatible", "ollama"}, Advanced: true, Default: "1024",
					Help: "A request's own limit wins."},
			}},
		{Key: "storage", Page: "storage", Title: "File storage", Icon: "folder", Selector: "STORAGE_PROVIDER",
			Description: "Where the storage pack keeps uploads and generated files.",
			Enabled:     func(ctx context.Context) bool { _, ok := lidza.Optional[storageService](ctx); return ok },
			Links: []Link{
				{For: "s3", Title: "AWS: access keys", URL: "https://console.aws.amazon.com/iam/home#/security_credentials"},
				{For: "r2", Title: "Cloudflare R2: API tokens", URL: "https://dash.cloudflare.com/?to=/:account/r2/api-tokens"},
				{For: "spaces", Title: "DigitalOcean: Spaces keys", URL: "https://cloud.digitalocean.com/account/api/spaces"},
				{For: "b2", Title: "Backblaze B2: application keys", URL: "https://secure.backblaze.com/app_keys.htm"},
				{For: "gcs", Title: "Google Cloud Storage: HMAC keys", URL: "https://console.cloud.google.com/storage/settings;tab=interoperability"},
			},
			Fields: []Field{
				{Name: "STORAGE_PROVIDER", Label: "Provider", Kind: "select", Options: []string{"s3", "r2", "spaces", "b2", "gcs", "minio", "local"}, Dev: []string{"local"}, Default: "local",
					Labels: map[string]string{"s3": "Amazon S3", "r2": "Cloudflare R2", "spaces": "DigitalOcean Spaces", "b2": "Backblaze B2", "gcs": "Google Cloud Storage", "minio": "MinIO or another S3 server", "local": "This server's disk"},
					Help:   "This server's disk suits development and a single node; every other choice is shared by all nodes."},
				{Name: "STORAGE_DIR", Label: "Directory", Kind: "text", For: []string{"local"}, Default: "storage"},
				{Name: "STORAGE_ENDPOINT", Label: "Server address", Kind: "text", For: []string{"minio"}, Placeholder: "http://127.0.0.1:9000"},
				{Name: "STORAGE_ACCOUNT_ID", Label: "Account ID", Kind: "text", For: []string{"r2"}, Help: "On the R2 overview page of the Cloudflare dashboard."},
				{Name: "STORAGE_REGION", Label: "Region", Kind: "text", For: []string{"s3", "spaces", "b2"},
					Placeholders: map[string]string{"s3": "us-east-1", "spaces": "nyc3, ams3, fra1, sgp1…", "b2": "us-west-004"}},
				{Name: "STORAGE_BUCKET", Label: "Bucket", Kind: "text", For: []string{"s3", "r2", "spaces", "b2", "gcs", "minio"}},
				{Name: "STORAGE_ACCESS_KEY", Label: "Access key ID", Kind: "text", For: []string{"s3", "r2", "spaces", "b2", "gcs", "minio"}},
				{Name: "STORAGE_SECRET_KEY", Label: "Secret access key", Kind: "secret", For: []string{"s3", "r2", "spaces", "b2", "gcs", "minio"}},
				{Name: "STORAGE_PUBLIC_URL", Label: "Public URL", Kind: "text", For: []string{"s3", "r2", "spaces", "b2", "gcs", "minio"}, Advanced: true,
					Help: "A CDN or public bucket address for public files; leave empty to keep files private."},
			}},
	}
}

// sections are the built-in sections and the app's, as configured.
func (h *Handler) sections() []Section {
	return append(builtinSections(), h.opt.Sections...)
}

// enabledSections are the sections that apply to the running app.
func (h *Handler) enabledSections(ctx context.Context) []Section {
	var out []Section
	for _, s := range h.sections() {
		if s.Enabled == nil || s.Enabled(ctx) {
			out = append(out, s)
		}
	}
	return out
}

// Views of the settings for the templates.

type optionView struct {
	Value   string
	Label   string
	Checked bool
	Dev     bool
}

type fieldView struct {
	Field
	Value   string // current value; empty for a secret
	Set     bool   // has a value
	Origin  string // the layer the value comes from (env.Origins)
	Locked  bool   // the process environment sets it: saving cannot change it
	Applies bool   // the selector's value makes it relevant
	ForAttr string
	Opts    []optionView
	// PlaceholderNow is the placeholder for the selector's current
	// value; PlaceholdersAttr lists them all for the script
	// ("anthropic=claude-sonnet-5|openai=gpt-5-mini").
	PlaceholderNow   string
	PlaceholdersAttr string
}

type linkView struct {
	Link
	Applies bool
}

type groupView struct {
	Title   string
	Icon    string
	ForAttr string
	Applies bool
	Fields  []fieldView
}

type sectionView struct {
	Section
	Selector  *fieldView
	Fields    []fieldView // ungrouped, selector excluded
	Groups    []groupView
	Advanced  []fieldView
	LinkViews []linkView
	// AdvancedSet reports whether an advanced field has a value (the
	// fold opens).
	AdvancedSet bool
	Status      string // ok, partial, dev, off
	StatusText  string
	Search      string
	Active      bool
}

// groupIcons are the headings' icons for the built-in providers.
var groupIcons = map[string]string{"Google": "brand-google", "GitHub": "brand-github", "Microsoft": "brand-windows"}

// view builds a section's view from the current values and origins.
func view(s Section, values, origins map[string]string) sectionView {
	sv := sectionView{Section: s}
	var selected []string
	if s.Selector != "" {
		for _, v := range strings.Split(values[s.Selector], ",") {
			if v = strings.TrimSpace(v); v != "" {
				selected = append(selected, v)
			}
		}
		if len(selected) == 0 {
			for _, f := range s.Fields {
				if f.Name == s.Selector && f.Default != "" {
					selected = []string{f.Default}
				}
			}
		}
	}
	applies := func(f Field) bool {
		if len(f.For) == 0 {
			return true
		}
		for _, v := range selected {
			if slices.Contains(f.For, v) {
				return true
			}
		}
		return false
	}
	search := []string{strings.ToLower(s.Title), strings.ToLower(s.Description)}
	groups := map[string]int{}
	secrets, secretsSet := 0, 0
	for _, f := range s.Fields {
		search = append(search, strings.ToLower(f.Name), strings.ToLower(f.Label), strings.ToLower(f.Group))
		fv := fieldView{Field: f, Value: values[f.Name], Origin: origins[f.Name], Applies: applies(f), ForAttr: strings.Join(f.For, ","), PlaceholderNow: f.Placeholder}
		if len(f.Placeholders) > 0 {
			var parts []string
			for _, k := range slices.Sorted(maps.Keys(f.Placeholders)) {
				parts = append(parts, k+"="+f.Placeholders[k])
			}
			fv.PlaceholdersAttr = strings.Join(parts, "|")
			for _, v := range selected {
				if ph, ok := f.Placeholders[v]; ok {
					fv.PlaceholderNow = ph
				}
			}
		}
		fv.Set = fv.Value != ""
		fv.Locked = fv.Origin == env.OriginProcess
		if f.Kind == "secret" {
			fv.Value = ""
			if fv.Applies {
				secrets++
				if fv.Set {
					secretsSet++
				}
			}
		}
		if f.Kind == "select" || f.Kind == "multi" {
			current := strings.Split(values[f.Name], ",")
			for i := range current {
				current[i] = strings.TrimSpace(current[i])
			}
			for _, o := range f.Options {
				label := f.Labels[o]
				if label == "" {
					label = o
				}
				fv.Opts = append(fv.Opts, optionView{Value: o, Label: label, Checked: slices.Contains(current, o) || (!fv.Set && f.Kind == "select" && o == f.Default), Dev: slices.Contains(f.Dev, o)})
			}
		}
		switch {
		case f.Name == s.Selector:
			v := fv
			sv.Selector = &v
		case f.Advanced:
			if fv.Set && fv.Applies {
				sv.AdvancedSet = true
			}
			sv.Advanced = append(sv.Advanced, fv)
		case f.Group != "":
			i, ok := groups[f.Group]
			if !ok {
				i = len(sv.Groups)
				groups[f.Group] = i
				sv.Groups = append(sv.Groups, groupView{Title: f.Group, Icon: groupIcons[f.Group], ForAttr: fv.ForAttr})
			}
			if fv.Applies {
				sv.Groups[i].Applies = true
			}
			sv.Groups[i].Fields = append(sv.Groups[i].Fields, fv)
		default:
			sv.Fields = append(sv.Fields, fv)
		}
	}
	sv.Search = strings.Join(search, " ")
	for _, l := range s.Links {
		sv.LinkViews = append(sv.LinkViews, linkView{Link: l, Applies: slices.Contains(selected, l.For)})
	}
	// The status: a development provider, nothing chosen, missing
	// secrets, or configured.
	dev := false
	if sv.Selector != nil {
		for _, v := range selected {
			if slices.Contains(sv.Selector.Dev, v) {
				dev = true
			}
		}
	}
	switch {
	case s.Selector != "" && len(selected) == 0 && s.None != "":
		sv.Status, sv.StatusText = "ok", s.None
	case s.Selector != "" && len(selected) == 0:
		sv.Status, sv.StatusText = "off", "Not set up"
	case dev:
		sv.Status, sv.StatusText = "dev", "Development ("+strings.Join(selected, ", ")+")"
	case secretsSet < secrets:
		sv.Status, sv.StatusText = "partial", "Incomplete"
	case s.Selector == "" && secrets == 0 && !anySet(s.Fields, values):
		sv.Status, sv.StatusText = "off", "Not set up"
	default:
		sv.Status, sv.StatusText = "ok", "Configured"
		if len(selected) > 0 {
			sv.StatusText = strings.Join(selected, ", ")
		}
	}
	return sv
}

func anySet(fields []Field, values map[string]string) bool {
	for _, f := range fields {
		if values[f.Name] != "" {
			return true
		}
	}
	return false
}

// settingsViews builds the views of every enabled section.
func (h *Handler) settingsViews(ctx context.Context) ([]sectionView, error) {
	values, err := env.Values(h.opt.CredentialsDir)
	if err != nil {
		return nil, err
	}
	origins, err := env.Origins(h.opt.CredentialsDir)
	if err != nil {
		return nil, err
	}
	var out []sectionView
	for _, s := range h.enabledSections(ctx) {
		out = append(out, view(s, values, origins))
	}
	return out, nil
}

// originLabel names an origin for people.
func originLabel(origin string) string {
	switch origin {
	case env.OriginSaved:
		return "saved here"
	case env.OriginCredentials:
		return "credentials file"
	case env.OriginProcess:
		return "environment"
	case "":
		return ""
	}
	return origin // .env, .env.production
}

// packSettings serves a pack page's Settings tab: its section's form.
func (h *Handler) packSettings(page, active string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sec, err := h.sectionFor(ctx, page)
		if err == nil && sec == nil {
			http.NotFound(w, r)
			return
		}
		data := map[string]any{"Section": sec, "HasKey": credentials.HasKey(h.opt.CredentialsDir)}
		if err != nil {
			data["Error"] = err.Error()
		}
		_, data["Database"] = lidza.Optional[credentials.Store](ctx)
		h.render(w, r, "packsettings", active, data)
	}
}

// appSettings serves the Settings page: the app's own sections
// (Options.Sections), one at a time.
func (h *Handler) appSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := h.settingsViews(ctx)
	var views []sectionView
	for _, v := range all {
		if v.Page == "" {
			views = append(views, v)
		}
	}
	if len(views) == 0 && err == nil {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"HasKey": credentials.HasKey(h.opt.CredentialsDir), "Query": r.URL.Query().Get("q")}
	if err != nil {
		data["Error"] = err.Error()
	}
	key := r.URL.Query().Get("section")
	var active *sectionView
	for i := range views {
		if views[i].Key == key {
			views[i].Active = true
			active = &views[i]
		}
	}
	if active == nil && len(views) > 0 {
		pick := 0
		for i := range views {
			if views[i].Status == "partial" || views[i].Status == "off" {
				pick = i
				break
			}
		}
		views[pick].Active = true
		active = &views[pick]
	}
	data["Sections"], data["Section"] = views, active
	_, data["Database"] = lidza.Optional[credentials.Store](ctx)
	h.render(w, r, "settings", "Settings", data)
}

// credentialsRedirect keeps the Credentials page's old address working:
// each section now sits on its pack's page.
func (h *Handler) credentialsRedirect(w http.ResponseWriter, r *http.Request) {
	to := h.path + "/"
	for _, s := range h.sections() {
		if s.Key == r.URL.Query().Get("section") {
			to = h.settingsHref(s)
		}
	}
	http.Redirect(w, r, to, http.StatusMovedPermanently)
}

// saveSettings stores what changed in one section's form: a field
// only when the form carries it; an empty secret keeps the stored one
// and clear_<NAME> removes it; an emptied text removes the saved value;
// a multi is posted with present_<NAME>. A setting the process
// environment holds is never saved (it would not take effect). Then
// the packs read their configuration again.
func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		h.redirect(w, r, "/", "", "bad form")
		return
	}
	key := r.Form.Get("section")
	var sec *Section
	for _, s := range h.enabledSections(ctx) {
		if s.Key == key {
			s := s
			sec = &s
		}
	}
	if sec == nil {
		h.redirect(w, r, "/", "", "unknown section")
		return
	}
	back := strings.TrimPrefix(h.settingsHref(*sec), h.path)
	store := h.store(ctx)
	current, _ := env.Values(h.opt.CredentialsDir)
	origins, _ := env.Origins(h.opt.CredentialsDir)
	stored := func(name string) bool {
		return origins[name] == env.OriginSaved || origins[name] == env.OriginCredentials
	}
	saved := 0
	save := func(name, value string) error {
		saved++
		if value == "" {
			return store.Delete(ctx, name)
		}
		return store.Save(ctx, name, value)
	}
	for _, f := range sec.Fields {
		if origins[f.Name] == env.OriginProcess {
			continue
		}
		var err error
		switch {
		case r.Form.Get("clear_"+f.Name) == "on":
			if stored(f.Name) {
				err = save(f.Name, "")
			}
		case f.Kind == "multi":
			if r.Form.Get("present_"+f.Name) == "" {
				continue
			}
			var next []string
			for _, v := range r.Form[f.Name] {
				if slices.Contains(f.Options, v) && !slices.Contains(next, v) {
					next = append(next, v)
				}
			}
			// Names the page does not offer (a custom OIDC issuer) stay.
			for _, v := range strings.Split(current[f.Name], ",") {
				if v = strings.TrimSpace(v); v != "" && !slices.Contains(f.Options, v) && !slices.Contains(next, v) {
					next = append(next, v)
				}
			}
			if joined := strings.Join(next, ","); joined != normalizeList(current[f.Name]) {
				err = save(f.Name, joined)
			}
		default:
			vals, present := r.Form[f.Name]
			if !present {
				continue
			}
			v := strings.TrimSpace(vals[0])
			switch {
			case f.Kind == "select" && v != "" && !slices.Contains(f.Options, v):
				h.redirect(w, r, back, "", f.Label+": "+v+" is not one of "+strings.Join(f.Options, ", "))
				return
			case f.Kind == "number" && v != "" && !isNumber(v):
				h.redirect(w, r, back, "", f.Label+": "+v+" is not a number")
				return
			case v == current[f.Name]:
			case v == "" && f.Kind == "secret":
				// An empty secret keeps the stored one.
			case v == "":
				if stored(f.Name) {
					err = save(f.Name, "")
				}
			default:
				err = save(f.Name, v)
			}
		}
		if err != nil {
			h.redirect(w, r, back, "", err.Error())
			return
		}
	}
	if saved == 0 {
		h.redirect(w, r, back, "nothing changed", "")
		return
	}
	if s := lidza.ServicesFrom(ctx); s != nil {
		if err := lidza.Reconfigure(ctx, s); err != nil {
			h.redirect(w, r, back, "", "saved, but a pack refused the new settings: "+err.Error())
			return
		}
	}
	h.redirect(w, r, back, sec.Title+" saved and applied", "")
}

func isNumber(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func normalizeList(s string) string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ",")
}

// sectionFor is the view of the section on a pack page, nil when that
// pack's section does not apply.
func (h *Handler) sectionFor(ctx context.Context, page string) (*sectionView, error) {
	views, err := h.settingsViews(ctx)
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].Page == page {
			return &views[i], nil
		}
	}
	return nil, nil
}

// settingsHref is where a section is edited.
func (h *Handler) settingsHref(s Section) string {
	switch s.Page {
	case "":
		return h.path + "/settings?section=" + s.Key
	case "users":
		return h.path + "/users/sign-in"
	}
	return h.path + "/" + s.Page + "/settings"
}
