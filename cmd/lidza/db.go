package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/env"
)

const dbUsage = `usage:
  lidza db migrate            apply pending migrations from db/migrations
  lidza db rollback [--steps 1]  revert the last applied migration(s)
  lidza db status             list migrations and whether they are applied
DATABASE_URL comes from .env, .env.<mode> or the environment.
`

func runDB(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, dbUsage)
		return errors.New("db: subcommand required")
	}
	sub, rest := args[0], args[1:]
	fs := flags("db " + sub)
	dir := fs.String("dir", ".", "project directory")
	steps := fs.Int("steps", 1, "migrations to revert (rollback)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	var cfg db.Config
	if err := env.Load(abs, &cfg); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	migrations := os.DirFS(filepath.Join(abs, cfg.MigrationsDir))

	switch sub {
	case "migrate":
		applied, err := db.Migrate(ctx, pool, migrations)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			fmt.Println("up to date")
		}
		for _, name := range applied {
			fmt.Println("applied", name)
		}
	case "rollback":
		reverted, err := db.Rollback(ctx, pool, migrations, *steps)
		if err != nil {
			return err
		}
		if len(reverted) == 0 {
			fmt.Println("nothing to roll back")
		}
		for _, name := range reverted {
			fmt.Println("reverted", name)
		}
	case "status":
		status, err := db.Status(ctx, pool, migrations)
		if err != nil {
			return err
		}
		if len(status) == 0 {
			fmt.Println("no migrations in", cfg.MigrationsDir)
		}
		for _, m := range status {
			state := "pending"
			if m.Applied {
				state = "applied " + m.AppliedAt.Format("2006-01-02 15:04")
			}
			fmt.Printf("%-40s %s\n", m.Name, state)
		}
	default:
		fmt.Fprint(os.Stderr, dbUsage)
		return fmt.Errorf("db: unknown subcommand %q", sub)
	}
	return nil
}
