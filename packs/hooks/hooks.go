// Package hooks is the official outbound webhooks pack: an app sends
// events to the URLs its users (or the app itself) subscribe, signed with
// the Standard Webhooks scheme (webhook-id, webhook-timestamp,
// webhook-signature; pkg/webhook.Standard verifies it), delivered in the
// background through the jobs pack with retries over about eleven hours,
// each attempt logged. An endpoint that keeps failing is disabled. Every
// target is checked when it is saved and again at each connection, after
// DNS: loopback, private, link-local and other internal addresses are
// refused (HOOKS_ALLOW_PRIVATE=true allows them for development), and
// redirects are not followed, so a subscriber cannot point the app at its
// own network.
//
//	n, err := hooks.From(ctx).Send(ctx, workspaceID, "order.paid", order)
//
// Endpoints belong to an owner, a string the app chooses: a workspace id
// (auth.WorkspaceID), a user id, or "" for the app's own. Secrets are
// sealed with the master key.
package hooks

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/jobs"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/webhook"
)

// Config comes from the environment.
type Config struct {
	// Timeout bounds one delivery attempt, connection to response.
	Timeout time.Duration `env:"HOOKS_TIMEOUT" default:"10s"`
	// DisableAfter disables an endpoint after this many deliveries in a
	// row that failed every attempt.
	DisableAfter int `env:"HOOKS_DISABLE_AFTER" default:"20"`
	// AllowPrivate lets endpoints point at loopback and private networks:
	// for development and tests only.
	AllowPrivate bool `env:"HOOKS_ALLOW_PRIVATE"`
	// MaxEndpoints bounds the endpoints of one owner.
	MaxEndpoints int `env:"HOOKS_MAX_ENDPOINTS" default:"20"`
	// Retention drops deliveries older than this, daily; 0 keeps them.
	Retention time.Duration `env:"HOOKS_RETENTION" default:"720h"`
}

// JobKind is the jobs pack kind that runs a delivery attempt.
const JobKind = "lidza/hooks.deliver"

// Schedule is the wait before each retry after a failed attempt: eight
// attempts over about eleven hours.
var Schedule = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour}

// Delivery states.
const (
	Pending   = "pending"
	Delivered = "delivered"
	Failed    = "failed"
)

// EndpointTable and DeliveryTable are the DDL (models HookEndpoint and
// HookDelivery in the schema fragment). Tests create them directly.
const (
	EndpointTable = `CREATE TABLE IF NOT EXISTS hook_endpoint (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner text NOT NULL,
  url text NOT NULL,
  events text[] NOT NULL,
  description text NOT NULL DEFAULT '',
  secret_sealed text NOT NULL,
  failures integer NOT NULL DEFAULT 0,
  disabled_at timestamptz,
  disabled_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hook_endpoint_owner_idx ON hook_endpoint (owner);`
	DeliveryTable = `CREATE TABLE IF NOT EXISTS hook_delivery (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  endpoint uuid NOT NULL,
  owner text NOT NULL,
  event text NOT NULL,
  body text NOT NULL,
  status text NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  last_status integer,
  last_error text,
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hook_delivery_endpoint_idx ON hook_delivery (endpoint);`
)

// Hooks is the running pack.
type Hooks struct {
	cfg    Config
	pool   *pgxpool.Pool
	queue  *jobs.Queue
	client *http.Client
	log    *slog.Logger
	stop   chan struct{}
}

// Pack returns the pack for packs.go; it needs lidza/db and lidza/jobs
// started first.
func Pack() lidza.Pack { return &Hooks{log: slog.Default()} }

// New builds the pack outside the lifecycle (tests).
func New(cfg Config, pool *pgxpool.Pool, q *jobs.Queue) *Hooks {
	h := &Hooks{cfg: cfg, pool: pool, queue: q, log: slog.Default()}
	h.defaults()
	h.client = h.newClient()
	if q != nil {
		q.Handle(JobKind, h.deliver)
	}
	return h
}

func (h *Hooks) defaults() {
	if h.cfg.Timeout <= 0 {
		h.cfg.Timeout = 10 * time.Second
	}
	if h.cfg.DisableAfter <= 0 {
		h.cfg.DisableAfter = 20
	}
	if h.cfg.MaxEndpoints <= 0 {
		h.cfg.MaxEndpoints = 20
	}
}

// From returns the pack from a request context.
func From(ctx context.Context) *Hooks { return lidza.Service[*Hooks](ctx) }

// Name implements lidza.Pack.
func (h *Hooks) Name() string { return "lidza/hooks" }

// Start reads the configuration, takes the pool and the queue, and
// registers the delivery job.
func (h *Hooks) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &h.cfg); err != nil {
		return err
	}
	h.defaults()
	pool, ok := s.Lookup(reflect.TypeFor[*pgxpool.Pool]())
	if !ok {
		return errors.New("hooks needs the db pack: list \"lidza/db\" before \"lidza/hooks\" in lidza.json")
	}
	q, ok := s.Lookup(reflect.TypeFor[*jobs.Queue]())
	if !ok {
		return errors.New("hooks needs the jobs pack: list \"lidza/jobs\" before \"lidza/hooks\" in lidza.json")
	}
	h.pool, h.queue = pool.(*pgxpool.Pool), q.(*jobs.Queue)
	h.client = h.newClient()
	h.queue.Handle(JobKind, h.deliver)
	h.stop = make(chan struct{})
	if h.cfg.Retention > 0 {
		go h.prune()
	}
	lidza.Provide(s, h)
	return nil
}

// Stop implements lidza.Pack.
func (h *Hooks) Stop(context.Context) error {
	if h.stop != nil {
		close(h.stop)
	}
	return nil
}

// prune drops old deliveries daily.
func (h *Hooks) prune() {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if _, err := h.pool.Exec(ctx, `DELETE FROM hook_delivery WHERE created_at < now() - $1::interval AND status <> 'pending'`, fmt.Sprintf("%d seconds", int(h.cfg.Retention.Seconds()))); err != nil {
			h.log.Warn("hooks: prune", "error", err)
		}
		cancel()
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
	}
}

// Endpoint is a subscriber URL; never its secret.
type Endpoint struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Events      []string   `json:"events"`
	Description string     `json:"description"`
	Failures    int        `json:"failures"`
	DisabledAt  *time.Time `json:"disabledAt,omitempty"`
	Disabled    string     `json:"disabledReason,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Delivery is one event sent to one endpoint, with its last attempt.
type Delivery struct {
	ID          string          `json:"id"`
	Endpoint    string          `json:"endpoint"`
	Event       string          `json:"event"`
	Body        json.RawMessage `json:"body"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	LastStatus  *int            `json:"lastStatus,omitempty"`
	LastError   string          `json:"lastError,omitempty"`
	DeliveredAt *time.Time      `json:"deliveredAt,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

// Errors the endpoint calls return.
var (
	ErrNotFound      = router.NotFound("no such endpoint")
	ErrTooMany       = router.ErrorCode(http.StatusConflict, "too_many_endpoints", "too many endpoints")
	ErrNoKey         = errors.New("hooks: endpoint secrets are sealed with the master key, and there is none (config/master.key or LIDZA_MASTER_KEY)")
	ErrDeliveryState = router.ErrorCode(http.StatusConflict, "pending", "the delivery is still being sent")
)

// EventName validates an event type: "order.paid", lower case words
// separated by dots, underscores or dashes.
func EventName(name string) error {
	if name == "" || len(name) > 100 {
		return fmt.Errorf("hooks: event %q: 1 to 100 characters", name)
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return fmt.Errorf("hooks: event %q: lower case letters, digits and . _ - only", name)
		}
	}
	return nil
}

// subscribed reports whether an endpoint's events take name: none means
// all, "*" all, "order.*" every event under order.
func subscribed(events []string, name string) bool {
	if len(events) == 0 {
		return true
	}
	for _, e := range events {
		if e == "*" || e == name || strings.HasSuffix(e, ".*") && strings.HasPrefix(name, strings.TrimSuffix(e, "*")) {
			return true
		}
	}
	return false
}

// CheckURL validates a target: an absolute http(s) URL without user
// information whose host, when it is an address, is a public one (unless
// AllowPrivate); https unless AllowPrivate. Names are checked again
// after DNS at each connection.
func (h *Hooks) CheckURL(raw string) error {
	if len(raw) > 2000 {
		return errors.New("the URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("an absolute http or https URL")
	}
	if u.Scheme == "http" && !h.cfg.AllowPrivate {
		return errors.New("an https URL")
	}
	if u.User != nil {
		return errors.New("no user or password in the URL")
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil && !h.allowed(a) {
		return errors.New("not a public address")
	}
	if strings.EqualFold(host, "localhost") && !h.cfg.AllowPrivate {
		return errors.New("not a public address")
	}
	return nil
}

// allowed reports whether a delivery may connect to an address.
func (h *Hooks) allowed(a netip.Addr) bool {
	a = a.Unmap()
	if h.cfg.AllowPrivate {
		return a.IsValid() && !a.IsUnspecified() && !a.IsMulticast()
	}
	return a.IsGlobalUnicast() && !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() &&
		!blocked(a)
}

// blocked are ranges IsGlobalUnicast counts as global that are not
// reachable services: carrier-grade NAT, benchmarking, documentation,
// the 6to4 relay, and IPv6's unique local and documentation ranges.
func blocked(a netip.Addr) bool {
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "192.88.99.0/24", "240.0.0.0/4", "fc00::/7", "2001:db8::/32", "64:ff9b::/96"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// newClient connects only to allowed addresses, resolved at dial time
// (a name that resolves inward is refused), follows no redirect and
// uses no proxy.
func (h *Hooks) newClient() *http.Client {
	dialer := &net.Dialer{Timeout: h.cfg.Timeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			var lastErr error = fmt.Errorf("hooks: %s resolves to no public address", host)
			for _, ip := range ips {
				if !h.allowed(ip) {
					continue
				}
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return c, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   h.cfg.Timeout,
		ResponseHeaderTimeout: h.cfg.Timeout,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   h.cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return webhook.StandardSecretPrefix + base64.StdEncoding.EncodeToString(b), nil
}

func seal(secret string) (string, error) {
	key, err := credentials.Key(".")
	if err != nil {
		return "", ErrNoKey
	}
	return credentials.Encrypt(key, []byte(secret))
}

func unseal(sealed string) (string, error) {
	key, err := credentials.Key(".")
	if err != nil {
		return "", ErrNoKey
	}
	b, err := credentials.Decrypt(key, sealed)
	return string(b), err
}

// EndpointInput is what an endpoint is created or changed with.
type EndpointInput struct {
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Description string   `json:"description"`
}

func (h *Hooks) checkInput(in EndpointInput) error {
	if err := h.CheckURL(in.URL); err != nil {
		return router.ErrorCode(http.StatusUnprocessableEntity, "url", "url: %s", err)
	}
	if len(in.Events) > 50 {
		return router.ErrorCode(http.StatusUnprocessableEntity, "events", "events: at most 50")
	}
	for _, e := range in.Events {
		if e == "*" || strings.HasSuffix(e, ".*") && EventName(strings.TrimSuffix(e, ".*")) == nil {
			continue
		}
		if err := EventName(e); err != nil {
			return router.ErrorCode(http.StatusUnprocessableEntity, "events", "events: %s", err)
		}
	}
	if utf8.RuneCountInString(in.Description) > 200 {
		return router.ErrorCode(http.StatusUnprocessableEntity, "description", "description: at most 200 characters")
	}
	return nil
}

const endpointColumns = `id::text, url, events, description, failures, disabled_at, disabled_reason, created_at`

func scanEndpoint(row pgx.CollectableRow) (Endpoint, error) {
	var e Endpoint
	var reason *string
	err := row.Scan(&e.ID, &e.URL, &e.Events, &e.Description, &e.Failures, &e.DisabledAt, &reason, &e.CreatedAt)
	if reason != nil {
		e.Disabled = *reason
	}
	if e.Events == nil {
		e.Events = []string{}
	}
	return e, err
}

// Create adds an endpoint for owner and returns it with its secret, the
// only time the secret is returned (Secret reveals it again).
func (h *Hooks) Create(ctx context.Context, owner string, in EndpointInput) (Endpoint, string, error) {
	if err := h.checkInput(in); err != nil {
		return Endpoint{}, "", err
	}
	var n int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM hook_endpoint WHERE owner = $1`, owner).Scan(&n); err != nil {
		return Endpoint{}, "", err
	}
	if n >= h.cfg.MaxEndpoints {
		return Endpoint{}, "", ErrTooMany
	}
	secret, err := newSecret()
	if err != nil {
		return Endpoint{}, "", err
	}
	sealed, err := seal(secret)
	if err != nil {
		return Endpoint{}, "", err
	}
	if in.Events == nil {
		in.Events = []string{}
	}
	rows, err := h.pool.Query(ctx, `INSERT INTO hook_endpoint (owner, url, events, description, secret_sealed) VALUES ($1, $2, $3, $4, $5) RETURNING `+endpointColumns,
		owner, in.URL, in.Events, strings.TrimSpace(in.Description), sealed)
	if err != nil {
		return Endpoint{}, "", err
	}
	e, err := pgx.CollectExactlyOneRow(rows, scanEndpoint)
	return e, secret, err
}

// List returns owner's endpoints, oldest first.
func (h *Hooks) List(ctx context.Context, owner string) ([]Endpoint, error) {
	rows, err := h.pool.Query(ctx, `SELECT `+endpointColumns+` FROM hook_endpoint WHERE owner = $1 ORDER BY created_at, id`, owner)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanEndpoint)
	if out == nil {
		out = []Endpoint{}
	}
	return out, err
}

// Get returns one of owner's endpoints; ErrNotFound for another's.
func (h *Hooks) Get(ctx context.Context, owner, id string) (Endpoint, error) {
	rows, err := h.pool.Query(ctx, `SELECT `+endpointColumns+` FROM hook_endpoint WHERE owner = $1 AND id::text = $2`, owner, id)
	if err != nil {
		return Endpoint{}, err
	}
	e, err := pgx.CollectExactlyOneRow(rows, scanEndpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	return e, err
}

// Update changes an endpoint's URL, events and description; enable
// clears a disabled endpoint's state and its failure count.
func (h *Hooks) Update(ctx context.Context, owner, id string, in EndpointInput, enable bool) (Endpoint, error) {
	if err := h.checkInput(in); err != nil {
		return Endpoint{}, err
	}
	if in.Events == nil {
		in.Events = []string{}
	}
	q := `UPDATE hook_endpoint SET url = $3, events = $4, description = $5, updated_at = now()`
	if enable {
		q += `, disabled_at = NULL, disabled_reason = NULL, failures = 0`
	}
	rows, err := h.pool.Query(ctx, q+` WHERE owner = $1 AND id::text = $2 RETURNING `+endpointColumns, owner, id, in.URL, in.Events, strings.TrimSpace(in.Description))
	if err != nil {
		return Endpoint{}, err
	}
	e, err := pgx.CollectExactlyOneRow(rows, scanEndpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	return e, err
}

// Delete removes an endpoint and its deliveries.
func (h *Hooks) Delete(ctx context.Context, owner, id string) error {
	tag, err := h.pool.Exec(ctx, `DELETE FROM hook_endpoint WHERE owner = $1 AND id::text = $2`, owner, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = h.pool.Exec(ctx, `DELETE FROM hook_delivery WHERE endpoint::text = $1`, id)
	return err
}

// Secret reveals an endpoint's secret to its owner.
func (h *Hooks) Secret(ctx context.Context, owner, id string) (string, error) {
	var sealed string
	err := h.pool.QueryRow(ctx, `SELECT secret_sealed FROM hook_endpoint WHERE owner = $1 AND id::text = $2`, owner, id).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return unseal(sealed)
}

// Rotate gives an endpoint a new secret and returns it; deliveries sign
// with it from now on.
func (h *Hooks) Rotate(ctx context.Context, owner, id string) (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	sealed, err := seal(secret)
	if err != nil {
		return "", err
	}
	tag, err := h.pool.Exec(ctx, `UPDATE hook_endpoint SET secret_sealed = $3, updated_at = now() WHERE owner = $1 AND id::text = $2`, owner, id, sealed)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return secret, nil
}

// envelope is the JSON body every delivery sends.
type envelope struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// Send delivers an event to every enabled endpoint of owner subscribed to
// it, in the background, and returns how many deliveries it queued. data
// is marshalled to JSON once: every endpoint, and every retry, receives
// the same body.
func (h *Hooks) Send(ctx context.Context, owner, event string, data any) (int, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	n, err := h.SendTx(ctx, tx, owner, event, data)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// SendTx is Send inside the caller's transaction: the deliveries exist
// only if the change that caused them commits.
func (h *Hooks) SendTx(ctx context.Context, tx pgx.Tx, owner, event string, data any) (int, error) {
	if err := EventName(event); err != nil {
		return 0, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return 0, fmt.Errorf("hooks: event data: %w", err)
	}
	body, err := json.Marshal(envelope{Type: event, Timestamp: time.Now().UTC(), Data: raw})
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text, events FROM hook_endpoint WHERE owner = $1 AND disabled_at IS NULL`, owner)
	if err != nil {
		return 0, err
	}
	type target struct {
		id     string
		events []string
	}
	targets, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (target, error) {
		var t target
		return t, r.Scan(&t.id, &t.events)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range targets {
		if !subscribed(t.events, event) {
			continue
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO hook_delivery (endpoint, owner, event, body, status) VALUES ($1, $2, $3, $4, 'pending') RETURNING id::text`,
			t.id, owner, event, string(body)).Scan(&id); err != nil {
			return 0, err
		}
		if _, err := h.queue.EnqueueTx(ctx, tx, JobKind, deliveryJob{Delivery: id}, jobs.MaxAttempts(3)); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

// deliveryJob is the job's payload.
type deliveryJob struct {
	Delivery string `json:"delivery"`
}

// deliver makes one attempt. It returns an error only when the attempt
// could not run (the database); a refused or failed delivery is
// recorded and, unless it was the last attempt, scheduled again.
func (h *Hooks) deliver(ctx context.Context, payload json.RawMessage) error {
	var job deliveryJob
	if err := json.Unmarshal(payload, &job); err != nil {
		return nil // nothing to retry
	}
	var d struct {
		endpoint, url, sealed, status string
		disabled                      bool
		attempts                      int
		body                          string
	}
	err := h.pool.QueryRow(ctx, `SELECT d.endpoint::text, e.url, e.secret_sealed, d.status, e.disabled_at IS NOT NULL, d.attempts, d.body
		FROM hook_delivery d JOIN hook_endpoint e ON e.id = d.endpoint WHERE d.id::text = $1`, job.Delivery).
		Scan(&d.endpoint, &d.url, &d.sealed, &d.status, &d.disabled, &d.attempts, &d.body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the endpoint was deleted
	}
	if err != nil {
		return err
	}
	if d.status != Pending {
		return nil
	}
	if d.disabled {
		_, err := h.pool.Exec(ctx, `UPDATE hook_delivery SET status = 'failed', last_error = 'the endpoint is disabled' WHERE id::text = $1`, job.Delivery)
		return err
	}
	code, attemptErr := h.attempt(ctx, job.Delivery, d.url, d.sealed, []byte(d.body))
	attempts := d.attempts + 1
	var status *int
	if code != 0 {
		status = &code
	}
	if attemptErr == nil {
		if _, err := h.pool.Exec(ctx, `UPDATE hook_delivery SET status = 'delivered', attempts = $2, last_status = $3, last_error = NULL, delivered_at = now() WHERE id::text = $1`, job.Delivery, attempts, status); err != nil {
			return err
		}
		_, err := h.pool.Exec(ctx, `UPDATE hook_endpoint SET failures = 0 WHERE id::text = $1 AND failures <> 0`, d.endpoint)
		return err
	}
	msg := attemptErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if attempts <= len(Schedule) {
		if _, err := h.pool.Exec(ctx, `UPDATE hook_delivery SET attempts = $2, last_status = $3, last_error = $4 WHERE id::text = $1`, job.Delivery, attempts, status, msg); err != nil {
			return err
		}
		_, err := h.queue.Enqueue(ctx, JobKind, job, jobs.RunAt(time.Now().Add(Schedule[attempts-1])), jobs.MaxAttempts(3))
		return err
	}
	if _, err := h.pool.Exec(ctx, `UPDATE hook_delivery SET status = 'failed', attempts = $2, last_status = $3, last_error = $4 WHERE id::text = $1`, job.Delivery, attempts, status, msg); err != nil {
		return err
	}
	// A delivery that failed every attempt counts against the endpoint;
	// enough in a row disable it.
	_, err = h.pool.Exec(ctx, `UPDATE hook_endpoint SET failures = failures + 1,
		disabled_at = CASE WHEN failures + 1 >= $2 THEN now() ELSE disabled_at END,
		disabled_reason = CASE WHEN failures + 1 >= $2 THEN $3 ELSE disabled_reason END
		WHERE id::text = $1`, d.endpoint, h.cfg.DisableAfter, fmt.Sprintf("%d deliveries in a row failed", h.cfg.DisableAfter))
	return err
}

// attempt posts the body to url, signed; the status code and nil for a
// 2xx reply.
func (h *Hooks) attempt(ctx context.Context, id, target, sealed string, body []byte) (int, error) {
	if err := h.CheckURL(target); err != nil {
		return 0, fmt.Errorf("refused target: %v", err)
	}
	secret, err := unseal(sealed)
	if err != nil {
		return 0, err
	}
	key, err := webhook.StandardKey(secret)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Lidza-Hooks/1")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", fmt.Sprint(now.Unix()))
	req.Header.Set("webhook-signature", webhook.StandardSignature(key, id, now, body))
	res, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 300))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res.StatusCode, nil
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return res.StatusCode, fmt.Errorf("HTTP %d: redirects are not followed", res.StatusCode)
	}
	return res.StatusCode, fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(strings.ToValidUTF8(string(excerpt), "")))
}

const deliveryColumns = `id::text, endpoint::text, event, body, status, attempts, last_status, COALESCE(last_error, ''), delivered_at, created_at`

func scanDelivery(row pgx.CollectableRow) (Delivery, error) {
	var d Delivery
	var body string
	err := row.Scan(&d.ID, &d.Endpoint, &d.Event, &body, &d.Status, &d.Attempts, &d.LastStatus, &d.LastError, &d.DeliveredAt, &d.CreatedAt)
	d.Body = json.RawMessage(body)
	return d, err
}

// Deliveries lists an endpoint's deliveries, newest first, at most limit
// (200).
func (h *Hooks) Deliveries(ctx context.Context, owner, endpoint string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := h.pool.Query(ctx, `SELECT `+deliveryColumns+` FROM hook_delivery WHERE owner = $1 AND endpoint::text = $2 ORDER BY created_at DESC, id LIMIT $3`, owner, endpoint, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanDelivery)
	if out == nil {
		out = []Delivery{}
	}
	return out, err
}

// Replay sends a delivery's body again as a new delivery, now; a
// pending delivery is not replayed.
func (h *Hooks) Replay(ctx context.Context, owner, id string) (Delivery, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer tx.Rollback(ctx)
	var endpoint, event, status, body string
	err = tx.QueryRow(ctx, `SELECT endpoint::text, event, body, status FROM hook_delivery WHERE owner = $1 AND id::text = $2`, owner, id).Scan(&endpoint, &event, &body, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, router.NotFound("no such delivery")
	}
	if err != nil {
		return Delivery{}, err
	}
	if status == Pending {
		return Delivery{}, ErrDeliveryState
	}
	rows, err := tx.Query(ctx, `INSERT INTO hook_delivery (endpoint, owner, event, body, status) VALUES ($1, $2, $3, $4, 'pending') RETURNING `+deliveryColumns, endpoint, owner, event, body)
	if err != nil {
		return Delivery{}, err
	}
	d, err := pgx.CollectExactlyOneRow(rows, scanDelivery)
	if err != nil {
		return Delivery{}, err
	}
	if _, err := h.queue.EnqueueTx(ctx, tx, JobKind, deliveryJob{Delivery: d.ID}, jobs.MaxAttempts(3)); err != nil {
		return Delivery{}, err
	}
	return d, tx.Commit(ctx)
}

// Ping sends a hooks.ping event to one endpoint, whatever it subscribes
// to, so its owner can check it receives and verifies.
func (h *Hooks) Ping(ctx context.Context, owner, id string) (Delivery, error) {
	if _, err := h.Get(ctx, owner, id); err != nil {
		return Delivery{}, err
	}
	body, _ := json.Marshal(envelope{Type: "hooks.ping", Timestamp: time.Now().UTC(), Data: json.RawMessage(`{}`)})
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `INSERT INTO hook_delivery (endpoint, owner, event, body, status) VALUES ($1, $2, 'hooks.ping', $3, 'pending') RETURNING `+deliveryColumns, id, owner, string(body))
	if err != nil {
		return Delivery{}, err
	}
	d, err := pgx.CollectExactlyOneRow(rows, scanDelivery)
	if err != nil {
		return Delivery{}, err
	}
	if _, err := h.queue.EnqueueTx(ctx, tx, JobKind, deliveryJob{Delivery: d.ID}, jobs.MaxAttempts(3)); err != nil {
		return Delivery{}, err
	}
	return d, tx.Commit(ctx)
}

// EndpointRow is an endpoint as the admin pages list it, across owners.
type EndpointRow struct {
	Endpoint
	Owner string `json:"owner"`
}

// DeliveryRow is a delivery as the admin pages list it, with its
// endpoint's URL.
type DeliveryRow struct {
	Delivery
	Owner string `json:"owner"`
	URL   string `json:"url"`
}

// AdminEndpoints lists every owner's endpoints, the disabled first, then
// by failures, at most limit (200).
func (h *Hooks) AdminEndpoints(ctx context.Context, limit int) ([]EndpointRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := h.pool.Query(ctx, `SELECT owner, `+endpointColumns+` FROM hook_endpoint ORDER BY disabled_at IS NULL, failures DESC, created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (EndpointRow, error) {
		var e EndpointRow
		var reason *string
		err := row.Scan(&e.Owner, &e.ID, &e.URL, &e.Events, &e.Description, &e.Failures, &e.DisabledAt, &reason, &e.CreatedAt)
		if reason != nil {
			e.Disabled = *reason
		}
		return e, err
	})
}

// AdminDeliveries lists the latest deliveries across owners, newest
// first; status narrows them ("failed", "pending", "delivered"; "" for
// all).
func (h *Hooks) AdminDeliveries(ctx context.Context, status string, limit int) ([]DeliveryRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := h.pool.Query(ctx, `SELECT d.owner, e.url, d.id::text, d.endpoint::text, d.event, d.body, d.status, d.attempts, d.last_status, COALESCE(d.last_error, ''), d.delivered_at, d.created_at
		FROM hook_delivery d JOIN hook_endpoint e ON e.id = d.endpoint
		WHERE $1 = '' OR d.status = $1 ORDER BY d.created_at DESC, d.id LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (DeliveryRow, error) {
		var d DeliveryRow
		var body string
		err := row.Scan(&d.Owner, &d.URL, &d.ID, &d.Endpoint, &d.Event, &body, &d.Status, &d.Attempts, &d.LastStatus, &d.LastError, &d.DeliveredAt, &d.CreatedAt)
		d.Body = json.RawMessage(body)
		return d, err
	})
}

// AdminReplay sends any owner's delivery again (Replay without the owner
// check): the admin pages' action.
func (h *Hooks) AdminReplay(ctx context.Context, id string) error {
	var owner string
	if err := h.pool.QueryRow(ctx, `SELECT owner FROM hook_delivery WHERE id::text = $1`, id).Scan(&owner); err != nil {
		return router.NotFound("no such delivery")
	}
	_, err := h.Replay(ctx, owner, id)
	return err
}

// AdminEnable turns a disabled endpoint back on, its failures cleared.
func (h *Hooks) AdminEnable(ctx context.Context, id string) error {
	tag, err := h.pool.Exec(ctx, `UPDATE hook_endpoint SET disabled_at = NULL, disabled_reason = NULL, failures = 0, updated_at = now() WHERE id::text = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}
