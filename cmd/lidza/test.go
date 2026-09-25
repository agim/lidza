package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agim/lidza/packs/db"
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	os.Setenv(devserver.EnvMode, "test")
	if cfg != nil {
		if err := generateAll(abs, cfg, os.Stdout); err != nil {
			return err
		}
		for _, p := range cfg.Packs {
			if p == pack.OfficialPrefix+"db" {
				if err := prepareTestDB(ctx, abs); err != nil {
					return err
				}
			}
		}
	}
	fmt.Println("[lidza] go test ./...")
	goArgs := append([]string{"test", "./..."}, fs.Args()...)
	if err := run(ctx, abs, "go", goArgs...); err != nil {
		return fmt.Errorf("go tests failed")
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
