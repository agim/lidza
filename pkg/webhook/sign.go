package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

// These sign a delivery the way the provider does, for an app's tests
// and for sending a delivery to `lidza dev` by hand.

// StripeSignature returns the Stripe-Signature header for body, signed
// at t with secret.
func StripeSignature(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + ",v1=" + hexHMAC(secret, []byte(ts+"."), body)
}

// MailgunSignature returns the "signature" object of a Mailgun JSON
// webhook signed at t with the signing key; the same three values are
// the form fields of a form post.
func MailgunSignature(key string, t time.Time, token string) json.RawMessage {
	ts := strconv.FormatInt(t.Unix(), 10)
	out, _ := json.Marshal(map[string]string{"timestamp": ts, "token": token, "signature": hexHMAC(key, []byte(ts), []byte(token))})
	return out
}

// HMACSignature returns the hex HMAC-SHA256 of body under secret, the
// value an HMAC endpoint expects after its Prefix.
func HMACSignature(secret string, body []byte) string { return hexHMAC(secret, body) }

func hexHMAC(secret string, parts ...[]byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	for _, p := range parts {
		m.Write(p)
	}
	return hex.EncodeToString(m.Sum(nil))
}
