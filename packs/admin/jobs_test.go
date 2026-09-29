package admin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/jobs"
	"github.com/agim/lidza/pkg/credentials"
)

// TestJobsPage: the Jobs page lists the declared schedules with their
// next and last run, and says what to run when their table is missing.
func TestJobsPage(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	// A schema of its own, so the jobs pack's tests may drop their tables
	// at the same time.
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url + sep + "search_path=lidza_admin_jobs", MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS lidza_admin_jobs CASCADE; CREATE SCHEMA lidza_admin_jobs`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS lidza_admin_jobs CASCADE`) })
	if _, err := pool.Exec(ctx, jobs.JobTable+";"+jobs.ScheduleTable); err != nil {
		t.Fatal(err)
	}
	q := jobs.New(jobs.Config{}, pool)
	if err := q.Schedule("weekly-digest", jobs.Weekly(time.Monday, "09:00", "Europe/Tirane"), nil); err != nil {
		t.Skipf("no zone data: %v", err)
	}
	if err := q.Schedule("sync", jobs.Every(15*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if err := q.Schedule("refresh", jobs.DailyAt("Europe/Tirane", "18:00", "02:00", "10:00"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO job_schedule (kind, spec, next_run_at, last_run_at) VALUES ('sync', 'every 15m', now() + interval '10 minutes', now() - interval '5 minutes')`); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	s := lidza.NewServices()
	lidza.Provide(s, q)
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: dir, Dir: filepath.Join(dir, "admin")}, s)
	code, body := get(t, srv, "/admin/jobs")
	if code != 200 || !strings.Contains(body, "Schedules") || !strings.Contains(body, "weekly Mon 09:00 Europe/Tirane") || !strings.Contains(body, "every 15m") ||
		!strings.Contains(body, "daily 02:00,10:00,18:00 Europe/Tirane") ||
		!strings.Contains(body, "5m ago") || !strings.Contains(body, "not yet") || !strings.Contains(body, "Mon ") || strings.Contains(body, "job_schedule table could not be read") {
		t.Fatalf("jobs page: %d %s", code, body)
	}

	pool.Exec(ctx, `DROP TABLE job_schedule`)
	if code, body := get(t, srv, "/admin/jobs"); code != 200 || !strings.Contains(body, "job_schedule table could not be read") || !strings.Contains(body, "every 15m") {
		t.Fatalf("jobs page without the table: %d %s", code, body)
	}
}
