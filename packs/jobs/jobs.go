// Package jobs is the official background work pack: a Postgres-backed
// queue (the job table from schema.lidza) with a bounded set of workers
// per node, retries with backoff, recurring schedules (Schedule), recovery
// of jobs whose worker died, and release of unfinished jobs at shutdown.
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
	// Drain is how long running jobs may finish at shutdown before they
	// are cancelled and released back to pending, to run again at once on
	// another node or after the restart (the attempt is not counted).
	// The shutdown deadline caps it; 0 releases at once.
	Drain time.Duration `env:"JOBS_DRAIN" default:"1s"`
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

	mu        sync.RWMutex
	handlers  map[string]Handler
	kinds     []string
	limits    map[string]int // Concurrency per kind; absent is unlimited
	schedules []*scheduled

	// stopClaim ends polling and scheduling; stopRun cancels the running
	// handlers once the drain is over. claimed holds this node's running
	// jobs (at most Workers) with the locked_at that marks each claim.
	stopClaim context.CancelFunc
	stopRun   context.CancelFunc
	runCtx    context.Context
	claimMu   sync.Mutex
	claimed   map[string]time.Time

	wg          sync.WaitGroup
	done        atomic.Int64
	failed      atomic.Int64
	active      atomic.Int64
	released    atomic.Int64
	warnedTable atomic.Bool
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
	if q.cfg.Drain < 0 {
		q.cfg.Drain = 0
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

// Run starts the workers and the scheduler loop; Start does it, tests
// call it directly. A node with no workers still enqueues its schedules.
func (q *Queue) Run() {
	claim, stopClaim := context.WithCancel(context.Background())
	run, stopRun := context.WithCancel(context.Background())
	q.stopClaim, q.stopRun, q.runCtx = stopClaim, stopRun, run
	q.claimed = map[string]time.Time{}
	q.wg.Add(1)
	go q.scheduler(claim)
	for i := 0; i < q.cfg.Workers; i++ {
		q.wg.Add(1)
		go q.worker(claim)
	}
}

// Stop ends polling and scheduling, lets running jobs finish for the
// Drain period, then cancels them and releases what is still claimed
// back to pending with the attempt uncounted, so a restart or another
// node takes it at once instead of after JOBS_STALE. All of it happens
// within ctx's deadline, part of which is kept for the release.
func (q *Queue) Stop(ctx context.Context) error {
	if q.stopClaim == nil {
		return nil
	}
	q.stopClaim()
	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	drain, grace := q.cfg.Drain, 500*time.Millisecond
	if dl, ok := ctx.Deadline(); ok {
		left := time.Until(dl)
		grace = max(0, min(grace, left/4))
		drain = max(0, min(drain, left-2*grace))
	}
	wait := func(d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-done:
			return true
		case <-t.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
	finished := wait(drain)
	// Cancel the handlers; a worker whose handler returns an error now
	// releases its job itself.
	q.stopRun()
	if finished || wait(grace) {
		return nil
	}
	// Handlers that ignore their context: release their jobs here. Should
	// one finish later, its result is dropped (the claim no longer holds).
	n, err := q.releaseClaimed(context.WithoutCancel(ctx), time.Until(deadlineOr(ctx, time.Now().Add(dbTimeout))))
	if err != nil {
		return fmt.Errorf("jobs: release at shutdown: %w", err)
	}
	return fmt.Errorf("jobs: %d job(s) ignored cancellation at shutdown; released to pending", n)
}

func deadlineOr(ctx context.Context, t time.Time) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return t
}

// track and untrack keep this node's claims for the release at shutdown.
func (q *Queue) track(id string, lockedAt time.Time) {
	q.claimMu.Lock()
	q.claimed[id] = lockedAt
	q.claimMu.Unlock()
}

func (q *Queue) untrack(id string) {
	q.claimMu.Lock()
	delete(q.claimed, id)
	q.claimMu.Unlock()
}

// releaseClaimed releases every job this node still holds, within limit.
func (q *Queue) releaseClaimed(ctx context.Context, limit time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, max(limit, 100*time.Millisecond))
	defer cancel()
	q.claimMu.Lock()
	claims := make(map[string]time.Time, len(q.claimed))
	for id, at := range q.claimed {
		claims[id] = at
	}
	q.claimMu.Unlock()
	n := 0
	for id, at := range claims {
		ok, err := q.release(ctx, id, at)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// release puts a job this node claimed back to pending, due now, with the
// attempt taken back. It matches only while the claim holds.
func (q *Queue) release(ctx context.Context, id string, lockedAt time.Time) (bool, error) {
	tag, err := q.pool.Exec(ctx, `UPDATE job SET state = 'pending', locked_at = NULL, run_at = now(), attempts = GREATEST(attempts - 1, 0)
		WHERE id = $1 AND state = 'running' AND locked_at = $2`, id, lockedAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	q.released.Add(1)
	return true, nil
}

// Handle registers the handler for a kind. Register in onStart (start.go:
// jobs.FromServices(s).Handle(kind, fn)); jobs of kinds without a handler
// stay pending for a node that has one. The handler's context carries
// the packs, so db.From(ctx) and mail.From(ctx) work inside it.
// Concurrency caps the kind's running jobs across nodes.
func (q *Queue) Handle(kind string, h Handler, opts ...HandleOption) {
	var o handling
	for _, fn := range opts {
		fn(&o)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.handlers[kind]; !ok {
		q.kinds = append(q.kinds, kind)
	}
	q.handlers[kind] = h
	if q.limits == nil {
		q.limits = map[string]int{}
	}
	delete(q.limits, kind)
	if o.concurrency > 0 {
		q.limits[kind] = o.concurrency
	}
}

// HandleOption adjusts how a kind runs.
type HandleOption func(*handling)

type handling struct{ concurrency int }

// Concurrency caps how many jobs of the kind run at once across all
// nodes: Concurrency(1) runs one at a time, for work that must not
// overlap (a sync against one external account, a rebuild of one
// index). Jobs over the cap stay pending and workers take other kinds
// meanwhile. Every node that handles the kind declares the same cap. A
// job whose node died counts against the cap until JOBS_STALE hands it
// to another worker. n <= 0 is unlimited, the default.
func Concurrency(n int) HandleOption { return func(h *handling) { h.concurrency = n } }

// Option adjusts an enqueued job.
type Option func(*enqueue)

type enqueue struct {
	runAt       time.Time
	maxAttempts int
	unique      *string
	existed     *bool
}

// RunAt schedules the job for later.
func RunAt(t time.Time) Option { return func(e *enqueue) { e.runAt = t } }

// MaxAttempts overrides the retry budget.
func MaxAttempts(n int) Option { return func(e *enqueue) { e.maxAttempts = n } }

// Unique makes key the job's idempotency key within its kind: while a
// job of the kind with that key is pending (retries included) or
// running, Enqueue stores nothing and returns that job's id. Once it is
// done or failed, the next Enqueue queues a new one. It holds across
// nodes (a unique index on kind and key), so a page that notices missing
// derived data on every view queues the work once:
//
//	q.Enqueue(ctx, "rebuild-thumbnails", p, jobs.Unique("post:"+p.ID))
//
// The key needs the uniqueKey column of the job table (`lidza gen`,
// `lidza db migrate`).
func Unique(key string) Option { return func(e *enqueue) { e.unique = &key } }

// Existed reports whether Unique found a live job with the key, so that
// Enqueue returned its id and stored nothing.
func Existed(found *bool) Option { return func(e *enqueue) { e.existed = found } }

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
	if e.existed != nil {
		*e.existed = false
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var id string
	if e.unique == nil {
		err = db.QueryRow(ctx, `INSERT INTO job (kind, payload, run_at, max_attempts) VALUES ($1, $2, $3, $4) RETURNING id`,
			kind, data, e.runAt, e.maxAttempts).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("jobs: enqueue: %w", err)
		}
		return id, nil
	}
	if *e.unique == "" {
		return "", errors.New("jobs: enqueue: Unique needs a key")
	}
	// The insert waits on a racing insert of the same key until it
	// commits or rolls back, then stores nothing or the job. The live job
	// it met may finish before the lookup; then try again.
	for range 3 {
		err = db.QueryRow(ctx, `INSERT INTO job (kind, payload, run_at, max_attempts, unique_key) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (kind, unique_key) DO NOTHING RETURNING id`, kind, data, e.runAt, e.maxAttempts, *e.unique).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("jobs: enqueue: %w", err)
		}
		err = db.QueryRow(ctx, `SELECT id FROM job WHERE kind = $1 AND unique_key = $2`, kind, *e.unique).Scan(&id)
		if err == nil {
			if e.existed != nil {
				*e.existed = true
			}
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("jobs: enqueue: %w", err)
		}
	}
	return "", fmt.Errorf("jobs: enqueue %s: key %q kept changing hands", kind, *e.unique)
}

// Recent returns the newest jobs by run time, every state. The admin
// pages show them.
func (q *Queue) Recent(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := q.pool.Query(ctx, `SELECT id, kind, payload, state, run_at, attempts, max_attempts, last_error FROM job ORDER BY run_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Kind, &j.Payload, &j.State, &j.RunAt, &j.Attempts, &j.MaxAttempts, &j.LastError); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Counts returns how many jobs are in each state: pending, running, done
// and failed (a state with none is 0).
func (q *Queue) Counts(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{"pending": 0, "running": 0, "done": 0, "failed": 0}
	rows, err := q.pool.Query(ctx, `SELECT state, count(*) FROM job GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// Retry puts a failed job back in the queue to run now, attempts reset.
func (q *Queue) Retry(ctx context.Context, id string) error {
	tag, err := q.pool.Exec(ctx, `UPDATE job SET state = 'pending', run_at = now(), attempts = 0, last_error = NULL, locked_at = NULL WHERE id = $1 AND state = 'failed'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("jobs: no failed job with that id")
	}
	return nil
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
// false when there was nothing to do. ctx ends claiming; the handler's
// context comes from runCtx, which Stop cancels only after the drain.
func (q *Queue) runOne(ctx context.Context) (bool, error) {
	q.mu.RLock()
	kinds := append([]string(nil), q.kinds...)
	handlers := q.handlers
	var free, limited []string
	var caps []int32
	for _, k := range kinds {
		if n, ok := q.limits[k]; ok {
			limited, caps = append(limited, k), append(caps, int32(n))
		} else {
			free = append(free, k)
		}
	}
	q.mu.RUnlock()
	if len(kinds) == 0 || ctx.Err() != nil {
		return false, nil
	}
	// A claim, once sent, runs to completion, so none is committed
	// without this node knowing it holds the job.
	dctx, dcancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
	defer dcancel()
	if _, err := q.pool.Exec(dctx, `UPDATE job SET state = 'pending', locked_at = NULL WHERE state = 'running' AND locked_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(q.cfg.Stale.Seconds()))); err != nil {
		return false, err
	}
	j, lockedAt, keyed, err := q.claim(dctx, free, limited, caps)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A finished job gives its Unique key up for the next one.
	clearKey := ""
	if keyed {
		clearKey = ", unique_key = NULL"
	}
	q.track(j.ID, lockedAt)
	defer q.untrack(j.ID)
	q.active.Add(1)
	defer q.active.Add(-1)
	jctx, cancel := context.WithTimeout(q.runCtx, q.cfg.Timeout)
	defer cancel()
	if q.services != nil {
		jctx = lidza.WithServices(jctx, q.services)
	}
	runErr := safeRun(jctx, handlers[j.Kind], j.Payload)
	// The outcome is written only while this node's claim holds: a job
	// taken over after JOBS_STALE, or released at shutdown, belongs to
	// its next run.
	fctx, fcancel := context.WithTimeout(context.Background(), dbTimeout)
	defer fcancel()
	if runErr == nil {
		q.done.Add(1)
		_, err = q.pool.Exec(fctx, `UPDATE job SET state = 'done', finished_at = now(), locked_at = NULL`+clearKey+` WHERE id = $1 AND locked_at = $2`, j.ID, lockedAt)
		return true, err
	}
	if q.runCtx.Err() != nil {
		// Cancelled by shutdown: not a failure; pending again, due now.
		if _, err := q.release(fctx, j.ID, lockedAt); err != nil {
			return true, err
		}
		q.log.Info("jobs: released at shutdown", "kind", j.Kind, "id", j.ID)
		return true, nil
	}
	msg := runErr.Error()
	if j.Attempts >= j.MaxAttempts {
		q.failed.Add(1)
		q.log.Error("jobs: failed", "kind", j.Kind, "id", j.ID, "attempts", j.Attempts, "error", msg)
		_, err = q.pool.Exec(fctx, `UPDATE job SET state = 'failed', finished_at = now(), locked_at = NULL, last_error = $3`+clearKey+` WHERE id = $1 AND locked_at = $2`, j.ID, lockedAt, msg)
		return true, err
	}
	delay := time.Duration(math.Min(math.Pow(2, float64(j.Attempts)), 3600)) * time.Second
	q.log.Warn("jobs: retry", "kind", j.Kind, "id", j.ID, "attempt", j.Attempts, "in", delay, "error", msg)
	_, err = q.pool.Exec(fctx, `UPDATE job SET state = 'pending', locked_at = NULL, last_error = $3, run_at = now() + $4::interval WHERE id = $1 AND locked_at = $2`,
		j.ID, lockedAt, msg, fmt.Sprintf("%d seconds", int(delay.Seconds())))
	return true, err
}

// claimSet is what a claim sets and returns. keyed reads the Unique key
// through the row's JSON, so a job table without the column (an app that
// has not migrated yet) still claims.
const claimSet = `SET state = 'running', locked_at = clock_timestamp(), attempts = attempts + 1`
const claimReturning = ` RETURNING id, kind, payload, attempts, max_attempts, locked_at, (to_jsonb(job) ->> 'unique_key') IS NOT NULL`

// claim takes the oldest due job of the free kinds or of the limited
// kinds under their cap, marks it running and returns it (pgx.ErrNoRows
// when there is none). Without limited kinds it is one statement. With
// them, a transaction first takes a per-kind advisory lock on each
// limited kind with due work, without waiting (a kind another worker is
// claiming is skipped this round), then counts that kind's running jobs
// in a new statement, which sees every claim committed before the lock
// was granted, and claims. The lock is held to the commit, so no two
// claims of a kind see the same count on any node.
func (q *Queue) claim(ctx context.Context, free, limited []string, caps []int32) (j Job, lockedAt time.Time, keyed bool, err error) {
	scan := func(row pgx.Row) error {
		return row.Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts, &lockedAt, &keyed)
	}
	if len(limited) == 0 {
		err = scan(q.pool.QueryRow(ctx, `UPDATE job `+claimSet+`
			WHERE id = (SELECT id FROM job WHERE state = 'pending' AND run_at <= now() AND kind = ANY($1) ORDER BY run_at LIMIT 1 FOR UPDATE SKIP LOCKED)`+claimReturning, free))
		return
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	rows, err := tx.Query(ctx, `SELECT l.k, l.n FROM unnest($1::text[], $2::int[]) AS l(k, n)
		WHERE CASE WHEN EXISTS (SELECT 1 FROM job WHERE kind = l.k AND state = 'pending' AND run_at <= now())
			THEN pg_try_advisory_xact_lock(hashtext('lidza/jobs'), hashtext(l.k)) ELSE false END`, limited, caps)
	if err != nil {
		return
	}
	var held []string
	var heldCaps []int32
	for rows.Next() {
		var k string
		var n int32
		if err = rows.Scan(&k, &n); err != nil {
			rows.Close()
			return
		}
		held, heldCaps = append(held, k), append(heldCaps, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}
	if len(free) == 0 && len(held) == 0 {
		err = pgx.ErrNoRows
		return
	}
	err = scan(tx.QueryRow(ctx, `WITH open AS (
			SELECT l.k FROM unnest($2::text[], $3::int[]) AS l(k, n)
			WHERE (SELECT count(*) FROM job WHERE kind = l.k AND state = 'running') < l.n)
		UPDATE job `+claimSet+`
		WHERE id = (SELECT id FROM job WHERE state = 'pending' AND run_at <= now() AND (kind = ANY($1) OR kind IN (SELECT k FROM open))
			ORDER BY run_at LIMIT 1 FOR UPDATE SKIP LOCKED)`+claimReturning, free, held, heldCaps))
	if err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}

// dbTimeout bounds the pack's own bookkeeping queries.
const dbTimeout = 10 * time.Second

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
		// Jobs put back to pending at shutdown, the attempt uncounted.
		"released_total": float64(q.released.Load()),
	}
}
