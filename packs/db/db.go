// Package db is the official database pack: a bounded pgx pool from
// DATABASE_URL, migrations from db/migrations, and the sqlc configuration
// `lidza pack add db` writes so queries in db/queries become typed Go.
package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// Config comes from the environment (see pkg/env).
type Config struct {
	// URL is the connection string, e.g. postgres://user:pass@host/db or
	// postgres:///db?host=/var/run/postgresql for a Unix socket.
	URL string `env:"DATABASE_URL" required:"true"`
	// MaxConns bounds the pool; size it below Postgres max_connections
	// divided by the number of app nodes.
	MaxConns       int           `env:"DB_MAX_CONNS" default:"10"`
	MinConns       int           `env:"DB_MIN_CONNS" default:"0"`
	ConnectTimeout time.Duration `env:"DB_CONNECT_TIMEOUT" default:"5s"`
	// Migrate applies pending migrations from db/migrations at start.
	Migrate bool `env:"DB_MIGRATE"`
	// MigrationsDir is where the migration files live, relative to the
	// working directory.
	MigrationsDir string `env:"DB_MIGRATIONS_DIR" default:"db/migrations"`
}

// DB is the running pack.
type DB struct {
	Pool *pgxpool.Pool
	cfg  Config
}

// Pack returns the pack for packs.go. Configuration is read at Start.
func Pack() lidza.Pack { return &DB{} }

// From returns the pool from a request context.
func From(ctx context.Context) *pgxpool.Pool { return lidza.Service[*DB](ctx).Pool }

// Name implements lidza.Pack.
func (d *DB) Name() string { return "lidza/db" }

// Start reads the configuration, opens the pool, pings once and, when
// DB_MIGRATE is set, applies pending migrations.
func (d *DB) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &d.cfg); err != nil {
		return err
	}
	pool, err := Open(ctx, d.cfg)
	if err != nil {
		return err
	}
	d.Pool = pool
	if d.cfg.Migrate {
		applied, err := Migrate(ctx, pool, os.DirFS(d.cfg.MigrationsDir))
		if err != nil {
			pool.Close()
			return fmt.Errorf("migrate: %w", err)
		}
		if len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "db: applied %d migration(s)\n", len(applied))
		}
	}
	lidza.Provide(s, d)
	lidza.Provide(s, pool)
	return nil
}

// Stop closes the pool after in-flight queries finish.
func (d *DB) Stop(context.Context) error {
	if d.Pool != nil {
		d.Pool.Close()
	}
	return nil
}

// Ping checks the database is reachable.
func (d *DB) Ping(ctx context.Context) error { return d.Pool.Ping(ctx) }

// Ready implements telemetry.Ready for /readyz.
func (d *DB) Ready(ctx context.Context) error { return d.Pool.Ping(ctx) }

// TelemetryStats reports the pool to /metrics.
func (d *DB) TelemetryStats() map[string]float64 {
	st := d.Pool.Stat()
	return map[string]float64{
		"pool_max":             float64(st.MaxConns()),
		"pool_total":           float64(st.TotalConns()),
		"pool_idle":            float64(st.IdleConns()),
		"pool_acquired":        float64(st.AcquiredConns()),
		"acquire_total":        float64(st.AcquireCount()),
		"acquire_wait_total":   float64(st.EmptyAcquireCount()),
		"acquire_wait_seconds": st.AcquireDuration().Seconds(),
	}
}

// EnsureDatabase creates the database named in url when it does not
// exist, connecting to the "postgres" database on the same server.
func EnsureDatabase(ctx context.Context, url string) error {
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("DATABASE_URL: %w", err)
	}
	name := pc.ConnConfig.Database
	if name == "" {
		return errors.New("DATABASE_URL has no database name")
	}
	admin := pc.ConnConfig.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return fmt.Errorf("connect to create %s: %w", name, err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize())
	return err
}

// Open creates a pool from cfg and verifies it with one ping.
func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if cfg.URL == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	if cfg.MaxConns > 0 {
		pc.MaxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 {
		pc.MinConns = int32(cfg.MinConns)
	}
	if cfg.ConnectTimeout > 0 {
		pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, max(cfg.ConnectTimeout, time.Second))
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database not reachable: %w", err)
	}
	return pool, nil
}
