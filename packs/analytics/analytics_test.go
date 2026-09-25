package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/report"
)

func testAnalytics(t *testing.T, otlp string) *Analytics {
	t.Helper()
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS app_error; DROP TABLE IF EXISTS app_event`)
	if _, err := pool.Exec(ctx, Tables); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Queue: 8, ClientRPS: 100, OTLPURL: otlp}, pool)
	a.Run()
	t.Cleanup(func() { a.Stop(ctx) })
	return a
}

func waitWritten(t *testing.T, a *Analytics, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.written.Load() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.written.Load() < n {
		t.Fatalf("written %d, want %d", a.written.Load(), n)
	}
}

func TestReportAndEvents(t *testing.T) {
	var otlpBodies []string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		otlpBodies = append(otlpBodies, string(b))
		w.WriteHeader(200)
	}))
	defer collector.Close()
	a := testAnalytics(t, collector.URL+"/v1/logs")
	ctx := context.Background()

	// Through the seam: a handler with a reporter in its context.
	s := lidza.NewServices()
	lidza.Provide[report.Reporter](s, a)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report.Capture(r.Context(), report.Error{Source: "server", Message: "boom\ndetail", Route: "GET /x", Method: "GET", RequestID: "r1"})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req = req.WithContext(report.WithLookup(lidza.WithServices(req.Context(), s), func() report.Reporter { return a }))
	h.ServeHTTP(rec, req)
	a.Track(ctx, "signup", map[string]any{"plan": "free"})
	waitWritten(t, a, 2)

	errs, err := Recent(ctx, a.pool, 10)
	if err != nil || len(errs) != 1 || errs[0].Message != "boom\ndetail" || *errs[0].Route != "GET /x" || errs[0].Fingerprint != Fingerprint(report.Error{Source: "server", Route: "GET /x", Message: "boom"}) {
		t.Fatalf("recent: %+v %v", errs, err)
	}
	var name string
	var props []byte
	if err := a.pool.QueryRow(ctx, `SELECT name, props FROM app_event`).Scan(&name, &props); err != nil || name != "signup" || !strings.Contains(string(props), `"plan"`) {
		t.Fatalf("event: %s %s %v", name, props, err)
	}
	if len(otlpBodies) != 1 || !strings.Contains(otlpBodies[0], `"stringValue":"boom\ndetail"`) || !strings.Contains(otlpBodies[0], "resourceLogs") {
		t.Fatalf("otlp: %v", otlpBodies)
	}

	// Frontend endpoint: errors and events, validation, size cap.
	lidza.Provide(s, a)
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/analytics/{kind}", Handler())
	post := func(kind, body string) int {
		req := httptest.NewRequest("POST", "/api/v1/analytics/"+kind, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:1"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req.WithContext(lidza.WithServices(req.Context(), s)))
		return rec.Code
	}
	if code := post("errors", `{"message":"TypeError: x is undefined","stack":"at a.js:1","url":"/about"}`); code != 202 {
		t.Fatalf("client error: %d", code)
	}
	if code := post("events", `{"name":"pageview","props":{"path":"/about"}}`); code != 202 {
		t.Fatalf("client event: %d", code)
	}
	if code := post("errors", `{"stack":"no message"}`); code != 400 {
		t.Fatalf("invalid report: %d", code)
	}
	if code := post("things", `{}`); code != 404 {
		t.Fatalf("unknown kind: %d", code)
	}
	if code := post("errors", `{"message":"`+strings.Repeat("x", 40<<10)+`"}`); code != 413 {
		t.Fatalf("oversized: %d", code)
	}
	waitWritten(t, a, 4)
	errs, _ = Recent(ctx, a.pool, 10)
	if len(errs) != 2 || errs[0].Source != "client" {
		t.Fatalf("client error stored: %+v", errs)
	}
	var st map[string]float64 = a.TelemetryStats()
	if st["written_total"] != 4 || st["dropped_total"] != 0 {
		t.Fatalf("stats %v", st)
	}
	_ = json.Valid
}
