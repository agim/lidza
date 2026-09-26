package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/pack"
)

// setupOptions is what `lidza setup` and `lidza new --packs` do after the
// files exist: packs, environment files with real values, generated code,
// databases created and migrated, node_modules, the agent CLI, the first
// commit.
type setupOptions struct {
	Packs       []string
	Agent       string
	DatabaseURL string
	Commit      bool
	Out         io.Writer
}

// agentPackages maps an agent CLI to its npm package.
var agentPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
	"gemini": "@google/gemini-cli",
}

// runSetup is `lidza setup [--packs a,b] [--agent claude] [--database-url]
// [--no-commit]` on an existing project.
func runSetup(ctx context.Context, args []string) error {
	fs := flags("setup")
	dir := fs.String("dir", ".", "project directory")
	packs := fs.String("packs", "", "official packs to enable, comma-separated (db is added when a pack needs it)")
	agent := fs.String("agent", "", "agent CLI to install when missing: claude, codex or gemini")
	dbURL := fs.String("database-url", os.Getenv("LIDZA_DATABASE_URL"), "development DATABASE_URL (default: a local socket database named after the app; env LIDZA_DATABASE_URL)")
	noCommit := fs.Bool("no-commit", false, "do not make the first commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("setup needs a lidza.json project (lidza new <name> first)")
	}
	return setup(ctx, abs, cfg, setupOptions{Packs: splitList(*packs), Agent: *agent, DatabaseURL: *dbURL, Commit: !*noCommit, Out: os.Stdout})
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// setup runs the steps in order and prints one line per step.
func setup(ctx context.Context, dir string, cfg *config.Config, opt setupOptions) error {
	out := opt.Out
	if out == nil {
		out = io.Discard
	}
	step := func(format string, a ...any) { fmt.Fprintf(out, "[setup] "+format+"\n", a...) }
	// A step that fails from here on is reported and setup goes on, so
	// one problem (Postgres not running, npm offline) does not leave the
	// later steps silently undone; the list is repeated at the end.
	var problems []string
	problem := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		problems = append(problems, msg)
		fmt.Fprintf(out, "[setup] PROBLEM: %s\n", msg)
	}

	// 1. Packs, db first when any pack needs it.
	packs := opt.Packs
	needsDB := false
	for _, p := range packs {
		if o, ok := pack.FindOfficial(p); ok && !o.Rust && slices.Contains([]string{"auth", "jobs", "mail", "analytics"}, o.Name) {
			needsDB = true
		}
	}
	if needsDB && !slices.Contains(packs, "db") && !slices.Contains(cfg.Packs, pack.OfficialPrefix+"db") {
		packs = append([]string{"db"}, packs...)
	}
	for _, name := range packs {
		entry := name
		if _, ok := pack.FindOfficial(name); ok {
			if o, _ := pack.FindOfficial(name); !o.Rust {
				entry = pack.OfficialPrefix + name
			}
		}
		if slices.Contains(cfg.Packs, entry) {
			step("pack %s: already enabled", name)
			continue
		}
		e, o, err := pack.Add(dir, name)
		if err != nil {
			return err
		}
		cfg.Packs = append(cfg.Packs, e)
		if err := cfg.Save(dir); err != nil {
			return err
		}
		step("pack %s: %s", name, o.Description)
	}
	if len(packs) > 0 && len(cfg.Packs) == len(packs) {
		if _, err := decisions.Add(dir, "Packs at creation: "+strings.Join(packs, ", "), "The app's initial shape, chosen with lidza new --packs (or lidza setup --packs): "+packReasons(packs)+" A pack no code uses is flagged by lidza check (L011); remove it or build the feature.", "lidza.json, packs.go"); err != nil {
			return err
		}
	}
	hasDB := slices.Contains(cfg.Packs, pack.OfficialPrefix+"db")
	hasMail := slices.Contains(cfg.Packs, pack.OfficialPrefix+"mail")

	// 2. The master key for the credentials, kept out of git.
	if created, err := credentials.Generate(dir); err != nil {
		return err
	} else if created {
		step("%s created (kept out of git); secrets go in with lidza credentials set NAME=value", credentials.MasterKeyFile)
	}

	// 3. .env and .env.test with real values.
	devURL := opt.DatabaseURL
	if devURL == "" {
		// Where this machine's Postgres listens: its socket directory
		// differs between Linux and macOS, TCP when there is none.
		devURL = db.LocalURL(dbName(cfg.Name, "dev"))
	}
	testURL := withDatabase(devURL, dbName(cfg.Name, "test"))
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		values := map[string]string{
			"DATABASE_URL": devURL,
			"AUTH_SECRET":  randomHex(32),
			"MAIL_FROM":    fmt.Sprintf("%q", cfg.Name+" <"+cfg.Name+"@example.com>"),
		}
		if err := writeEnvFromExample(dir, values); err != nil {
			return err
		}
		step(".env written from .env.example: DATABASE_URL, a random AUTH_SECRET, MAIL_FROM")
	} else {
		step(".env exists, left alone")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.test")); err != nil {
		var b strings.Builder
		b.WriteString("# Test environment: lidza test creates and migrates this database.\n")
		if hasDB {
			b.WriteString("DATABASE_URL=" + testURL + "\n")
		}
		if hasMail {
			b.WriteString("MAIL_PROVIDER=outbox\n")
		}
		if err := os.WriteFile(filepath.Join(dir, ".env.test"), []byte(b.String()), 0o644); err != nil {
			return err
		}
		step(".env.test written: the %s database%s", dbName(cfg.Name, "test"), map[bool]string{true: ", mail to the outbox", false: ""}[hasMail])
	}

	// 3. Generated code.
	if err := generateAll(dir, cfg, io.Discard); err != nil {
		return err
	}
	step("generated: schema, migrations, client, skills")

	// 4. Databases. An address an earlier run wrote for another
	// machine's socket directory (Linux's on a Mac) is repaired first.
	if hasDB {
		for _, name := range []string{".env", ".env.test"} {
			if from, to, err := repairEnvSocket(filepath.Join(dir, name)); err != nil {
				return err
			} else if to != "" {
				step("%s: DATABASE_URL pointed at %s, where Postgres has no socket; now %s", name, from, to)
			}
		}
		for _, m := range []string{"", "test"} {
			applied, err := migrate(ctx, dir, m)
			if err != nil {
				problem("database (%s): %v; is Postgres running (lidza doctor)? Then lidza setup again", map[bool]string{true: "dev", false: m}[m == ""], err)
				continue
			}
			step("database %s: created if missing, %d migration(s) applied", dbName(cfg.Name, map[bool]string{true: "dev", false: m}[m == ""]), applied)
		}
	}

	// 5. Frontend dependencies. npm's own output is shown when it fails.
	if cfg.Frontend.Dist != "" {
		var npmOut bytes.Buffer
		if err := devserver.EnsureNodeModules(ctx, dir, &npmOut); err != nil {
			problem("npm install failed (%v); its output:\n%s", err, tail(npmOut.String(), 25))
		} else {
			step("node_modules: installed")
		}
	}

	// 6. The browser for the e2e suite, so the first lidza test --e2e and
	// lidza ship do not stop to fetch it. System libraries need root:
	// with passwordless sudo they are installed too, otherwise the
	// command is printed.
	if _, err := os.Stat(filepath.Join(dir, "playwright.config.ts")); err == nil {
		if _, err := exec.LookPath("npx"); err == nil {
			args := []string{"playwright", "install", "chromium"}
			if exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil {
				args = []string{"playwright", "install", "--with-deps", "chromium"}
			}
			cmd := exec.CommandContext(ctx, "npx", args...)
			cmd.Dir = dir
			if res, err := cmd.CombinedOutput(); err != nil {
				problem("browser for e2e tests not installed (%v): %s; run: npx playwright install chromium", err, tail(string(res), 5))
			} else if len(args) == 3 {
				step("browser for e2e tests: chromium installed; its system libraries need root: sudo npx playwright install-deps chromium")
			} else {
				step("browser for e2e tests: chromium installed")
			}
		}
	}

	// 7. The agent CLI.
	if opt.Agent != "" {
		pkgName, ok := agentPackages[opt.Agent]
		if !ok {
			return fmt.Errorf("unknown agent %q: claude, codex or gemini", opt.Agent)
		}
		if _, err := exec.LookPath(opt.Agent); err == nil {
			step("agent %s: installed", opt.Agent)
		} else {
			cmd := exec.CommandContext(ctx, "npm", "install", "-g", pkgName)
			if res, err := cmd.CombinedOutput(); err != nil {
				problem("agent CLI not installed: npm install -g %s: %v\n%s", pkgName, err, tail(string(res), 10))
			} else {
				step("agent %s: installed (%s); run `%s` once to sign in", opt.Agent, pkgName, opt.Agent)
			}
		}
	} else {
		var have []string
		for _, a := range []string{"claude", "codex", "gemini"} {
			if _, err := exec.LookPath(a); err == nil {
				have = append(have, a)
			}
		}
		if len(have) == 0 {
			step("no agent CLI on the PATH; lidza setup --agent claude installs one")
		}
	}

	// 7. The first commit, through the hook (lidza verify).
	if opt.Commit {
		if _, err := exec.LookPath("git"); err == nil {
			run := func(args ...string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "git", args...)
				cmd.Dir = dir
				return cmd.CombinedOutput()
			}
			if res, err := run("rev-parse", "HEAD"); err != nil || len(res) == 0 {
				if _, err := run("add", "-A"); err == nil {
					if res, err := run("commit", "-q", "-m", "Scaffold "+cfg.Name); err != nil {
						problem("first commit not made (the pre-commit hook runs lidza verify): %s", tail(strings.TrimSpace(string(res)), 15))
					} else {
						step("first commit made; lidza verify ran in the pre-commit hook")
					}
				}
			} else {
				step("repository has commits; nothing committed")
			}
		}
	}
	if len(problems) > 0 {
		fmt.Fprintf(out, "\n%d step(s) need attention; fix them and run lidza setup again (it skips what is done):\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(out, "  - %s\n", strings.SplitN(p, "\n", 2)[0])
		}
		return fmt.Errorf("setup incomplete: %d step(s) need attention", len(problems))
	}
	fmt.Fprintf(out, "\nnext:\n  lidza dev        # http://127.0.0.1:3000\n  claude           # or codex, gemini; the MCP server and the recipes are configured\n")
	return nil
}

// tail keeps the last n lines of a command's output.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// repairEnvSocket rewrites the DATABASE_URL of an env file whose Unix
// socket directory has no Postgres when this machine's Postgres listens
// in another one; it returns the old and new address, or "" when the file
// needed nothing.
func repairEnvSocket(path string) (from, to string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", nil
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "DATABASE_URL" {
			continue
		}
		raw := strings.Trim(strings.TrimSpace(value), `"'`)
		fixed, changed := db.RepairSocket(raw)
		if !changed {
			return "", "", nil
		}
		lines[i] = key + "=" + fixed
		mode := os.FileMode(0o600)
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), mode); err != nil {
			return "", "", err
		}
		return raw, fixed, nil
	}
	return "", "", nil
}

func dbName(app, mode string) string {
	return strings.ReplaceAll(app, "-", "_") + "_" + mode
}

// withDatabase replaces the database name in a Postgres URL.
func withDatabase(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// writeEnvFromExample copies .env.example to .env, setting the keys in
// values on their lines (commented or not) and appending the ones the
// example lacks.
func writeEnvFromExample(dir string, values map[string]string) error {
	example, err := os.ReadFile(filepath.Join(dir, ".env.example"))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(example), "\n"), "\n") {
		trimmed := strings.TrimLeft(line, "# ")
		key, _, ok := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if ok && !strings.Contains(key, " ") {
			if v, want := values[key]; want && !seen[key] {
				lines = append(lines, key+"="+v)
				seen[key] = true
				continue
			}
		}
		lines = append(lines, line)
	}
	for _, key := range []string{"DATABASE_URL", "AUTH_SECRET", "MAIL_FROM"} {
		if v, want := values[key]; want && !seen[key] && needsKey(dir, key) {
			lines = append(lines, key+"="+v)
		}
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// needsKey says whether a key belongs in .env when the example lacks it:
// only when its pack is enabled.
func needsKey(dir, key string) bool {
	cfg, err := config.Load(dir)
	if err != nil {
		return false
	}
	switch key {
	case "DATABASE_URL":
		return slices.Contains(cfg.Packs, pack.OfficialPrefix+"db")
	case "AUTH_SECRET":
		return slices.Contains(cfg.Packs, pack.OfficialPrefix+"auth")
	case "MAIL_FROM":
		return slices.Contains(cfg.Packs, pack.OfficialPrefix+"mail")
	}
	return false
}

// migrate creates the database of a mode ("" for development, "test")
// when missing and applies the migrations; it returns how many.
func migrate(ctx context.Context, dir, mode string) (int, error) {
	prev, had := os.LookupEnv(devserver.EnvMode)
	if mode == "" {
		os.Unsetenv(devserver.EnvMode)
	} else {
		os.Setenv(devserver.EnvMode, mode)
	}
	defer func() {
		if had {
			os.Setenv(devserver.EnvMode, prev)
		} else {
			os.Unsetenv(devserver.EnvMode)
		}
	}()
	var cfg db.Config
	if err := env.Load(dir, &cfg); err != nil {
		return 0, err
	}
	if err := db.EnsureDatabase(ctx, cfg.URL); err != nil {
		return 0, err
	}
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool, os.DirFS(filepath.Join(dir, cfg.MigrationsDir)))
	if err != nil {
		return 0, err
	}
	return len(applied), nil
}

// packReasons lists what each chosen pack is for, from the registry.
func packReasons(names []string) string {
	var parts []string
	for _, n := range names {
		if o, ok := pack.FindOfficial(n); ok {
			parts = append(parts, n+": "+strings.SplitN(o.Description, ":", 2)[0]+".")
		}
	}
	return strings.Join(parts, " ")
}
