package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// A Schedule says when a recurring job is due. Every, Daily, DailyAt and
// Weekly build one; an app may implement its own (a cron library, say).
type Schedule interface {
	// Next returns the first due time strictly after t.
	Next(t time.Time) time.Time
	// String is the spec kept with the schedule's row and shown on the
	// admin Jobs page: "every 15m", "weekly Mon 09:00 Europe/Tirane".
	String() string
}

// Every is due at every multiple of d, counted from midnight UTC: Every(15
// * time.Minute) runs at :00, :15, :30 and :45. d is at least a second.
// For a time of day in a time zone, use Daily.
func Every(d time.Duration) Schedule { return every{d} }

type every struct{ d time.Duration }

func (e every) Next(t time.Time) time.Time { return t.UTC().Truncate(e.d).Add(e.d) }

func (e every) String() string {
	s := e.d.String() // 15m0s, 1h0m0s
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return "every " + s
}

func (e every) validate() error {
	if e.d < time.Second {
		return fmt.Errorf("jobs: Every(%s): the interval is at least a second", e.d)
	}
	return nil
}

// Daily is due every day at clock ("09:00", 24-hour) in the time zone
// named zone ("Europe/Tirane"; "" is UTC). On the day clocks skip clock
// (02:30 when they jump from 02:00 to 03:00), the job runs once, moved
// forward by the jump (03:30); on the day clocks go back over it, it runs
// once, not twice. For several times a day, use DailyAt.
func Daily(clock, zone string) Schedule { return newCalendar(-1, []string{clock}, zone) }

// DailyAt is Daily at several times of day in one time zone:
// DailyAt("Europe/Tirane", "02:00", "10:00", "18:00") runs three times a
// local day, at those wall-clock times whatever the zone's offset from
// UTC that day. Each time follows Daily's daylight-saving rules; two
// times a clock change moves onto one instant run once. The spec lists
// the times in order: "daily 02:00,10:00,18:00 Europe/Tirane".
func DailyAt(zone string, clocks ...string) Schedule { return newCalendar(-1, clocks, zone) }

// Weekly is Daily on one weekday: Weekly(time.Monday, "09:00",
// "Europe/Tirane").
func Weekly(day time.Weekday, clock, zone string) Schedule {
	return newCalendar(day, []string{clock}, zone)
}

type calendar struct {
	day    time.Weekday // -1 for every day
	clocks []clockTime  // in order of the day, no repeats
	zone   string
	loc    *time.Location
	err    error
}

// clockTime is one time of day, as written and as read.
type clockTime struct {
	text         string
	hour, minute int
}

func newCalendar(day time.Weekday, clocks []string, zone string) *calendar {
	c := &calendar{day: day, zone: zone, loc: time.UTC}
	if len(clocks) == 0 {
		c.err = errors.New("jobs: schedule needs a time of day")
		return c
	}
	seen := map[int]bool{}
	for _, clock := range clocks {
		t, err := time.Parse("15:04", clock)
		if err != nil {
			c.err = fmt.Errorf("jobs: schedule time %q: want 24-hour HH:MM", clock)
			return c
		}
		if m := t.Hour()*60 + t.Minute(); !seen[m] {
			seen[m] = true
			c.clocks = append(c.clocks, clockTime{clock, t.Hour(), t.Minute()})
		}
	}
	sort.Slice(c.clocks, func(i, j int) bool {
		a, b := c.clocks[i], c.clocks[j]
		return a.hour*60+a.minute < b.hour*60+b.minute
	})
	if day < -1 || day > time.Saturday {
		c.err = fmt.Errorf("jobs: schedule weekday %d", day)
		return c
	}
	if zone != "" {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			c.err = fmt.Errorf("jobs: schedule time zone %q: %w", zone, err)
			return c
		}
		c.loc = loc
	}
	return c
}

func (c *calendar) validate() error { return c.err }

// Next is the earliest of a date's times after t, on the first date that
// has one. time.Date places each time on its date: a skipped wall-clock
// time moves forward by the jump, a repeated one is taken once.
func (c *calendar) Next(t time.Time) time.Time {
	if c.err != nil {
		return time.Time{}
	}
	lt := t.In(c.loc)
	for i := 0; i <= 8; i++ {
		// The weekday of the date itself, before a skipped clock time
		// could move the candidate.
		noon := time.Date(lt.Year(), lt.Month(), lt.Day()+i, 12, 0, 0, 0, c.loc)
		if c.day >= 0 && noon.Weekday() != c.day {
			continue
		}
		var best time.Time
		for _, ct := range c.clocks {
			at := time.Date(noon.Year(), noon.Month(), noon.Day(), ct.hour, ct.minute, 0, 0, c.loc)
			if at.After(t) && (best.IsZero() || at.Before(best)) {
				best = at
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{} // unreachable: a week always has the day
}

func (c *calendar) String() string {
	zone := c.zone
	if zone == "" {
		zone = "UTC"
	}
	texts := make([]string, len(c.clocks))
	for i, ct := range c.clocks {
		texts[i] = ct.text
	}
	clock := strings.Join(texts, ",")
	if c.day < 0 {
		return "daily " + clock + " " + zone
	}
	return "weekly " + c.day.String()[:3] + " " + clock + " " + zone
}

// scheduled is one registered schedule.
type scheduled struct {
	kind    string
	when    Schedule
	payload json.RawMessage
	// synced is set once this node has seen or written its spec in the
	// row; a node writes its spec once per process, so nodes of two
	// releases during a rolling deploy do not undo each other each tick.
	synced atomic.Bool
}

// Schedule enqueues a job of kind whenever when is due, with payload
// (encoded as JSON). Declare it in OnStart next to the kind's handler:
//
//	q := jobs.FromServices(s)
//	q.Handle("weekly-digest", sendDigest)
//	if err := q.Schedule("weekly-digest", jobs.Weekly(time.Monday, "09:00", "Europe/Tirane"), nil); err != nil {
//		return err
//	}
//
// Every node that declares it runs the check in the pack's one scheduler
// loop, and the job_schedule table decides: exactly one job is enqueued
// per due time however many nodes run. After downtime, one missed run is
// enqueued, not one per missed time. A changed schedule takes effect when
// a node with the new one starts. The job_schedule table comes from
// schema.lidza (`lidza gen`, `lidza db migrate`).
func (q *Queue) Schedule(kind string, when Schedule, payload any) error {
	if kind == "" || when == nil {
		return errors.New("jobs: Schedule needs a kind and a schedule")
	}
	if v, ok := when.(interface{ validate() error }); ok {
		if err := v.validate(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("jobs: schedule %s payload: %w", kind, err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, s := range q.schedules {
		if s.kind == kind {
			return fmt.Errorf("jobs: %s is already scheduled", kind)
		}
	}
	q.schedules = append(q.schedules, &scheduled{kind: kind, when: when, payload: data})
	return nil
}

// ScheduleInfo is a declared schedule with its next and last run, for the
// admin pages.
type ScheduleInfo struct {
	Kind      string
	Spec      string
	NextRun   time.Time
	LastRun   *time.Time
	LastJobID *string
}

// Schedules lists the schedules this node declares, with the next and
// last run from the job_schedule table. When the table cannot be read,
// the next run is computed and the error returned alongside.
func (q *Queue) Schedules(ctx context.Context) ([]ScheduleInfo, error) {
	list := q.scheduleList()
	if len(list) == 0 {
		return nil, nil
	}
	now := time.Now()
	out := make([]ScheduleInfo, len(list))
	index := map[string]int{}
	kinds := make([]string, len(list))
	for i, s := range list {
		out[i] = ScheduleInfo{Kind: s.kind, Spec: s.when.String(), NextRun: s.when.Next(now)}
		index[s.kind] = i
		kinds[i] = s.kind
	}
	rows, err := q.pool.Query(ctx, `SELECT kind, next_run_at, last_run_at, last_job_id::text FROM job_schedule WHERE kind = ANY($1)`, kinds)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var next time.Time
		var last *time.Time
		var job *string
		if err := rows.Scan(&kind, &next, &last, &job); err != nil {
			return out, err
		}
		i := index[kind]
		loc := zoneOf(list[i].when)
		out[i].NextRun, out[i].LastJobID = next.In(loc), job
		if last != nil {
			l := last.In(loc)
			out[i].LastRun = &l
		}
	}
	return out, rows.Err()
}

// zoneOf is the time zone a schedule's times read best in: its own for
// Daily, DailyAt and Weekly, UTC otherwise.
func zoneOf(s Schedule) *time.Location {
	if c, ok := s.(*calendar); ok && c.err == nil {
		return c.loc
	}
	return time.UTC
}

func (q *Queue) scheduleList() []*scheduled {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return append([]*scheduled(nil), q.schedules...)
}

// scheduler is the pack's one loop over every schedule.
func (q *Queue) scheduler(ctx context.Context) {
	defer q.wg.Done()
	t := time.NewTicker(q.cfg.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if len(q.scheduleList()) == 0 {
			continue
		}
		tctx, cancel := context.WithTimeout(ctx, q.cfg.Poll+10*time.Second)
		err := q.checkSchedules(tctx, time.Now())
		cancel()
		if err != nil && ctx.Err() == nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
				if !q.warnedTable.Swap(true) {
					q.log.Error("jobs: schedules need the job_schedule table: run `lidza gen` and `lidza db migrate`", "error", err)
				}
				continue
			}
			q.log.Error("jobs: scheduler", "error", err)
		}
	}
}

// checkSchedules enqueues every schedule due at now. Several nodes may
// run it at once: the conditional update of next_run_at lets one of them
// through per due time.
func (q *Queue) checkSchedules(ctx context.Context, now time.Time) error {
	list := q.scheduleList()
	if len(list) == 0 {
		return nil
	}
	kinds := make([]string, len(list))
	for i, s := range list {
		kinds[i] = s.kind
	}
	type row struct {
		spec string
		next time.Time
	}
	have := map[string]row{}
	rows, err := q.pool.Query(ctx, `SELECT kind, spec, next_run_at FROM job_schedule WHERE kind = ANY($1)`, kinds)
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind string
		var r row
		if err := rows.Scan(&kind, &r.spec, &r.next); err != nil {
			rows.Close()
			return err
		}
		have[kind] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var errs []error
	for _, s := range list {
		spec := s.when.String()
		r, ok := have[s.kind]
		switch {
		case !ok:
			// First sight: the next due time from now; a node that loses
			// the insert race writes its spec on the next check.
			tag, err := q.pool.Exec(ctx, `INSERT INTO job_schedule (kind, spec, next_run_at) VALUES ($1, $2, $3) ON CONFLICT (kind) DO NOTHING`,
				s.kind, spec, s.when.Next(now))
			if err != nil {
				errs = append(errs, err)
			} else if tag.RowsAffected() == 1 {
				s.synced.Store(true)
			}
		case r.spec != spec && !s.synced.Load():
			// A schedule changed in code: it takes effect from now.
			_, err := q.pool.Exec(ctx, `UPDATE job_schedule SET spec = $2, next_run_at = $3, updated_at = now() WHERE kind = $1 AND spec = $4`,
				s.kind, spec, s.when.Next(now), r.spec)
			if err != nil {
				errs = append(errs, err)
			} else {
				s.synced.Store(true)
			}
		case !r.next.After(now):
			if r.spec == spec {
				s.synced.Store(true)
			}
			if err := q.fire(ctx, s, spec, r.next, now); err != nil {
				errs = append(errs, err)
			}
		case r.spec == spec:
			s.synced.Store(true)
		}
	}
	return errors.Join(errs...)
}

// fire moves the schedule past now and enqueues its job, in one
// transaction. The update matches only while next_run_at is still due:
// a node racing for the same due time waits for the row, then matches
// nothing and enqueues nothing. The next due time is counted from now,
// so after downtime one missed run is enqueued, not every one.
func (q *Queue) fire(ctx context.Context, s *scheduled, spec string, due, now time.Time) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE job_schedule SET next_run_at = $3, last_run_at = $2, spec = $4, updated_at = now() WHERE kind = $1 AND next_run_at = $2`,
		s.kind, due, s.when.Next(now), spec)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // another node enqueued it
	}
	id, err := q.enqueue(ctx, tx, s.kind, s.payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE job_schedule SET last_job_id = $2 WHERE kind = $1`, s.kind, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
