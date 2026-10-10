package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza"
)

// TestParallelRefreshKeepsOneToken: a page that fires several requests
// at once after the access token expired sends the same refresh cookie
// on each. Exactly one rotates; every reply the browser may keep last
// names a refresh token that still works after the grace period.
func TestParallelRefreshKeepsOneToken(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	tokens, err := a.Login(ctx, "u-parallel", nil)
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	got := make([]Tokens, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = a.Refresh(ctx, tokens.Refresh, nil)
		}()
	}
	wg.Wait()
	rotated := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("refresh %d: %v", i, errs[i])
		}
		if got[i].Refresh != "" {
			rotated++
		}
	}
	if rotated != 1 {
		t.Fatalf("%d of %d parallel refreshes rotated the session; the browser keeps whichever reply comes last, and the others' tokens die after the grace", rotated, n)
	}
	// Past the grace, the token the one rotation issued still works.
	a.pool.Exec(ctx, `UPDATE auth_session SET rotated_at = now() - interval '1 hour' WHERE subject = 'u-parallel'`)
	for i := range n {
		if got[i].Refresh != "" {
			if _, err := a.Refresh(ctx, got[i].Refresh, nil); err != nil {
				t.Fatalf("the rotated token died: %v", err)
			}
		}
	}
}

// TestSessionSurvival: a session ends when it is ended or unused for
// RefreshTTL, not because the database hiccuped, a request came without
// the refresh cookie, or the sign-in was long ago.
func TestSessionSurvival(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	a.cfg.AccessTTL = -time.Minute // born expired: every request renews
	tokens, err := a.Login(ctx, "u-survive", nil)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.AccessTTL = time.Minute // the renewed ones are not
	s := lidza.NewServices()
	lidza.Provide(s, a)
	call := func(h http.Handler, access, refresh string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
		req = req.WithContext(lidza.WithServices(req.Context(), s))
		if access != "" {
			req.AddCookie(&http.Cookie{Name: AccessCookie, Value: access})
		}
		if refresh != "" {
			req.AddCookie(&http.Cookie{Name: RefreshCookie, Value: refresh})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	cleared := func(rec *httptest.ResponseRecorder) bool {
		for _, c := range rec.Result().Cookies() {
			if c.Name == RefreshCookie && c.MaxAge < 0 {
				return true
			}
		}
		return false
	}

	// No refresh cookie on this request (a link from another site to a
	// page the server renders): signed out for it, cookies kept.
	if rec := call(Optional()(ok), tokens.Access, ""); rec.Code != 200 || cleared(rec) {
		t.Fatalf("missing refresh cookie cleared the session: %d", rec.Code)
	}
	if rec := call(Require()(ok), tokens.Access, ""); rec.Code != 401 || cleared(rec) {
		t.Fatalf("missing refresh cookie on a required route: %d cleared=%v", rec.Code, cleared(rec))
	}

	// The refresh cookie is Lax, like the access cookie.
	for _, c := range a.Cookies(tokens) {
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("%s is SameSite %v", c.Name, c.SameSite)
		}
	}

	// The session slides: renewing moves its end RefreshTTL past now.
	a.pool.Exec(ctx, `UPDATE auth_session SET expires_at = now() + interval '1 minute' WHERE subject = 'u-survive'`)
	rec := call(Require()(ok), tokens.Access, tokens.Refresh)
	if rec.Code != 200 {
		t.Fatalf("renew: %d", rec.Code)
	}
	var left time.Duration
	var until time.Time
	a.pool.QueryRow(ctx, `SELECT expires_at FROM auth_session WHERE subject = 'u-survive'`).Scan(&until)
	left = time.Until(until)
	if left < a.cfg.RefreshTTL-time.Minute {
		t.Fatalf("the session ends in %v after use, want about %v", left, a.cfg.RefreshTTL)
	}
	var refresh string
	for _, c := range rec.Result().Cookies() {
		if c.Name == RefreshCookie {
			refresh = c.Value
		}
	}

	// The database failing is a 503, not a sign-out.
	broken, _ := New(a.cfg, a.pool)
	broken.pool.Close()
	s2 := lidza.NewServices()
	lidza.Provide(s2, broken)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	req = req.WithContext(lidza.WithServices(req.Context(), s2))
	req.AddCookie(&http.Cookie{Name: AccessCookie, Value: tokens.Access})
	req.AddCookie(&http.Cookie{Name: RefreshCookie, Value: refresh})
	rec = httptest.NewRecorder()
	Require()(ok).ServeHTTP(rec, req)
	if rec.Code != 503 || cleared(rec) {
		t.Fatalf("database down: %d cleared=%v", rec.Code, cleared(rec))
	}
}

// TestLostRenewalReply: the browser never got the reply that rotated its
// refresh token; the old one still renews within the grace, not after.
func TestLostRenewalReply(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	tokens, err := a.Login(ctx, "u-lost", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refresh(ctx, tokens.Refresh, nil); err != nil { // the reply that got lost
		t.Fatal(err)
	}
	a.pool.Exec(ctx, `UPDATE auth_session SET rotated_at = now() - interval '14 minutes' WHERE subject = 'u-lost'`)
	if g, err := a.Refresh(ctx, tokens.Refresh, nil); err != nil || g.Access == "" {
		t.Fatalf("old token 14 minutes after a lost reply: %v", err)
	}
	a.pool.Exec(ctx, `UPDATE auth_session SET rotated_at = now() - interval '16 minutes' WHERE subject = 'u-lost'`)
	if _, err := a.Refresh(ctx, tokens.Refresh, nil); err != ErrSessionExpired {
		t.Fatalf("old token past the grace: %v", err)
	}
}
