package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// StandardSecretPrefix starts a Standard Webhooks secret: whsec_ and the
// key in base64.
const StandardSecretPrefix = "whsec_"

// StandardKey reads a Standard Webhooks secret ("whsec_<base64>") as the
// HMAC key; a secret without the prefix is used as it is.
func StandardKey(secret string) ([]byte, error) {
	b64, ok := strings.CutPrefix(secret, StandardSecretPrefix)
	if !ok {
		return []byte(secret), nil
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("webhook: the secret after whsec_ is not base64")
	}
	return key, nil
}

// StandardSignature is the webhook-signature value of a delivery under
// the Standard Webhooks scheme (standardwebhooks.com): "v1," and the
// base64 HMAC-SHA256 of "<id>.<unix timestamp>.<body>" under key. The
// hooks pack signs with it; Standard verifies it.
func StandardSignature(key []byte, id string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + strconv.FormatInt(ts.Unix(), 10) + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Standard verifies the Standard Webhooks headers (webhook-id,
// webhook-timestamp, webhook-signature: space-separated "v1,<base64>"
// values, any matching) under the setting's secret ("whsec_..."), the
// scheme Līdza's hooks pack sends and Svix, Resend and others use. The
// timestamp must lie within the tolerance; webhook-id drops repeats.
func Standard(setting string, h Handler, opts ...Option) *Endpoint {
	return newEndpoint(scheme{name: "standard", verify: verifyStandard}, setting, h, opts)
}

func verifyStandard(_ *Endpoint, secret []byte, r *http.Request, body []byte) (verified, error) {
	id, ts, sig := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), r.Header.Get("webhook-signature")
	if id == "" || ts == "" || sig == "" {
		return verified{}, errors.New("no webhook-id, webhook-timestamp or webhook-signature header")
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return verified{}, errors.New("webhook-timestamp is not a Unix time")
	}
	key, err := StandardKey(string(secret))
	if err != nil {
		return verified{}, err
	}
	want := StandardSignature(key, id, time.Unix(unix, 0), body)
	match := false
	for _, s := range strings.Fields(sig) {
		// Every candidate is compared, so the time taken does not say
		// which one matched.
		if hmac.Equal([]byte(s), []byte(want)) {
			match = true
		}
	}
	if !match {
		return verified{}, errors.New("signature does not match")
	}
	return verified{id: id, ts: time.Unix(unix, 0)}, nil
}
