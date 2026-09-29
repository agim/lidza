package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
)

// node is a second queue on its own pool over the same tables, as
// another node would be.
func node(t *testing.T, base *Queue, workers int) *Queue {
	t.Helper()
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	pool, err := db.Open(context.Background(), db.Config{URL: url, MaxConns: 4, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := base.cfg
	cfg.Workers = workers
	return New(cfg, pool)
}

// TestUnique: enqueues of one key racing on two nodes, in and out of
// transactions, store one job; the key frees up once the job finishes.
func TestUnique(t *testing.T) {
	a := testQueue(t, 0)
	b := node(t, a, 0)
	ctx := context.Background()

	const racers = 16
	ids := make([]string, racers)
	existed := make([]bool, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := a
			if i%2 == 1 {
				q = b
			}
			<-start
			opts := []Option{Unique("post:1"), Existed(&existed[i])}
			if i%4 < 2 {
				id, err := q.Enqueue(ctx, "rebuild", map[string]int{"i": i}, opts...)
				ids[i] = id
				errs <- err
				return
			}
			tx, err := q.pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			id, err := q.EnqueueTx(ctx, tx, "rebuild", map[string]int{"i": i}, opts...)
			if err != nil {
				tx.Rollback(ctx)
				errs <- err
				return
			}
			ids[i] = id
			errs <- tx.Commit(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	created := 0
	for i := range racers {
		if ids[i] != ids[0] {
			t.Fatalf("racer %d got job %s, racer 0 got %s", i, ids[i], ids[0])
		}
		if !existed[i] {
			created++
		}
	}
	if n := countJobs(t, a, "rebuild"); n != 1 || created != 1 {
		t.Fatalf("%d jobs stored, %d enqueues created one; want 1 and 1", n, created)
	}

	// Another key, or the same key of another kind, is another job.
	other, err := a.Enqueue(ctx, "rebuild", nil, Unique("post:2"))
	if err != nil || other == ids[0] {
		t.Fatalf("second key: %s %v", other, err)
	}
	if id, err := a.Enqueue(ctx, "reindex", nil, Unique("post:1")); err != nil || id == ids[0] {
		t.Fatalf("same key, other kind: %s %v", id, err)
	}
	if _, err := a.Enqueue(ctx, "rebuild", nil, Unique("")); err == nil {
		t.Fatal("empty key accepted")
	}

	// Running still holds the key; done and failed free it.
	release := make(chan struct{})
	var runs atomic.Int64
	a.Handle("rebuild", func(ctx context.Context, _ json.RawMessage) error {
		runs.Add(1)
		<-release
		return nil
	})
	a.Handle("reindex", func(context.Context, json.RawMessage) error { return errors.New("no") })
	a.cfg.Workers = 2
	a.Run()
	t.Cleanup(func() { a.Stop(context.Background()) })
	waitState(t, a, ids[0], "running")
	var found bool
	if id, err := b.Enqueue(ctx, "rebuild", nil, Unique("post:1"), Existed(&found)); err != nil || id != ids[0] || !found {
		t.Fatalf("enqueue while running: %s %v existed=%v", id, err, found)
	}
	close(release)
	waitState(t, a, ids[0], "done")
	var key *string
	a.pool.QueryRow(ctx, `SELECT unique_key FROM job WHERE id = $1`, ids[0]).Scan(&key)
	if key != nil {
		t.Fatalf("done job keeps its key %q", *key)
	}
	again, err := b.Enqueue(ctx, "rebuild", nil, Unique("post:1"), Existed(&found))
	if err != nil || again == ids[0] || found {
		t.Fatalf("enqueue after done: %s %v existed=%v", again, err, found)
	}
	waitState(t, a, again, "done")
	failing, err := a.Enqueue(ctx, "reindex", nil, Unique("post:9"), MaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, a, failing, "failed")
	if id, err := a.Enqueue(ctx, "reindex", nil, Unique("post:9"), Existed(&found)); err != nil || id == failing || found {
		t.Fatalf("enqueue after failed: %s %v existed=%v", id, err, found)
	}
}

// TestClaimWithoutKeyColumn: a job table from before Unique (no
// unique_key column) still runs jobs.
func TestClaimWithoutKeyColumn(t *testing.T) {
	q := testQueue(t, 1)
	ctx := context.Background()
	if _, err := q.pool.Exec(ctx, `ALTER TABLE job DROP COLUMN unique_key`); err != nil {
		t.Fatal(err)
	}
	q.Handle("plain", func(context.Context, json.RawMessage) error { return nil })
	q.Handle("one", func(context.Context, json.RawMessage) error { return nil }, Concurrency(1))
	q.Run()
	t.Cleanup(func() { q.Stop(context.Background()) })
	for _, kind := range []string{"plain", "one"} {
		id, err := q.Enqueue(ctx, kind, nil)
		if err != nil {
			t.Fatal(err)
		}
		waitState(t, q, id, "done")
	}
}

// TestConcurrencyAcrossNodes: a kind with Concurrency(1) runs one job at
// a time over two nodes with three workers each, while the other workers
// keep running an unlimited kind.
func TestConcurrencyAcrossNodes(t *testing.T) {
	a := testQueue(t, 3)
	b := node(t, a, 3)
	ctx := context.Background()
	var serial, serialMax, free, freeMax atomic.Int64
	bump := func(cur, peak *atomic.Int64) func() {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		return func() { cur.Add(-1) }
	}
	for _, q := range []*Queue{a, b} {
		q.Handle("sync-account", func(context.Context, json.RawMessage) error {
			defer bump(&serial, &serialMax)()
			time.Sleep(60 * time.Millisecond)
			return nil
		}, Concurrency(1))
		q.Handle("resize", func(context.Context, json.RawMessage) error {
			defer bump(&free, &freeMax)()
			time.Sleep(60 * time.Millisecond)
			return nil
		})
	}
	var ids []string
	for i := range 6 {
		for _, kind := range []string{"sync-account", "resize", "resize"} {
			id, err := a.Enqueue(ctx, kind, map[string]int{"i": i})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
	}
	a.Run()
	b.Run()
	t.Cleanup(func() { a.Stop(context.Background()); b.Stop(context.Background()) })
	for _, id := range ids {
		waitState(t, a, id, "done")
	}
	if serialMax.Load() != 1 {
		t.Fatalf("Concurrency(1) kind ran %d at once", serialMax.Load())
	}
	if freeMax.Load() < 2 {
		t.Fatalf("unlimited kind ran at most %d at once beside the capped one", freeMax.Load())
	}
}
