package billing

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/webhook"
)

// Options configure the billing routes.
type Options struct {
	// Owner names who is billed for the request: by default the
	// request's workspace (auth.WorkspaceID, behind
	// auth.RequireWorkspace), else the signed-in user.
	Owner func(ctx context.Context) string
	// Guard protects the account routes: auth.Require() by default; a
	// workspace app passes auth.RequireWorkspace() or a permission
	// (roles.Require("billing.manage", ...)). The webhook route is
	// Stripe's and checks Stripe's signature instead.
	Guard func(http.Handler) http.Handler
	// SuccessPath, CancelPath and ReturnPath are the app's pages Checkout
	// and the portal send the customer back to: /billing?checkout=success,
	// /billing and /billing when empty.
	SuccessPath, CancelPath, ReturnPath string
}

// Account is what the billing route reports to the owner.
type Account struct {
	Plan              string     `json:"plan"`
	Status            string     `json:"status,omitempty"`
	Features          []string   `json:"features"`
	CurrentPeriodEnd  *time.Time `json:"currentPeriodEnd,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancelAtPeriodEnd"`
	Plans             []PlanInfo `json:"plans"`
}

// PlanInfo is a plan as customers choose it.
type PlanInfo struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Features []string `json:"features"`
}

// CheckoutInput chooses a plan.
type CheckoutInput struct {
	Plan string `json:"plan"`
}

// Redirect is where to send the customer.
type Redirect struct {
	URL string `json:"url"`
}

type routes struct {
	plans Plans
	opt   Options
}

func (rt routes) owner(ctx context.Context) string {
	if rt.opt.Owner != nil {
		return rt.opt.Owner(ctx)
	}
	if ws := auth.WorkspaceID(ctx); ws != "" {
		return ws
	}
	if u := auth.CurrentUser(ctx); u != nil {
		return u.ID
	}
	return ""
}

// Mount registers the billing routes:
//
//	GET  /api/v1/billing           the owner's plan, status, features and the plans to choose from
//	POST /api/v1/billing/checkout  a Stripe Checkout URL subscribing to a plan
//	POST /api/v1/billing/portal    a Stripe customer portal URL
//	POST /api/v1/billing/webhook   Stripe's events, signed with STRIPE_WEBHOOK_SECRET
//
// In Stripe, point a webhook endpoint at /api/v1/billing/webhook with the
// events checkout.session.completed and customer.subscription.created,
// .updated and .deleted, and save its signing secret as
// STRIPE_WEBHOOK_SECRET.
func (p Plans) Mount(r *router.Router, opt Options) {
	guard := opt.Guard
	if guard == nil {
		guard = auth.Require()
	}
	if opt.SuccessPath == "" {
		opt.SuccessPath = "/billing?checkout=success"
	}
	if opt.CancelPath == "" {
		opt.CancelPath = "/billing"
	}
	if opt.ReturnPath == "" {
		opt.ReturnPath = "/billing"
	}
	rt := routes{plans: p, opt: opt}
	router.Route(r, "GET /api/v1/billing", rt.billingAccount, guard)
	router.Route(r, "POST /api/v1/billing/checkout", rt.billingCheckout, guard)
	router.Route(r, "POST /api/v1/billing/portal", rt.billingPortal, guard)
	r.Handle("POST /api/v1/billing/webhook", webhook.Stripe(WebhookSecretSetting, rt.event))
}

func (rt routes) billingAccount(ctx context.Context, _ *router.Request[router.None]) (Account, error) {
	key, sub, err := rt.plans.Of(ctx, rt.owner(ctx))
	if err != nil {
		return Account{}, err
	}
	a := Account{Plan: key, Features: rt.plans[key].Features}
	if a.Features == nil {
		a.Features = []string{}
	}
	if sub != nil {
		a.Status, a.CurrentPeriodEnd, a.CancelAtPeriodEnd = sub.Status, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd
	}
	for k, pl := range rt.plans {
		if pl.Price == "" {
			continue
		}
		f := pl.Features
		if f == nil {
			f = []string{}
		}
		a.Plans = append(a.Plans, PlanInfo{Key: k, Name: pl.Name, Features: f})
	}
	sort.Slice(a.Plans, func(i, j int) bool { return a.Plans[i].Key < a.Plans[j].Key })
	if a.Plans == nil {
		a.Plans = []PlanInfo{}
	}
	return a, nil
}

func (rt routes) billingCheckout(ctx context.Context, req *router.Request[CheckoutInput]) (Redirect, error) {
	email := ""
	if u := auth.CurrentUser(ctx); u != nil {
		email, _ = u.Claims["email"].(string)
	}
	u, err := rt.plans.Checkout(ctx, rt.owner(ctx), req.Body.Plan, email, rt.opt.SuccessPath, rt.opt.CancelPath)
	return Redirect{URL: u}, err
}

func (rt routes) billingPortal(ctx context.Context, _ *router.Request[router.None]) (Redirect, error) {
	u, err := From(ctx).Portal(ctx, rt.owner(ctx), rt.opt.ReturnPath)
	return Redirect{URL: u}, err
}

// event applies a verified Stripe event; an error makes Stripe retry.
func (rt routes) event(ctx context.Context, d *webhook.Delivery) error {
	ev, err := d.StripeEvent()
	if err != nil {
		return err
	}
	return From(ctx).Event(ctx, ev.Type, time.Unix(ev.Created, 0).UTC(), ev.Data.Object)
}
