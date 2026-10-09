package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/webhook"
)

const whsec = "whsec_billing_test"

var plans = Plans{
	FreePlan: {Name: "Free", Features: []string{"notes"}},
	"pro":    {Name: "Pro", Price: "price_pro", Features: []string{"notes", "exports"}},
	"team":   {Name: "Team", Price: "price_team", Features: []string{"notes", "exports", "sso"}},
}

// fakeStripe answers the three calls the pack makes, checking the key.
type fakeStripe struct {
	srv      *httptest.Server
	mu       sync.Mutex
	checkout url.Values
	sub      map[string]any
}

func newFakeStripe(t *testing.T) *fakeStripe {
	f := &fakeStripe{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_key" {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/checkout/sessions":
			f.checkout = r.PostForm
			fmt.Fprint(w, `{"id":"cs_1","url":"https://checkout.stripe.test/c/cs_1"}`)
		case r.Method == "POST" && r.URL.Path == "/v1/billing_portal/sessions":
			fmt.Fprintf(w, `{"url":"https://billing.stripe.test/p/%s"}`, r.PostForm.Get("customer"))
		case r.Method == "GET" && r.URL.Path == "/v1/subscriptions/sub_1":
			json.NewEncoder(w).Encode(f.sub)
		default:
			http.Error(w, `{"error":{"message":"no such call"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func subscription(status string, cancel bool, price string) map[string]any {
	return map[string]any{
		"id": "sub_1", "customer": "cus_1", "status": status, "cancel_at_period_end": cancel,
		"metadata": map[string]string{"lidza_owner": "ws1"},
		"items":    map[string]any{"data": []any{map[string]any{"current_period_end": 1893456000, "price": map[string]string{"id": price}}}},
	}
}

func TestBilling(t *testing.T) {
	dbURL := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: dbURL, MaxConns: 4, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS billing_subscription; DROP TABLE IF EXISTS billing_customer`)
	if _, err := pool.Exec(ctx, CustomerTable+SubscriptionTable); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIDZA_MODE", "test")
	t.Setenv(WebhookSecretSetting, whsec)
	stripe := newFakeStripe(t)
	b := New(Config{SecretKey: "sk_test_key", APIURL: stripe.srv.URL, AppURL: "https://app.example.com"}, pool)

	s := lidza.NewServices()
	lidza.Provide(s, b)
	user := &auth.User{ID: "u1", Claims: map[string]any{"email": "ann@example.com"}}
	r := router.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(lidza.WithServices(req.Context(), s)))
		})
	})
	asMember := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), user)))
		})
	}
	plans.Mount(r, Options{Guard: asMember, Owner: func(context.Context) string { return "ws1" }})
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			j, _ := json.Marshal(body)
			rd = strings.NewReader(string(j))
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	event := func(typ string, created time.Time, object any) int {
		t.Helper()
		obj, _ := json.Marshal(object)
		body := fmt.Sprintf(`{"id":"evt_%d_%s","type":%q,"created":%d,"data":{"object":%s}}`, created.UnixNano(), typ, typ, created.Unix(), obj)
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/billing/webhook", strings.NewReader(body))
		req.Header.Set("Stripe-Signature", webhook.StripeSignature(whsec, time.Now(), []byte(body)))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	has := func(feature string) bool {
		t.Helper()
		ok, err := plans.Has(lidza.WithServices(ctx, s), "ws1", feature)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	// Before anything: the free plan.
	if code, acc := call("GET", "/api/v1/billing", nil); code != 200 || acc["plan"] != FreePlan || len(acc["plans"].([]any)) != 2 {
		t.Fatalf("account: %d %v", code, acc)
	}
	if has("exports") || !has("notes") {
		t.Fatal("free plan features")
	}
	if code, out := call("POST", "/api/v1/billing/portal", nil); code != 409 {
		t.Fatalf("portal without a customer: %d %v", code, out)
	}
	if code, _ := call("POST", "/api/v1/billing/checkout", map[string]string{"plan": "gold"}); code != 422 {
		t.Fatalf("unknown plan: %d", code)
	}

	// Checkout: Stripe gets the price, the owner and the app's return pages.
	code, out := call("POST", "/api/v1/billing/checkout", map[string]string{"plan": "pro"})
	if code != 200 || out["url"] != "https://checkout.stripe.test/c/cs_1" {
		t.Fatalf("checkout: %d %v", code, out)
	}
	f := stripe.checkout
	if f.Get("line_items[0][price]") != "price_pro" || f.Get("client_reference_id") != "ws1" || f.Get("subscription_data[metadata][lidza_owner]") != "ws1" ||
		f.Get("success_url") != "https://app.example.com/billing?checkout=success" || f.Get("customer_email") != "ann@example.com" || f.Get("mode") != "subscription" {
		t.Fatalf("checkout form: %v", f)
	}

	// The signed event completes it: the subscription is fetched and kept.
	stripe.sub = subscription("active", false, "price_pro")
	now := time.Now()
	if code := event("checkout.session.completed", now, map[string]any{"mode": "subscription", "customer": "cus_1", "client_reference_id": "ws1", "subscription": "sub_1"}); code != 200 {
		t.Fatalf("checkout event: %d", code)
	}
	if !has("exports") || has("sso") {
		t.Fatal("pro features")
	}
	if code, acc := call("GET", "/api/v1/billing", nil); acc["plan"] != "pro" || acc["status"] != "active" || acc["currentPeriodEnd"] == nil {
		t.Fatalf("account after checkout: %d %v", code, acc)
	}

	// An older event changes nothing; a newer upgrade applies.
	event("customer.subscription.updated", now.Add(-time.Hour), subscription("canceled", false, "price_pro"))
	if !has("exports") {
		t.Fatal("an older event applied")
	}
	event("customer.subscription.updated", now.Add(time.Minute), subscription("active", true, "price_team"))
	if !has("sso") {
		t.Fatal("upgrade not applied")
	}
	if _, acc := call("GET", "/api/v1/billing", nil); acc["plan"] != "team" || acc["cancelAtPeriodEnd"] != true {
		t.Fatalf("after upgrade: %v", acc)
	}
	// The portal for the known customer.
	if code, out := call("POST", "/api/v1/billing/portal", nil); code != 200 || out["url"] != "https://billing.stripe.test/p/cus_1" {
		t.Fatalf("portal: %d %v", code, out)
	}
	// Past due still grants; deleted does not.
	event("customer.subscription.updated", now.Add(2*time.Minute), subscription("past_due", false, "price_team"))
	if !has("sso") {
		t.Fatal("past due lost the plan")
	}
	event("customer.subscription.deleted", now.Add(3*time.Minute), subscription("canceled", false, "price_team"))
	if has("exports") {
		t.Fatal("a canceled subscription still grants")
	}
	if err := plans.Require(lidza.WithServices(ctx, s), "ws1", "exports"); err != ErrNoFeature {
		t.Fatalf("Require: %v", err)
	}

	// An unsigned or wrongly signed event is refused.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/billing/webhook", strings.NewReader(`{"id":"evt_x","type":"customer.subscription.updated","created":1,"data":{"object":{}}}`))
	req.Header.Set("Stripe-Signature", webhook.StripeSignature("whsec_other", time.Now(), []byte("{}")))
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("forged event: %d", res.StatusCode)
	}
}

// TestBackPaths: Checkout and the portal return to paths on the app,
// never to another host.
func TestBackPaths(t *testing.T) {
	b := New(Config{AppURL: "https://app.example.com/"}, nil)
	if u, err := b.back("/billing?x=1"); err != nil || u != "https://app.example.com/billing?x=1" {
		t.Fatalf("%q %v", u, err)
	}
	for _, bad := range []string{"https://evil.example", "//evil.example/x", "billing", "/\\evil.example"} {
		if _, err := b.back(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := New(Config{}, nil).back("/billing"); err == nil {
		t.Error("no APP_URL accepted")
	}
}
