// Package analytics is the official, opt-in error reporting and product
// analytics pack. Server panics and 500s, frontend errors and named events
// land in Postgres (app_error, app_event) through a bounded queue, and
// optionally go out as OTLP log records to any collector. Nothing leaves
// the app unless ANALYTICS_OTLP_URL is set; no IP addresses are stored.
package analytics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/report"
	"github.com/agim/lidza/pkg/router"
)

// Config comes from the environment.
type Config struct {
	// OTLPURL is an OTLP/HTTP logs endpoint (http://collector:4318/v1/logs).
	// Empty keeps everything in Postgres.
	OTLPURL string `env:"ANALYTICS_OTLP_URL"`
	// Queue bounds records waiting to be written; the rest are dropped and
	// counted.
	Queue int `env:"ANALYTICS_QUEUE" default:"1024"`
	// ClientRPS limits what one client may post per second.
	ClientRPS float64 `env:"ANALYTICS_CLIENT_RPS" default:"5"`
	// Retention drops rows older than this on start; 0 keeps them.
	Retention time.Duration `env:"ANALYTICS_RETENTION" default:"720h"`
}

// Event is a named product event.
type Event struct {
	Name      string         `json:"name"`
	Props     map[string]any `json:"props,omitempty"`
	URL       string         `json:"url,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	At        time.Time      `json:"at"`
}

// Analytics is the running pack.
type Analytics struct {
	cfg  Config
	pool *pgxpool.Pool
	log  *slog.Logger
	http *http.Client

	queue   chan record
	wg      sync.WaitGroup
	stop    context.CancelFunc
	dropped atomic.Int64
	written atomic.Int64
	limiter middleware.Middleware
}

type record struct {
	err   *report.Error
	event *Event
}

// Pack returns the pack for packs.go; it needs the db pack started first.
func Pack() lidza.Pack { return &Analytics{log: slog.Default()} }

// New builds the pack outside the lifecycle (tests).
func New(cfg Config, pool *pgxpool.Pool) *Analytics {
	a := &Analytics{cfg: cfg, pool: pool, log: slog.Default(), http: &http.Client{Timeout: 5 * time.Second}}
	a.defaults()
	return a
}

func (a *Analytics) defaults() {
	if a.cfg.Queue <= 0 {
		a.cfg.Queue = 1024
	}
	if a.cfg.ClientRPS <= 0 {
		a.cfg.ClientRPS = 5
	}
	if a.http == nil {
		a.http = &http.Client{Timeout: 5 * time.Second}
	}
	a.queue = make(chan record, a.cfg.Queue)
	a.limiter = middleware.RateLimit(middleware.RateLimitOptions{RPS: a.cfg.ClientRPS, Burst: int(a.cfg.ClientRPS * 4)})
}

// From returns the pack from a request context.
func From(ctx context.Context) *Analytics { return lidza.Service[*Analytics](ctx) }

// Name implements lidza.Pack.
func (a *Analytics) Name() string { return "lidza/analytics" }

// Start reads the configuration, takes the pool, applies retention and
// starts the writer.
func (a *Analytics) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &a.cfg); err != nil {
		return err
	}
	pool, ok := s.Lookup(typeOf[*pgxpool.Pool]())
	if !ok {
		return errors.New("analytics needs the db pack: list \"lidza/db\" before \"lidza/analytics\" in lidza.json")
	}
	a.pool = pool.(*pgxpool.Pool)
	a.defaults()
	if a.cfg.Retention > 0 {
		cutoff := time.Now().Add(-a.cfg.Retention)
		a.pool.Exec(ctx, `DELETE FROM app_error WHERE created_at < $1`, cutoff)
		a.pool.Exec(ctx, `DELETE FROM app_event WHERE created_at < $1`, cutoff)
	}
	a.Run()
	lidza.Provide(s, a)
	lidza.Provide[report.Reporter](s, a)
	return nil
}

// Run starts the writer goroutine.
func (a *Analytics) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	a.wg.Add(1)
	go a.writer(ctx)
}

// Stop flushes what is queued and stops the writer.
func (a *Analytics) Stop(ctx context.Context) error {
	if a.stop != nil {
		a.stop()
	}
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("analytics: writer still busy at shutdown")
	}
}

// Report implements report.Reporter: queue and return.
func (a *Analytics) Report(ctx context.Context, e report.Error) {
	if e.UserID == "" {
		if u := auth.CurrentUser(ctx); u != nil {
			e.UserID = u.ID
		}
	}
	a.enqueue(record{err: &e})
}

// Track records a product event from the server side.
func (a *Analytics) Track(ctx context.Context, name string, props map[string]any) {
	e := Event{Name: name, Props: props, At: time.Now().UTC()}
	if u := auth.CurrentUser(ctx); u != nil {
		e.UserID = u.ID
	}
	a.enqueue(record{event: &e})
}

func (a *Analytics) enqueue(r record) {
	select {
	case a.queue <- r:
	default:
		a.dropped.Add(1)
	}
}

func (a *Analytics) writer(ctx context.Context) {
	defer a.wg.Done()
	for {
		select {
		case r := <-a.queue:
			a.write(r)
		case <-ctx.Done():
			for {
				select {
				case r := <-a.queue:
					a.write(r)
				default:
					return
				}
			}
		}
	}
}

func (a *Analytics) write(r record) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	switch {
	case r.err != nil:
		err = a.insertError(ctx, *r.err)
		if err == nil && a.cfg.OTLPURL != "" {
			if exportErr := a.exportOTLP(ctx, *r.err); exportErr != nil {
				a.log.Warn("analytics: otlp export", "error", exportErr)
			}
		}
	case r.event != nil:
		err = a.insertEvent(ctx, *r.event)
	}
	if err != nil {
		a.log.Warn("analytics: write", "error", err)
		return
	}
	a.written.Add(1)
}

func (a *Analytics) insertError(ctx context.Context, e report.Error) error {
	extra, _ := json.Marshal(e.Extra)
	if len(extra) == 0 || string(extra) == "null" {
		extra = []byte("{}")
	}
	_, err := a.pool.Exec(ctx, `INSERT INTO app_error (source, message, stack, route, method, url, request_id, user_id, user_agent, fingerprint, extra, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		e.Source, truncate(e.Message, 2000), nullable(truncate(e.Stack, 20000)), nullable(e.Route), nullable(e.Method), nullable(truncate(e.URL, 2000)),
		nullable(e.RequestID), nullable(e.UserID), nullable(truncate(e.UserAgent, 300)), Fingerprint(e), extra, e.At)
	return err
}

func (a *Analytics) insertEvent(ctx context.Context, e Event) error {
	props, _ := json.Marshal(e.Props)
	if len(props) == 0 || string(props) == "null" {
		props = []byte("{}")
	}
	_, err := a.pool.Exec(ctx, `INSERT INTO app_event (name, props, url, session_id, user_id, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		truncate(e.Name, 100), props, nullable(truncate(e.URL, 2000)), nullable(e.SessionID), nullable(e.UserID), e.At)
	return err
}

// Fingerprint groups the same failure: source, route and the first line
// of the message.
func Fingerprint(e report.Error) string {
	first := e.Message
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	sum := sha256.Sum256([]byte(e.Source + "|" + e.Route + "|" + first))
	return hex.EncodeToString(sum[:8])
}

// exportOTLP sends one error as an OTLP/HTTP JSON log record.
func (a *Analytics) exportOTLP(ctx context.Context, e report.Error) error {
	attrs := []map[string]any{
		{"key": "lidza.source", "value": map[string]any{"stringValue": e.Source}},
		{"key": "http.route", "value": map[string]any{"stringValue": e.Route}},
		{"key": "http.request.method", "value": map[string]any{"stringValue": e.Method}},
		{"key": "lidza.request_id", "value": map[string]any{"stringValue": e.RequestID}},
		{"key": "exception.stacktrace", "value": map[string]any{"stringValue": e.Stack}},
	}
	body := map[string]any{"resourceLogs": []any{map[string]any{
		"resource": map[string]any{"attributes": []map[string]any{{"key": "service.name", "value": map[string]any{"stringValue": "lidza-app"}}}},
		"scopeLogs": []any{map[string]any{"scope": map[string]any{"name": "lidza/analytics"}, "logRecords": []any{map[string]any{
			"timeUnixNano": fmt.Sprint(e.At.UnixNano()), "severityNumber": 17, "severityText": "ERROR",
			"body": map[string]any{"stringValue": e.Message}, "attributes": attrs,
		}}}},
	}}}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.OTLPURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("collector replied %d", res.StatusCode)
	}
	return nil
}

// Recent returns the latest errors, newest first, for the MCP tool and
// dashboards.
func Recent(ctx context.Context, pool *pgxpool.Pool, limit int) ([]StoredError, error) {
	rows, err := pool.Query(ctx, `SELECT id, source, message, stack, route, method, url, request_id, user_id, fingerprint, created_at FROM app_error ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredError
	for rows.Next() {
		var e StoredError
		if err := rows.Scan(&e.ID, &e.Source, &e.Message, &e.Stack, &e.Route, &e.Method, &e.URL, &e.RequestID, &e.UserID, &e.Fingerprint, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// StoredError is a row of app_error.
type StoredError struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Message     string    `json:"message"`
	Stack       *string   `json:"stack,omitempty"`
	Route       *string   `json:"route,omitempty"`
	Method      *string   `json:"method,omitempty"`
	URL         *string   `json:"url,omitempty"`
	RequestID   *string   `json:"requestId,omitempty"`
	UserID      *string   `json:"userId,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Handler accepts frontend reports: POST /api/v1/analytics/errors with a
// report.Error, POST /api/v1/analytics/events with an Event. Register with
// r.Handle("POST /api/v1/analytics/{kind}", analytics.Handler()). Bodies
// are capped at 32 KB and clients are rate limited.
func Handler() http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := From(r.Context())
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
		if err != nil {
			router.Error(w, http.StatusRequestEntityTooLarge, "report too large")
			return
		}
		switch r.PathValue("kind") {
		case "errors":
			var e report.Error
			if err := json.Unmarshal(body, &e); err != nil || e.Message == "" {
				router.Error(w, http.StatusBadRequest, "expected {message, stack?, url?}")
				return
			}
			e.Source = "client"
			e.RequestID = middleware.GetRequestID(r.Context())
			e.UserAgent = r.UserAgent()
			e.At = time.Now().UTC()
			a.Report(r.Context(), e)
		case "events":
			var e Event
			if err := json.Unmarshal(body, &e); err != nil || e.Name == "" {
				router.Error(w, http.StatusBadRequest, "expected {name, props?, url?, sessionId?}")
				return
			}
			e.At = time.Now().UTC()
			if u := auth.CurrentUser(r.Context()); u != nil {
				e.UserID = u.ID
			}
			a.enqueue(record{event: &e})
		default:
			router.Error(w, http.StatusNotFound, "unknown report kind")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).limiter(inner).ServeHTTP(w, r)
	})
}

// TelemetryStats reports the queue to /metrics.
func (a *Analytics) TelemetryStats() map[string]float64 {
	return map[string]float64{
		"queue":         float64(len(a.queue)),
		"queue_max":     float64(cap(a.queue)),
		"written_total": float64(a.written.Load()),
		"dropped_total": float64(a.dropped.Load()),
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
