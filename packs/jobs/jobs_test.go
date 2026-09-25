package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
)

func testQueue(t *testing.T, workers int) *Queue {
	t.Helper()
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 4, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS job`)
	if _, err := pool.Exec(ctx, JobTable); err != nil {
		t.Fatal(err)
	}
	q := New(Config{Workers: workers, Poll: 50 * time.Millisecond, MaxAttempts: 2, Stale: time.Second}, pool)
	return q
}

func waitState(t *testing.T, q *Queue, id, want string) *Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := q.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == want {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, _ := q.Get(context.Background(), id)
	t.Fatalf("job %s is %s, want %s", id, j.State, want)
	return nil
}

func TestQueue(t *testing.T) {
	q := testQueue(t, 2)
	ctx := context.Background()
	var ran atomic.Int64
	var got string
	q.Handle("email", func(ctx context.Context, payload json.RawMessage) error {
		var p struct{ To string }
		json.Unmarshal(payload, &p)
		got = p.To
		ran.Add(1)
		return nil
	})
	attempts := 0
	q.Handle("flaky", func(context.Context, json.RawMessage) error {
		attempts++
		return errors.New("try again")
	})
	q.Handle("panics", func(context.Context, json.RawMessage) error { panic("boom") })
	q.Run()
	defer q.Stop(ctx)

	id, err := q.Enqueue(ctx, "email", map[string]string{"to": "a@b.c"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, q, id, "done")
	if got != "a@b.c" || ran.Load() != 1 {
		t.Fatalf("handler: %q %d", got, ran.Load())
	}

	// Unknown kinds stay pending for a node that handles them.
	other, _ := q.Enqueue(ctx, "report", nil)
	time.Sleep(150 * time.Millisecond)
	if j, _ := q.Get(ctx, other); j.State != "pending" || j.Attempts != 0 {
		t.Fatalf("unhandled kind: %+v", j)
	}

	// Scheduled jobs wait.
	later, _ := q.Enqueue(ctx, "email", map[string]string{"to": "later"}, RunAt(time.Now().Add(400*time.Millisecond)))
	time.Sleep(150 * time.Millisecond)
	if j, _ := q.Get(ctx, later); j.State != "pending" {
		t.Fatal("ran early")
	}
	waitState(t, q, later, "done")

	// Retries then failure with the error kept; panics are failures too.
	flaky, _ := q.Enqueue(ctx, "flaky", nil, MaxAttempts(2))
	j := waitState(t, q, flaky, "failed")
	if j.Attempts != 2 || j.LastError == nil || *j.LastError != "try again" || attempts != 2 {
		t.Fatalf("retry: %+v attempts=%d", j, attempts)
	}
	p, _ := q.Enqueue(ctx, "panics", nil, MaxAttempts(1))
	if j := waitState(t, q, p, "failed"); j.LastError == nil || *j.LastError != "panic: boom" {
		t.Fatalf("panic: %+v", j)
	}

	// A job whose worker died is taken over after Stale.
	stuck, _ := q.Enqueue(ctx, "email", map[string]string{"to": "stuck"})
	q.pool.Exec(ctx, `UPDATE job SET state = 'running', locked_at = now() - interval '5 seconds' WHERE id = $1`, stuck)
	waitState(t, q, stuck, "done")

	st := q.TelemetryStats()
	if st["done_total"] < 3 || st["failed_total"] != 2 || st["workers"] != 2 {
		t.Fatalf("stats %v", st)
	}
}

// TestServicesAndTx: a handler reaches the packs through its context, and
// a job enqueued in a rolled-back transaction never runs.
func TestServicesAndTx(t *testing.T) {
	q := testQueue(t, 1)
	s := lidza.NewServices()
	lidza.Provide(s, q)
	q.WithServices(s)
	q.Run()
	t.Cleanup(func() { q.Stop(context.Background()) })
	var sawQueue atomic.Bool
	q.Handle("probe", func(ctx context.Context, _ json.RawMessage) error {
		sawQueue.Store(lidza.Service[*Queue](ctx) == q)
		return nil
	})
	ctx := context.Background()
	id, err := q.Enqueue(ctx, "probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, q, id, "done")
	if !sawQueue.Load() {
		t.Fatal("handler context has no services")
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lost, err := q.EnqueueTx(ctx, tx, "probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	if _, err := q.Get(ctx, lost); err == nil {
		t.Fatal("job enqueued in a rolled-back transaction exists")
	}
	tx, _ = q.pool.Begin(ctx)
	kept, err := q.EnqueueTx(ctx, tx, "probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, q, kept, "done")
}
