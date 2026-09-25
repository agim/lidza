package db

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza/pkg/credentials"
)

// CredentialStore keeps the credentials saved while the app runs (the
// admin pages) in the credential table, each value sealed with the
// master key, so every node reads the same ones. The db pack loads them
// into the runtime overrides at start and provides the store.
type CredentialStore struct {
	pool *pgxpool.Pool
	dir  string
}

// NewCredentialStore returns a store over pool for the app in dir.
func NewCredentialStore(pool *pgxpool.Pool, dir string) *CredentialStore {
	return &CredentialStore{pool: pool, dir: dir}
}

func (c *CredentialStore) ensure(ctx context.Context) error {
	_, err := c.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS credential (
  name text PRIMARY KEY,
  sealed text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
)`)
	return err
}

// Load reads and decrypts every stored value into the runtime overrides.
// Without a master key there is nothing to decrypt: the store stays
// unused and the file (if any) alone applies.
func (c *CredentialStore) Load(ctx context.Context) error {
	key, err := credentials.Key(c.dir)
	if errors.Is(err, credentials.ErrNoKey) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := c.ensure(ctx); err != nil {
		return err
	}
	rows, err := c.pool.Query(ctx, `SELECT name, sealed FROM credential`)
	if err != nil {
		return err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var name, sealed string
		if err := rows.Scan(&name, &sealed); err != nil {
			return err
		}
		plain, err := credentials.Decrypt(key, sealed)
		if err != nil {
			return fmt.Errorf("credential %s: %w (wrong master key?)", name, err)
		}
		values[name] = string(plain)
	}
	credentials.SetOverrides(values)
	return rows.Err()
}

// Save seals and stores one value and applies it at once.
func (c *CredentialStore) Save(ctx context.Context, name, value string) error {
	key, err := credentials.Key(c.dir)
	if err != nil {
		return err
	}
	if err := c.ensure(ctx); err != nil {
		return err
	}
	sealed, err := credentials.Encrypt(key, []byte(value))
	if err != nil {
		return err
	}
	if _, err := c.pool.Exec(ctx, `INSERT INTO credential (name, sealed) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET sealed = EXCLUDED.sealed, updated_at = now()`, name, sealed); err != nil {
		return err
	}
	credentials.SetOverride(name, value)
	return nil
}

// Delete removes a stored value; the file's value, if any, applies again.
func (c *CredentialStore) Delete(ctx context.Context, name string) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	if _, err := c.pool.Exec(ctx, `DELETE FROM credential WHERE name = $1`, name); err != nil {
		return err
	}
	credentials.SetOverride(name, "")
	return nil
}

// Names lists the stored names.
func (c *CredentialStore) Names(ctx context.Context) ([]string, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	rows, err := c.pool.Query(ctx, `SELECT name FROM credential`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, rows.Err()
}
