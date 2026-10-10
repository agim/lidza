package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Table records applied migrations.
const Table = "lidza_migrations"

// lockID is the advisory lock that serializes migrators across nodes.
const lockID = 0x6c69647a61 // "lidza"

// Migration is one name from db/migrations with its state.
type Migration struct {
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// files lists the migration names (NNNN_name) that have an up script. A
// directory that does not exist yet (no model in schema.lidza so far)
// holds no migrations.
func files(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, strings.TrimSuffix(e.Name(), ".up.sql"))
		}
	}
	sort.Strings(names)
	return names, nil
}

func ensureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+Table+` (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`)
	return err
}

func applied(ctx context.Context, pool *pgxpool.Pool) (map[string]time.Time, error) {
	rows, err := pool.Query(ctx, `SELECT name, applied_at FROM `+Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var name string
		var at time.Time
		if err := rows.Scan(&name, &at); err != nil {
			return nil, err
		}
		out[name] = at
	}
	return out, rows.Err()
}

// Status lists every migration file and whether it is applied.
func Status(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]Migration, error) {
	if err := ensureTable(ctx, pool); err != nil {
		return nil, err
	}
	names, err := files(fsys)
	if err != nil {
		return nil, err
	}
	done, err := applied(ctx, pool)
	if err != nil {
		return nil, err
	}
	out := make([]Migration, len(names))
	for i, n := range names {
		at, ok := done[n]
		out[i] = Migration{Name: n, Applied: ok, AppliedAt: at}
	}
	return out, nil
}

// NoTransaction, as the first line of a migration script, runs its
// statements one at a time outside a transaction: what CREATE INDEX
// CONCURRENTLY needs. Each statement ends with ";" at the end of a line
// and must be safe to run again (IF NOT EXISTS, IF EXISTS), since a
// failure leaves the ones before it applied. lidza gen writes such a
// migration, after the transactional one, for indexes and checks on
// tables that already hold rows.
const NoTransaction = "-- lidza:no-transaction"

// DefaultLockTimeout bounds how long a migration statement waits for a
// lock: a migration queued behind a long query would otherwise block
// every query queued behind it. A statement that times out is retried.
const DefaultLockTimeout = 5 * time.Second

// lockRetries is how often a statement that hit the lock timeout is
// tried again, waiting 1s, 2s, 4s between.
const lockRetries = 3

// MigrateOption tunes a migration run.
type MigrateOption func(*migrateOptions)

type migrateOptions struct {
	lockTimeout time.Duration
	sleep       func(time.Duration)
}

// LockTimeout sets the lock timeout (DB_MIGRATE_LOCK_TIMEOUT; default
// DefaultLockTimeout). A script that sets lock_timeout itself keeps its
// own.
func LockTimeout(d time.Duration) MigrateOption {
	return func(o *migrateOptions) {
		if d > 0 {
			o.lockTimeout = d
		}
	}
}

func options(opts []MigrateOption) migrateOptions {
	o := migrateOptions{lockTimeout: DefaultLockTimeout, sleep: time.Sleep}
	for _, f := range opts {
		f(&o)
	}
	return o
}

// Migrate applies every pending up script in order, each in its own
// transaction (or statement by statement, for a NoTransaction script),
// under an advisory lock so two nodes never race. It returns the names
// applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, opts ...MigrateOption) ([]string, error) {
	applied, _, err := MigrateUntil(ctx, pool, fsys, nil, opts...)
	return applied, err
}

// DataLoss marks a statement of a generated migration that drops data
// (a column, a table); a developer reads it before it runs.
const DataLoss = "-- review: data loss"

// MigrateUntil is Migrate stopping before the first pending migration
// hold returns true for (fresh says no migration was applied before this
// run: a new database holds no data to lose). It returns the names
// applied and the one it stopped at, if any.
func MigrateUntil(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, hold func(name, sql string, fresh bool) bool, opts ...MigrateOption) (appliedNow []string, held string, err error) {
	o := options(opts)
	err = withLock(ctx, pool, func(ctx context.Context) error {
		status, err := Status(ctx, pool, fsys)
		if err != nil {
			return err
		}
		fresh := true
		for _, m := range status {
			if m.Applied {
				fresh = false
			}
		}
		for _, m := range status {
			if m.Applied {
				continue
			}
			sql, err := fs.ReadFile(fsys, m.Name+".up.sql")
			if err != nil {
				return err
			}
			if hold != nil && hold(m.Name, string(sql), fresh) {
				held = m.Name
				return nil
			}
			if err := o.run(ctx, pool, string(sql), `INSERT INTO `+Table+` (name) VALUES ($1)`, m.Name); err != nil {
				return fmt.Errorf("%s: %w", m.Name, err)
			}
			appliedNow = append(appliedNow, m.Name)
		}
		return nil
	})
	return appliedNow, held, err
}

// HoldDataLoss holds a migration that drops data on a database that has
// some: the hold function for MigrateUntil.
func HoldDataLoss(_, sql string, fresh bool) bool {
	return !fresh && strings.Contains(sql, DataLoss)
}

// Rollback reverts the last steps applied migrations using their down
// scripts, newest first.
func Rollback(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, steps int, opts ...MigrateOption) ([]string, error) {
	o := options(opts)
	var reverted []string
	err := withLock(ctx, pool, func(ctx context.Context) error {
		status, err := Status(ctx, pool, fsys)
		if err != nil {
			return err
		}
		for i := len(status) - 1; i >= 0 && steps > 0; i-- {
			m := status[i]
			if !m.Applied {
				continue
			}
			sql, err := fs.ReadFile(fsys, m.Name+".down.sql")
			if err != nil {
				return err
			}
			if err := o.run(ctx, pool, string(sql), `DELETE FROM `+Table+` WHERE name = $1`, m.Name); err != nil {
				return fmt.Errorf("%s: %w", m.Name, err)
			}
			reverted = append(reverted, m.Name)
			steps--
		}
		return nil
	})
	return reverted, err
}

// run applies one script and records it, retrying what hit the lock
// timeout.
func (o migrateOptions) run(ctx context.Context, pool *pgxpool.Pool, script, record, name string) error {
	if strings.HasPrefix(strings.TrimSpace(script), NoTransaction) {
		return o.runEach(ctx, pool, script, record, name)
	}
	return o.retry(ctx, func() error { return o.runInTx(ctx, pool, script, record, name) })
}

// retry runs f again after a lock timeout (SQLSTATE 55P03), backing off.
func (o migrateOptions) retry(ctx context.Context, f func() error) error {
	wait := time.Second
	for attempt := 0; ; attempt++ {
		err := f()
		var pgErr *pgconn.PgError
		if err == nil || attempt == lockRetries || !errors.As(err, &pgErr) || pgErr.Code != "55P03" || ctx.Err() != nil {
			if err != nil && errors.As(err, &pgErr) && pgErr.Code == "55P03" {
				return fmt.Errorf("%w (waited %s for a lock %d times: a long transaction holds the table; retry when it ends, or raise DB_MIGRATE_LOCK_TIMEOUT)", err, o.lockTimeout, attempt+1)
			}
			return err
		}
		o.sleep(wait)
		wait *= 2
	}
}

func (o migrateOptions) runInTx(ctx context.Context, pool *pgxpool.Pool, script, record string, name string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", o.lockTimeout.Milliseconds())); err != nil {
		return err
	}
	if strings.TrimSpace(stripComments(script)) != "" {
		if _, err := tx.Exec(ctx, script); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, record, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// runEach runs a NoTransaction script statement by statement on one
// connection, then records it.
func (o migrateOptions) runEach(ctx context.Context, pool *pgxpool.Pool, script, record, name string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%dms'", o.lockTimeout.Milliseconds())); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "RESET lock_timeout")
	for _, stmt := range statements(script) {
		if err := o.retry(ctx, func() error {
			_, err := conn.Exec(ctx, stmt)
			return err
		}); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	_, err = conn.Exec(ctx, record, name)
	return err
}

// statements splits a NoTransaction script: comment lines dropped,
// each statement ending with ";" at the end of a line, or before a
// trailing "-- " comment.
func statements(script string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(stripComments(script), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cur.WriteString(line + "\n")
		if t := strings.TrimSpace(line); strings.HasSuffix(t, ";") || strings.Contains(t, "; --") {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 80 {
		line = line[:80] + "..."
	}
	return line
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "--") {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func withLock(ctx context.Context, pool *pgxpool.Pool, f func(context.Context) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(lockID)); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(lockID))
	return f(ctx)
}

// withSleep replaces the wait between lock retries (tests).
func withSleep(f func(time.Duration)) MigrateOption {
	return func(o *migrateOptions) { o.sleep = f }
}
