// Package audit is the official audit log pack: a durable record of who
// did what to which resource, when, and how it went, for an app's own
// routes and jobs. The actor is the signed-in user (packs/auth), or a
// named system actor for jobs; it never comes from the request. Records
// are written to Postgres (audit_event) synchronously, so a write that
// fails is the caller's error, never a silent loss; RecordTx writes in
// the caller's transaction, beside the change it records. Metadata is a
// short list of strings: a key named like a secret, or a value shaped
// like one (an API key, a token, a database URL with its password),
// is stored as "[redacted]". Nothing is recorded automatically: no
// request body ever reaches the table unless the app puts it there.
package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/secrets"
)

// Config comes from the environment.
type Config struct {
	// Retention drops records older than this, at start and then daily;
	// 0 keeps them all.
	Retention time.Duration `env:"AUDIT_RETENTION" default:"8760h"`
}

// Outcomes of an action.
const (
	OK     = "ok"
	Denied = "denied"
	Failed = "failed"
)

// Bounds of what one record holds.
const (
	MaxMeta      = 20
	MaxMetaValue = 500
	maxField     = 200
)

// Redacted replaces a secret in the metadata.
const Redacted = "[redacted]"

// Event is what the app records.
type Event struct {
	// Action names what was done: "deploy.run", "member.grant". Lower
	// case letters, digits and . _ : - only.
	Action string
	// Resource is what it was done to: "app/42", "team/a/member/u7".
	Resource string
	// Scope is the team or workspace it happened in; "" for the app.
	Scope string
	// Outcome is OK (the default), Denied or Failed.
	Outcome string
	// Meta is up to MaxMeta short values the app chose (a version, a
	// role), never a request body.
	Meta map[string]string
}

// Record is a stored event.
type Record struct {
	ID        string            `json:"id"`
	At        time.Time         `json:"at"`
	Actor     string            `json:"actor"`
	Action    string            `json:"action"`
	Resource  string            `json:"resource,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	Outcome   string            `json:"outcome"`
	RequestID string            `json:"requestId,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// ErrNoActor is Record's error without a signed-in user or a System
// actor in the context.
var ErrNoActor = errors.New("audit: no actor: record under auth.Require, or in a context from audit.System")

// Audit is the running pack.
type Audit struct {
	cfg  Config
	pool *pgxpool.Pool
	log  *slog.Logger
	stop context.CancelFunc
	wg   sync.WaitGroup
}

// Pack returns the pack for packs.go; list lidza/db (and lidza/auth for
// user actors) before it.
func Pack() lidza.Pack { return &Audit{log: slog.Default()} }

// New builds the pack outside the lifecycle (tests).
func New(cfg Config, pool *pgxpool.Pool) *Audit {
	return &Audit{cfg: cfg, pool: pool, log: slog.Default()}
}

// From returns the pack from a handler's or job's context.
func From(ctx context.Context) *Audit { return lidza.Service[*Audit](ctx) }

// Name implements lidza.Pack.
func (a *Audit) Name() string { return "lidza/audit" }

// Start reads the configuration, takes the pool, prunes and schedules
// the daily prune.
func (a *Audit) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &a.cfg); err != nil {
		return err
	}
	pool, ok := s.Lookup(typeOf[*pgxpool.Pool]())
	if !ok {
		return errors.New("audit needs the db pack: list \"lidza/db\" before \"lidza/audit\" in lidza.json")
	}
	a.pool = pool.(*pgxpool.Pool)
	if a.cfg.Retention > 0 {
		if _, err := a.Prune(ctx); err != nil {
			a.log.Warn("audit: prune failed", "err", err)
		}
		pctx, cancel := context.WithCancel(context.Background())
		a.stop = cancel
		a.wg.Add(1)
		go a.pruneDaily(pctx)
	}
	lidza.Provide(s, a)
	return nil
}

// Stop ends the daily prune.
func (a *Audit) Stop(context.Context) error {
	if a.stop != nil {
		a.stop()
		a.wg.Wait()
	}
	return nil
}

func (a *Audit) pruneDaily(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := a.Prune(ctx); err != nil && ctx.Err() == nil {
				a.log.Warn("audit: prune failed", "err", err)
			}
		}
	}
}

// Prune deletes the records older than the retention, 10000 at a time,
// and returns how many.
func (a *Audit) Prune(ctx context.Context) (int64, error) {
	if a.cfg.Retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-a.cfg.Retention)
	var total int64
	for {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		tag, err := a.pool.Exec(ctx, `DELETE FROM audit_event WHERE id IN (SELECT id FROM audit_event WHERE at < $1 LIMIT 10000)`, cutoff)
		cancel()
		if err != nil {
			return total, fmt.Errorf("audit: prune: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 10000 {
			return total, nil
		}
	}
}

type systemKey struct{}

// System returns a context whose records name the actor "system:<name>":
// a job, a scheduled task, a startup step. A signed-in user in the
// context still wins.
func System(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, systemKey{}, "system:"+name)
}

// actor is who acts in ctx.
func actor(ctx context.Context) (string, error) {
	if u := auth.CurrentUser(ctx); u != nil && u.ID != "" {
		return u.ID, nil
	}
	if s, ok := ctx.Value(systemKey{}).(string); ok && s != "system:" {
		return s, nil
	}
	return "", ErrNoActor
}

// Record stores an event now; its error is the caller's to handle (an
// action that must be audited fails with it).
func (a *Audit) Record(ctx context.Context, e Event) error {
	return a.insert(ctx, a.pool, e)
}

// RecordTx stores an event in the caller's transaction: it commits or
// rolls back with the change it records.
func (a *Audit) RecordTx(ctx context.Context, tx pgx.Tx, e Event) error {
	if tx == nil {
		return errors.New("audit: RecordTx requires a transaction")
	}
	return a.insert(ctx, tx, e)
}

func (a *Audit) insert(ctx context.Context, db interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, e Event) error {
	who, err := actor(ctx)
	if err != nil {
		return err
	}
	if err := clean(&e); err != nil {
		return err
	}
	meta, err := json.Marshal(e.Meta)
	if err != nil {
		return fmt.Errorf("audit: meta: %w", err)
	}
	// clock_timestamp, not the transaction's start: records written in
	// one transaction keep their order.
	var id string
	err = db.QueryRow(ctx, `INSERT INTO audit_event (at, actor, action, resource, scope, outcome, request_id, meta) VALUES (clock_timestamp(), $1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		who, e.Action, e.Resource, e.Scope, e.Outcome, middleware.GetRequestID(ctx), meta).Scan(&id)
	if err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

var (
	actionRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,99}$`)
	metaKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	// secretKey names a key whose value is a secret whatever it holds.
	secretKey = regexp.MustCompile(`(?i)pass|secret|token|key|otp|code|signature|credential|authorization|cookie|session|dsn|private`)
)

// clean validates the event and redacts its secrets.
func clean(e *Event) error {
	if !actionRe.MatchString(e.Action) {
		return fmt.Errorf("audit: action %q: lower case letters, digits and . _ : - only, at most 100", e.Action)
	}
	if len(e.Resource) > maxField || len(e.Scope) > maxField {
		return fmt.Errorf("audit: resource and scope are at most %d bytes", maxField)
	}
	if secrets.Kind(e.Resource) != "" {
		e.Resource = Redacted
	}
	switch e.Outcome {
	case "":
		e.Outcome = OK
	case OK, Denied, Failed:
	default:
		return fmt.Errorf("audit: outcome %q: ok, denied or failed", e.Outcome)
	}
	if len(e.Meta) > MaxMeta {
		return fmt.Errorf("audit: at most %d metadata values", MaxMeta)
	}
	meta := make(map[string]string, len(e.Meta))
	for k, v := range e.Meta {
		if !metaKeyRe.MatchString(k) {
			return fmt.Errorf("audit: metadata key %q: letters, digits and _ . - only, at most 64", k)
		}
		switch {
		case secretKey.MatchString(k) || secrets.Kind(v) != "":
			v = Redacted
		case len(v) > MaxMetaValue:
			v = v[:MaxMetaValue] + "…"
		}
		meta[k] = v
	}
	e.Meta = meta
	return nil
}

// Query selects records; empty fields match all.
type Query struct {
	Actor, Action, Resource, Scope, Outcome string
	// Since and Until bound the time, Until exclusive.
	Since, Until time.Time
	// Cursor is the previous page's Next.
	Cursor string
	// Limit is at most 200; 50 when 0.
	Limit int
}

// Page is a page of records, newest first.
type Page struct {
	Records []Record `json:"records"`
	// Next is the cursor of the following page, "" at the end.
	Next string `json:"next,omitempty"`
}

// List returns the records that match, newest first, a page at a time.
func (a *Audit) List(ctx context.Context, q Query) (Page, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	for _, f := range []struct{ col, v string }{{"actor", q.Actor}, {"action", q.Action}, {"resource", q.Resource}, {"scope", q.Scope}, {"outcome", q.Outcome}} {
		if f.v != "" {
			add(f.col+" = ?", f.v)
		}
	}
	if !q.Since.IsZero() {
		add("at >= ?", q.Since)
	}
	if !q.Until.IsZero() {
		add("at < ?", q.Until)
	}
	if q.Cursor != "" {
		at, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return Page{}, err
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	sql := `SELECT id, at, actor, action, resource, scope, outcome, request_id, meta FROM audit_event`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, q.Limit+1)
	sql += " ORDER BY at DESC, id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := a.pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, fmt.Errorf("audit: list: %w", err)
	}
	recs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Record, error) {
		var r Record
		var meta []byte
		if err := row.Scan(&r.ID, &r.At, &r.Actor, &r.Action, &r.Resource, &r.Scope, &r.Outcome, &r.RequestID, &meta); err != nil {
			return r, err
		}
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &r.Meta); err != nil {
				return r, err
			}
		}
		return r, nil
	})
	if err != nil {
		return Page{}, fmt.Errorf("audit: list: %w", err)
	}
	p := Page{Records: recs}
	if p.Records == nil {
		p.Records = []Record{}
	}
	if len(recs) > q.Limit {
		p.Records = recs[:q.Limit]
		last := p.Records[q.Limit-1]
		p.Next = encodeCursor(last.At, last.ID)
	}
	return p, nil
}

// The cursor is the last record's time and id, opaque to clients.
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + " " + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err == nil {
		if ts, id, ok := strings.Cut(string(b), " "); ok {
			if at, terr := time.Parse(time.RFC3339Nano, ts); terr == nil && id != "" {
				return at, id, nil
			}
		}
	}
	return time.Time{}, "", errors.New("audit: invalid cursor")
}
