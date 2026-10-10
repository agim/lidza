// Package billing is the official payments pack, on Stripe: an owner (a
// workspace, a user) subscribes to one of the app's plans through Stripe
// Checkout, manages it in Stripe's customer portal, and the app checks
// what the plan grants with Has. Subscriptions reach the app through
// Stripe's signed webhooks (pkg/webhook.Stripe) and are kept in
// billing_subscription, so a check is one query, never a call to Stripe.
// The plans are the app's, in code, like auth.Roles:
//
//	var plans = billing.Plans{
//		"pro":  {Name: "Pro", Price: "price_123", Features: []string{"exports", "seats:10"}},
//		"team": {Name: "Team", Price: "price_456", Features: []string{"exports", "seats:50", "sso"}},
//	}
//	plans.Mount(r, billing.Options{}) // routes.go
//	ok, err := plans.Has(ctx, auth.WorkspaceID(ctx), "exports")
//
// Stripe's API is called over HTTP with the secret key; no SDK. Test mode
// is Stripe's own: a sk_test_ key and test cards.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/router"
)

// Config comes from the environment and the credentials.
type Config struct {
	// SecretKey is the Stripe secret key: sk_test_... in development,
	// sk_live_... in production (in the credentials).
	SecretKey string `env:"STRIPE_SECRET_KEY"`
	// APIURL is Stripe's API; tests point it at a fake.
	APIURL string `env:"STRIPE_API_URL" default:"https://api.stripe.com"`
	// AppURL is the app's public origin, where Checkout and the portal
	// send the customer back.
	AppURL string `env:"APP_URL"`
}

// WebhookSecretSetting names the setting holding the endpoint's signing
// secret (whsec_...), which webhook.Stripe reads.
const WebhookSecretSetting = "STRIPE_WEBHOOK_SECRET"

// Plan is one of the app's plans.
type Plan struct {
	// Name is shown to customers.
	Name string `json:"name"`
	// Price is the Stripe price (price_...) the plan subscribes to.
	Price string `json:"-"`
	// Features are what the plan grants, the names Has checks: "exports",
	// "seats:10", whatever the app means by them.
	Features []string `json:"features"`
}

// Plans are the app's plans by key ("pro"). Free, when set, holds the
// features of an owner without a subscription.
type Plans map[string]Plan

// FreePlan is the key of the plan an owner without a subscription is on;
// its Price is empty.
const FreePlan = "free"

// Statuses that grant a plan's features: Stripe's active and trialing,
// and past_due while Stripe retries the payment.
var granting = []string{"active", "trialing", "past_due"}

// CustomerTable and SubscriptionTable are the DDL (models BillingCustomer
// and BillingSubscription in the schema fragment). Tests create them.
const (
	CustomerTable = `CREATE TABLE IF NOT EXISTS billing_customer (
  owner text PRIMARY KEY,
  customer text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);`
	SubscriptionTable = `CREATE TABLE IF NOT EXISTS billing_subscription (
  id text PRIMARY KEY,
  owner text NOT NULL,
  customer text NOT NULL,
  price text NOT NULL,
  status text NOT NULL,
  current_period_end timestamptz,
  cancel_at_period_end boolean NOT NULL DEFAULT false,
  event_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS billing_subscription_owner_idx ON billing_subscription (owner);`
)

// Billing is the running pack.
type Billing struct {
	mu     sync.RWMutex
	cfg    Config
	pool   *pgxpool.Pool
	client *http.Client
	log    *slog.Logger
}

// Pack returns the pack for packs.go; it needs lidza/db started first.
func Pack() lidza.Pack { return &Billing{log: slog.Default()} }

// New builds the pack outside the lifecycle (tests).
func New(cfg Config, pool *pgxpool.Pool) *Billing {
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.stripe.com"
	}
	return &Billing{cfg: cfg, pool: pool, client: &http.Client{Timeout: 20 * time.Second}, log: slog.Default()}
}

// From returns the pack from a request context.
func From(ctx context.Context) *Billing { return lidza.Service[*Billing](ctx) }

// Name implements lidza.Pack.
func (b *Billing) Name() string { return "lidza/billing" }

// Start reads the configuration and takes the pool.
func (b *Billing) Start(ctx context.Context, s *lidza.Services) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	pool, ok := s.Lookup(reflect.TypeFor[*pgxpool.Pool]())
	if !ok {
		return errors.New("billing needs the db pack: list \"lidza/db\" before \"lidza/billing\" in lidza.json")
	}
	built := New(cfg, pool.(*pgxpool.Pool))
	b.cfg, b.pool, b.client = built.cfg, built.pool, built.client
	lidza.Provide(s, b)
	return nil
}

// Reconfigure reads the keys again (after the admin pages save them).
func (b *Billing) Reconfigure(ctx context.Context) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	b.mu.Lock()
	b.cfg = cfg
	b.mu.Unlock()
	return nil
}

// conf is the configuration now (Reconfigure replaces it).
func (b *Billing) conf() Config {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cfg
}

// Stop implements lidza.Pack.
func (b *Billing) Stop(context.Context) error { return nil }

// Subscription is an owner's subscription as the app keeps it.
type Subscription struct {
	ID                string     `json:"id"`
	Owner             string     `json:"owner"`
	Customer          string     `json:"customer"`
	Price             string     `json:"price"`
	Status            string     `json:"status"`
	CurrentPeriodEnd  *time.Time `json:"currentPeriodEnd,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancelAtPeriodEnd"`
}

// Current is an owner's subscription that grants its plan (active,
// trialing or past due), the latest; nil without one.
func (b *Billing) Current(ctx context.Context, owner string) (*Subscription, error) {
	var s Subscription
	err := b.pool.QueryRow(ctx, `SELECT id, owner, customer, price, status, current_period_end, cancel_at_period_end FROM billing_subscription
		WHERE owner = $1 AND status = ANY($2) ORDER BY event_at DESC LIMIT 1`, owner, granting).
		Scan(&s.ID, &s.Owner, &s.Customer, &s.Price, &s.Status, &s.CurrentPeriodEnd, &s.CancelAtPeriodEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Of returns the plan key an owner is on and its subscription: FreePlan
// (and nil) without one granting, "" when the subscription's price is not
// one of the plans (a price retired from the code).
func (p Plans) Of(ctx context.Context, owner string) (string, *Subscription, error) {
	s, err := From(ctx).Current(ctx, owner)
	if err != nil || s == nil {
		return FreePlan, nil, err
	}
	for key, plan := range p {
		if plan.Price != "" && plan.Price == s.Price {
			return key, s, nil
		}
	}
	return "", s, nil
}

// Has reports whether owner's plan grants feature. An error (the
// database down) is never a yes.
func (p Plans) Has(ctx context.Context, owner, feature string) (bool, error) {
	key, _, err := p.Of(ctx, owner)
	if err != nil {
		return false, err
	}
	return slices.Contains(p[key].Features, feature), nil
}

// ErrNoFeature is Require's 402: the owner's plan does not grant it.
var ErrNoFeature = router.ErrorCode(http.StatusPaymentRequired, "upgrade", "your plan does not include this")

// Require is Has as an error for a handler: nil, ErrNoFeature (402) or
// the database's error.
func (p Plans) Require(ctx context.Context, owner, feature string) error {
	ok, err := p.Has(ctx, owner, feature)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoFeature
	}
	return nil
}

// stripe calls Stripe's API: a form POST (or a GET without form) with
// the secret key; the reply decoded into out.
func (b *Billing) stripe(ctx context.Context, method, path string, form url.Values, out any) error {
	cfg := b.conf()
	if cfg.SecretKey == "" {
		return errors.New("billing: STRIPE_SECRET_KEY is not set (lidza credentials set STRIPE_SECRET_KEY=sk_test_...)")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.APIURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.SecretKey)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("billing: stripe: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(data, &e)
		return fmt.Errorf("billing: stripe %s %s: %d %s", method, path, res.StatusCode, e.Error.Message)
	}
	return json.Unmarshal(data, out)
}

// customer is owner's Stripe customer id; "" before its first checkout.
func (b *Billing) customer(ctx context.Context, owner string) (string, error) {
	var c string
	err := b.pool.QueryRow(ctx, `SELECT customer FROM billing_customer WHERE owner = $1`, owner).Scan(&c)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return c, err
}

// back is a return address on the app's origin: path only, so nobody
// can make Checkout send a customer elsewhere.
func (b *Billing) back(path string) (string, error) {
	appURL := b.conf().AppURL
	if appURL == "" {
		return "", errors.New("billing: APP_URL is not set: Checkout needs the address to send the customer back to")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") {
		return "", fmt.Errorf("billing: %q: a path on the app", path)
	}
	return strings.TrimRight(appURL, "/") + path, nil
}

// Checkout starts a Stripe Checkout session subscribing owner to plan
// and returns its URL to send the customer to. successPath and cancelPath
// are paths on the app. email prefills a new customer's address.
func (p Plans) Checkout(ctx context.Context, owner, plan, email, successPath, cancelPath string) (string, error) {
	b := From(ctx)
	pl, ok := p[plan]
	if !ok || pl.Price == "" {
		return "", router.ErrorCode(http.StatusUnprocessableEntity, "plan", "no such plan")
	}
	success, err := b.back(successPath)
	if err != nil {
		return "", err
	}
	cancel, err := b.back(cancelPath)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"mode":                    {"subscription"},
		"line_items[0][price]":    {pl.Price},
		"line_items[0][quantity]": {"1"},
		"success_url":             {success},
		"cancel_url":              {cancel},
		"client_reference_id":     {owner},
		"metadata[lidza_owner]":   {owner},
		"subscription_data[metadata][lidza_owner]": {owner},
	}
	cus, err := b.customer(ctx, owner)
	if err != nil {
		return "", err
	}
	if cus != "" {
		form.Set("customer", cus)
	} else if email != "" {
		form.Set("customer_email", email)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := b.stripe(ctx, http.MethodPost, "/v1/checkout/sessions", form, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// ErrNoCustomer is Portal's 409 for an owner that never checked out.
var ErrNoCustomer = router.ErrorCode(http.StatusConflict, "no_customer", "subscribe first: there is no billing account yet")

// Portal opens Stripe's customer portal for owner (change plan, payment
// method, invoices, cancel) and returns its URL; returnPath is where it
// sends the customer back.
func (b *Billing) Portal(ctx context.Context, owner, returnPath string) (string, error) {
	cus, err := b.customer(ctx, owner)
	if err != nil {
		return "", err
	}
	if cus == "" {
		return "", ErrNoCustomer
	}
	ret, err := b.back(returnPath)
	if err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := b.stripe(ctx, http.MethodPost, "/v1/billing_portal/sessions", url.Values{"customer": {cus}, "return_url": {ret}}, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// stripeSubscription is the part of Stripe's subscription object the
// pack keeps. The billing period is on the item in current API versions,
// on the subscription in older ones.
type stripeSubscription struct {
	ID                string            `json:"id"`
	Customer          string            `json:"customer"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	CurrentPeriodEnd  int64             `json:"current_period_end"`
	Metadata          map[string]string `json:"metadata"`
	Items             struct {
		Data []struct {
			CurrentPeriodEnd int64 `json:"current_period_end"`
			Price            struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

// sync records a subscription as of an event at eventAt; an older event
// than the one recorded changes nothing (Stripe does not order them).
func (b *Billing) sync(ctx context.Context, s stripeSubscription, eventAt time.Time) error {
	owner := s.Metadata["lidza_owner"]
	if owner == "" {
		if err := b.pool.QueryRow(ctx, `SELECT owner FROM billing_customer WHERE customer = $1`, s.Customer).Scan(&owner); err != nil {
			b.log.Warn("billing: a subscription of a customer the app does not know", "subscription", s.ID, "customer", s.Customer)
			return nil
		}
	}
	price := ""
	var end int64 = s.CurrentPeriodEnd
	if len(s.Items.Data) > 0 {
		price = s.Items.Data[0].Price.ID
		if s.Items.Data[0].CurrentPeriodEnd != 0 {
			end = s.Items.Data[0].CurrentPeriodEnd
		}
	}
	var periodEnd *time.Time
	if end != 0 {
		t := time.Unix(end, 0).UTC()
		periodEnd = &t
	}
	if _, err := b.pool.Exec(ctx, `INSERT INTO billing_customer (owner, customer) VALUES ($1, $2) ON CONFLICT (owner) DO UPDATE SET customer = EXCLUDED.customer`, owner, s.Customer); err != nil {
		return err
	}
	_, err := b.pool.Exec(ctx, `INSERT INTO billing_subscription (id, owner, customer, price, status, current_period_end, cancel_at_period_end, event_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET price = EXCLUDED.price, status = EXCLUDED.status, current_period_end = EXCLUDED.current_period_end,
		  cancel_at_period_end = EXCLUDED.cancel_at_period_end, event_at = EXCLUDED.event_at, updated_at = now()
		WHERE billing_subscription.event_at <= EXCLUDED.event_at`,
		s.ID, owner, s.Customer, price, s.Status, periodEnd, s.CancelAtPeriodEnd, eventAt)
	return err
}

// Event applies one verified Stripe event: a completed checkout links
// the customer to its owner and records the subscription; a
// subscription created, updated or deleted is recorded as it now is.
// Other events are ignored.
func (b *Billing) Event(ctx context.Context, typ string, created time.Time, object json.RawMessage) error {
	switch typ {
	case "checkout.session.completed":
		var cs struct {
			Mode              string            `json:"mode"`
			Customer          string            `json:"customer"`
			ClientReferenceID string            `json:"client_reference_id"`
			Subscription      string            `json:"subscription"`
			Metadata          map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(object, &cs); err != nil {
			return err
		}
		owner := cs.Metadata["lidza_owner"]
		if owner == "" {
			owner = cs.ClientReferenceID
		}
		if cs.Mode != "subscription" || owner == "" || cs.Customer == "" {
			return nil
		}
		if _, err := b.pool.Exec(ctx, `INSERT INTO billing_customer (owner, customer) VALUES ($1, $2) ON CONFLICT (owner) DO UPDATE SET customer = EXCLUDED.customer`, owner, cs.Customer); err != nil {
			return err
		}
		if cs.Subscription == "" {
			return nil
		}
		var s stripeSubscription
		if err := b.stripe(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(cs.Subscription), nil, &s); err != nil {
			return err
		}
		if s.Metadata == nil {
			s.Metadata = map[string]string{}
		}
		if s.Metadata["lidza_owner"] == "" {
			s.Metadata["lidza_owner"] = owner
		}
		return b.sync(ctx, s, created)
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted",
		"customer.subscription.paused", "customer.subscription.resumed":
		var s stripeSubscription
		if err := json.Unmarshal(object, &s); err != nil {
			return err
		}
		return b.sync(ctx, s, created)
	}
	return nil
}

// Subscriptions lists the subscriptions the app keeps, newest event
// first, across owners (status narrows them; "" for all): what the
// admin pages and the agent tools show.
func (b *Billing) Subscriptions(ctx context.Context, status string, limit int) ([]Subscription, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := b.pool.Query(ctx, `SELECT id, owner, customer, price, status, current_period_end, cancel_at_period_end FROM billing_subscription
		WHERE $1 = '' OR status = $1 ORDER BY event_at DESC, id LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Subscription, error) {
		var s Subscription
		return s, r.Scan(&s.ID, &s.Owner, &s.Customer, &s.Price, &s.Status, &s.CurrentPeriodEnd, &s.CancelAtPeriodEnd)
	})
	if out == nil {
		out = []Subscription{}
	}
	return out, err
}
