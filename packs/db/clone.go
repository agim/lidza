package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A test package's own database is a clone of the test database:
// <base>_pkg_<package>_test for <base>_test. The clones of one test
// database share that prefix, so lidza test drops them all at the end.
const cloneInfix = "_pkg_"

// CloneTestDatabase creates a fresh copy of the test database url names
// for one test package (key, its directory: "tests/billing") and returns
// the copy's URL. The copy is dropped first when it exists, so it starts
// from the test database as it is now (migrated by lidza test). Only a
// test database on this machine is cloned, as DropTestDatabase drops.
func CloneTestDatabase(ctx context.Context, rawURL, key string) (string, error) {
	pc, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL: %w", err)
	}
	name := pc.ConnConfig.Database
	if err := localTestDatabase(name, pc.ConnConfig.Host); err != nil {
		return "", err
	}
	clone := cloneName(name, key)
	admin := pc.ConnConfig.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return "", fmt.Errorf("connect to clone %s: %w", name, err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{clone}.Sanitize()+` WITH (FORCE)`); err != nil {
		return "", fmt.Errorf("drop %s: %w", clone, err)
	}
	// Postgres copies a template only while nobody is connected to it:
	// another package's clone or a test outside lidzatest may be, for a
	// moment.
	create := `CREATE DATABASE ` + pgx.Identifier{clone}.Sanitize() + ` TEMPLATE ` + pgx.Identifier{name}.Sanitize()
	deadline := time.Now().Add(30 * time.Second)
	for wait := 50 * time.Millisecond; ; wait = min(wait*2, time.Second) {
		_, err = conn.Exec(ctx, create)
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "55006" || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	if err != nil {
		return "", fmt.Errorf("clone %s as %s: %w", name, clone, err)
	}
	return withDatabase(rawURL, clone)
}

// DropTestClones drops every clone CloneTestDatabase made of the test
// database url names, and returns how many.
func DropTestClones(ctx context.Context, rawURL string) (int, error) {
	pc, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return 0, fmt.Errorf("DATABASE_URL: %w", err)
	}
	name := pc.ConnConfig.Database
	if err := localTestDatabase(name, pc.ConnConfig.Host); err != nil {
		return 0, err
	}
	admin := pc.ConnConfig.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return 0, fmt.Errorf("connect to drop the clones of %s: %w", name, err)
	}
	defer conn.Close(ctx)
	prefix := strings.TrimSuffix(name, "_test") + cloneInfix
	rows, err := conn.Query(ctx, `SELECT datname FROM pg_database WHERE starts_with(datname, $1) AND datname LIKE '%\_test'`, prefix)
	if err != nil {
		return 0, err
	}
	clones, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, c := range clones {
		if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{c}.Sanitize()+` WITH (FORCE)`); err != nil {
			return 0, fmt.Errorf("drop %s: %w", c, err)
		}
	}
	return len(clones), nil
}

// localTestDatabase refuses a database that is not a test database on
// this machine (a Unix socket or a loopback address).
func localTestDatabase(name, host string) error {
	if !strings.HasSuffix(name, "_test") {
		return fmt.Errorf("refusing %q: only a database whose name ends in _test", name)
	}
	if !strings.HasPrefix(host, "/") && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("refusing %q on %s: only a database on this machine", name, host)
	}
	return nil
}

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

// cloneName is <base>_pkg_<key>_test, within Postgres's 63 bytes: a key
// too long for it is cut and given a hash, so two keys never share a
// name.
func cloneName(name, key string) string {
	base := strings.TrimSuffix(name, "_test")
	slug := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(key), "_"), "_")
	if slug == "" {
		slug = "root"
	}
	if room := 63 - len(base) - len(cloneInfix) - len("_test"); len(slug) > room {
		sum := sha256.Sum256([]byte(key))
		h := hex.EncodeToString(sum[:])[:8]
		slug = slug[:max(0, room-len(h)-1)] + "_" + h
	}
	return base + cloneInfix + slug + "_test"
}

// withDatabase is rawURL naming database name instead: a postgres:// URL
// gets a new path, a keyword string a new dbname.
func withDatabase(rawURL, name string) (string, error) {
	if u, err := url.Parse(rawURL); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path, u.RawPath = "/"+name, ""
		return u.String(), nil
	}
	re := regexp.MustCompile(`(^|\s)dbname=('[^']*'|\S+)`)
	if re.MatchString(rawURL) {
		return re.ReplaceAllString(rawURL, "${1}dbname="+name), nil
	}
	return rawURL + " dbname=" + name, nil
}
