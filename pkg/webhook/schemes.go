package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// errMalformed marks a delivery that cannot be read (400), as opposed to
// one that fails verification (401).
var errMalformed = errors.New("malformed delivery")

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errMalformed, fmt.Sprintf(format, a...))
}

// verified is what a scheme learned from a delivery it accepted.
type verified struct {
	id   string
	ts   time.Time
	form url.Values
}

// scheme verifies one provider's signature.
type scheme struct {
	name   string
	verify func(e *Endpoint, secret []byte, r *http.Request, body []byte) (verified, error)
}

// Stripe verifies Stripe's Stripe-Signature header: t=<unix>,v1=<hex>,
// the HMAC-SHA256 of "<t>.<body>" under the endpoint's signing secret
// (whsec_...), any of several v1 values matching. The timestamp must lie
// in the replay window. The delivery id is the event's id.
func Stripe(setting string, h Handler, opts ...Option) *Endpoint {
	return newEndpoint(scheme{name: "stripe", verify: verifyStripe}, setting, h, opts)
}

func verifyStripe(_ *Endpoint, secret []byte, r *http.Request, body []byte) (verified, error) {
	header := r.Header.Get("Stripe-Signature")
	if header == "" {
		return verified{}, errors.New("no Stripe-Signature header")
	}
	var ts string
	var sigs [][]byte
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			if b, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return verified{}, errors.New("no timestamp in Stripe-Signature")
	}
	if len(sigs) == 0 {
		return verified{}, errors.New("no v1 signature in Stripe-Signature")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	want := mac.Sum(nil)
	match := false
	for _, s := range sigs {
		// Every candidate is compared, so the time taken does not say
		// which one matched.
		if hmac.Equal(s, want) {
			match = true
		}
	}
	if !match {
		return verified{}, errors.New("signature does not match")
	}
	var ev struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return verified{}, malformed("body is not a JSON event: %v", err)
	}
	return verified{id: ev.ID, ts: time.Unix(unix, 0)}, nil
}

// Mailgun verifies Mailgun's signature: the HMAC-SHA256 (hex) of
// timestamp and token, concatenated, under the webhook signing key. It
// reads them from the "signature" object of a JSON event webhook, or
// from the timestamp, token and signature fields of a form post (legacy
// webhooks, inbound routes). The timestamp must lie in the replay
// window; the delivery id is the token.
func Mailgun(setting string, h Handler, opts ...Option) *Endpoint {
	return newEndpoint(scheme{name: "mailgun", verify: verifyMailgun}, setting, h, opts)
}

func verifyMailgun(_ *Endpoint, secret []byte, r *http.Request, body []byte) (verified, error) {
	var timestamp, token, signature string
	var form url.Values
	mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mt {
	case "application/x-www-form-urlencoded":
		f, err := url.ParseQuery(string(body))
		if err != nil {
			return verified{}, malformed("form: %v", err)
		}
		form = f
	case "multipart/form-data":
		f, err := multipartFields(body, params["boundary"])
		if err != nil {
			return verified{}, malformed("form: %v", err)
		}
		form = f
	default:
		var ev struct {
			Signature struct {
				Timestamp json.RawMessage `json:"timestamp"`
				Token     string          `json:"token"`
				Signature string          `json:"signature"`
			} `json:"signature"`
		}
		if err := json.Unmarshal(body, &ev); err != nil {
			return verified{}, malformed("body is not JSON or a form: %v", err)
		}
		timestamp = strings.Trim(string(ev.Signature.Timestamp), `"`)
		token, signature = ev.Signature.Token, ev.Signature.Signature
	}
	if form != nil {
		timestamp, token, signature = form.Get("timestamp"), form.Get("token"), form.Get("signature")
	}
	if timestamp == "" || token == "" || signature == "" {
		return verified{}, errors.New("no Mailgun signature (timestamp, token, signature)")
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return verified{}, errors.New("Mailgun timestamp is not a number")
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return verified{}, errors.New("Mailgun signature is not hex")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte(token))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return verified{}, errors.New("signature does not match")
	}
	return verified{id: token, ts: time.Unix(unix, 0), form: form}, nil
}

// multipartFields reads the non-file fields of a multipart body, each at
// most 64 KB; file parts are skipped.
func multipartFields(body []byte, boundary string) (url.Values, error) {
	if boundary == "" {
		return nil, errors.New("no boundary")
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	out := url.Values{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if p.FormName() == "" || p.FileName() != "" {
			continue
		}
		v, err := io.ReadAll(io.LimitReader(p, 64<<10))
		if err != nil {
			return nil, err
		}
		out.Add(p.FormName(), string(v))
	}
}

// HMAC verifies an HMAC-SHA256 of the raw body in a request header, hex
// by default (Base64 for base64), after an optional Prefix: GitHub sends
// X-Hub-Signature-256: sha256=<hex>. The scheme carries no timestamp and
// no id; give one with IDHeader ("X-GitHub-Delivery") so repeats are
// dropped.
func HMAC(setting, header string, h Handler, opts ...Option) *Endpoint {
	if header == "" {
		panic("webhook: HMAC needs the signature's header name")
	}
	return newEndpoint(scheme{name: "hmac", verify: func(e *Endpoint, secret []byte, r *http.Request, body []byte) (verified, error) {
		value := r.Header.Get(header)
		if value == "" {
			return verified{}, fmt.Errorf("no %s header", header)
		}
		sig, ok := strings.CutPrefix(value, e.prefix)
		if !ok {
			return verified{}, fmt.Errorf("%s does not start with %q", header, e.prefix)
		}
		var got []byte
		var err error
		if e.base64 {
			got, err = base64.StdEncoding.DecodeString(sig)
		} else {
			got, err = hex.DecodeString(sig)
		}
		if err != nil {
			return verified{}, fmt.Errorf("%s is not encoded as expected", header)
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		if !hmac.Equal(got, mac.Sum(nil)) {
			return verified{}, errors.New("signature does not match")
		}
		return verified{}, nil
	}}, setting, h, opts)
}

// Token compares a shared secret sent in a request header, after an
// optional Prefix ("Bearer "), with the setting's value in constant time.
// The token is not a signature: the body is not covered, so use it only
// for providers that offer nothing better, over HTTPS.
func Token(setting, header string, h Handler, opts ...Option) *Endpoint {
	if header == "" {
		panic("webhook: Token needs the header name")
	}
	return newEndpoint(scheme{name: "token", verify: func(e *Endpoint, secret []byte, r *http.Request, _ []byte) (verified, error) {
		value, ok := strings.CutPrefix(r.Header.Get(header), e.prefix)
		if !ok || value == "" {
			return verified{}, fmt.Errorf("no token in %s", header)
		}
		if !equal([]byte(value), secret) {
			return verified{}, errors.New("token does not match")
		}
		return verified{}, nil
	}}, setting, h, opts)
}

// TokenField compares a shared secret sent in the JSON body, at field (a
// dotted path: "token", "auth.key"), with the setting's value in constant
// time, as some DNS and monitoring providers send it. Like Token, it
// does not cover the rest of the body.
func TokenField(setting, field string, h Handler, opts ...Option) *Endpoint {
	if field == "" {
		panic("webhook: TokenField needs the field's path")
	}
	path := strings.Split(field, ".")
	return newEndpoint(scheme{name: "token", verify: func(_ *Endpoint, secret []byte, _ *http.Request, body []byte) (verified, error) {
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return verified{}, malformed("body is not JSON: %v", err)
		}
		for _, k := range path {
			obj, ok := v.(map[string]any)
			if !ok {
				return verified{}, fmt.Errorf("no token at %s", field)
			}
			v = obj[k]
		}
		s, ok := v.(string)
		if !ok || s == "" {
			return verified{}, fmt.Errorf("no token at %s", field)
		}
		if !equal([]byte(s), secret) {
			return verified{}, errors.New("token does not match")
		}
		return verified{}, nil
	}}, setting, h, opts)
}
