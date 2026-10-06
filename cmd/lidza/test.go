package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/scaffold"
)

// runTest runs the app's Go tests with LIDZA_MODE=test: the test database
// from .env.test is created when missing and migrated, then `go test`
// runs with the remaining arguments, then the frontend check.
func runTest(ctx context.Context, args []string) error {
	fs := flags("test")
	dir := fs.String("dir", ".", "project directory")
	e2e := fs.Bool("e2e", false, "build the binary, start it with .env.test and run the Playwright suite (e2e/) instead of the Go tests")
	install := fs.Bool("install", false, "with --e2e: install the browser when it is missing")
	verbose := fs.Bool("v", false, "go test -v: every test by name as it runs")
	runOnly := fs.String("run", "", "only the Go tests matching this regexp (go test -run)")
	fresh := fs.Bool("fresh", false, "drop and recreate the test database first (a migration from another branch left in it)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	extra := fs.Args()
	if *runOnly != "" {
		extra = append([]string{"-run", *runOnly}, extra...)
	}
	if *verbose {
		extra = append([]string{"-v"}, extra...)
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	os.Setenv(devserver.EnvMode, "test")
	freshTestDB = *fresh
	if *e2e {
		if cfg == nil {
			return errors.New("test --e2e needs a lidza.json project")
		}
		return runE2E(ctx, abs, cfg, *install, extra)
	}
	if cfg != nil {
		if err := generateAll(abs, cfg, os.Stdout); err != nil {
			return err
		}
	}
	if err := goTest(ctx, abs, cfg, extra, os.Stdout); err != nil {
		return err
	}
	if cfg != nil && cfg.Frontend.Dist != "" {
		if _, err := os.Stat(filepath.Join(abs, "node_modules")); err == nil {
			fmt.Println("[lidza] npm run check")
			if err := run(ctx, abs, "npm", "run", "check"); err != nil {
				return fmt.Errorf("frontend check failed")
			}
		}
	}
	return nil
}

// freshTestDB is lidza test --fresh: the test database is recreated.
var freshTestDB bool

// prepareTestDB creates the database named in DATABASE_URL (after .env
// and .env.test) when it does not exist, and applies the migrations.
func prepareTestDB(ctx context.Context, dir string) error {
	ensureWorktreeTestDB(dir)
	var cfg db.Config
	if err := env.Load(dir, &cfg); err != nil {
		return fmt.Errorf("test database: %w", err)
	}
	if freshTestDB {
		fmt.Println("[lidza] test database: dropped and created again (--fresh)")
		if err := db.DropTestDatabase(ctx, cfg.URL); err != nil {
			return fmt.Errorf("test database: %w", err)
		}
	}
	if err := db.EnsureDatabase(ctx, cfg.URL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool, os.DirFS(filepath.Join(dir, cfg.MigrationsDir)))
	if err != nil {
		// Loud: a run that stops here has run no test.
		return fmt.Errorf("FAILED, no test ran: the test database could not be migrated: %w. A migration from another branch may be in it: lidza test --fresh drops and recreates it", err)
	}
	if len(applied) > 0 {
		fmt.Printf("[lidza] test database: applied %d migration(s)\n", len(applied))
	}
	return nil
}

// runE2E builds the app, starts the binary on a free port with the test
// environment, and runs `npx playwright test` against it. The browser is
// resolved by the app's own Playwright; a missing one is reported with
// the install command, or installed with --install.
func runE2E(ctx context.Context, dir string, cfg *config.Config, install bool, extra []string) error {
	if _, err := os.Stat(filepath.Join(dir, "playwright.config.ts")); err != nil {
		return errors.New("no playwright.config.ts: the react, svelte and astro templates ship one; htmx pages are tested in Go (pages_test.go)")
	}
	if err := devserver.EnsureNodeModules(ctx, dir, os.Stdout); err != nil {
		return err
	}
	exe, err := browserPath(ctx, dir)
	if err != nil {
		return err
	}
	if !fileExists(exe) {
		if !install {
			return fmt.Errorf("browser for e2e tests is missing (%s); run: npx playwright install --with-deps chromium (or lidza test --e2e --install)", exe)
		}
		fmt.Println("[lidza] npx playwright install --with-deps chromium")
		if err := run(ctx, dir, "npx", "playwright", "install", "--with-deps", "chromium"); err != nil {
			return fmt.Errorf("browser install failed: %w", err)
		}
	}
	base, stop, err := startTestApp(ctx, dir, cfg)
	if err != nil {
		return err
	}
	defer stop()
	fmt.Printf("[lidza] app on %s; npx playwright test\n", base)
	pw := exec.CommandContext(ctx, "npx", append([]string{"playwright", "test"}, extra...)...)
	pw.Dir = dir
	pw.Env = append(testEnv(dir), "BASE_URL="+base)
	pw.Stdout = os.Stdout
	pw.Stderr = os.Stderr
	if err := pw.Run(); err != nil {
		return errors.New("e2e tests failed")
	}
	return nil
}

// E2ESeed is the SQL lidza test --e2e loads into the test database before
// the app starts: rows the browser suite needs that no page creates
// (listings a sync job would fetch, reference data). It runs on every
// run, so it is written to be rerun (ON CONFLICT DO NOTHING, or a
// DELETE first).
const E2ESeed = "e2e/seed.sql"

// seedTestDB runs E2ESeed against the test database when the app has one.
func seedTestDB(ctx context.Context, dir string) error {
	sql, err := os.ReadFile(filepath.Join(dir, E2ESeed))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg db.Config
	if err := env.Load(dir, &cfg); err != nil {
		return fmt.Errorf("e2e seed: %w", err)
	}
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("e2e seed: %s: %w", E2ESeed, err)
	}
	fmt.Printf("[lidza] test database: %s loaded\n", E2ESeed)
	return nil
}

// testEnv is the environment of a test process: this one's without the
// master key and without any variable named like one of the app's
// credentials (a production value exported in an agent's shell), and
// with the key file off, so tests run on the test providers and settings
// only (.env.test) and never seal or read a real secret. A test that
// needs a key sets LIDZA_MASTER_KEY itself.
func testEnv(dir string) []string {
	drop := map[string]bool{credentials.EnvMasterKey: true}
	if raw, err := credentials.Read(dir); err == nil {
		for name := range raw {
			_, bare, ok := strings.Cut(name, ".")
			if !ok {
				bare = name
			}
			drop[bare] = true
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			env = append(env, kv)
		}
	}
	return append(env, credentials.EnvKeyOff+"=1")
}

// ensureWorktreeTestDB gives a git worktree its own test database when
// lidza install has not: the committed .env.test names the shared one,
// which another worktree's migrations would change under this one.
func ensureWorktreeTestDB(dir string) {
	wt := worktreeOf(dir)
	if wt.suffix == "" {
		return
	}
	local := filepath.Join(dir, ".env.test.local")
	existing := map[string]string{}
	if data, err := os.ReadFile(local); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
				existing[k] = v
			}
		}
	}
	if existing["DATABASE_URL"] != "" {
		return
	}
	values, err := env.Values(dir)
	if err != nil || values["DATABASE_URL"] == "" {
		return
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return
	}
	url := withDatabase(values["DATABASE_URL"], dbName(cfg.Name+wt.suffix, "test"))
	if changed, err := setEnvValue(local, "DATABASE_URL", url); err == nil && changed {
		fmt.Printf("[lidza] test database: this worktree's own, %s (.env.test.local)\n", url)
	}
}

// startTestApp generates, prepares and seeds the test database, builds
// the frontend and the binary, and starts it on a free port with the test
// environment: what the browser suite and the layout audit run against.
// stop ends it.
func startTestApp(ctx context.Context, dir string, cfg *config.Config) (base string, stop func(), err error) {
	if err := generateAll(dir, cfg, os.Stdout); err != nil {
		return "", nil, err
	}
	for _, p := range cfg.Packs {
		if p == pack.OfficialPrefix+"db" {
			if err := prepareTestDB(ctx, dir); err != nil {
				return "", nil, err
			}
			if err := seedTestDB(ctx, dir); err != nil {
				return "", nil, err
			}
		}
	}
	bin := filepath.Join(dir, devserver.BuildDir, "e2e-app")
	if cfg.Frontend.Dist != "" {
		fmt.Println("[lidza] npm run build")
		if err := run(ctx, dir, "npm", "run", "build"); err != nil {
			return "", nil, fmt.Errorf("frontend build failed: %w", err)
		}
		if err := devserver.KeepDist(dir, cfg.Frontend.Dist); err != nil {
			return "", nil, err
		}
		if n, saved, err := compressDist(dir, cfg.Frontend.Dist); err != nil {
			return "", nil, fmt.Errorf("compressing the build: %w", err)
		} else if n > 0 {
			fmt.Printf("[lidza] compressed %d file(s): %d KB less with Brotli\n", n, saved/1024)
		}
	}
	fmt.Println("[lidza] go build")
	if err := run(ctx, dir, "go", "build", "-o", bin, "."); err != nil {
		return "", nil, fmt.Errorf("go build failed: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	app := exec.CommandContext(ctx, bin)
	app.Dir = dir
	app.Env = append(testEnv(dir), devserver.EnvMode+"=test", devserver.EnvAddr+"="+addr)
	// Limits a browser suite signing everyone up from 127.0.0.1 does not
	// hit, unless .env.test or the environment sets its own.
	set, _ := env.Values(dir)
	for _, line := range scaffold.E2EEnv(cfg.Packs) {
		if k, _, _ := strings.Cut(line, "="); set[k] == "" {
			app.Env = append(app.Env, line)
		}
	}
	app.Stdout = os.Stdout
	app.Stderr = os.Stderr
	if err := app.Start(); err != nil {
		return "", nil, err
	}
	stop = func() {
		app.Process.Signal(os.Interrupt)
		app.Wait()
	}
	base = "http://" + addr
	for i := 0; i < 100; i++ {
		if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return base, stop, nil
}
