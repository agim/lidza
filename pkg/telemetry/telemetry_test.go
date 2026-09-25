package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeDB struct{ fail bool }

func (f *fakeDB) Name() string { return "db" }
func (f *fakeDB) Ready(context.Context) error {
	if f.fail {
		return errors.New("connection refused")
	}
	return nil
}
func (f *fakeDB) TelemetryStats() map[string]float64 { return map[string]float64{"conns": 3} }

type plain struct{}

func TestMetricsAndReadiness(t *testing.T) {
	db := &fakeDB{}
	services := []any{db, plain{}}
	tel := New(func(f func(any)) {
		for _, s := range services {
			f(s)
		}
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/things/{id}", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "x") })
	h := tel.Middleware()(mux)
	for _, p := range []string{"/api/v1/things/1", "/api/v1/things/2", "/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
	}

	rec := httptest.NewRecorder()
	tel.Metrics().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`lidza_http_requests_total{method="GET",route="GET /api/v1/things/{id}",status="200"} 2`,
		`lidza_http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`lidza_http_request_duration_seconds_bucket{method="GET",route="GET /api/v1/things/{id}"`,
		`lidza_service_stat{service="db",stat="conns"} 3`,
		"go_goroutines",
		"process_resident_memory_bytes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if strings.Contains(body, "things/1") {
		t.Error("raw path leaked into labels")
	}

	rec = httptest.NewRecorder()
	tel.Readyz(time.Second).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"db":"ok"`) {
		t.Fatalf("ready: %d %s", rec.Code, rec.Body.String())
	}
	db.fail = true
	rec = httptest.NewRecorder()
	tel.Readyz(time.Second).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("not ready: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	Healthz().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatal("healthz")
	}
}
