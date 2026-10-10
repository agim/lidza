package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza/pkg/schema"
)

// schemaPool is a pool on a fresh Postgres schema of its own.
func schemaPool(t *testing.T, ctx context.Context, name string) *pgxpool.Pool {
	t.Helper()
	admin, err := Open(ctx, Config{URL: testURL(t), MaxConns: 1, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+name+` CASCADE; CREATE SCHEMA `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+name+` CASCADE`) })
	sep := "&"
	if !strings.Contains(testURL(t), "?") {
		sep = "?"
	}
	pool, err := Open(ctx, Config{URL: testURL(t) + sep + "search_path=" + name, MaxConns: 4, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNoTransactionMatchesSchema(t *testing.T) {
	if NoTransaction != schema.NoTransaction {
		t.Fatalf("%q != %q", NoTransaction, schema.NoTransaction)
	}
}

// TestZeroDowntimeMigration: a generated change to a table with rows
// (a column made required and unique, a reference, a search index)
// applies, leaves only valid indexes and constraints, and rolls back.
func TestZeroDowntimeMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := schemaPool(t, ctx, "lidza_test_zdm")

	v1, err := schema.Parse("model Author {\n  id uuid @id\n}\nmodel Post {\n  id uuid @id\n  title string?\n  slug string\n  authorId uuid?\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := schema.Parse("model Author {\n  id uuid @id\n}\nmodel Post {\n  id uuid @id\n  title string @search\n  slug string @unique\n  authorId uuid? @ref(Author) @index\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	first := schema.Diff(nil, v1, 1)
	m := schema.Diff(v1, v2, 2)
	if len(m.After) == 0 {
		t.Fatal("no statements after the transaction")
	}
	fsys := fstest.MapFS{}
	add := func(name, up, down string) {
		fsys[name+".up.sql"] = &fstest.MapFile{Data: []byte(up)}
		fsys[name+".down.sql"] = &fstest.MapFile{Data: []byte(down)}
	}
	up, down := first.Files()
	add("0001_init", up, down)
	if _, err := Migrate(ctx, pool, fsys); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO author (id) VALUES ('00000000-0000-0000-0000-000000000001');
		INSERT INTO post (id, title, slug, author_id) SELECT gen_random_uuid(), 'post ' || i, 'post-' || i, '00000000-0000-0000-0000-000000000001' FROM generate_series(1, 500) i`); err != nil {
		t.Fatal(err)
	}
	m.Name = "0002_alter_post"
	up, down = m.Files()
	add(m.Name, up, down)
	up, down = m.AfterFiles("0003_alter_post_concurrently")
	add("0003_alter_post_concurrently", up, down)
	if applied, err := Migrate(ctx, pool, fsys); err != nil || len(applied) != 2 {
		t.Fatalf("migrate: %v %v\n%s", applied, err, up)
	}

	var invalid, checks int
	pool.QueryRow(ctx, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid WHERE c.relname = 'post' AND NOT i.indisvalid`).Scan(&invalid)
	pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid = 'post'::regclass AND (NOT convalidated OR conname LIKE '%not_null')`).Scan(&checks)
	if invalid != 0 || checks != 0 {
		t.Fatalf("%d invalid indexes, %d unvalidated or leftover constraints", invalid, checks)
	}
	var notNull bool
	pool.QueryRow(ctx, `SELECT attnotnull FROM pg_attribute WHERE attrelid = 'post'::regclass AND attname = 'title'`).Scan(&notNull)
	if !notNull {
		t.Fatal("title is not NOT NULL")
	}
	var idx []string
	rows, _ := pool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE tablename = 'post' ORDER BY 1`)
	for rows.Next() {
		var n string
		rows.Scan(&n)
		idx = append(idx, n)
	}
	if got := strings.Join(idx, ","); !strings.Contains(got, "post_search_idx") || !strings.Contains(got, "post_slug_key") || !strings.Contains(got, "author_id") {
		t.Fatalf("indexes: %s", got)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post (id, title, slug) VALUES (gen_random_uuid(), 't', 'post-1')`); err == nil {
		t.Fatal("unique slug not enforced")
	}

	if reverted, err := Rollback(ctx, pool, fsys, 2); err != nil || len(reverted) != 2 {
		t.Fatalf("rollback: %v %v", reverted, err)
	}
	pool.QueryRow(ctx, `SELECT attnotnull FROM pg_attribute WHERE attrelid = 'post'::regclass AND attname = 'title'`).Scan(&notNull)
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename = 'post' AND indexname <> 'post_pkey'`).Scan(&left)
	if notNull || left != 0 {
		t.Fatalf("after rollback: not null %v, %d indexes", notNull, left)
	}
	// And forward again: the scripts run twice without leftovers.
	if _, err := Migrate(ctx, pool, fsys); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationLockTimeout: a statement waits at most the lock timeout
// for a table another transaction holds, retries, and succeeds once
// the lock is gone; while it is held it fails with the reason.
func TestMigrationLockTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := schemaPool(t, ctx, "lidza_test_lock")
	if _, err := pool.Exec(ctx, `CREATE TABLE things (id int)`); err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"0001_add.up.sql":   {Data: []byte("ALTER TABLE things ADD COLUMN name text;\n")},
		"0001_add.down.sql": {Data: []byte("ALTER TABLE things DROP COLUMN name;\n")},
		"0002_idx.up.sql":   {Data: []byte(NoTransaction + "\nDROP INDEX CONCURRENTLY IF EXISTS things_id_idx;\nCREATE INDEX CONCURRENTLY things_id_idx ON things (id);\n")},
		"0002_idx.down.sql": {Data: []byte(NoTransaction + "\nDROP INDEX CONCURRENTLY IF EXISTS things_id_idx;\n")},
	}
	hold := func() pgx.Tx {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `LOCK TABLE things IN ACCESS SHARE MODE`); err != nil {
			t.Fatal(err)
		}
		return tx
	}

	// Held throughout: every attempt times out quickly, and the error says so.
	tx := hold()
	sleeps := 0
	start := time.Now()
	_, err := Migrate(ctx, pool, fsys, LockTimeout(100*time.Millisecond), withSleep(func(time.Duration) { sleeps++ }))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" || !strings.Contains(err.Error(), "DB_MIGRATE_LOCK_TIMEOUT") || sleeps != lockRetries {
		t.Fatalf("held lock: %v (%d sleeps)", err, sleeps)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("waited %v", time.Since(start))
	}
	tx.Rollback(ctx)

	// Released during the backoff: the retry applies it.
	tx = hold()
	applied, err := Migrate(ctx, pool, fsys, LockTimeout(100*time.Millisecond), withSleep(func(time.Duration) { tx.Rollback(ctx) }))
	if err != nil || strings.Join(applied, ",") != "0001_add,0002_idx" {
		t.Fatalf("retry: %v %v", applied, err)
	}
}

func TestStatements(t *testing.T) {
	got := statements(NoTransaction + "\n-- note\nALTER TABLE a VALIDATE CONSTRAINT c; -- review: x\nCREATE INDEX CONCURRENTLY i\n  ON a (b);\n\nDO $$ BEGIN PERFORM 1; END $$;\n")
	want := []string{"ALTER TABLE a VALIDATE CONSTRAINT c; -- review: x", "CREATE INDEX CONCURRENTLY i\n  ON a (b);", "DO $$ BEGIN PERFORM 1; END $$;"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
}
