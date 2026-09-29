package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
)

// sign is HMAC-SHA256 in hex, as the providers document it.
func sign(secret, payload string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// stripeHeader builds a Stripe-Signature header per Stripe's docs:
// v1 is the HMAC-SHA256 of "<t>.<body>" under the whsec_ secret.
func stripeHeader(secret string, t time.Time, body string) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + ",v1=" + sign(secret, ts+"."+body)
}

func post(h http.Handler, body string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/webhooks/x", strings.NewReader(body))
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// counter is a handler that counts its calls.
func counter(n *atomic.Int32) Handler {
	return func(ctx context.Context, d *Delivery) error { n.Add(1); return nil }
}

const whsec = "whsec_test_5b1c9f0d2a"

func TestStripe(t *testing.T) {
	t.Setenv("PAYMENTS_WEBHOOK_SECRET", whsec)
	var calls atomic.Int32
	var got *Delivery
	h := Stripe("PAYMENTS_WEBHOOK_SECRET", func(ctx context.Context, d *Delivery) error {
		calls.Add(1)
		got = d
		return nil
	}, WithStore(Memory(10)))
	body := `{"id":"evt_1","type":"checkout.session.completed","created":1700000000,"data":{"object":{"id":"cs_1","amount_total":1200}}}`
	now := time.Now()

	if w := post(h, body, map[string]string{"Stripe-Signature": stripeHeader(whsec, now, body)}); w.Code != 200 {
		t.Fatalf("valid: %d %s", w.Code, w.Body)
	}
	if calls.Load() != 1 || got.ID != "evt_1" || got.Timestamp.Unix() != now.Unix() {
		t.Fatalf("delivery: %d %+v", calls.Load(), got)
	}
	ev, err := got.StripeEvent()
	if err != nil || ev.Type != "checkout.session.completed" || !strings.Contains(string(ev.Data.Object), "cs_1") {
		t.Fatalf("event: %+v %v", ev, err)
	}

	// The same event again (a retry, or a replay inside the window).
	if w := post(h, body, map[string]string{"Stripe-Signature": stripeHeader(whsec, now, body)}); w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("replay: %d, %d calls", w.Code, calls.Load())
	}

	// Several v1 signatures (Stripe sends one per active secret): any
	// that matches is enough.
	body2 := strings.Replace(body, "evt_1", "evt_2", 1)
	ts := strconv.FormatInt(now.Unix(), 10)
	multi := "t=" + ts + ",v1=" + sign("whsec_old", ts+"."+body2) + ",v1=" + sign(whsec, ts+"."+body2) + ",v0=abc"
	if w := post(h, body2, map[string]string{"Stripe-Signature": multi}); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("multiple v1: %d", w.Code)
	}

	refused := map[string]struct {
		body, header string
	}{
		"tampered body":  {strings.Replace(body, "1200", "1", 1), stripeHeader(whsec, now, body)},
		"wrong secret":   {body, stripeHeader("whsec_other", now, body)},
		"old timestamp":  {body, stripeHeader(whsec, now.Add(-6*time.Minute), body)},
		"future":         {body, stripeHeader(whsec, now.Add(6*time.Minute), body)},
		"no header":      {body, ""},
		"no v1":          {body, "t=" + ts + ",v0=" + sign(whsec, ts+"."+body)},
		"timestamp swap": {body, "t=" + strconv.FormatInt(now.Unix()+1, 10) + ",v1=" + sign(whsec, ts+"."+body)},
	}
	for name, c := range refused {
		if w := post(h, c.body, map[string]string{"Stripe-Signature": c.header}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, w.Code)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("handler ran for a refused delivery: %d", calls.Load())
	}

	// A wider window takes the older delivery.
	old := strings.Replace(body, "evt_1", "evt_3", 1)
	wide := Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls), WithStore(Memory(10)), Tolerance(10*time.Minute))
	if w := post(wide, old, map[string]string{"Stripe-Signature": stripeHeader(whsec, now.Add(-6*time.Minute), old)}); w.Code != 200 {
		t.Errorf("tolerance: %d", w.Code)
	}
}

func TestHandlerFailureIsRetried(t *testing.T) {
	t.Setenv("PAYMENTS_WEBHOOK_SECRET", whsec)
	var calls atomic.Int32
	fail := true
	h := Stripe("PAYMENTS_WEBHOOK_SECRET", func(ctx context.Context, d *Delivery) error {
		calls.Add(1)
		if fail {
			return errors.New("database down")
		}
		return nil
	}, WithStore(Memory(10)))
	body := `{"id":"evt_9","type":"invoice.paid"}`
	sig := map[string]string{"Stripe-Signature": stripeHeader(whsec, time.Now(), body)}
	if w := post(h, body, sig); w.Code != 500 {
		t.Fatalf("failure: %d", w.Code)
	}
	fail = false
	if w := post(h, body, sig); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("retry: %d, %d calls", w.Code, calls.Load())
	}
	if w := post(h, body, sig); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("after success: %d, %d calls", w.Code, calls.Load())
	}

	// A panic releases the id too.
	p := Stripe("PAYMENTS_WEBHOOK_SECRET", func(ctx context.Context, d *Delivery) error { panic("boom") }, WithStore(Memory(10)))
	if w := post(p, body, sig); w.Code != 500 {
		t.Fatalf("panic: %d", w.Code)
	}
}

func TestFailClosed(t *testing.T) {
	t.Setenv("PAYMENTS_WEBHOOK_SECRET", "")
	var calls atomic.Int32
	h := Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls), WithStore(Memory(10)))
	body := `{"id":"evt_1","type":"x"}`
	// Signed with the empty key: still refused.
	if w := post(h, body, map[string]string{"Stripe-Signature": stripeHeader("", time.Now(), body)}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no secret: %d", w.Code)
	}
	tok := Token("HOOK_TOKEN", "X-Token", counter(&calls), WithStore(Memory(10)))
	if w := post(tok, "{}", map[string]string{"X-Token": ""}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("token without a secret: %d", w.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("handler ran without a secret")
	}

	// Production without the db pack or a store refuses as well.
	t.Setenv("PAYMENTS_WEBHOOK_SECRET", whsec)
	t.Setenv("LIDZA_MODE", "production")
	h = Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls))
	if w := post(h, body, map[string]string{"Stripe-Signature": stripeHeader(whsec, time.Now(), body)}); w.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
		t.Fatalf("no store in production: %d", w.Code)
	}
	// Under test the bounded memory store stands in.
	t.Setenv("LIDZA_MODE", "test")
	h = Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls))
	if w := post(h, body, map[string]string{"Stripe-Signature": stripeHeader(whsec, time.Now(), body)}); w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("memory store under test: %d", w.Code)
	}
}

const signingKey = "key-3ax6xnjp29jd6fds4gc373sgvjxteol0"

func TestMailgunJSON(t *testing.T) {
	t.Setenv("MAIL_WEBHOOK_SIGNING_KEY", signingKey)
	var calls atomic.Int32
	var got *Delivery
	h := Mailgun("MAIL_WEBHOOK_SIGNING_KEY", func(ctx context.Context, d *Delivery) error {
		calls.Add(1)
		got = d
		return nil
	}, WithStore(Memory(10)))
	event := func(ts time.Time, token, key string) string {
		s := strconv.FormatInt(ts.Unix(), 10)
		return fmt.Sprintf(`{"signature":{"timestamp":%q,"token":%q,"signature":%q},"event-data":{"event":"failed","id":"ev-1","severity":"permanent","recipient":"reader@example.com","message":{"headers":{"message-id":"m-1@example.com"}}}}`, s, token, sign(key, s+token))
	}
	body := event(time.Now(), "tok-1", signingKey)
	if w := post(h, body, map[string]string{"Content-Type": "application/json"}); w.Code != 200 {
		t.Fatalf("valid: %d %s", w.Code, w.Body)
	}
	ev, err := got.MailgunEvent()
	if err != nil || ev.Event != "failed" || ev.Recipient != "reader@example.com" || ev.Message.Headers.MessageID != "m-1@example.com" || got.ID != "tok-1" {
		t.Fatalf("event: %+v %v", ev, err)
	}
	if w := post(h, body, map[string]string{"Content-Type": "application/json"}); w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("replayed token: %d, %d calls", w.Code, calls.Load())
	}
	for name, b := range map[string]string{
		"wrong key":     event(time.Now(), "tok-2", "key-other"),
		"old timestamp": event(time.Now().Add(-10*time.Minute), "tok-3", signingKey),
		"tampered":      strings.Replace(event(time.Now(), "tok-4", signingKey), `"tok-4"`, `"tok-5"`, 1),
		"no signature":  `{"event-data":{"event":"delivered"}}`,
	} {
		if w := post(h, b, map[string]string{"Content-Type": "application/json"}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, w.Code)
		}
	}
	if w := post(h, "not json", nil); w.Code != http.StatusBadRequest {
		t.Errorf("malformed: %d", w.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("calls: %d", calls.Load())
	}
}

func TestMailgunForm(t *testing.T) {
	t.Setenv("MAIL_WEBHOOK_SIGNING_KEY", signingKey)
	var got *Delivery
	h := Mailgun("MAIL_WEBHOOK_SIGNING_KEY", func(ctx context.Context, d *Delivery) error { got = d; return nil }, WithStore(Memory(10)))
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	form := url.Values{"timestamp": {ts}, "token": {"tok-f1"}, "signature": {sign(signingKey, ts+"tok-f1")}, "recipient": {"inbox@example.com"}}
	if w := post(h, form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); w.Code != 200 || got.Form.Get("recipient") != "inbox@example.com" {
		t.Fatalf("urlencoded: %d %+v", w.Code, got)
	}
	form.Set("signature", sign("key-other", ts+"tok-f1"))
	form.Set("token", "tok-f2")
	if w := post(h, form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("urlencoded wrong key: %d", w.Code)
	}

	// An inbound message with an attachment, as multipart.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("timestamp", ts)
	mw.WriteField("token", "tok-m1")
	mw.WriteField("signature", sign(signingKey, ts+"tok-m1"))
	mw.WriteField("subject", "Order 42")
	fw, _ := mw.CreateFormFile("attachment-1", "invoice.pdf")
	fw.Write([]byte("%PDF-1.4"))
	mw.Close()
	if w := post(h, buf.String(), map[string]string{"Content-Type": mw.FormDataContentType()}); w.Code != 200 || got.Form.Get("subject") != "Order 42" || got.Form.Has("attachment-1") {
		t.Fatalf("multipart: %d %v", w.Code, got.Form)
	}
}

func TestHMAC(t *testing.T) {
	t.Setenv("REPO_WEBHOOK_SECRET", "It's a Secret to Everybody")
	var calls atomic.Int32
	h := HMAC("REPO_WEBHOOK_SECRET", "X-Hub-Signature-256", counter(&calls), Prefix("sha256="), IDHeader("X-Delivery"), WithStore(Memory(10)))
	// GitHub's documented test vector.
	body := "Hello, World!"
	const want = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := "sha256=" + sign("It's a Secret to Everybody", body); got != want {
		t.Fatalf("vector: %s", got)
	}
	if w := post(h, body, map[string]string{"X-Hub-Signature-256": want, "X-Delivery": "d-1"}); w.Code != 200 {
		t.Fatalf("valid: %d", w.Code)
	}
	if w := post(h, body, map[string]string{"X-Hub-Signature-256": want, "X-Delivery": "d-1"}); w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("repeat: %d, %d calls", w.Code, calls.Load())
	}
	if w := post(h, body, map[string]string{"X-Hub-Signature-256": want}); w.Code != http.StatusBadRequest {
		t.Fatalf("no delivery id: %d", w.Code)
	}
	for name, hdr := range map[string]map[string]string{
		"tampered":  {"X-Hub-Signature-256": "sha256=" + sign("It's a Secret to Everybody", body+"!"), "X-Delivery": "d-2"},
		"no prefix": {"X-Hub-Signature-256": strings.TrimPrefix(want, "sha256="), "X-Delivery": "d-3"},
		"missing":   {"X-Delivery": "d-4"},
		"not hex":   {"X-Hub-Signature-256": "sha256=zz", "X-Delivery": "d-5"},
	} {
		if w := post(h, body, hdr); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, w.Code)
		}
	}

	// Base64 digests (a store platform's style).
	t.Setenv("SHOP_WEBHOOK_SECRET", "shpss_1")
	b := HMAC("SHOP_WEBHOOK_SECRET", "X-Signature", counter(&calls), Base64(), WithStore(Memory(10)))
	m := hmac.New(sha256.New, []byte("shpss_1"))
	m.Write([]byte(`{"id":7}`))
	if w := post(b, `{"id":7}`, map[string]string{"X-Signature": base64.StdEncoding.EncodeToString(m.Sum(nil))}); w.Code != 200 {
		t.Fatalf("base64: %d", w.Code)
	}
}

func TestToken(t *testing.T) {
	t.Setenv("DNS_WEBHOOK_TOKEN", "s3cr3t-token")
	var calls atomic.Int32
	h := Token("DNS_WEBHOOK_TOKEN", "Authorization", counter(&calls), Prefix("Bearer "), WithStore(Memory(10)))
	if w := post(h, "{}", map[string]string{"Authorization": "Bearer s3cr3t-token"}); w.Code != 200 {
		t.Fatalf("valid: %d", w.Code)
	}
	for _, v := range []string{"Bearer s3cr3t-toke", "Bearer s3cr3t-token2", "s3cr3t-token", ""} {
		if w := post(h, "{}", map[string]string{"Authorization": v}); w.Code != http.StatusUnauthorized {
			t.Errorf("%q: %d", v, w.Code)
		}
	}

	f := TokenField("DNS_WEBHOOK_TOKEN", "auth.token", counter(&calls), ID(func(_ *http.Request, body []byte) string {
		return strconv.Itoa(len(body))
	}), WithStore(Memory(10)))
	ok := `{"auth":{"token":"s3cr3t-token"},"record":"www","ip":"192.0.2.1"}`
	if w := post(f, ok, nil); w.Code != 200 {
		t.Fatalf("field: %d", w.Code)
	}
	if w := post(f, ok, nil); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("field repeat: %d %d", w.Code, calls.Load())
	}
	for _, b := range []string{`{"auth":{"token":"wrong"}}`, `{"token":"s3cr3t-token"}`, `{"auth":"s3cr3t-token"}`, `{"auth":{"token":1}}`} {
		if w := post(f, b, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", b, w.Code)
		}
	}
	if w := post(f, "token=x", nil); w.Code != http.StatusBadRequest {
		t.Errorf("not json: %d", w.Code)
	}
}

func TestLimits(t *testing.T) {
	t.Setenv("PAYMENTS_WEBHOOK_SECRET", whsec)
	var calls atomic.Int32
	h := Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls), MaxBody(64), WithStore(Memory(10)))
	body := `{"id":"evt_big","type":"x","pad":"` + strings.Repeat("a", 100) + `"}`
	if w := post(h, body, map[string]string{"Stripe-Signature": stripeHeader(whsec, time.Now(), body)}); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", w.Code)
	}
	var deadline time.Time
	d := Stripe("PAYMENTS_WEBHOOK_SECRET", func(ctx context.Context, _ *Delivery) error {
		deadline, _ = ctx.Deadline()
		return nil
	}, Timeout(2*time.Second), WithStore(Memory(10)))
	small := `{"id":"evt_t","type":"x"}`
	if w := post(d, small, map[string]string{"Stripe-Signature": stripeHeader(whsec, time.Now(), small)}); w.Code != 200 || time.Until(deadline) > 2*time.Second || deadline.IsZero() {
		t.Fatalf("deadline: %d %v", w.Code, deadline)
	}
	long := `{"id":"` + strings.Repeat("x", MaxIDLength+1) + `","type":"x"}`
	if w := post(Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls), WithStore(Memory(10))), long, map[string]string{"Stripe-Signature": stripeHeader(whsec, time.Now(), long)}); w.Code != http.StatusBadRequest {
		t.Fatalf("long id: %d", w.Code)
	}
}

func TestSigningHelpers(t *testing.T) {
	now := time.Unix(1700000000, 0)
	body := []byte(`{"id":"evt_1"}`)
	if got, want := StripeSignature(whsec, now, body), stripeHeader(whsec, now, string(body)); got != want {
		t.Errorf("stripe: %s, want %s", got, want)
	}
	if got := HMACSignature("It's a Secret to Everybody", []byte("Hello, World!")); got != "757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17" {
		t.Errorf("hmac: %s", got)
	}
	sig := MailgunSignature(signingKey, now, "tok")
	if want := `"signature":"` + sign(signingKey, "1700000000tok") + `"`; !strings.Contains(string(sig), want) {
		t.Errorf("mailgun: %s", sig)
	}
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	m := Memory(3)
	if c, _ := m.Claim(ctx, "s", "a", time.Minute); c != Claimed {
		t.Fatal(c)
	}
	if c, _ := m.Claim(ctx, "s", "a", time.Minute); c != Busy {
		t.Fatalf("in progress: %v", c)
	}
	if c, _ := m.Claim(ctx, "s", "a", 0); c != Claimed {
		t.Fatalf("lapsed lease: %v", c)
	}
	m.Done(ctx, "s", "a")
	if c, _ := m.Claim(ctx, "s", "a", 0); c != Duplicate {
		t.Fatalf("done: %v", c)
	}
	if c, _ := m.Claim(ctx, "other", "a", time.Minute); c != Claimed {
		t.Fatalf("scopes are separate: %v", c)
	}
	for i := range 100 {
		id := strconv.Itoa(i)
		m.Claim(ctx, "s", id, time.Minute)
		if i%2 == 0 {
			m.Release(ctx, "s", id)
		}
	}
	if len(m.items) > 3 || len(m.order) > 7 {
		t.Fatalf("unbounded: %d items, %d in order", len(m.items), len(m.order))
	}
}

func TestPostgresStore(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS webhook_delivery`)
	s := Postgres(pool, time.Hour)
	if c, err := s.Claim(ctx, "stripe:X", "evt_1", time.Minute); err != nil || c != Claimed {
		t.Fatal(c, err)
	}
	if c, _ := s.Claim(ctx, "stripe:X", "evt_1", time.Minute); c != Busy {
		t.Fatalf("in progress: %v", c)
	}
	if err := s.Release(ctx, "stripe:X", "evt_1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Claim(ctx, "stripe:X", "evt_1", time.Minute); c != Claimed {
		t.Fatalf("after release: %v", c)
	}
	if err := s.Done(ctx, "stripe:X", "evt_1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Claim(ctx, "stripe:X", "evt_1", 0); c != Duplicate {
		t.Fatalf("done: %v", c)
	}
	// A lapsed claim (a node died mid-handler) is claimed again.
	s.Claim(ctx, "stripe:X", "evt_2", time.Minute)
	pool.Exec(ctx, `UPDATE webhook_delivery SET claimed_at = now() - interval '2 minutes' WHERE id = 'evt_2'`)
	if c, _ := s.Claim(ctx, "stripe:X", "evt_2", time.Minute); c != Claimed {
		t.Fatalf("lapsed: %v", c)
	}
	// Retention: old ids go on the next cleanup pass.
	pool.Exec(ctx, `UPDATE webhook_delivery SET claimed_at = now() - interval '2 hours' WHERE id = 'evt_1'`)
	s.cleanedAt = time.Time{}
	if err := s.Done(ctx, "stripe:X", "evt_2"); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery`).Scan(&n)
	if n != 1 {
		t.Fatalf("after cleanup: %d rows", n)
	}

	// With the db pack's pool among the services, endpoints use the
	// table: two endpoints (two nodes) share the delivery ids.
	t.Setenv("PAYMENTS_WEBHOOK_SECRET", whsec)
	services := lidza.NewServices()
	lidza.Provide(services, pool)
	var calls atomic.Int32
	nodeA := Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls))
	nodeB := Stripe("PAYMENTS_WEBHOOK_SECRET", counter(&calls))
	body := `{"id":"evt_shared","type":"invoice.paid"}`
	for _, h := range []http.Handler{nodeA, nodeB} {
		r := httptest.NewRequest("POST", "/api/v1/webhooks/payments", strings.NewReader(body))
		r = r.WithContext(lidza.WithServices(r.Context(), services))
		r.Header.Set("Stripe-Signature", stripeHeader(whsec, time.Now(), body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("node: %d %s", w.Code, w.Body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("handled on both nodes: %d", calls.Load())
	}
}
