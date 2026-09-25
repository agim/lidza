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
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/pack"
)

// runTest runs the app's Go tests with LIDZA_MODE=test: the test database
// from .env.test is created when missing and migrated, then `go test`
// runs with the remaining arguments, then the frontend check.
func runTest(ctx context.Context, args []string) error {
	fs := flags("test")
	dir := fs.String("dir", ".", "project directory")
	e2e := fs.Bool("e2e", false, "build the binary, start it with .env.test and run the Playwright suite (e2e/) instead of the Go tests")
	install := fs.Bool("install", false, "with --e2e: install the browser when it is missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	os.Setenv(devserver.EnvMode, "test")
	if *e2e {
		if cfg == nil {
			return errors.New("test --e2e needs a lidza.json project")
		}
		return runE2E(ctx, abs, cfg, *install, fs.Args())
	}
	if cfg != nil {
		if err := generateAll(abs, cfg, os.Stdout); err != nil {
			return err
		}
	}
	if err := goTest(ctx, abs, cfg, fs.Args(), os.Stdout); err != nil {
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

// prepareTestDB creates the database named in DATABASE_URL (after .env
// and .env.test) when it does not exist, and applies the migrations.
func prepareTestDB(ctx context.Context, dir string) error {
	var cfg db.Config
	if err := env.Load(dir, &cfg); err != nil {
		return fmt.Errorf("test database: %w", err)
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
		return fmt.Errorf("test database: migrate: %w", err)
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
		return errors.New("no playwright.config.ts: the react template ships one; see docs/lidza-guide.md")
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
	if err := generateAll(dir, cfg, os.Stdout); err != nil {
		return err
	}
	for _, p := range cfg.Packs {
		if p == pack.OfficialPrefix+"db" {
			if err := prepareTestDB(ctx, dir); err != nil {
				return err
			}
		}
	}
	bin := filepath.Join(dir, devserver.BuildDir, "e2e-app")
	if cfg.Frontend.Dist != "" {
		fmt.Println("[lidza] npm run build")
		if err := run(ctx, dir, "npm", "run", "build"); err != nil {
			return fmt.Errorf("frontend build failed: %w", err)
		}
	}
	fmt.Println("[lidza] go build")
	if err := run(ctx, dir, "go", "build", "-o", bin, "."); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	app := exec.CommandContext(ctx, bin)
	app.Dir = dir
	app.Env = append(os.Environ(), devserver.EnvMode+"=test", devserver.EnvAddr+"="+addr)
	app.Stdout = os.Stdout
	app.Stderr = os.Stderr
	if err := app.Start(); err != nil {
		return err
	}
	defer func() {
		app.Process.Signal(os.Interrupt)
		app.Wait()
	}()
	base := "http://" + addr
	for i := 0; i < 100; i++ {
		if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("[lidza] app on %s; npx playwright test\n", base)
	pw := exec.CommandContext(ctx, "npx", append([]string{"playwright", "test"}, extra...)...)
	pw.Dir = dir
	pw.Env = append(os.Environ(), "BASE_URL="+base)
	pw.Stdout = os.Stdout
	pw.Stderr = os.Stderr
	if err := pw.Run(); err != nil {
		return errors.New("e2e tests failed")
	}
	return nil
}
