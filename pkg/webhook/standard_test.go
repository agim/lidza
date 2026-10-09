package webhook

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// The example of the Standard Webhooks specification (as Svix documents
// it): the same secret, id, timestamp and body give the same signature.
func TestStandardSignatureVector(t *testing.T) {
	key, err := StandardKey("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw")
	if err != nil {
		t.Fatal(err)
	}
	got := StandardSignature(key, "msg_p5jXN8AQM9LWM0D4loKWxJek", time.Unix(1614265330, 0), []byte(`{"test": 2432232314}`))
	if got != "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=" {
		t.Fatalf("signature %s", got)
	}
}

func TestStandard(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	t.Setenv("HOOK_SECRET", secret)
	var calls atomic.Int32
	h := Standard("HOOK_SECRET", func(ctx context.Context, d *Delivery) error { calls.Add(1); return nil }, WithStore(Memory(10)))
	key, _ := StandardKey(secret)
	body := `{"type":"order.paid","data":{"id":"o1"}}`
	now := time.Now()
	headers := func(id string, ts time.Time, sig string) map[string]string {
		return map[string]string{"webhook-id": id, "webhook-timestamp": strconv.FormatInt(ts.Unix(), 10), "webhook-signature": sig}
	}
	good := StandardSignature(key, "msg_1", now, []byte(body))
	if w := post(h, body, headers("msg_1", now, good)); w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("valid: %d %s", w.Code, w.Body)
	}
	if w := post(h, body, headers("msg_1", now, good)); w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("repeat delivered twice: %d", w.Code)
	}
	// Several signatures (a rotated secret): any match is enough.
	other, _ := StandardKey("whsec_b2xkb2xkb2xkb2xkb2xkb2xkb2xk")
	two := StandardSignature(other, "msg_2", now, []byte(body)) + " " + StandardSignature(key, "msg_2", now, []byte(body))
	if w := post(h, body, headers("msg_2", now, two)); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("rotated: %d", w.Code)
	}
	refused := map[string]map[string]string{
		"tampered":   headers("msg_3", now, StandardSignature(key, "msg_3", now, []byte(body+" "))),
		"other id":   headers("msg_4", now, StandardSignature(key, "msg_x", now, []byte(body))),
		"old":        headers("msg_5", now.Add(-10*time.Minute), StandardSignature(key, "msg_5", now.Add(-10*time.Minute), []byte(body))),
		"wrong key":  headers("msg_6", now, StandardSignature(other, "msg_6", now, []byte(body))),
		"no headers": {},
	}
	for name, hdr := range refused {
		if w := post(h, body, hdr); w.Code != http.StatusUnauthorized && w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}
