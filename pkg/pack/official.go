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
			"# lidza/db",
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
		},
		Notes: []string{
			"store password hashes with auth.HashPassword; check with auth.CheckPassword",
			"login: tokens, err := auth.From(ctx).Login(ctx, userID, claims); for browsers add auth.From(ctx).Cookies(tokens) with req.SetCookie",
			"protect routes: r.Use(auth.Require()) or wrap a sub-router; auth.CurrentUser(ctx) inside",
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

// appendSchema adds the pack's types to schema.lidza unless they exist.
func appendSchema(root, pack string, fragment []byte) error {
	frag, err := schema.Parse(string(fragment))
	if err != nil {
		return fmt.Errorf("pack %s schema fragment: %w", pack, err)
	}
	p := filepath.Join(root, schema.FileName)
	existing, _ := os.ReadFile(p)
	cur, err := schema.Parse(string(existing))
	if err != nil {
		return err
	}
	for _, t := range append(frag.Types, frag.Models...) {
		if cur.Model(t.Name) != nil {
			return fmt.Errorf("schema.lidza already declares %s; remove it or rename it before adding pack %s", t.Name, pack)
		}
	}
	for _, e := range frag.Enums {
		if cur.Enum(e.Name) != nil {
			return fmt.Errorf("schema.lidza already declares %s; remove it or rename it before adding pack %s", e.Name, pack)
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(bytes.TrimSpace(existing)) > 0 {
		f.WriteString("\n")
	}
	fmt.Fprintf(f, "// Types of the %s pack.\n", pack)
	_, err = f.Write(bytes.TrimLeft(fragment, "\n"))
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
