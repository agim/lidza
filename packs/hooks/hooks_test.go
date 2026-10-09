package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/jobs"
	"github.com/agim/lidza/pkg/webhook"
)

func testHooks(t *testing.T, cfg Config) *Hooks {
	t.Helper()
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 4, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, tbl := range []string{"hook_delivery", "hook_endpoint", "job", "job_schedule"} {
		pool.Exec(ctx, `DROP TABLE IF EXISTS `+tbl)
	}
	if _, err := pool.Exec(ctx, EndpointTable+DeliveryTable+jobs.JobTable+jobs.ScheduleTable); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIDZA_MASTER_KEY", strings.Repeat("ab", 32))
	q := jobs.New(jobs.Config{}, pool)
	return New(cfg, pool, q)
}

// receiver is a subscriber: it verifies each delivery as a Līdza app
// would (webhook.Standard), answers with the statuses given in turn,
// and records what it accepted.
type receiver struct {
	srv      *httptest.Server
	mu       sync.Mutex
	statuses []int
	got      []string
	secret   string
}

func newReceiver(t *testing.T, statuses ...int) *receiver {
	rc := &receiver{statuses: statuses}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		status := 200
		if len(rc.statuses) > 0 {
			status, rc.statuses = rc.statuses[0], rc.statuses[1:]
		}
		secret := rc.secret
		rc.mu.Unlock()
		if status == http.StatusFound {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		os.Setenv("RECEIVER_SECRET", secret)
		verified := false
		webhook.Standard("RECEIVER_SECRET", func(context.Context, *webhook.Delivery) error { verified = true; return nil }, webhook.WithStore(webhook.Memory(100))).
			ServeHTTP(httptest.NewRecorder(), mustRequest(r, body))
		if !verified {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		if status >= 300 {
			http.Error(w, "busy", status)
			return
		}
		rc.mu.Lock()
		rc.got = append(rc.got, string(body))
		rc.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func mustRequest(r *http.Request, body []byte) *http.Request {
	req := httptest.NewRequest("POST", "/api/v1/webhooks/x", strings.NewReader(string(body)))
	req.Header = r.Header.Clone()
	return req
}

// runJobs runs the queued delivery jobs that are due, as the queue would.
func runJobs(t *testing.T, h *Hooks, includeLater bool) int {
	t.Helper()
	ctx := context.Background()
	q := `SELECT id, payload FROM job WHERE kind = $1 AND state = 'pending'`
	if !includeLater {
		q += ` AND run_at <= now()`
	}
	rows, err := h.pool.Query(ctx, q, JobKind)
	if err != nil {
		t.Fatal(err)
	}
	type job struct {
		id      string
		payload []byte
	}
	var due []job
	for rows.Next() {
		var j job
		rows.Scan(&j.id, &j.payload)
		due = append(due, j)
	}
	rows.Close()
	for _, j := range due {
		h.pool.Exec(ctx, `UPDATE job SET state = 'done' WHERE id = $1`, j.id)
		if err := h.deliver(ctx, j.payload); err != nil {
			t.Fatal(err)
		}
	}
	return len(due)
}

func TestDeliverRetryAndVerify(t *testing.T) {
	h := testHooks(t, Config{AllowPrivate: true, DisableAfter: 2})
	ctx := context.Background()
	rc := newReceiver(t, 503, 200)
	e, secret, err := h.Create(ctx, "ws1", EndpointInput{URL: rc.srv.URL + "/in", Events: []string{"order.*"}})
	if err != nil {
		t.Fatal(err)
	}
	rc.secret = secret
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("secret %q", secret)
	}
	if n, err := h.Send(ctx, "ws1", "user.created", map[string]string{"id": "u1"}); err != nil || n != 0 {
		t.Fatalf("an event the endpoint does not take: %d %v", n, err)
	}
	if n, err := h.Send(ctx, "ws2", "order.paid", map[string]string{"id": "o1"}); err != nil || n != 0 {
		t.Fatalf("another owner's event: %d %v", n, err)
	}
	if n, err := h.Send(ctx, "ws1", "order.paid", map[string]string{"id": "o1"}); err != nil || n != 1 {
		t.Fatalf("send: %d %v", n, err)
	}
	// First attempt: 503, scheduled again with the error recorded.
	runJobs(t, h, false)
	ds, _ := h.Deliveries(ctx, "ws1", e.ID, 10)
	if len(ds) != 1 || ds[0].Status != Pending || ds[0].Attempts != 1 || ds[0].LastStatus == nil || *ds[0].LastStatus != 503 || !strings.Contains(ds[0].LastError, "busy") {
		t.Fatalf("after a 503: %+v", ds)
	}
	var runAt time.Time
	h.pool.QueryRow(ctx, `SELECT run_at FROM job WHERE kind = $1 AND state = 'pending'`, JobKind).Scan(&runAt)
	if d := time.Until(runAt); d < 3*time.Second || d > 6*time.Second {
		t.Fatalf("retry in %v, want about %v", d, Schedule[0])
	}
	// Second attempt: delivered, signed, the same body.
	runJobs(t, h, true)
	ds, _ = h.Deliveries(ctx, "ws1", e.ID, 10)
	if ds[0].Status != Delivered || ds[0].Attempts != 2 || ds[0].DeliveredAt == nil {
		t.Fatalf("delivered: %+v", ds[0])
	}
	if len(rc.got) != 1 || !strings.Contains(rc.got[0], `"type":"order.paid"`) || !strings.Contains(rc.got[0], `"data":{"id":"o1"}`) {
		t.Fatalf("received: %v", rc.got)
	}

	// A rotated secret signs from then on; the old one fails verification.
	newSecret, err := h.Rotate(ctx, "ws1", e.ID)
	if err != nil || newSecret == secret {
		t.Fatal("rotate")
	}
	h.Ping(ctx, "ws1", e.ID)
	runJobs(t, h, true)
	ds, _ = h.Deliveries(ctx, "ws1", e.ID, 1)
	if ds[0].Status == Delivered || ds[0].LastStatus == nil || *ds[0].LastStatus != 401 {
		t.Fatalf("delivered under the old secret: %+v", ds[0])
	}
	rc.secret = newSecret
	runJobs(t, h, true)
	ds, _ = h.Deliveries(ctx, "ws1", e.ID, 1)
	if ds[0].Status != Delivered || ds[0].Event != "hooks.ping" {
		t.Fatalf("ping after rotation: %+v", ds[0])
	}
	if s, _ := h.Secret(ctx, "ws1", e.ID); s != newSecret {
		t.Fatal("secret not revealed")
	}
	if _, err := h.Secret(ctx, "ws2", e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner read the secret: %v", err)
	}
}

func TestDeliverGivesUpAndDisables(t *testing.T) {
	h := testHooks(t, Config{AllowPrivate: true, DisableAfter: 1})
	ctx := context.Background()
	rc := newReceiver(t, 500, 500, 500)
	e, secret, _ := h.Create(ctx, "app", EndpointInput{URL: rc.srv.URL})
	rc.secret = secret
	h.Send(ctx, "app", "order.paid", map[string]int{"n": 1})
	// The last attempt: the delivery fails for good, the endpoint counts it.
	h.pool.Exec(ctx, `UPDATE hook_delivery SET attempts = $1`, len(Schedule))
	runJobs(t, h, true)
	ds, _ := h.Deliveries(ctx, "app", e.ID, 1)
	if ds[0].Status != Failed || ds[0].Attempts != len(Schedule)+1 {
		t.Fatalf("given up: %+v", ds[0])
	}
	got, _ := h.Get(ctx, "app", e.ID)
	if got.DisabledAt == nil || got.Failures != 1 || got.Disabled == "" {
		t.Fatalf("endpoint not disabled: %+v", got)
	}
	if n, _ := h.Send(ctx, "app", "order.paid", nil); n != 0 {
		t.Fatalf("a disabled endpoint got a delivery: %d", n)
	}
	// Replay a failed delivery after enabling the endpoint again.
	if _, err := h.Update(ctx, "app", e.ID, EndpointInput{URL: rc.srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Replay(ctx, "app", ds[0].ID); err != nil {
		t.Fatal(err)
	}
	rc.statuses = nil
	runJobs(t, h, true)
	ds, _ = h.Deliveries(ctx, "app", e.ID, 1)
	if ds[0].Status != Delivered {
		t.Fatalf("replay: %+v", ds[0])
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	h := testHooks(t, Config{AllowPrivate: true})
	ctx := context.Background()
	rc := newReceiver(t, http.StatusFound)
	e, secret, _ := h.Create(ctx, "app", EndpointInput{URL: rc.srv.URL})
	rc.secret = secret
	h.Ping(ctx, "app", e.ID)
	runJobs(t, h, true)
	ds, _ := h.Deliveries(ctx, "app", e.ID, 1)
	if ds[0].Status != Pending || !strings.Contains(ds[0].LastError, "redirects are not followed") {
		t.Fatalf("redirect: %+v", ds[0])
	}
}

// TestTargets: without AllowPrivate, an endpoint on an internal address
// is refused when saved, and a name that resolves inward is refused at
// connection time.
func TestTargets(t *testing.T) {
	h := testHooks(t, Config{})
	ctx := context.Background()
	for _, u := range []string{
		"http://example.com/hook", "https://127.0.0.1/hook", "https://10.1.2.3/x", "https://[::1]/x", "https://169.254.169.254/latest",
		"https://localhost/x", "https://user:pass@example.com/x", "ftp://example.com/x", "https://100.64.0.1/x", "https://[fd00::1]/x", "/relative",
	} {
		if _, _, err := h.Create(ctx, "app", EndpointInput{URL: u}); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	if _, _, err := h.Create(ctx, "app", EndpointInput{URL: "https://hooks.example.com/in", Events: []string{"order.*", "user.created"}}); err != nil {
		t.Errorf("a public https URL: %v", err)
	}
	if _, _, err := h.Create(ctx, "app", EndpointInput{URL: "https://hooks.example.com/in", Events: []string{"Order Paid"}}); err == nil {
		t.Error("a bad event name accepted")
	}
	// Dial time: localhost resolves to loopback.
	_, err := h.client.Get("http://localhost:9/")
	if err == nil || !strings.Contains(err.Error(), "no public address") {
		t.Errorf("dialed an internal address: %v", err)
	}
}

func TestSubscribed(t *testing.T) {
	for _, c := range []struct {
		events []string
		name   string
		want   bool
	}{
		{nil, "a.b", true}, {[]string{"*"}, "a.b", true}, {[]string{"order.*"}, "order.paid", true},
		{[]string{"order.*"}, "orders.paid", false}, {[]string{"order.paid"}, "order.paid", true}, {[]string{"user.created"}, "order.paid", false},
	} {
		if got := subscribed(c.events, c.name); got != c.want {
			t.Errorf("%v %s: %v", c.events, c.name, got)
		}
	}
}

func TestMaxEndpoints(t *testing.T) {
	h := testHooks(t, Config{AllowPrivate: true, MaxEndpoints: 1})
	ctx := context.Background()
	if _, _, err := h.Create(ctx, "o", EndpointInput{URL: "https://hooks.example.com/1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Create(ctx, "o", EndpointInput{URL: "https://hooks.example.com/2"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("second endpoint: %v", err)
	}
	list, _ := h.List(ctx, "o")
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "whsec_") || strings.Contains(string(b), "secret") {
		t.Fatalf("an endpoint listing carries the secret: %s", b)
	}
}
