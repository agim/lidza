package webhook

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Claim is the outcome of Store.Claim.
type Claim int

const (
	// Claimed: the id is new (or its earlier claim lapsed); handle it.
	Claimed Claim = iota
	// Duplicate: a delivery with this id was handled; acknowledge it
	// without handling it again.
	Duplicate
	// Busy: another request is handling this id now.
	Busy
)

// Store records delivery ids, so each is handled once. Scope names the
// endpoint ("stripe:PAYMENTS_WEBHOOK_SECRET").
type Store interface {
	// Claim marks id as being handled for lease. A claim older than its
	// lease and never done (a node that died mid-handler) is claimed
	// again.
	Claim(ctx context.Context, scope, id string, lease time.Duration) (Claim, error)
	// Done records id as handled.
	Done(ctx context.Context, scope, id string) error
	// Release drops an unfinished claim, so the provider's retry is
	// handled.
	Release(ctx context.Context, scope, id string) error
}

// cleanupEvery is how often a Postgres store deletes expired ids, and
// cleanupBatch how many at most per pass.
const (
	cleanupEvery = time.Hour
	cleanupBatch = 1000
)

// PostgresStore keeps delivery ids in the webhook_delivery table, so
// every node sees them. The table is created on first use, outside the
// app's schema: it is the framework's, not the app's data. Ids older
// than the retention are deleted in bounded batches, at most once an
// hour per node, after a delivery is recorded.
type PostgresStore struct {
	pool      *pgxpool.Pool
	retention time.Duration

	mu        sync.Mutex
	ensured   bool
	cleanedAt time.Time
}

// Postgres returns a store over pool that keeps ids for retention
// (DefaultRetention when zero or less). The endpoints use one over the
// db pack's pool on their own; call this to share it or set it by hand.
func Postgres(pool *pgxpool.Pool, retention time.Duration) *PostgresStore {
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &PostgresStore{pool: pool, retention: retention}
}

func (s *PostgresStore) ensure(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ensured {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS webhook_delivery (
  scope text NOT NULL,
  id text NOT NULL,
  claimed_at timestamptz NOT NULL DEFAULT now(),
  done_at timestamptz,
  PRIMARY KEY (scope, id)
)`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS webhook_delivery_claimed_at ON webhook_delivery (claimed_at)`); err != nil {
		return err
	}
	s.ensured = true
	return nil
}

// Claim implements Store.
func (s *PostgresStore) Claim(ctx context.Context, scope, id string, lease time.Duration) (Claim, error) {
	if err := s.ensure(ctx); err != nil {
		return 0, err
	}
	var ok bool
	err := s.pool.QueryRow(ctx, `INSERT INTO webhook_delivery (scope, id) VALUES ($1, $2)
ON CONFLICT (scope, id) DO UPDATE SET claimed_at = now()
  WHERE webhook_delivery.done_at IS NULL AND webhook_delivery.claimed_at < now() - make_interval(secs => $3)
RETURNING true`, scope, id, lease.Seconds()).Scan(&ok)
	if err == nil {
		return Claimed, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	var done bool
	err = s.pool.QueryRow(ctx, `SELECT done_at IS NOT NULL FROM webhook_delivery WHERE scope = $1 AND id = $2`, scope, id).Scan(&done)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Busy, nil // released between the two statements; the retry claims it
	case err != nil:
		return 0, err
	case done:
		return Duplicate, nil
	}
	return Busy, nil
}

// Done implements Store.
func (s *PostgresStore) Done(ctx context.Context, scope, id string) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE webhook_delivery SET done_at = now() WHERE scope = $1 AND id = $2`, scope, id); err != nil {
		return err
	}
	return s.cleanup(ctx)
}

// Release implements Store.
func (s *PostgresStore) Release(ctx context.Context, scope, id string) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM webhook_delivery WHERE scope = $1 AND id = $2 AND done_at IS NULL`, scope, id)
	return err
}

// cleanup deletes one batch of ids past the retention, at most once per
// cleanupEvery on this node.
func (s *PostgresStore) cleanup(ctx context.Context) error {
	s.mu.Lock()
	now := time.Now()
	due := now.Sub(s.cleanedAt) >= cleanupEvery
	if due {
		s.cleanedAt = now
	}
	s.mu.Unlock()
	if !due {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM webhook_delivery WHERE ctid IN (
  SELECT ctid FROM webhook_delivery WHERE claimed_at < now() - make_interval(secs => $1) LIMIT $2)`, s.retention.Seconds(), cleanupBatch)
	return err
}

// MemoryStore keeps delivery ids in a bounded map on this node: for
// tests and dev. When full, the oldest id is forgotten. It does not
// share ids between nodes, so a retry reaching another node is handled
// again; production uses the db pack's table.
type MemoryStore struct {
	mu    sync.Mutex
	max   int
	seq   uint64
	items map[string]memEntry
	order []memKey // insertion order, for eviction
}

type memEntry struct {
	seq       uint64
	claimedAt time.Time
	done      bool
}

type memKey struct {
	key string
	seq uint64
}

// Memory returns a store holding at most max ids (1000 when max < 1).
func Memory(max int) *MemoryStore {
	if max < 1 {
		max = 1000
	}
	return &MemoryStore{max: max, items: map[string]memEntry{}}
}

// Claim implements Store.
func (m *MemoryStore) Claim(_ context.Context, scope, id string, lease time.Duration) (Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := scope + "\x00" + id
	now := time.Now()
	if e, ok := m.items[k]; ok {
		if e.done {
			return Duplicate, nil
		}
		if now.Sub(e.claimedAt) < lease {
			return Busy, nil
		}
		e.claimedAt = now
		m.items[k] = e
		return Claimed, nil
	}
	for len(m.items) >= m.max && len(m.order) > 0 {
		old := m.order[0]
		m.order = m.order[1:]
		if e, ok := m.items[old.key]; ok && e.seq == old.seq {
			delete(m.items, old.key)
		}
	}
	// Released ids leave stale entries in order; compact when it grows
	// past twice the bound.
	if len(m.order) > 2*m.max {
		live := m.order[:0]
		for _, o := range m.order {
			if e, ok := m.items[o.key]; ok && e.seq == o.seq {
				live = append(live, o)
			}
		}
		m.order = append([]memKey(nil), live...)
	}
	m.seq++
	m.items[k] = memEntry{seq: m.seq, claimedAt: now}
	m.order = append(m.order, memKey{k, m.seq})
	return Claimed, nil
}

// Done implements Store.
func (m *MemoryStore) Done(_ context.Context, scope, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := scope + "\x00" + id
	if e, ok := m.items[k]; ok {
		e.done = true
		m.items[k] = e
	}
	return nil
}

// Release implements Store.
func (m *MemoryStore) Release(_ context.Context, scope, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := scope + "\x00" + id
	if e, ok := m.items[k]; ok && !e.done {
		delete(m.items, k)
	}
	return nil
}
