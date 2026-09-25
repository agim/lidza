// Package cache is the official cache pack: string keys with TTL on Valkey
// (or Redis), and Remember for the read-through pattern. CACHE_URL=memory
// gives a bounded in-process cache for tests and single-node development;
// production uses the server so every node sees the same entries.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// Config comes from the environment.
type Config struct {
	// URL is redis://host:port, valkey://..., or "memory".
	URL string `env:"CACHE_URL" required:"true"`
	// Prefix namespaces keys, so several apps can share a server.
	Prefix string `env:"CACHE_PREFIX" default:"lidza:"`
	// MemoryMax bounds the in-process backend's entries.
	MemoryMax int `env:"CACHE_MEMORY_MAX" default:"10000"`
}

// ErrMiss is returned by Get when the key is absent or expired.
var ErrMiss = errors.New("cache miss")

// Store is the backend contract.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	// DeletePrefix removes every key under prefix (an invalidation
	// sweep); the server backend scans, so keep prefixes specific.
	DeletePrefix(ctx context.Context, prefix string) error
	Ping(ctx context.Context) error
	Close() error
}

// Cache is the running pack.
type Cache struct {
	cfg   Config
	store Store
}

// Pack returns the pack for packs.go.
func Pack() lidza.Pack { return &Cache{} }

// New builds a cache over a store outside the pack lifecycle.
func New(store Store, prefix string) *Cache { return &Cache{store: store, cfg: Config{Prefix: prefix}} }

// From returns the cache from a request context.
func From(ctx context.Context) *Cache { return lidza.Service[*Cache](ctx) }

// Name implements lidza.Pack.
func (c *Cache) Name() string { return "lidza/cache" }

// Start reads the configuration and connects.
func (c *Cache) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &c.cfg); err != nil {
		return err
	}
	if c.cfg.URL == "memory" {
		c.store = NewMemory(c.cfg.MemoryMax)
	} else {
		st, err := NewValkey(ctx, c.cfg.URL)
		if err != nil {
			return err
		}
		c.store = st
	}
	lidza.Provide(s, c)
	return nil
}

// Stop closes the backend.
func (c *Cache) Stop(context.Context) error { return c.store.Close() }

// Ready implements telemetry.Ready.
func (c *Cache) Ready(ctx context.Context) error { return c.store.Ping(ctx) }

func (c *Cache) key(k string) string { return c.cfg.Prefix + k }

// Get decodes the JSON stored under key into out; ErrMiss when absent.
func (c *Cache) Get(ctx context.Context, key string, out any) error {
	data, err := c.store.Get(ctx, c.key(key))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// Set stores value as JSON for ttl (0 keeps it until deleted).
func (c *Cache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.store.Set(ctx, c.key(key), data, ttl)
}

// Delete removes keys.
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = c.key(k)
	}
	return c.store.Delete(ctx, full...)
}

// Invalidate removes every key under prefix.
func (c *Cache) Invalidate(ctx context.Context, prefix string) error {
	return c.store.DeletePrefix(ctx, c.key(prefix))
}

// Remember returns the cached value for key or computes it with load,
// stores it for ttl and returns it. Errors from load are not cached.
func Remember[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, load func(ctx context.Context) (T, error)) (T, error) {
	var v T
	err := c.Get(ctx, key, &v)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, ErrMiss) {
		return v, err
	}
	v, err = load(ctx)
	if err != nil {
		return v, err
	}
	return v, c.Set(ctx, key, v, ttl)
}

// Memory is the bounded in-process backend.
type Memory struct {
	max   int
	mu    sync.Mutex
	items map[string]memItem
	order []string // insertion order for eviction
}

type memItem struct {
	value   []byte
	expires time.Time
}

// NewMemory returns a backend holding at most max entries.
func NewMemory(max int) *Memory {
	if max <= 0 {
		max = 10000
	}
	return &Memory{max: max, items: map[string]memItem{}}
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[key]
	if !ok || (!it.expires.IsZero() && time.Now().After(it.expires)) {
		delete(m.items, key)
		return nil, ErrMiss
	}
	return it.value, nil
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[key]; !exists {
		for len(m.items) >= m.max && len(m.order) > 0 {
			oldest := m.order[0]
			m.order = m.order[1:]
			delete(m.items, oldest)
		}
		m.order = append(m.order, key)
	}
	it := memItem{value: value}
	if ttl > 0 {
		it.expires = time.Now().Add(ttl)
	}
	m.items[key] = it
	return nil
}

func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.items, k)
	}
	return nil
}

func (m *Memory) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.items {
		if strings.HasPrefix(k, prefix) {
			delete(m.items, k)
		}
	}
	return nil
}

func (m *Memory) Ping(context.Context) error { return nil }
func (m *Memory) Close() error               { return nil }

// Len counts live entries (tests).
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// Valkey is the server backend.
type Valkey struct{ client valkey.Client }

// NewValkey connects to url.
func NewValkey(ctx context.Context, url string) (*Valkey, error) {
	opt, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("CACHE_URL: %w", err)
	}
	client, err := valkey.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return nil, fmt.Errorf("cache not reachable: %w", err)
	}
	return &Valkey{client: client}, nil
}

func (v *Valkey) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := v.client.Do(ctx, v.client.B().Get().Key(key).Build()).AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil, ErrMiss
	}
	return data, err
}

func (v *Valkey) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl > 0 {
		return v.client.Do(ctx, v.client.B().Set().Key(key).Value(string(value)).Px(ttl).Build()).Error()
	}
	return v.client.Do(ctx, v.client.B().Set().Key(key).Value(string(value)).Build()).Error()
}

func (v *Valkey) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return v.client.Do(ctx, v.client.B().Del().Key(keys...).Build()).Error()
}

func (v *Valkey) DeletePrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	for {
		res, err := v.client.Do(ctx, v.client.B().Scan().Cursor(cursor).Match(prefix+"*").Count(500).Build()).AsScanEntry()
		if err != nil {
			return err
		}
		if len(res.Elements) > 0 {
			if err := v.Delete(ctx, res.Elements...); err != nil {
				return err
			}
		}
		if res.Cursor == 0 {
			return nil
		}
		cursor = res.Cursor
	}
}

func (v *Valkey) Ping(ctx context.Context) error {
	return v.client.Do(ctx, v.client.B().Ping().Build()).Error()
}

func (v *Valkey) Close() error {
	v.client.Close()
	return nil
}
