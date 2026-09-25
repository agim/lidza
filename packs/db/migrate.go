package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

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

// files lists the migration names (NNNN_name) that have an up script.
func files(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
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

// Migrate applies every pending up script in order, each in its own
// transaction, under an advisory lock so two nodes never race. It returns
// the names applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	var appliedNow []string
	err := withLock(ctx, pool, func(ctx context.Context) error {
		status, err := Status(ctx, pool, fsys)
		if err != nil {
			return err
		}
		for _, m := range status {
			if m.Applied {
				continue
			}
			sql, err := fs.ReadFile(fsys, m.Name+".up.sql")
			if err != nil {
				return err
			}
			if err := runInTx(ctx, pool, string(sql), `INSERT INTO `+Table+` (name) VALUES ($1)`, m.Name); err != nil {
				return fmt.Errorf("%s: %w", m.Name, err)
			}
			appliedNow = append(appliedNow, m.Name)
		}
		return nil
	})
	return appliedNow, err
}

// Rollback reverts the last steps applied migrations using their down
// scripts, newest first.
func Rollback(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, steps int) ([]string, error) {
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
			if err := runInTx(ctx, pool, string(sql), `DELETE FROM `+Table+` WHERE name = $1`, m.Name); err != nil {
				return fmt.Errorf("%s: %w", m.Name, err)
			}
			reverted = append(reverted, m.Name)
			steps--
		}
		return nil
	})
	return reverted, err
}

func runInTx(ctx context.Context, pool *pgxpool.Pool, script, record string, name string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
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
