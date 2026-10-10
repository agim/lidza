package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/webhook"
)

// Summary is the paid feature's reply in the flow below.
type Summary struct {
	Text string `json:"text"`
}

// paidTeamFeature is the route the recipe "Sell a feature to teams"
// builds: a workspace's members use it when the workspace's plan
// includes it, and the call is labelled by workspace for the usage
// page.
func paidTeamFeature(plans Plans) func(ctx context.Context, req *router.Request[map[string]string]) (Summary, error) {
	return func(ctx context.Context, req *router.Request[map[string]string]) (Summary, error) {
		ws := auth.WorkspaceID(ctx)
		if err := plans.Require(ctx, ws, "ai"); err != nil {
			return Summary{}, err
		}
		res, err := llm.From(ctx).Chat(ctx, llm.Request{
			System:   "Summarize in one sentence.",
			Messages: []llm.Message{{Role: llm.User, Content: req.Body["text"]}},
			Label:    "summary",
		})
		return Summary{Text: res.Text}, err
	}
}

// TestPaidTeamFeature: auth, workspaces, billing and the LLM pack in one
// app. An owner creates a workspace and invites a member; the feature
// answers 402 until the workspace subscribes through a signed Stripe
// event, then serves its members, and only that workspace's.
func TestPaidTeamFeature(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	admin, err := db.Open(ctx, db.Config{URL: url, MaxConns: 1, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer admin.Close()
	// A schema of its own: the auth pack's tests drop the same tables.
	const schema = "lidza_test_teamflow"
	if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE; CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	sep := "&"
	if !strings.Contains(url, "?") {
		sep = "?"
	}
	pool, err := db.Open(ctx, db.Config{URL: url + sep + "search_path=" + schema, MaxConns: 4, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, auth.SessionTable+auth.TokenTable+auth.AccountTable+auth.UserTable+auth.IdentityTable+
		auth.MemberTable+auth.WorkspaceTable+auth.InviteTable+mail.OutboxTable+CustomerTable+SubscriptionTable); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LIDZA_MODE", "test")
	t.Setenv(WebhookSecretSetting, whsec)
	a, err := auth.New(auth.Config{Secret: strings.Repeat("s", 32), LoginRPS: 1000, LoginBurst: 1000, SignInRPS: 1000, SignInBurst: 1000}, pool)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.New(mail.Config{Provider: "outbox", From: "app@example.com", AppURL: "https://app.example.com"}, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := llm.New(llm.Config{Provider: "fake", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	model.Fake().Reply("Revenue grew.")
	stripe := newFakeStripe(t)
	b := New(Config{SecretKey: "sk_test_key", APIURL: stripe.srv.URL, AppURL: "https://app.example.com"}, pool)
	teams := &auth.Teams{Roles: auth.Roles{"owner": {"*"}, "member": {"summaries.create"}}, Title: "Shop"}
	plans := Plans{FreePlan: {Name: "Free"}, "team": {Name: "Team", Price: "price_team", Features: []string{"ai"}}}

	s := lidza.NewServices()
	lidza.Provide(s, a)
	lidza.Provide(s, m)
	lidza.Provide(s, model)
	lidza.Provide(s, b)
	r := router.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(lidza.WithServices(req.Context(), s)))
		})
	})
	auth.Mount(r, auth.Options{NoVerifyEmail: true})
	teams.Mount(r)
	plans.Mount(r, Options{Guard: auth.RequireWorkspace()})
	scoped := r.Group("/api/v1/summaries", auth.RequireWorkspace())
	router.Route(scoped, "POST /api/v1/summaries", paidTeamFeature(plans))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	type user struct {
		c  *http.Client
		ws string
	}
	call := func(u *user, method, path string, body any) (int, string) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			j, _ := json.Marshal(body)
			rd = strings.NewReader(string(j))
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		if u.ws != "" {
			req.Header.Set(auth.WorkspaceHeader, u.ws)
		}
		res, err := u.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	field := func(body, name string) string {
		var v map[string]any
		json.Unmarshal([]byte(body), &v)
		s, _ := v[name].(string)
		return s
	}
	signUp := func(email string) *user {
		jar, _ := cookiejar.New(nil)
		u := &user{c: &http.Client{Jar: jar}}
		if code, body := call(u, "POST", "/api/v1/auth/register", map[string]string{"email": email, "password": "correct horse battery", "name": "x"}); code >= 300 {
			t.Fatalf("register %s: %d %s", email, code, body)
		}
		return u
	}
	ann, bob, eve := signUp("ann@example.com"), signUp("bob@example.com"), signUp("eve@example.com")

	// Ann's workspace, Bob invited by mail and joining.
	code, body := call(ann, "POST", "/api/v1/workspaces", map[string]string{"name": "Acme"})
	if code != 200 {
		t.Fatalf("workspace: %d %s", code, body)
	}
	ws := field(body, "id")
	ann.ws, bob.ws = ws, ws
	if code, body := call(ann, "POST", "/api/v1/workspaces/"+ws+"/invites", map[string]string{"email": "bob@example.com", "role": "member"}); code != 200 {
		t.Fatalf("invite: %d %s", code, body)
	}
	var text string
	pool.QueryRow(ctx, `SELECT text FROM mail_message WHERE recipient = 'bob@example.com' ORDER BY created_at DESC LIMIT 1`).Scan(&text)
	token := regexp.MustCompile(`token=([A-Za-z0-9_-]+)`).FindStringSubmatch(text)
	if token == nil {
		t.Fatalf("no invitation link in %q", text)
	}
	if code, body := call(bob, "POST", "/api/v1/invites/accept", map[string]string{"token": token[1]}); code != 200 {
		t.Fatalf("accept: %d %s", code, body)
	}

	// Not on a plan with the feature yet.
	if code, body := call(bob, "POST", "/api/v1/summaries", map[string]string{"text": "Q3 numbers"}); code != 402 || !strings.Contains(body, "upgrade") {
		t.Fatalf("before paying: %d %s", code, body)
	}
	// Ann subscribes the workspace; Stripe confirms with a signed event.
	if code, body := call(ann, "POST", "/api/v1/billing/checkout", map[string]string{"plan": "team"}); code != 200 || stripe.checkout.Get("client_reference_id") != ws {
		t.Fatalf("checkout: %d %s %v", code, body, stripe.checkout)
	}
	stripe.sub = subscription("active", false, "price_team")
	stripe.sub["metadata"] = map[string]string{"lidza_owner": ws}
	obj, _ := json.Marshal(map[string]any{"mode": "subscription", "customer": "cus_1", "client_reference_id": ws, "subscription": "sub_1"})
	event := fmt.Sprintf(`{"id":"evt_1","type":"checkout.session.completed","created":%d,"data":{"object":%s}}`, time.Now().Unix(), obj)
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/billing/webhook", strings.NewReader(event))
	req.Header.Set("Stripe-Signature", webhook.StripeSignature(whsec, time.Now(), []byte(event)))
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("webhook: %v %v", res, err)
	}
	res.Body.Close()

	// Now every member has it.
	if code, body := call(bob, "POST", "/api/v1/summaries", map[string]string{"text": "Q3 numbers"}); code != 200 || field(body, "text") != "Revenue grew." {
		t.Fatalf("after paying: %d %s", code, body)
	}
	if calls := model.Fake().Calls(); len(calls) != 1 || calls[0].Messages[0].Content != "Q3 numbers" {
		t.Fatalf("model calls: %+v", calls)
	}
	// Another workspace has not paid; nor can a stranger use Acme's.
	if code, body = call(eve, "POST", "/api/v1/workspaces", map[string]string{"name": "Other"}); code != 200 {
		t.Fatalf("other workspace: %d %s", code, body)
	}
	eve.ws = field(body, "id")
	if code, _ := call(eve, "POST", "/api/v1/summaries", map[string]string{"text": "x"}); code != 402 {
		t.Fatalf("another workspace: %d", code)
	}
	eve.ws = ws
	if code, _ := call(eve, "POST", "/api/v1/summaries", map[string]string{"text": "x"}); code != 403 && code != 404 {
		t.Fatalf("a stranger in Acme: %d", code)
	}
	if len(model.Fake().Calls()) != 1 {
		t.Fatal("the model was called for a request that should have been refused")
	}
}
