package lidza

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// The policy: a listed domain always, another host only when the hook
// approves it; verdicts are cached, a revoked host is refused once its
// approval lapses, and the hook sees the services.
func TestTLSPolicy(t *testing.T) {
	type key struct{}
	svc := NewServices()
	Provide(svc, key{})
	approved := map[string]bool{"mta-sts.customer.example": true}
	calls := 0
	hook := func(ctx context.Context, host string) error {
		calls++
		Service[key](ctx) // the services are in ctx
		if !approved[host] {
			return errors.New("no such customer domain")
		}
		return nil
	}
	var logs bytes.Buffer
	p := newTLSPolicy([]string{"app.example.com"}, hook, svc, slog.New(slog.NewTextHandler(&logs, nil)))
	clock := &fakeClock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	p.now = clock.now
	ctx := context.Background()

	if err := p.allow(ctx, "APP.example.com."); err != nil || calls != 0 {
		t.Fatalf("listed domain: %v, %d hook calls", err, calls)
	}
	if err := p.allow(ctx, "mta-sts.customer.example"); err != nil {
		t.Fatalf("approved host: %v", err)
	}
	p.allow(ctx, "mta-sts.customer.example")
	if calls != 1 {
		t.Fatalf("approval not cached: %d calls", calls)
	}
	if err := p.allow(ctx, "random.attacker.example"); err == nil || !strings.Contains(err.Error(), "no such customer domain") {
		t.Fatalf("unknown host: %v", err)
	}
	p.allow(ctx, "random.attacker.example")
	if calls != 2 {
		t.Fatalf("refusal not cached: %d calls", calls)
	}

	// Revoked: still approved from the cache until the approval lapses.
	approved["mta-sts.customer.example"] = false
	clock.t = clock.t.Add(tlsApproveTTL - time.Second)
	if err := p.allow(ctx, "mta-sts.customer.example"); err != nil {
		t.Fatalf("within the approval: %v", err)
	}
	clock.t = clock.t.Add(2 * time.Second)
	if err := p.allow(ctx, "mta-sts.customer.example"); err == nil {
		t.Fatal("revoked host still approved")
	}

	snap := p.status.Snapshot()
	if len(snap.Domains) != 1 || snap.Domains[0].Host != "app.example.com" || len(snap.Approved) != 0 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if len(snap.Refused) != 2 || snap.Refused[0].Host != "mta-sts.customer.example" || snap.Refused[0].Reason != "no such customer domain" {
		t.Fatalf("refusals, newest first: %+v", snap.Refused)
	}
	if !strings.Contains(logs.String(), "tls: host approved") || !strings.Contains(logs.String(), "tls: host refused") {
		t.Fatalf("logs:\n%s", logs.String())
	}

	// Without a hook, only the listed domains.
	none := newTLSPolicy([]string{"app.example.com"}, nil, svc, slog.New(slog.DiscardHandler))
	if err := none.allow(ctx, "mta-sts.customer.example"); !errors.Is(err, errNotListed) {
		t.Fatalf("no hook: %v", err)
	}
}

// Verdicts and refusals are bounded, and refusals are logged at most
// tlsRefusalLogs a minute.
func TestTLSPolicyBounds(t *testing.T) {
	var logs bytes.Buffer
	p := newTLSPolicy([]string{"app.example.com"}, func(context.Context, string) error { return errors.New("unknown") }, NewServices(), slog.New(slog.NewTextHandler(&logs, nil)))
	clock := &fakeClock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	p.now = clock.now
	for i := 0; i < tlsVerdicts+100; i++ {
		p.allow(context.Background(), fmt.Sprintf("h%d.example", i))
	}
	if n := len(p.verdicts); n > tlsVerdicts {
		t.Fatalf("%d verdicts kept", n)
	}
	if n := len(p.status.Snapshot().Refused); n != tlsRefusalsKept {
		t.Fatalf("%d refusals kept", n)
	}
	if n := strings.Count(logs.String(), "tls: host refused"); n != tlsRefusalLogs {
		t.Fatalf("%d refusal lines in one minute", n)
	}
	clock.t = clock.t.Add(time.Minute)
	p.allow(context.Background(), "next.example")
	if !strings.Contains(logs.String(), "unlogged_before=") {
		t.Fatalf("the unlogged count is not reported:\n%s", logs.String())
	}
}

type recordRT struct{ called int }

func (r *recordRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.called++
	io.ReadAll(req.Body)
	return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
}

// jws is an ACME request body with payload p.
func jws(p string) string {
	return `{"protected":"e30","payload":"` + base64.RawURLEncoding.EncodeToString([]byte(p)) + `","signature":"c2ln"}`
}

// The order guard: a new order naming a refused host never leaves the
// node, so a certificate the app stopped approving is not renewed; an
// approved order and any other request pass.
func TestOrderGuard(t *testing.T) {
	allow := func(_ context.Context, host string) error {
		if host == "app.example.com" || host == "mta-sts.customer.example" {
			return nil
		}
		return errors.New("not approved")
	}
	for _, c := range []struct {
		body   string
		passes bool
	}{
		{jws(`{"identifiers":[{"type":"dns","value":"mta-sts.customer.example"}]}`), true},
		{jws(`{"identifiers":[{"type":"dns","value":"app.example.com"},{"type":"dns","value":"revoked.customer.example"}]}`), false},
		{jws(`{"csr":"abc"}`), true},
		{jws(``), true},
		{`not json`, true},
	} {
		next := &recordRT{}
		g := orderGuard{next: next, allow: allow}
		req, _ := http.NewRequest(http.MethodPost, "https://acme.example/new-order", strings.NewReader(c.body))
		res, err := g.RoundTrip(req)
		if c.passes != (err == nil) || c.passes != (next.called == 1) {
			t.Errorf("%s: err %v, sent %d", c.body, err, next.called)
		}
		if res != nil {
			res.Body.Close()
		}
	}
}
