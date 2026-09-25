// Package jobs is the official background work pack: a Postgres-backed
// queue (the job table from schema.lidza) with a bounded set of workers
// per node, retries with backoff, and recovery of jobs whose worker died.
// Any node can enqueue; any node with a handler for the kind can run it.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// Config comes from the environment.
type Config struct {
	// Workers bounds concurrent jobs on this node; 0 disables running
	// (enqueue only, for nodes that just serve HTTP).
	Workers int `env:"JOBS_WORKERS" default:"4"`
	// Poll is how often idle workers look for work.
	Poll time.Duration `env:"JOBS_POLL" default:"1s"`
	// MaxAttempts is the default retry budget per job.
	MaxAttempts int `env:"JOBS_MAX_ATTEMPTS" default:"5"`
	// Stale is how long a running job may go without finishing before
	// another worker takes it over (a crashed node).
	Stale time.Duration `env:"JOBS_STALE" default:"10m"`
	// Timeout bounds one execution.
	Timeout time.Duration `env:"JOBS_TIMEOUT" default:"5m"`
}

// Handler runs one job of a kind.
type Handler func(ctx context.Context, payload json.RawMessage) error

// Job is a row of the job table.
type Job struct {
	ID          string
	Kind        string
	Payload     json.RawMessage
	State       string
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	LastError   *string
}

// Queue is the running pack.
type Queue struct {
	cfg      Config
	pool     *pgxpool.Pool
	log      *slog.Logger
	services *lidza.Services

	mu       sync.RWMutex
	handlers map[string]Handler
	kinds    []string

	stop   context.CancelFunc
	wg     sync.WaitGroup
	done   atomic.Int64
	failed atomic.Int64
	active atomic.Int64
}

// Pack returns the pack for packs.go; it needs the db pack started first.
func Pack() lidza.Pack { return &Queue{handlers: map[string]Handler{}, log: slog.Default()} }

// New builds a queue outside the lifecycle (tests).
func New(cfg Config, pool *pgxpool.Pool) *Queue {
	q := &Queue{cfg: cfg, pool: pool, handlers: map[string]Handler{}, log: slog.Default()}
	q.defaults()
	return q
}

func (q *Queue) defaults() {
	if q.cfg.Poll <= 0 {
		q.cfg.Poll = time.Second
	}
	if q.cfg.MaxAttempts <= 0 {
		q.cfg.MaxAttempts = 5
	}
	if q.cfg.Stale <= 0 {
		q.cfg.Stale = 10 * time.Minute
	}
	if q.cfg.Timeout <= 0 {
		q.cfg.Timeout = 5 * time.Minute
	}
}

// From returns the queue from a request context.
func From(ctx context.Context) *Queue { return lidza.Service[*Queue](ctx) }

// FromServices returns the queue at OnStart, to register handlers.
func FromServices(s *lidza.Services) *Queue {
	v, ok := s.Lookup(typeOf[*Queue]())
	if !ok {
		panic("jobs: pack not started; list \"lidza/jobs\" after \"lidza/db\" in lidza.json")
	}
	return v.(*Queue)
}

// Name implements lidza.Pack.
func (q *Queue) Name() string { return "lidza/jobs" }

// Start reads the configuration, takes the pool and starts the workers.
func (q *Queue) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &q.cfg); err != nil {
		return err
	}
	q.defaults()
	pool, ok := s.Lookup(typeOf[*pgxpool.Pool]())
	if !ok {
		return errors.New("jobs needs the db pack: list \"lidza/db\" before \"lidza/jobs\" in lidza.json")
	}
	q.pool = pool.(*pgxpool.Pool)
	q.services = s
	lidza.Provide(s, q)
	q.Run()
	return nil
}

// WithServices makes the packs reachable from job handlers (db.From,
// mail.From, ...) the way they are from request handlers. Start does it;
// a Queue built with New for a test calls it with the test's services.
func (q *Queue) WithServices(s *lidza.Services) { q.services = s }

// Run starts the workers; Start does it, tests call it directly.
func (q *Queue) Run() {
	if q.cfg.Workers <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.stop = cancel
	for i := 0; i < q.cfg.Workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}
}

// Stop lets running jobs finish (up to the job timeout) and stops polling.
func (q *Queue) Stop(ctx context.Context) error {
	if q.stop != nil {
		q.stop()
	}
	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("jobs: workers still running at shutdown")
	}
}

// Handle registers the handler for a kind. Register in onStart (start.go:
// jobs.FromServices(s).Handle(kind, fn)); jobs of kinds without a handler
// stay pending for a node that has one. The handler's context carries
// the packs, so db.From(ctx) and mail.From(ctx) work inside it.
func (q *Queue) Handle(kind string, h Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = h
	q.kinds = append(q.kinds, kind)
}

// Option adjusts an enqueued job.
type Option func(*enqueue)

type enqueue struct {
	runAt       time.Time
	maxAttempts int
}

// RunAt schedules the job for later.
func RunAt(t time.Time) Option { return func(e *enqueue) { e.runAt = t } }

// MaxAttempts overrides the retry budget.
func MaxAttempts(n int) Option { return func(e *enqueue) { e.maxAttempts = n } }

// Enqueue stores a job and returns its id. payload is encoded as JSON.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any, opts ...Option) (string, error) {
	return q.enqueue(ctx, q.pool, kind, payload, opts...)
}

// EnqueueTx is Enqueue inside the caller's transaction: the job row is
// committed or rolled back with the handler's own writes, so a job is
// never queued for a write that did not happen, and never lost after one
// that did.
func (q *Queue) EnqueueTx(ctx context.Context, tx pgx.Tx, kind string, payload any, opts ...Option) (string, error) {
	return q.enqueue(ctx, tx, kind, payload, opts...)
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (q *Queue) enqueue(ctx context.Context, db rowQuerier, kind string, payload any, opts ...Option) (string, error) {
	e := enqueue{runAt: time.Now(), maxAttempts: q.cfg.MaxAttempts}
	for _, o := range opts {
		o(&e)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var id string
	err = db.QueryRow(ctx, `INSERT INTO job (kind, payload, run_at, max_attempts) VALUES ($1, $2, $3, $4) RETURNING id`,
		kind, data, e.runAt, e.maxAttempts).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("jobs: enqueue: %w", err)
	}
	return id, nil
}

// Get reads a job's state.
func (q *Queue) Get(ctx context.Context, id string) (*Job, error) {
	var j Job
	err := q.pool.QueryRow(ctx, `SELECT id, kind, payload, state, run_at, attempts, max_attempts, last_error FROM job WHERE id = $1`, id).
		Scan(&j.ID, &j.Kind, &j.Payload, &j.State, &j.RunAt, &j.Attempts, &j.MaxAttempts, &j.LastError)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()
	for {
		ran, err := q.runOne(ctx)
		if err != nil && ctx.Err() == nil {
			q.log.Error("jobs: worker", "error", err)
		}
		if ran {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(q.cfg.Poll):
		}
	}
}

// runOne claims one due job of a handled kind and runs it. It returns
// false when there was nothing to do.
func (q *Queue) runOne(ctx context.Context) (bool, error) {
	q.mu.RLock()
	kinds := append([]string(nil), q.kinds...)
	handlers := q.handlers
	q.mu.RUnlock()
	if len(kinds) == 0 {
		return false, nil
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET state = 'pending', locked_at = NULL WHERE state = 'running' AND locked_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(q.cfg.Stale.Seconds()))); err != nil {
		return false, err
	}
	var j Job
	err := q.pool.QueryRow(ctx, `UPDATE job SET state = 'running', locked_at = now(), attempts = attempts + 1
		WHERE id = (SELECT id FROM job WHERE state = 'pending' AND run_at <= now() AND kind = ANY($1) ORDER BY run_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, kind, payload, attempts, max_attempts`, kinds).Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	q.active.Add(1)
	defer q.active.Add(-1)
	jctx, cancel := context.WithTimeout(ctx, q.cfg.Timeout)
	defer cancel()
	if q.services != nil {
		jctx = lidza.WithServices(jctx, q.services)
	}
	runErr := safeRun(jctx, handlers[j.Kind], j.Payload)
	if runErr == nil {
		q.done.Add(1)
		_, err = q.pool.Exec(context.Background(), `UPDATE job SET state = 'done', finished_at = now(), locked_at = NULL WHERE id = $1`, j.ID)
		return true, err
	}
	msg := runErr.Error()
	if j.Attempts >= j.MaxAttempts {
		q.failed.Add(1)
		q.log.Error("jobs: failed", "kind", j.Kind, "id", j.ID, "attempts", j.Attempts, "error", msg)
		_, err = q.pool.Exec(context.Background(), `UPDATE job SET state = 'failed', finished_at = now(), locked_at = NULL, last_error = $2 WHERE id = $1`, j.ID, msg)
		return true, err
	}
	delay := time.Duration(math.Min(math.Pow(2, float64(j.Attempts)), 3600)) * time.Second
	q.log.Warn("jobs: retry", "kind", j.Kind, "id", j.ID, "attempt", j.Attempts, "in", delay, "error", msg)
	_, err = q.pool.Exec(context.Background(), `UPDATE job SET state = 'pending', locked_at = NULL, last_error = $2, run_at = now() + $3::interval WHERE id = $1`,
		j.ID, msg, fmt.Sprintf("%d seconds", int(delay.Seconds())))
	return true, err
}

func safeRun(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return h(ctx, payload)
}

// TelemetryStats reports the workers to /metrics.
func (q *Queue) TelemetryStats() map[string]float64 {
	return map[string]float64{
		"workers":      float64(q.cfg.Workers),
		"active":       float64(q.active.Load()),
		"done_total":   float64(q.done.Load()),
		"failed_total": float64(q.failed.Load()),
	}
}
