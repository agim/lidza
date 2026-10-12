package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agim/lidza"
)

// Outcomes of one anonymous access check.
const (
	// Denied: the request was refused (401, 403) or the existing object
	// was hidden (404): verified for this request, at this time.
	Denied = "denied"
	// Public: an anonymous request was served.
	Public = "public"
	// Inconclusive: no verdict (a redirect, a server error, a timeout, an
	// answer this check cannot read). Never counted as denial.
	Inconclusive = "inconclusive"
)

// AccessCheck is one anonymous request and what it showed.
type AccessCheck struct {
	// Check is "object" (HEAD of the object at the bucket), "listing"
	// (listing under the store's prefix) or "public-url" (HEAD at
	// STORAGE_PUBLIC_URL, a CDN or a public bucket address).
	Check string `json:"check"`
	// Scope is what was asked, without any credential or query.
	Scope   string    `json:"scope"`
	Outcome string    `json:"outcome"`
	Status  int       `json:"status,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	At      time.Time `json:"at"`
}

// PrivacyReport is whether anonymous clients can reach an object.
type PrivacyReport struct {
	// Key is the object assessed (as the app names it).
	Key string `json:"key"`
	// Probe marks an assessment of a temporary object PrivacyProbe put
	// and removed: it says how the bucket treats that key, not proof for
	// any other key (a policy may expose only some prefixes or names).
	Probe bool `json:"probe,omitempty"`
	// Private is true only when every check is Denied; Public when any
	// is Public. Neither: inconclusive, look at Checks.
	Private bool          `json:"private"`
	Public  bool          `json:"public"`
	Checks  []AccessCheck `json:"checks"`
	At      time.Time     `json:"at"`
}

// ErrPrivacyUnsupported is Privacy on the local provider: its files are
// reachable only through the app's own routes.
var ErrPrivacyUnsupported = errors.New("storage: privacy checks need a bucket provider; the local provider's files are reachable only through the app's routes")

// Privacy checks whether anonymous clients can read the object at key:
// it confirms the object exists with an authenticated Stat, then sends,
// with no credentials, cookies or signature, a HEAD of the object, a
// listing under the store's prefix, and a HEAD at STORAGE_PUBLIC_URL
// when one is set. Nothing is downloaded, redirects are reported, not
// followed, and each request is bounded. A public URL answering
// "denied" now says nothing of copies a CDN cached earlier. Policies,
// ACLs and public-access blocks are not read.
func (s *Storage) Privacy(ctx context.Context, key string) (PrivacyReport, error) {
	if err := checkKey(key); err != nil {
		return PrivacyReport{}, err
	}
	v := s.now()
	p, ok := v.p.(*s3)
	if !ok {
		return PrivacyReport{}, ErrPrivacyUnsupported
	}
	if _, err := s.Stat(ctx, key); err != nil {
		if errors.Is(err, ErrNotFound) {
			return PrivacyReport{}, fmt.Errorf("storage: privacy: %s does not exist; an anonymous refusal of a missing object proves nothing (PrivacyProbe tests a temporary one)", key)
		}
		return PrivacyReport{}, fmt.Errorf("storage: privacy: %w", err)
	}
	r := PrivacyReport{Key: key, At: time.Now().UTC()}
	full := v.full(key)
	obj := p.objectURL(full)
	r.Checks = append(r.Checks, anonymous(ctx, v.cfg, "object", http.MethodHead, obj, true))
	list := p.objectURL("")
	if !p.pathStyle {
		list.Path, list.RawPath = "/", ""
	}
	list.RawQuery = url.Values{"list-type": {"2"}, "max-keys": {"1"}, "prefix": {v.cfg.Prefix}}.Encode()
	r.Checks = append(r.Checks, anonymous(ctx, v.cfg, "listing", http.MethodGet, list, false))
	if p.public != "" {
		if u, err := url.Parse(p.public + "/" + encodePath(full)); err == nil {
			r.Checks = append(r.Checks, anonymous(ctx, v.cfg, "public-url", http.MethodHead, u, true))
		}
	}
	r.Private, r.Public = true, false
	for _, c := range r.Checks {
		if c.Outcome != Denied {
			r.Private = false
		}
		if c.Outcome == Public {
			r.Public = true
		}
	}
	return r, nil
}

// PrivacyProbe puts a small object at key (which must not exist yet),
// assesses it with Privacy, and removes it whatever happened. Name it
// like the objects it stands for ("backups/probe.dump"): a policy may
// expose some names and not others, and the report (Probe true) only
// speaks for this key.
func (s *Storage) PrivacyProbe(ctx context.Context, key string) (PrivacyReport, error) {
	if err := checkKey(key); err != nil {
		return PrivacyReport{}, err
	}
	if _, ok := s.now().p.(*s3); !ok {
		return PrivacyReport{}, ErrPrivacyUnsupported
	}
	if _, err := s.Stat(ctx, key); err == nil {
		return PrivacyReport{}, fmt.Errorf("storage: privacy probe: %s exists; name a key that does not, so nothing is overwritten", key)
	} else if !errors.Is(err, ErrNotFound) {
		return PrivacyReport{}, fmt.Errorf("storage: privacy probe: %w", err)
	}
	b := make([]byte, 8)
	rand.Read(b)
	if _, err := s.Put(ctx, key, bytes.NewReader([]byte("lidza privacy probe "+hex.EncodeToString(b)+"\n")), PutOptions{ContentType: "text/plain"}); err != nil {
		return PrivacyReport{}, fmt.Errorf("storage: privacy probe: %w", err)
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.Delete(dctx, key); err != nil {
			lidza.Log(ctx).Error("storage: privacy probe left behind", "key", key, "error", err)
		}
	}()
	r, err := s.Privacy(ctx, key)
	r.Probe = true
	return r, err
}

// anonymousTimeout bounds one anonymous request.
const anonymousTimeout = 15 * time.Second

// anonymous sends one request with no credentials and reads the
// outcome. object says the target is the existing object (404 then
// means hidden, a denial).
func anonymous(ctx context.Context, cfg Config, check, method string, u *url.URL, object bool) AccessCheck {
	scope := *u
	scope.User, scope.RawQuery = nil, ""
	if check == "listing" {
		scope.RawQuery = "list-type=2&prefix=" + url.QueryEscape(cfg.Prefix)
	}
	c := AccessCheck{Check: check, Scope: scope.String(), At: time.Now().UTC()}
	timeout := min(cfg.Timeout, anonymousTimeout)
	if timeout <= 0 {
		timeout = anonymousTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, method, u.String(), nil)
	if err != nil {
		c.Outcome, c.Detail = Inconclusive, err.Error()
		return c
	}
	// The app's transport (a test's stub), never its cookies or auth;
	// redirects are answers, not to be followed.
	base := lidza.HTTPClient(ctx)
	client := &http.Client{Transport: base.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		c.Outcome = Inconclusive
		c.Detail = "no answer: " + strings.ReplaceAll(err.Error(), u.String(), c.Scope)
		return c
	}
	defer res.Body.Close()
	c.Status = res.StatusCode
	var body []byte
	if method == http.MethodGet {
		body, _ = io.ReadAll(io.LimitReader(res.Body, 64<<10))
	}
	switch {
	case res.StatusCode >= 300 && res.StatusCode < 400:
		c.Outcome, c.Detail = Inconclusive, "redirected (not followed): "+redactLocation(res.Header.Get("Location"))
	case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusPartialContent:
		if check == "listing" && !bytes.Contains(body, []byte("ListBucketResult")) {
			c.Outcome, c.Detail = Inconclusive, "200 without a bucket listing"
		} else {
			c.Outcome = Public
		}
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		c.Outcome = Denied
	case res.StatusCode == http.StatusNotFound && object:
		c.Outcome, c.Detail = Denied, "the existing object is hidden from anonymous requests (404)"
	default:
		c.Outcome, c.Detail = Inconclusive, "unexpected answer "+res.Status
	}
	return c
}

// redactLocation drops the query (a signature, a token) of a redirect.
func redactLocation(loc string) string {
	if u, err := url.Parse(loc); err == nil {
		u.RawQuery, u.User = "", nil
		return u.String()
	}
	return "(unreadable)"
}
