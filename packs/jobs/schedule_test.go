package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone data for %s: %v", name, err)
	}
	return loc
}

// TestNextRun: the due times of every schedule kind, across time zones
// and the days clocks change.
func TestNextRun(t *testing.T) {
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	check := func(s Schedule, from, want string) {
		t.Helper()
		if got := s.Next(utc(from)); !got.Equal(utc(want)) {
			t.Fatalf("%s from %s: %s, want %s", s, from, got.UTC().Format(time.RFC3339), want)
		}
	}

	q15 := Every(15 * time.Minute)
	check(q15, "2026-09-27T10:07:30Z", "2026-09-27T10:15:00Z")
	check(q15, "2026-09-27T10:15:00Z", "2026-09-27T10:30:00Z") // strictly after
	check(q15, "2026-09-27T23:59:59+02:00", "2026-09-27T22:00:00Z")
	for s, want := range map[Schedule]string{
		q15:                     "every 15m",
		Every(time.Hour):        "every 1h",
		Every(90 * time.Minute): "every 1h30m",
		Every(30 * time.Second): "every 30s",
		Every(90 * time.Second): "every 1m30s",
		Daily("07:05", ""):      "daily 07:05 UTC",
		Weekly(time.Monday, "09:00", "Europe/Tirane"): "weekly Mon 09:00 Europe/Tirane",
	} {
		if s.String() != want {
			t.Fatalf("spec %q, want %q", s.String(), want)
		}
	}

	mustLoc(t, "Europe/Tirane")
	mon := Weekly(time.Monday, "09:00", "Europe/Tirane")
	check(mon, "2026-09-26T12:00:00Z", "2026-09-28T07:00:00Z") // summer time, +02:00
	check(mon, "2026-09-28T07:00:00Z", "2026-10-05T07:00:00Z")
	check(mon, "2026-09-28T06:59:59Z", "2026-09-28T07:00:00Z")
	check(mon, "2026-10-19T07:00:00Z", "2026-10-26T08:00:00Z") // clocks went back on the 25th: +01:00
	check(mon, "2027-03-22T08:00:00Z", "2027-03-29T07:00:00Z") // and forward on 28 March
	// The weekday is the zone's: Sunday 23:30 UTC is Monday in Tirane.
	check(Weekly(time.Monday, "00:15", "Europe/Tirane"), "2026-09-27T21:00:00Z", "2026-09-27T22:15:00Z")

	mustLoc(t, "America/New_York")
	ny := Daily("09:00", "America/New_York")
	check(ny, "2026-09-27T14:00:00Z", "2026-09-28T13:00:00Z") // 10:00 EDT: tomorrow
	check(ny, "2026-09-27T12:00:00Z", "2026-09-27T13:00:00Z") // 08:00 EDT: today
	check(ny, "2026-11-01T12:00:00Z", "2026-11-01T14:00:00Z") // EST from 1 November
	check(Daily("00:00", ""), "2026-12-31T23:00:00Z", "2027-01-01T00:00:00Z")

	// One run per local day across both changes, the skipped time moved
	// forward by the jump.
	tirane := mustLoc(t, "Europe/Tirane")
	night := Daily("02:30", "Europe/Tirane")
	for _, from := range []time.Time{
		time.Date(2027, 3, 26, 12, 0, 0, 0, tirane),  // clocks jump 02:00 to 03:00 on the 28th
		time.Date(2026, 10, 23, 12, 0, 0, 0, tirane), // back 03:00 to 02:00 on the 25th
	} {
		at, days := from, map[string]int{}
		for range 4 {
			at = night.Next(at)
			days[at.In(tirane).Format("2006-01-02")]++
			if l := at.In(tirane); l.Day() == 28 && l.Month() == time.March && (l.Hour() != 3 || l.Minute() != 30) {
				t.Fatalf("skipped 02:30 ran at %s, want 03:30", l)
			}
		}
		if len(days) != 4 {
			t.Fatalf("runs by day from %s: %v", from, days)
		}
	}

	// Specs that cannot run are refused when declared.
	q := New(Config{}, nil)
	for _, s := range []Schedule{Every(0), Every(time.Millisecond), Daily("9am", ""), Daily("25:00", ""), Weekly(time.Monday, "09:00", "Mars/Olympus")} {
		if err := q.Schedule("x", s, nil); err == nil {
			t.Fatalf("%s accepted", s)
		}
	}
	if err := q.Schedule("x", q15, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.Schedule("x", Every(time.Hour), nil); err == nil {
		t.Fatal("a kind scheduled twice")
	}
}

// scheduleQueue is a queue on the test database with the schedule table.
func scheduleQueue(t *testing.T) *Queue {
	t.Helper()
	q := testQueue(t, 0)
	ctx := context.Background()
	q.pool.Exec(ctx, `DROP TABLE IF EXISTS job_schedule`)
	if _, err := q.pool.Exec(ctx, ScheduleTable); err != nil {
		t.Fatal(err)
	}
	return q
}

func countJobs(t *testing.T, q *Queue, kind string) int {
	t.Helper()
	var n int
	if err := q.pool.QueryRow(context.Background(), `SELECT count(*) FROM job WHERE kind = $1`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestScheduleOnePerDue: nodes racing on the same due time enqueue one
// job between them; a changed schedule takes over from a node started
// later, and the older node does not undo it.
func TestScheduleOnePerDue(t *testing.T) {
	base := scheduleQueue(t)
	ctx := context.Background()
	every := Every(time.Minute)
	nodes := make([]*Queue, 4)
	for i := range nodes {
		nodes[i] = New(base.cfg, base.pool)
		if err := nodes[i].Schedule("tick", every, map[string]int{"n": 7}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := nodes[0].checkSchedules(ctx, now); err != nil {
		t.Fatal(err)
	}
	if n := countJobs(t, base, "tick"); n != 0 {
		t.Fatalf("enqueued on first sight: %d", n)
	}
	for round := 1; round <= 3; round++ {
		var next time.Time
		base.pool.QueryRow(ctx, `SELECT next_run_at FROM job_schedule WHERE kind = 'tick'`).Scan(&next)
		due := next.Add(time.Second)
		var wg sync.WaitGroup
		start := make(chan struct{})
		var failures atomic.Int64
		for i := range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := nodes[i%len(nodes)].checkSchedules(ctx, due); err != nil {
					failures.Add(1)
					t.Error(err)
				}
			}()
		}
		close(start)
		wg.Wait()
		if n := countJobs(t, base, "tick"); n != round || failures.Load() != 0 {
			t.Fatalf("round %d: %d jobs", round, n)
		}
	}
	infos, err := nodes[1].Schedules(ctx)
	if err != nil || len(infos) != 1 || infos[0].Spec != "every 1m" || infos[0].LastRun == nil || infos[0].LastJobID == nil || !infos[0].NextRun.After(*infos[0].LastRun) {
		t.Fatalf("schedules: %+v %v", infos, err)
	}
	j, err := base.Get(ctx, *infos[0].LastJobID)
	if err != nil || string(j.Payload) != `{"n": 7}` || j.Kind != "tick" {
		t.Fatalf("last job: %+v %v (payload %s)", j, err, j.Payload)
	}

	// A release with a new schedule: the node started with it writes it
	// once, and the older node, which already saw its own, leaves it.
	newer := New(base.cfg, base.pool)
	newer.Schedule("tick", Every(time.Hour), nil)
	later := time.Now().Add(time.Second)
	if err := newer.checkSchedules(ctx, later); err != nil {
		t.Fatal(err)
	}
	if err := nodes[0].checkSchedules(ctx, later); err != nil {
		t.Fatal(err)
	}
	var spec string
	var next time.Time
	base.pool.QueryRow(ctx, `SELECT spec, next_run_at FROM job_schedule WHERE kind = 'tick'`).Scan(&spec, &next)
	if spec != "every 1h" || !next.Equal(Every(time.Hour).Next(later)) {
		t.Fatalf("changed schedule: %s %s", spec, next)
	}
}

// TestScheduleCatchUp: after downtime one missed run is enqueued, not one
// per missed due time, and the next is counted from now.
func TestScheduleCatchUp(t *testing.T) {
	q := scheduleQueue(t)
	ctx := context.Background()
	hourly := Every(time.Hour)
	if err := q.Schedule("report", hourly, nil); err != nil {
		t.Fatal(err)
	}
	down := time.Now().Add(-5 * time.Hour).Truncate(time.Hour)
	if _, err := q.pool.Exec(ctx, `INSERT INTO job_schedule (kind, spec, next_run_at) VALUES ('report', $1, $2)`, hourly.String(), down); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for range 3 {
		if err := q.checkSchedules(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if n := countJobs(t, q, "report"); n != 1 {
		t.Fatalf("catch-up enqueued %d jobs", n)
	}
	var next, last time.Time
	q.pool.QueryRow(ctx, `SELECT next_run_at, last_run_at FROM job_schedule WHERE kind = 'report'`).Scan(&next, &last)
	if !next.Equal(hourly.Next(now)) || !last.Equal(down) {
		t.Fatalf("after catch-up: next %s last %s", next, last)
	}
}

// TestScheduleLoop: the pack's loop enqueues on its own, and a missing
// table is an error the loop reports, not a crash.
func TestScheduleLoop(t *testing.T) {
	q := scheduleQueue(t)
	ctx := context.Background()
	q.cfg.Poll = 50 * time.Millisecond
	q.Schedule("beat", Every(time.Second), nil)
	var ran atomic.Int64
	q.Handle("beat", func(context.Context, json.RawMessage) error { ran.Add(1); return nil })
	q.cfg.Workers = 1
	q.Run()
	deadline := time.Now().Add(5 * time.Second)
	for ran.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if err := q.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if ran.Load() < 2 {
		t.Fatalf("scheduled job ran %d times", ran.Load())
	}
	q.pool.Exec(ctx, `DROP TABLE job_schedule`)
	if err := q.checkSchedules(ctx, time.Now()); err == nil || !strings.Contains(err.Error(), "job_schedule") {
		t.Fatalf("missing table: %v", err)
	}
	if infos, err := q.Schedules(ctx); err == nil || len(infos) != 1 || infos[0].NextRun.IsZero() {
		t.Fatalf("schedules without the table: %+v %v", infos, err)
	}
}

// TestReleaseOnShutdown: at shutdown a job still running is cancelled
// and put back to pending with its attempt uncounted, so the restarted
// node runs it at once; a handler that ignores cancellation has its job
// released by Stop, and its late result is dropped.
func TestReleaseOnShutdown(t *testing.T) {
	q := testQueue(t, 2)
	q.cfg.Drain = 100 * time.Millisecond
	ctx := context.Background()
	started := make(chan string, 2)
	q.Handle("batch", func(ctx context.Context, _ json.RawMessage) error {
		started <- "batch"
		<-ctx.Done()
		return ctx.Err()
	})
	finish := make(chan struct{})
	q.Handle("stubborn", func(ctx context.Context, _ json.RawMessage) error {
		started <- "stubborn"
		<-finish
		return nil
	})
	q.Run()
	batch, _ := q.Enqueue(ctx, "batch", nil)
	<-started
	stop, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	begin := time.Now()
	if err := q.Stop(stop); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("stop took %s", took)
	}
	j, _ := q.Get(ctx, batch)
	if j.State != "pending" || j.Attempts != 0 || j.LastError != nil || j.RunAt.After(time.Now()) {
		t.Fatalf("released job: %+v", j)
	}
	if q.TelemetryStats()["released_total"] != 1 {
		t.Fatalf("stats %v", q.TelemetryStats())
	}

	// The restarted node takes it at once, not after JOBS_STALE.
	next := New(Config{Workers: 1, Poll: 50 * time.Millisecond, Stale: time.Hour}, q.pool)
	next.Handle("batch", func(context.Context, json.RawMessage) error { return nil })
	next.Run()
	if j := waitState(t, next, batch, "done"); j.Attempts != 1 {
		t.Fatalf("rerun: %+v", j)
	}
	next.Stop(ctx)

	// A handler that ignores its context.
	q2 := New(Config{Workers: 1, Poll: 50 * time.Millisecond, Stale: time.Hour}, q.pool)
	q2.Handle("stubborn", q.handlers["stubborn"])
	q2.Run()
	stuck, _ := q2.Enqueue(ctx, "stubborn", nil)
	<-started
	stop2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	err := q2.Stop(stop2)
	if err == nil || !strings.Contains(err.Error(), "released") || errors.Is(stop2.Err(), context.DeadlineExceeded) {
		t.Fatalf("stop with a stubborn job: %v (ctx %v)", err, stop2.Err())
	}
	if j, _ := q2.Get(ctx, stuck); j.State != "pending" || j.Attempts != 0 {
		t.Fatalf("stubborn job: %+v", j)
	}
	close(finish)
	q2.wg.Wait()
	if j, _ := q2.Get(ctx, stuck); j.State != "pending" {
		t.Fatalf("late result written over the release: %+v", j)
	}
}
