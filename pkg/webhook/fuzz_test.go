package webhook

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The verifiers read headers and bodies an attacker controls: whatever
// arrives, they never panic, never accept a signature made under another
// secret, and always accept one made under the right secret.

const (
	fuzzSecret = "fuzz-secret"
	otherKey   = "another-secret"
)

func FuzzStripe(f *testing.F) {
	f.Add("t=1700000000,v1=00", []byte(`{"id":"evt_1"}`), int64(1700000000))
	f.Add("t=,v1=zz,,=,t=1", []byte(`{}`), int64(0))
	f.Fuzz(func(t *testing.T, header string, body []byte, unix int64) {
		r, _ := http.NewRequest("POST", "/", nil)
		r.Header.Set("Stripe-Signature", header)
		_, errA := verifyStripe(nil, []byte(fuzzSecret), r, body)
		_, errB := verifyStripe(nil, []byte(otherKey), r, body)
		if errA == nil && errB == nil {
			t.Fatalf("accepted under two secrets: %q", header)
		}
		r.Header.Set("Stripe-Signature", StripeSignature(otherKey, time.Unix(unix, 0), body))
		if _, err := verifyStripe(nil, []byte(fuzzSecret), r, body); err == nil {
			t.Fatal("accepted a signature made under another secret")
		}
		r.Header.Set("Stripe-Signature", StripeSignature(fuzzSecret, time.Unix(unix, 0), body))
		if _, err := verifyStripe(nil, []byte(fuzzSecret), r, body); err != nil && !errors.Is(err, errMalformed) {
			t.Fatalf("refused a valid signature: %v", err)
		}
	})
}

func FuzzStandard(f *testing.F) {
	f.Add("msg_1", "1700000000", "v1,AAAA v2,xx", []byte(`{}`))
	f.Add("", "-1", "", []byte(nil))
	f.Fuzz(func(t *testing.T, id, ts, sig string, body []byte) {
		r, _ := http.NewRequest("POST", "/", nil)
		r.Header.Set("webhook-id", id)
		r.Header.Set("webhook-timestamp", ts)
		r.Header.Set("webhook-signature", sig)
		verifyStandard(nil, []byte(fuzzSecret), r, body)
		verifyStandard(nil, []byte("whsec_"+ts), r, body)

		unix, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || id == "" || r.Header.Get("webhook-id") != id || r.Header.Get("webhook-timestamp") != ts {
			return
		}
		r.Header.Set("webhook-signature", StandardSignature([]byte(otherKey), id, time.Unix(unix, 0), body))
		if _, err := verifyStandard(nil, []byte(fuzzSecret), r, body); err == nil {
			t.Fatal("accepted a signature made under another secret")
		}
		r.Header.Set("webhook-signature", sig+" "+StandardSignature([]byte(fuzzSecret), id, time.Unix(unix, 0), body))
		if _, err := verifyStandard(nil, []byte(fuzzSecret), r, body); err != nil {
			t.Fatalf("refused a valid signature: %v", err)
		}
	})
}

func FuzzMailgun(f *testing.F) {
	f.Add("application/json", []byte(`{"signature":{"timestamp":"1","token":"t","signature":"00"}}`))
	f.Add("application/x-www-form-urlencoded", []byte("timestamp=1&token=t&signature=zz"))
	f.Add("multipart/form-data; boundary=x", []byte("--x\r\nContent-Disposition: form-data; name=\"token\"\r\n\r\nt\r\n--x--\r\n"))
	f.Fuzz(func(t *testing.T, contentType string, body []byte) {
		r, _ := http.NewRequest("POST", "/", nil)
		r.Header.Set("Content-Type", contentType)
		_, errA := verifyMailgun(nil, []byte(fuzzSecret), r, body)
		_, errB := verifyMailgun(nil, []byte(otherKey), r, body)
		if errA == nil && errB == nil {
			t.Fatalf("accepted under two secrets: %q %q", contentType, body)
		}
	})
}

func FuzzHMAC(f *testing.F) {
	f.Add("sha256=00", []byte("{}"), false)
	f.Add("sha256=AA==", []byte(""), true)
	f.Fuzz(func(t *testing.T, header string, body []byte, b64 bool) {
		opts := []Option{Prefix("sha256=")}
		if b64 {
			opts = append(opts, Base64())
		}
		e := HMAC("FUZZ_SECRET", "X-Sig", func(context.Context, *Delivery) error { return nil }, opts...)
		r, _ := http.NewRequest("POST", "/", nil)
		r.Header.Set("X-Sig", header)
		_, errA := e.scheme.verify(e, []byte(fuzzSecret), r, body)
		_, errB := e.scheme.verify(e, []byte(otherKey), r, body)
		if errA == nil && errB == nil {
			t.Fatalf("accepted under two secrets: %q", header)
		}
	})
}
