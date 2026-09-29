package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/env"
)

const dbUsage = `usage:
  lidza db migrate            apply pending migrations from db/migrations
  lidza db rollback [--steps 1]  revert the last applied migration(s)
  lidza db status             list migrations and whether they are applied
DATABASE_URL comes from .env, .env.<mode> or the environment. migrate and
rollback refuse a database on another host (not a Unix socket, localhost
or a loopback address) unless --production is given.
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
	production := fs.Bool("production", false, "allow migrate or rollback on a database on another host")
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
	if (sub == "migrate" || sub == "rollback") && !*production {
		if host := remoteDBHost(cfg.URL); host != "" {
			return fmt.Errorf("db %s: DATABASE_URL points at %s, not this machine; run lidza db %s --production to change that database", sub, host, sub)
		}
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
		if len(applied) > 0 {
			// A lidza dev running here restarts the app on the new schema.
			devserver.RequestRestart(abs)
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
		if len(reverted) > 0 {
			devserver.RequestRestart(abs)
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

// remoteDBHost is the first host of a connection string that is not this
// machine (a Unix socket, localhost or a loopback address), or "" when
// every host is local. A string that does not parse is left to db.Open.
func remoteDBHost(url string) string {
	pc, err := pgconn.ParseConfig(url)
	if err != nil {
		return ""
	}
	hosts := []string{pc.Host}
	for _, f := range pc.Fallbacks {
		hosts = append(hosts, f.Host)
	}
	for _, h := range hosts {
		if !localDBHost(h) {
			return h
		}
	}
	return ""
}

func localDBHost(host string) bool {
	if host == "" || strings.HasPrefix(host, "/") || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
