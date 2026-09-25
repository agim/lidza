package db

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/acme/autocert"
)

// CertCache stores TLS certificates and ACME account keys in the
// tls_certificate table, so every node serving LIDZA_TLS_DOMAINS reads
// the same ones (autocert.Cache). The db pack provides it at start; the
// framework picks it up when it serves TLS. The table is created on
// first use, outside the app's schema: it is the framework's, not the
// app's data.
type CertCache struct {
	pool *pgxpool.Pool
	once sync.Once
	err  error
}

// NewCertCache returns a cache over pool.
func NewCertCache(pool *pgxpool.Pool) *CertCache { return &CertCache{pool: pool} }

func (c *CertCache) ensure(ctx context.Context) error {
	c.once.Do(func() {
		_, c.err = c.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tls_certificate (
  key text PRIMARY KEY,
  data bytea NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
)`)
	})
	return c.err
}

// Get implements autocert.Cache.
func (c *CertCache) Get(ctx context.Context, key string) ([]byte, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	var data []byte
	err := c.pool.QueryRow(ctx, `SELECT data FROM tls_certificate WHERE key = $1`, key).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, autocert.ErrCacheMiss
	}
	return data, err
}

// Put implements autocert.Cache.
func (c *CertCache) Put(ctx context.Context, key string, data []byte) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	_, err := c.pool.Exec(ctx, `INSERT INTO tls_certificate (key, data) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, key, data)
	return err
}

// Delete implements autocert.Cache.
func (c *CertCache) Delete(ctx context.Context, key string) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	_, err := c.pool.Exec(ctx, `DELETE FROM tls_certificate WHERE key = $1`, key)
	return err
}
