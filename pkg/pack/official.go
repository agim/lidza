package pack

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/schema"
)

// OfficialPrefix marks a Go pack imported from the framework in
// lidza.json ("lidza/db"). Official Rust packs are copied into the app as
// local packs, so they carry no prefix.
const OfficialPrefix = "lidza/"

// Official describes a pack the framework ships.
type Official struct {
	Name        string
	Description string
	// Rust packs are source (manifest, crate, schema fragment) copied into
	// packs/<name> and built there; Go packs are imported.
	Rust bool
	// Env are lines appended to .env.example.
	Env []string
	// Notes are printed after adding.
	Notes []string
}

// Officials lists the packs `lidza pack add` knows.
var Officials = []Official{
	{
		Name:        "db",
		Description: "Postgres: bounded pgx pool from DATABASE_URL, migrations from db/migrations, typed queries with sqlc.",
		Env: []string{
			"# lidza/db. lidza setup writes this machine's address: the Postgres socket",
			"# (/var/run/postgresql on Linux, /tmp on macOS) or 127.0.0.1:5432.",
			"DATABASE_URL=postgres:///app_dev?host=/var/run/postgresql",
			"DB_MAX_CONNS=10",
			"# DB_MIGRATE=true   # apply db/migrations at start",
		},
		Notes: []string{
			"set DATABASE_URL in .env; `lidza db migrate` applies db/migrations",
			"write SQL in db/queries/*.sql; `lidza gen` turns it into Go (db/queries/gen) with sqlc",
			"in a handler: queries.New(db.From(ctx)).YourQuery(ctx, ...)",
		},
	},
	{
		Name:        "realtime",
		Description: "WebSocket topics at GET /api/v1/realtime; handlers publish JSON; a Valkey bus fans out across nodes.",
		Env: []string{
			"# lidza/realtime",
			"# REALTIME_BUS_URL=redis://127.0.0.1:6379   # required with more than one node",
			"REALTIME_MAX_CONNS=10000",
		},
		Notes: []string{
			"register the endpoint in routes.go: r.Handle(\"GET /api/v1/realtime\", realtime.Handler())",
			"publish from a handler: realtime.From(ctx).Publish(ctx, \"topic\", value)",
			"browser: new WebSocket(`ws://${location.host}/api/v1/realtime?topics=topic`)",
		},
	},
	{
		Name:        "cache",
		Description: "Cache on Valkey or Redis with TTL and read-through Remember; CACHE_URL=memory for tests.",
		Env: []string{
			"# lidza/cache",
			"CACHE_URL=redis://127.0.0.1:6379",
			"# CACHE_URL=memory   # bounded in-process cache for one node or tests",
		},
		Notes: []string{
			"cache.Remember(ctx, cache.From(ctx), \"key\", ttl, load) caches a computed value",
			"cache.From(ctx).Invalidate(ctx, \"prefix:\") after writes",
		},
	},
	{
		Name:        "auth",
		Description: "Sessions and tokens: argon2id passwords, short-lived JWT access tokens, refresh sessions in Postgres, cookies or bearer, auth.Require middleware.",
		Env: []string{
			"# lidza/auth (needs lidza/db)",
			"AUTH_SECRET=change-me-to-32-random-bytes-or-more-please",
			"AUTH_ACCESS_TTL=15m",
			"AUTH_REFRESH_TTL=720h",
			"# AUTH_COOKIE_SECURE=true   # behind TLS",
			"# AUTH_LOGIN_RPS=1           # credential attempts per second per client (auth.Throttle)",
			"# AUTH_LOGIN_BURST=5",
			"# AUTH_MIN_PASSWORD=10       # auth.ValidatePassword floor",
			"# AUTH_TOKEN_TTL=1h          # verification and reset links",
			"# AUTH_PROVIDERS=google,github   # sign-in providers for auth.Mount; ids and secrets in the credentials",
		},
		Notes: []string{
			"sign-in: auth.Mount(r, auth.Options{}) in routes.go serves register, login, logout, session, verification, reset and the AUTH_PROVIDERS sign-ins on the pack's own tables (recipe \"Add sign-in\"); or keep your own users table and call auth.From(ctx).Login after auth.CheckPassword",
			"login: tokens, err := auth.From(ctx).Login(ctx, userID, claims); for browsers add auth.From(ctx).Cookies(tokens) with req.SetCookie",
			"protect routes: g := r.Group(\"/api/v1/notes\", auth.Require()); auth.CurrentUser(ctx) inside; auth.Optional() where visitors are served too",
			"throttle the credential routes: router.Route(r, \"POST /api/v1/auth/login\", login, auth.Throttle()); check passwords with auth.From(ctx).ValidatePassword(pw, email)",
			"email verification and password reset: auth.From(ctx).IssueToken(ctx, auth.PurposeVerifyEmail, email, 0) and ConsumeToken; the app sends the link",
			"working code: lidza snippet routes, lidza snippet auth-handlers",
			"run `lidza gen` and `lidza db migrate`: the auth_session table comes from schema.lidza",
		},
	},
	{
		Name:        "jobs",
		Description: "Background jobs on Postgres: bounded workers per node, retries with backoff, scheduling, takeover of jobs whose node died.",
		Env: []string{
			"# lidza/jobs (needs lidza/db)",
			"JOBS_WORKERS=4",
			"JOBS_MAX_ATTEMPTS=5",
		},
		Notes: []string{
			"register handlers in OnStart: jobs.FromServices(s).Handle(\"email\", func(ctx, payload) error {...})",
			"enqueue from a handler: jobs.From(ctx).Enqueue(ctx, \"email\", payload, jobs.RunAt(t))",
			"run `lidza gen` and `lidza db migrate`: the job table comes from schema.lidza",
		},
	},
	{
		Name:        "i18n",
		Description: "Localization: catalogs in locales/<lang>.json embedded in the binary, locale per request, numbers, currency and dates.",
		Env:         []string{"# lidza/i18n", "I18N_DEFAULT=en"},
		Notes: []string{
			"messages: i18n.From(ctx).T(ctx, \"greeting\", name); add locales/<lang>.json files",
			"frontend catalog: r.Handle(\"GET /api/v1/i18n/{lang}\", i18n.Handler())",
		},
	},
	{
		Name:        "analytics",
		Description: "Opt-in error reporting and product events: server panics and 500s, frontend errors and named events stored in Postgres, optional OTLP export.",
		Env: []string{
			"# lidza/analytics (needs lidza/db)",
			"# ANALYTICS_OTLP_URL=http://127.0.0.1:4318/v1/logs   # export errors to a collector (Sentry, Grafana, GlitchTip accept OTLP)",
			"ANALYTICS_RETENTION=720h",
			"# VITE_ANALYTICS=1   # turn on the frontend reporter (react template)",
		},
		Notes: []string{
			"register the endpoint in routes.go: r.Handle(\"POST /api/v1/analytics/{kind}\", analytics.Handler())",
			"server errors are captured automatically (panics, 500s); events: analytics.From(ctx).Track(ctx, \"signup\", props)",
			"frontend: set VITE_ANALYTICS=1 in .env; src/analytics.ts reports errors and pageviews, track(name, props) for events",
			"run `lidza gen` and `lidza db migrate`: the app_error and app_event tables come from schema.lidza",
			"agents: the MCP tool lidza_errors lists recent errors",
		},
	},
	{
		Name:        "mail",
		Description: "Transactional email behind one Send: Mailgun, SendGrid, Postmark, Resend or SMTP over plain HTTP, log and outbox providers for development and tests, templates in mail/, an outbox table with the db pack, background delivery with retries through the jobs pack.",
		Env: []string{
			"# lidza/mail (list lidza/db and lidza/jobs before it for the outbox and background delivery)",
			"MAIL_PROVIDER=log          # log | outbox | smtp | mailgun | sendgrid | postmark | resend",
			"MAIL_FROM=\"App <app@example.com>\"",
			"APP_URL=http://127.0.0.1:3000   # where links in emails point (mail.From(ctx).Link(path))",
			"# MAIL_API_KEY=            # mailgun, sendgrid, postmark, resend",
			"# MAIL_DOMAIN=example.com  # mailgun",
			"# MAIL_BASE_URL=https://api.eu.mailgun.net   # a provider's regional API",
			"# MAIL_SMTP_URL=smtp://user:pass@smtp.example.com:587   # or smtps://...:465",
			"# MAIL_MAX_ATTEMPTS=5      # delivery retries through the jobs pack",
		},
		Notes: []string{
			"send: mail.From(ctx).Send(ctx, mail.Message{To: email, Subject: \"Verify your email\", Template: \"verify\", Data: data})",
			"templates: mail/<name>.txt.tmpl and mail/<name>.html.tmpl (Go templates over Data); or set Text and HTML on the message",
			"tests: MAIL_PROVIDER=outbox in .env.test, then mail.From(ctx).Outbox(ctx, 10) has the messages; agents: the MCP tool lidza_mail",
			"never import a vendor SDK (lidza check L006): the pack speaks each API directly",
			"run `lidza gen` and `lidza db migrate`: the mail_message table comes from schema.lidza",
		},
	},
	{
		Name:        "llm",
		Description: "Language models behind one Chat, Stream, Embed, Generate and Run: Anthropic, OpenAI, Google and Ollama spoken directly over HTTP, a fake provider for tests, structured output from schema types, the app's tools offered to the model, retries, and token usage per call in llm_usage with the db pack.",
		Env: []string{
			"# lidza/llm",
			"LLM_PROVIDER=fake          # fake | ollama | anthropic | openai | google",
			"# LLM_MODEL=                # the provider's default when empty (claude-sonnet-5, gpt-5-mini, gemini-2.5-flash, llama3.2)",
			"# LLM_API_KEY=              # anthropic, openai, google",
			"# LLM_BASE_URL=             # a proxy or region; Ollama elsewhere than http://127.0.0.1:11434",
			"# LLM_EMBED_MODEL=          # text-embedding-3-small, text-embedding-004, nomic-embed-text",
			"# LLM_MAX_TOKENS=1024       # reply bound; LLM_TIMEOUT=60s per attempt; LLM_MAX_ATTEMPTS=3 on 429 and 5xx",
		},
		Notes: []string{
			"chat: llm.From(ctx).Chat(ctx, llm.Request{System: \"...\", Messages: []llm.Message{{Role: llm.User, Content: text}}}) returns Text and Usage; Stream delivers the text as it arrives",
			"structured: out, err := llm.Generate[schema.NoteTags](ctx, llm.From(ctx), req) sends the type's JSON Schema and validates the reply",
			"tools: llm.From(ctx).Run(ctx, req, tools()) offers the app's lidza.Tool values to the model and runs the calls it makes",
			"tests: LLM_PROVIDER=fake in .env.test; llm.From(srv.Context()).Fake().Reply(\"...\") or .ReplyJSON(v) scripts the next reply",
			"usage: with the db pack every call is a row in llm_usage (set Request.Label to the feature name); llm.From(ctx).Usage(ctx, since) reports it, the MCP tool lidza_llm_usage too; run `lidza gen` and `lidza db migrate` for the table",
			"never import a vendor SDK (lidza check L007): the pack speaks each API directly",
		},
	},
	{
		Name:        "storage",
		Description: "Files behind one Put, Get, Stat, List, Delete and presigned URLs: any S3-compatible service (AWS S3, MinIO, Cloudflare R2, Backblaze B2, Wasabi, Spaces) spoken directly with Signature V4, and a local directory for development and tests.",
		Env: []string{
			"# lidza/storage",
			"STORAGE_PROVIDER=local     # local | s3",
			"# STORAGE_DIR=storage       # local: where the files go",
			"# STORAGE_BUCKET=           # s3",
			"# STORAGE_ENDPOINT=https://s3.amazonaws.com   # or https://<account>.r2.cloudflarestorage.com, http://127.0.0.1:9000 (MinIO)",
			"# STORAGE_REGION=us-east-1",
			"# STORAGE_ACCESS_KEY=       # keep both keys in the credentials: lidza credentials set STORAGE_ACCESS_KEY=... STORAGE_SECRET_KEY=...",
			"# STORAGE_PUBLIC_URL=       # a CDN or public bucket; URL(key) returns it plus the key",
			"# STORAGE_MAX_SIZE=104857600   # one Put, bytes",
		},
		Notes: []string{
			"store: obj, err := storage.From(ctx).Put(ctx, \"avatars/\"+id+\".png\", r, storage.PutOptions{}) (content type detected); Get, Stat, List, Delete",
			"browsers: PresignGet(ctx, key, ttl) for a private object, PresignPut for a direct upload, URL(key) with STORAGE_PUBLIC_URL; or mount storage.Handler(\"/api/v1/files/\") behind auth.Require()",
			"tests: STORAGE_PROVIDER=local and STORAGE_DIR under a temporary directory; nothing leaves the machine",
			"never import a vendor SDK (lidza check L008) and never write files to the local disk (os.WriteFile, L009): the pack keeps them; agents: the MCP tool lidza_storage lists what is stored",
		},
	},
	{
		Name:        "media",
		Description: "Image processing in Rust: dimensions and format, resize with format conversion.",
		Rust:        true,
		Notes:       []string{"capabilities take image bytes as base64 (schema type bytes)"},
	},
	{
		Name:        "geo",
		Description: "Geospatial in Rust: haversine distance, nearest neighbours (R-tree), bounding boxes.",
		Rust:        true,
	},
}

// FindOfficial returns the official pack called name.
func FindOfficial(name string) (Official, bool) {
	name = strings.TrimPrefix(name, OfficialPrefix)
	for _, o := range Officials {
		if o.Name == name {
			return o, true
		}
	}
	return Official{}, false
}

// IsOfficialGo reports whether a lidza.json entry is an imported Go pack.
func IsOfficialGo(entry string) bool { return strings.HasPrefix(entry, OfficialPrefix) }

//go:embed all:official
var officialFS embed.FS

// Add installs an official pack into the app and returns the lidza.json
// entry to enable it. Go packs need no files beyond .env.example lines
// and, for db, the sqlc configuration; Rust packs are copied into
// packs/<name> and their schema fragment appended to schema.lidza.
func Add(root, name string) (entry string, o Official, err error) {
	o, ok := FindOfficial(name)
	if !ok {
		names := make([]string, len(Officials))
		for i, x := range Officials {
			names[i] = x.Name
		}
		return "", o, fmt.Errorf("no official pack %q; available: %s", name, strings.Join(names, ", "))
	}
	if err := appendEnvExample(root, o.Env); err != nil {
		return "", o, err
	}
	if !o.Rust {
		if o.Name == "db" {
			if err := writeSQLCConfig(root); err != nil {
				return "", o, err
			}
		}
		if fragment, err := officialFS.ReadFile("official/" + o.Name + "/schema.lidza"); err == nil {
			if err := appendSchema(root, o.Name, fragment); err != nil {
				return "", o, err
			}
		}
		if o.Name == "i18n" {
			if err := writeIfMissing(filepath.Join(root, "locales", "en.json"), "official/i18n/locales/en.json"); err != nil {
				return "", o, err
			}
		}
		return OfficialPrefix + o.Name, o, nil
	}
	dst := filepath.Join(root, Dir, o.Name)
	if _, err := os.Stat(dst); err == nil {
		return "", o, fmt.Errorf("%s exists", filepath.Join(Dir, o.Name))
	}
	src := "official/" + o.Name
	err = fs.WalkDir(officialFS, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if rel == "schema.lidza" {
			return nil
		}
		data, err := officialFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, strings.TrimSuffix(rel, ".tmpl")), data, 0o644)
	})
	if err != nil {
		return "", o, err
	}
	abi, _ := files.ReadFile("files/abi.rs.tmpl")
	if err := os.WriteFile(filepath.Join(dst, "rust", "src", "abi.rs"), abi, 0o644); err != nil {
		return "", o, err
	}
	gitignore, _ := files.ReadFile("files/gitignore.tmpl")
	if err := os.WriteFile(filepath.Join(dst, ".gitignore"), gitignore, 0o644); err != nil {
		return "", o, err
	}
	if fragment, err := officialFS.ReadFile(src + "/schema.lidza"); err == nil {
		if err := appendSchema(root, o.Name, fragment); err != nil {
			return "", o, err
		}
	}
	return o.Name, o, nil
}

// SyncFragments appends to schema.lidza every model, type or enum of the
// enabled official packs' schema fragments that the app does not declare
// yet: a pack upgraded to a newer framework version may bring a table
// (the auth pack's auth_token, for instance), and `lidza gen` picks it up
// without a second `lidza pack add`. It returns the names it added.
func SyncFragments(root string, packs []string) ([]string, error) {
	p := filepath.Join(root, schema.FileName)
	existing, err := os.ReadFile(p)
	if err != nil {
		return nil, nil
	}
	cur, err := schema.Parse(string(existing))
	if err != nil {
		return nil, err
	}
	have := map[string]schemaBlock{}
	for _, b := range schemaBlocks(string(existing)) {
		have[b.name] = b
	}
	var synced []string
	out := string(existing)
	for _, entry := range packs {
		name := strings.TrimPrefix(entry, OfficialPrefix)
		fragment, err := officialFS.ReadFile("official/" + name + "/schema.lidza")
		if err != nil {
			continue
		}
		for _, block := range schemaBlocks(string(fragment)) {
			if cur.Model(block.name) != nil || cur.Enum(block.name) != nil {
				// The pack's declaration is the pack's: when a release changes
				// it (a column added), the app's copy follows and lidza gen
				// writes the migration.
				if old, ok := have[block.name]; ok && declaration(old.text) != declaration(block.text) {
					out = strings.Replace(out, old.text, block.text, 1)
					synced = append(synced, block.name)
				}
				continue
			}
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += "\n" + block.text + "\n"
			synced = append(synced, block.name)
		}
	}
	if len(synced) == 0 {
		return nil, nil
	}
	return synced, os.WriteFile(p, []byte(out), 0o644)
}

// declaration is a block without its comments and spacing, for comparing
// what it declares.
func declaration(text string) string {
	var parts []string
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "//") {
			parts = append(parts, strings.Join(strings.Fields(t), " "))
		}
	}
	return strings.Join(parts, "\n")
}

type schemaBlock struct{ name, text string }

// schemaBlocks splits a schema source into its top-level declarations,
// each with the comment lines above it.
func schemaBlocks(src string) []schemaBlock {
	var blocks []schemaBlock
	var pending []string // comment lines above the next declaration
	var current []string
	name := ""
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case name != "":
			current = append(current, line)
			if trimmed == "}" {
				blocks = append(blocks, schemaBlock{name: name, text: strings.Join(current, "\n")})
				name, current = "", nil
			}
		case strings.HasPrefix(trimmed, "model ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "enum "):
			f := strings.Fields(trimmed)
			name = f[1]
			current = append(append([]string{}, pending...), line)
			pending = nil
			if strings.HasSuffix(trimmed, "}") {
				blocks = append(blocks, schemaBlock{name: name, text: strings.Join(current, "\n")})
				name, current = "", nil
			}
		case strings.HasPrefix(trimmed, "//"):
			pending = append(pending, line)
		default:
			pending = nil
		}
	}
	return blocks
}

// appendSchema adds the pack's types to schema.lidza unless they exist.
func appendSchema(root, pack string, fragment []byte) error {
	if _, err := schema.Parse(string(fragment)); err != nil {
		return fmt.Errorf("pack %s schema fragment: %w", pack, err)
	}
	p := filepath.Join(root, schema.FileName)
	existing, _ := os.ReadFile(p)
	cur, err := schema.Parse(string(existing))
	if err != nil {
		return err
	}
	// A declaration the app already has (an earlier add that stopped
	// halfway, or one synced by lidza gen) is left in place; lidza gen
	// keeps it current. Only what is missing is appended.
	var missing []string
	for _, block := range schemaBlocks(string(fragment)) {
		if cur.Model(block.name) != nil || cur.Enum(block.name) != nil {
			continue
		}
		missing = append(missing, block.text)
	}
	if len(missing) == 0 {
		return nil
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(bytes.TrimSpace(existing)) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		f.WriteString("\n")
	}
	if len(bytes.TrimSpace(existing)) > 0 {
		f.WriteString("\n")
	}
	fmt.Fprintf(f, "// Types of the %s pack.\n", pack)
	_, err = f.WriteString(strings.Join(missing, "\n\n") + "\n")
	return err
}

func writeIfMissing(dst, src string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	data, err := officialFS.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func appendEnvExample(root string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	p := filepath.Join(root, ".env.example")
	existing, _ := os.ReadFile(p)
	if bytes.Contains(existing, []byte(lines[0])) {
		return nil
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	return err
}

// SQLCFile is the sqlc configuration `lidza pack add db` writes.
const SQLCFile = "sqlc.yaml"

// QueriesDir holds the SQL files sqlc compiles.
const QueriesDir = "db/queries"

func writeSQLCConfig(root string) error {
	p := filepath.Join(root, SQLCFile)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	cfg := `# Written by lidza pack add db. Queries in db/queries/*.sql become typed
# Go in db/queries/gen (package queries) on lidza gen. The schema is
# db/schema.sql, generated from schema.lidza.
version: "2"
sql:
  - engine: "postgresql"
    schema: "db/schema.sql"
    queries: "db/queries"
    gen:
      go:
        package: "queries"
        out: "db/queries/gen"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_pointers_for_null_types: true
        overrides:
          - db_type: "uuid"
            go_type: "string"
          - db_type: "uuid"
            nullable: true
            go_type:
              type: "string"
              pointer: true
          - db_type: "timestamptz"
            go_type: "time.Time"
          - db_type: "timestamptz"
            nullable: true
            go_type:
              type: "time.Time"
              pointer: true
          - db_type: "date"
            go_type: "time.Time"
          - db_type: "date"
            nullable: true
            go_type:
              type: "time.Time"
              pointer: true
`
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, QueriesDir), 0o755); err != nil {
		return err
	}
	example := `-- Queries for sqlc. One comment names each query and its shape:
--   :one   a single row      :many  a list      :exec  no rows
-- Tables and columns come from db/schema.sql (generated from schema.lidza).
--
-- name: Now :one
SELECT now()::timestamptz AS now;
`
	return os.WriteFile(filepath.Join(root, QueriesDir, "queries.sql"), []byte(example), 0o644)
}
