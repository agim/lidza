// Package webhook receives a provider's webhooks safely: the signature is
// verified on the raw body before anything decodes it, deliveries outside
// the replay window are refused, and each delivery id is handled once.
//
//	r.Handle("POST /api/v1/webhooks/payments", webhook.Stripe("PAYMENTS_WEBHOOK_SECRET", onPayment))
//
//	func onPayment(ctx context.Context, d *webhook.Delivery) error {
//		ev, err := d.StripeEvent()
//		if err != nil {
//			return err
//		}
//		switch ev.Type {
//		case "checkout.session.completed":
//			// ...
//		}
//		return nil // an event the app ignores is a success too
//	}
//
// The secret is read by name like every setting (pkg/env): .env files,
// the sealed credentials (`lidza credentials set production.NAME=...`),
// values saved at runtime, the process environment. It is read again at
// most once a minute, so a saved value takes effect without a restart.
// Without a secret every delivery is refused with 503 and a log line
// naming the setting: an endpoint never runs unverified.
//
// Replies: 200 when the handler returned nil or the delivery was handled
// before; 401 for a missing or wrong signature or a timestamp outside
// the window; 400 for a malformed delivery; 413 past the body limit;
// 409 while the same delivery is being handled on another request; 500
// when the handler failed (the provider retries); 503 without a secret
// or a delivery store.
//
// Delivery ids are recorded in Postgres (the webhook_delivery table,
// created on first use) when the db pack runs, so every node sees them.
// Without it, dev and test keep them in a bounded map in memory; in
// production an endpoint needs the db pack or an explicit Store (Memory
// is per node: a retry reaching another node runs the handler again).
package webhook

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// Defaults.
const (
	// DefaultTolerance is the replay window: a delivery whose signed
	// timestamp is further from now than this is refused.
	DefaultTolerance = 5 * time.Minute
	// DefaultMaxBody bounds the body; raise it with MaxBody for inbound
	// mail with attachments.
	DefaultMaxBody = 1 << 20
	// DefaultTimeout bounds reading the body and running the handler.
	DefaultTimeout = 10 * time.Second
	// DefaultRetention is how long delivery ids are kept; a provider's
	// retries must fall inside it (Stripe retries for three days).
	DefaultRetention = 30 * 24 * time.Hour
	// MaxIDLength bounds a delivery id; a longer one is refused (400).
	MaxIDLength = 255
)

// secretRefresh is how often a secret is read again from the settings.
const secretRefresh = time.Minute

// Handler handles one verified delivery. Returning nil acknowledges it
// (200) and records its id as handled; an error is a 500, the id is
// released and the provider's retry runs the handler again. The handler
// must be quick: long work goes to a job.
type Handler func(ctx context.Context, d *Delivery) error

// Delivery is one verified request.
type Delivery struct {
	// Body is the raw body, verified; decode it with Decode.
	Body []byte
	// ID identifies the delivery for deduplication ("" when the scheme
	// carries none and no ID option is set).
	ID string
	// Timestamp is the signed time of the delivery, zero when the scheme
	// carries none.
	Timestamp time.Time
	// Form holds the fields of a form post (Mailgun's form webhooks and
	// inbound routes; files are left out), nil for other bodies.
	Form url.Values
	// Request is the incoming request; its body has been read.
	Request *http.Request
}

// Decode unmarshals the verified body as JSON into v.
func (d *Delivery) Decode(v any) error { return json.Unmarshal(d.Body, v) }

// Option configures an endpoint.
type Option func(*Endpoint)

// Tolerance sets the replay window for schemes that sign a timestamp
// (Stripe, Mailgun); default DefaultTolerance.
func Tolerance(d time.Duration) Option { return func(e *Endpoint) { e.tolerance = d } }

// MaxBody sets the body limit in bytes; default DefaultMaxBody.
func MaxBody(n int64) Option { return func(e *Endpoint) { e.maxBody = n } }

// Timeout sets the deadline for reading the body and running the
// handler; default DefaultTimeout. The request's own deadline, when
// sooner, still applies.
func Timeout(d time.Duration) Option { return func(e *Endpoint) { e.timeout = d } }

// IDHeader takes the delivery id from a request header ("X-Delivery",
// "X-GitHub-Delivery"); a delivery without it is refused (400).
func IDHeader(name string) Option {
	return func(e *Endpoint) {
		e.idFunc = func(r *http.Request, _ []byte) string { return r.Header.Get(name) }
		e.idRequired = true
	}
}

// ID takes the delivery id from f, called with the verified body; ""
// means the delivery has no id and is not deduplicated.
func ID(f func(r *http.Request, body []byte) string) Option {
	return func(e *Endpoint) { e.idFunc = f; e.idRequired = false }
}

// Prefix is the text before the value in the header of HMAC and Token
// endpoints: "sha256=" (GitHub), "Bearer ". It is removed before the
// comparison and required.
func Prefix(p string) Option { return func(e *Endpoint) { e.prefix = p } }

// Base64 reads an HMAC endpoint's signature as base64 instead of hex.
func Base64() Option { return func(e *Endpoint) { e.base64 = true } }

// WithStore records delivery ids in s instead of the db pack's table.
func WithStore(s Store) Option { return func(e *Endpoint) { e.store = s } }

// Retention sets how long the db pack's table keeps delivery ids;
// default DefaultRetention.
func Retention(d time.Duration) Option { return func(e *Endpoint) { e.retention = d } }

// Endpoint is an http.Handler for one provider's webhook. Build it with
// Stripe, Mailgun, HMAC, Token or TokenField and register it on a route.
type Endpoint struct {
	scheme     scheme
	setting    string
	handler    Handler
	tolerance  time.Duration
	maxBody    int64
	timeout    time.Duration
	idFunc     func(*http.Request, []byte) string
	idRequired bool
	prefix     string
	base64     bool
	retention  time.Duration
	store      Store

	mu          sync.Mutex
	secret      string
	readAt      time.Time
	warnedAt    time.Time // last log line about the missing secret
	storeWarned time.Time // last log line about the missing store
	fallback    Store     // the store found at request time
	pool        *pgxpool.Pool
}

func newEndpoint(s scheme, setting string, h Handler, opts []Option) *Endpoint {
	if setting == "" {
		panic("webhook: the name of the secret's setting is required")
	}
	if h == nil {
		panic("webhook: a handler is required")
	}
	e := &Endpoint{scheme: s, setting: setting, handler: h, tolerance: DefaultTolerance, maxBody: DefaultMaxBody, timeout: DefaultTimeout, retention: DefaultRetention}
	for _, o := range opts {
		o(e)
	}
	if e.tolerance <= 0 {
		e.tolerance = DefaultTolerance
	}
	if e.maxBody <= 0 {
		e.maxBody = DefaultMaxBody
	}
	if e.timeout <= 0 {
		e.timeout = DefaultTimeout
	}
	if e.retention <= 0 {
		e.retention = DefaultRetention
	}
	return e
}

// scope names the endpoint in the delivery store.
func (e *Endpoint) scope() string { return e.scheme.name + ":" + e.setting }

// ServeHTTP verifies the delivery, deduplicates it and runs the handler.
func (e *Endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), e.timeout)
	defer cancel()
	log := lidza.Log(ctx).With("webhook", e.scope())

	secret := e.readSecret(ctx)
	if secret == "" {
		http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
		return
	}
	store := e.deliveryStore(ctx)
	if store == nil {
		http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
		return
	}

	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(e.timeout))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, e.maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			log.Warn("webhook: body over the limit", "limit", e.maxBody)
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "could not read the body", http.StatusBadRequest)
		return
	}

	v, err := e.scheme.verify(e, []byte(secret), r, body)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, errMalformed) {
			status = http.StatusBadRequest
		}
		log.Warn("webhook: delivery refused", "reason", err.Error())
		http.Error(w, http.StatusText(status), status)
		return
	}
	if !v.ts.IsZero() {
		if age := lidza.Now(ctx).Sub(v.ts); age > e.tolerance || age < -e.tolerance {
			log.Warn("webhook: delivery refused", "reason", "timestamp outside the replay window", "age", age.Round(time.Second).String())
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
	}
	id := v.id
	if e.idFunc != nil {
		id = e.idFunc(r, body)
		if id == "" && e.idRequired {
			http.Error(w, "missing delivery id", http.StatusBadRequest)
			return
		}
	}
	if len(id) > MaxIDLength {
		http.Error(w, "delivery id too long", http.StatusBadRequest)
		return
	}

	d := &Delivery{Body: body, ID: id, Timestamp: v.ts, Form: v.form, Request: r}
	if id == "" {
		e.run(ctx, w, log, d, nil)
		return
	}
	claim, err := store.Claim(ctx, e.scope(), id, 2*e.timeout)
	if err != nil {
		log.Error("webhook: delivery store", "err", err)
		http.Error(w, "delivery store unavailable", http.StatusServiceUnavailable)
		return
	}
	switch claim {
	case Duplicate:
		log.Info("webhook: repeated delivery, already handled", "id", id)
		w.WriteHeader(http.StatusOK)
		return
	case Busy:
		http.Error(w, "delivery in progress", http.StatusConflict)
		return
	}
	e.run(ctx, w, log, d, store)
}

// run calls the handler and settles the claim: done on success, released
// on failure so the provider's retry is handled. Both use a fresh short
// deadline, since the request's may be spent.
func (e *Endpoint) run(ctx context.Context, w http.ResponseWriter, log *slog.Logger, d *Delivery, store Store) {
	err := e.call(ctx, d)
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		log.Error("webhook: handler failed", "id", d.ID, "err", err)
		if store != nil {
			if rerr := store.Release(settle, e.scope(), d.ID); rerr != nil {
				log.Error("webhook: release delivery id", "id", d.ID, "err", rerr)
			}
		}
		http.Error(w, "handler failed", http.StatusInternalServerError)
		return
	}
	if store != nil {
		if derr := store.Done(settle, e.scope(), d.ID); derr != nil {
			log.Error("webhook: record delivery id", "id", d.ID, "err", derr)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// call runs the handler, turning a panic into an error so the claim is
// released.
func (e *Endpoint) call(ctx context.Context, d *Delivery) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return e.handler(ctx, d)
}

// readSecret returns the secret, read from the settings at most once per
// secretRefresh. A missing one is logged at most once per refresh.
func (e *Endpoint) readSecret(ctx context.Context) string {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.readAt.IsZero() || now.Sub(e.readAt) >= secretRefresh {
		values, err := env.Values(".")
		if err != nil {
			lidza.Log(ctx).Error("webhook: reading settings", "setting", e.setting, "err", err)
		}
		e.secret = values[e.setting]
		e.readAt = now
	}
	if e.secret == "" && (e.warnedAt.IsZero() || now.Sub(e.warnedAt) >= secretRefresh) {
		e.warnedAt = now
		fix := "lidza credentials set " + env.Mode() + "." + e.setting + "=..."
		if env.Mode() == "test" {
			fix = "set " + e.setting + " in .env.test or with t.Setenv"
		}
		lidza.Log(ctx).Error("webhook: refusing every delivery: "+e.setting+" is not set", "setting", e.setting, "fix", fix)
	}
	return e.secret
}

// deliveryStore is the explicit store, else the db pack's table, else
// (dev and test only) a bounded map in memory.
func (e *Endpoint) deliveryStore(ctx context.Context) Store {
	if e.store != nil {
		return e.store
	}
	pool, _ := lidza.Optional[*pgxpool.Pool](ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if pool != nil {
		if e.pool != pool {
			e.pool, e.fallback = pool, Postgres(pool, e.retention)
		}
		return e.fallback
	}
	if m := env.Mode(); m == "dev" || m == "test" {
		if e.fallback == nil || e.pool != nil {
			e.pool, e.fallback = nil, Memory(10000)
		}
		return e.fallback
	}
	if now := time.Now(); e.storeWarned.IsZero() || now.Sub(e.storeWarned) >= secretRefresh {
		e.storeWarned = now
		lidza.Log(ctx).Error("webhook: refusing every delivery: no delivery store; add the db pack (lidza pack add db) or pass webhook.WithStore", "setting", e.setting)
	}
	return nil
}

// equal compares two secrets in constant time, their lengths included.
func equal(a, b []byte) bool {
	ha, hb := sha256.Sum256(a), sha256.Sum256(b)
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
