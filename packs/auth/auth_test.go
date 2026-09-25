package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func testAuth(t *testing.T) *Auth {
	t.Helper()
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS auth_session`)
	pool.Exec(ctx, `DROP TABLE IF EXISTS auth_token`)
	if _, err := pool.Exec(ctx, SessionTable+TokenTable); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Secret: testSecret, AccessTTL: time.Minute, RefreshTTL: time.Hour, LoginRPS: 1, LoginBurst: 2, MinPasswordLength: 10, TokenTTL: time.Hour}, pool)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPasswords(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil || !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("%q %v", h, err)
	}
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong") || CheckPassword("garbage", "x") {
		t.Fatal("check")
	}
}

func TestSessionsAndMiddleware(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	tokens, err := a.Login(ctx, "user-1", map[string]any{"role": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.Verify(tokens.Access)
	if err != nil || u.ID != "user-1" || u.Claims["role"] != "admin" || u.SessionID == "" {
		t.Fatalf("verify: %+v %v", u, err)
	}
	// A second Auth with the same secret (a restarted node) accepts it.
	other, _ := New(Config{Secret: testSecret}, a.pool)
	if _, err := other.Verify(tokens.Access); err != nil {
		t.Fatalf("token not portable across restarts: %v", err)
	}
	wrong, _ := New(Config{Secret: strings.Repeat("x", 32)}, a.pool)
	if _, err := wrong.Verify(tokens.Access); err == nil {
		t.Fatal("wrong secret accepted")
	}

	rotated, err := a.Refresh(ctx, tokens.Refresh, nil)
	if err != nil || rotated.Refresh == tokens.Refresh {
		t.Fatalf("refresh: %v", err)
	}
	// The replaced token works for RefreshGrace, for an access token only.
	if g, err := a.Refresh(ctx, tokens.Refresh, nil); err != nil || g.Refresh != "" || g.Access == "" {
		t.Fatalf("old refresh token within the grace: %+v %v", g, err)
	}
	if err := a.Logout(ctx, u.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refresh(ctx, rotated.Refresh, nil); err == nil {
		t.Fatal("refresh after logout")
	}

	// Middleware: bearer, cookie, CSRF rule, missing. The session above
	// was logged out, so a live one is needed.
	tokens, err = a.Login(ctx, "user-1", map[string]any{"role": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := lidza.NewServices()
	lidza.Provide(s, a)
	var seen *User
	h := Require()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = CurrentUser(r.Context()) }))
	call := func(build func(*http.Request)) int {
		req := httptest.NewRequest("POST", "/api/v1/things", nil)
		req = req.WithContext(lidza.WithServices(req.Context(), s))
		build(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tokens.Access) }); code != 200 || seen == nil || seen.ID != "user-1" {
		t.Fatalf("bearer: %d %+v", code, seen)
	}
	if code := call(func(r *http.Request) {}); code != 401 {
		t.Fatalf("missing: %d", code)
	}
	// An access token stops working the moment its session ends.
	ended, _ := a.Login(ctx, "user-2", nil)
	if code := call(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+ended.Access) }); code != 200 {
		t.Fatalf("fresh session: %d", code)
	}
	if err := a.RevokeAll(ctx, "user-2"); err != nil {
		t.Fatal(err)
	}
	if code := call(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+ended.Access) }); code != 401 {
		t.Fatalf("revoked session still accepted: %d", code)
	}
	if code := call(func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	if code := call(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: AccessCookie, Value: tokens.Access}) }); code != 403 {
		t.Fatalf("cookie POST without JSON content type should be refused: %d", code)
	}
	if code := call(func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: AccessCookie, Value: tokens.Access})
		r.Header.Set("Content-Type", "application/json")
	}); code != 200 {
		t.Fatalf("cookie with JSON: %d", code)
	}
	if code := call(func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: AccessCookie, Value: tokens.Access})
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}); code != 200 {
		t.Fatalf("cookie from a same-origin fetch: %d", code)
	}
	if code := call(func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: AccessCookie, Value: tokens.Access})
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); code != 403 {
		t.Fatalf("cookie from a cross-site request: %d", code)
	}

	var optionalUser *User
	optional := Optional()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { optionalUser = CurrentUser(r.Context()) }))
	anon := httptest.NewRequest(http.MethodGet, "/", nil)
	rec0 := httptest.NewRecorder()
	optional.ServeHTTP(rec0, anon.WithContext(lidza.WithServices(ctx, s)))
	if rec0.Code != 200 || optionalUser != nil {
		t.Fatalf("optional without token: %d %+v", rec0.Code, optionalUser)
	}
	withToken := httptest.NewRequest(http.MethodGet, "/", nil)
	withToken.Header.Set("Authorization", "Bearer "+tokens.Access)
	rec0 = httptest.NewRecorder()
	optional.ServeHTTP(rec0, withToken.WithContext(lidza.WithServices(ctx, s)))
	if rec0.Code != 200 || optionalUser == nil || optionalUser.ID != "user-1" {
		t.Fatalf("optional with token: %d %+v", rec0.Code, optionalUser)
	}
	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	bad.Header.Set("Authorization", "Bearer nope")
	rec0 = httptest.NewRecorder()
	optional.ServeHTTP(rec0, bad.WithContext(lidza.WithServices(ctx, s)))
	if rec0.Code != 401 {
		t.Fatalf("optional with a bad token: %d", rec0.Code)
	}

	rec := httptest.NewRecorder()
	a.SetCookies(rec, tokens)
	if len(rec.Result().Cookies()) != 2 || !rec.Result().Cookies()[0].HttpOnly || len(ClearedCookies()) != 3 {
		t.Fatal("cookies")
	}
}

func TestTokens(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	tok, err := a.IssueToken(ctx, PurposeVerifyEmail, "a@example.com", 0)
	if err != nil || len(tok) < 32 {
		t.Fatal(tok, err)
	}
	if _, err := a.ConsumeToken(ctx, PurposeResetPassword, tok); err == nil {
		t.Fatal("wrong purpose accepted")
	}
	subject, err := a.ConsumeToken(ctx, PurposeVerifyEmail, tok)
	if err != nil || subject != "a@example.com" {
		t.Fatal(subject, err)
	}
	if _, err := a.ConsumeToken(ctx, PurposeVerifyEmail, tok); err == nil {
		t.Fatal("token reused")
	}
	// A new token invalidates the previous one for the same purpose and subject.
	first, _ := a.IssueToken(ctx, PurposeResetPassword, "b@example.com", 0)
	second, _ := a.IssueToken(ctx, PurposeResetPassword, "b@example.com", 0)
	if _, err := a.ConsumeToken(ctx, PurposeResetPassword, first); err == nil {
		t.Fatal("superseded token accepted")
	}
	if _, err := a.ConsumeToken(ctx, PurposeResetPassword, second); err != nil {
		t.Fatal(err)
	}
	expired, _ := a.IssueToken(ctx, PurposeVerifyEmail, "c@example.com", time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, err := a.ConsumeToken(ctx, PurposeVerifyEmail, expired); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestPasswordPolicy(t *testing.T) {
	a := &Auth{cfg: Config{MinPasswordLength: 10}}
	for pw, want := range map[string]string{
		"short":                  "at least 10 characters",
		"password123":            "too common",
		"Password123":            "too common",
		"agim@example.com":       "must not be the email address",
		"correct horse battery":  "",
		"a very long passphrase": "",
	} {
		err := a.ValidatePassword(pw, "agim@example.com")
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: unexpected %v", pw, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: got %v, want %q", pw, err, want)
		}
	}
}

func TestThrottle(t *testing.T) {
	a := testAuth(t)
	s := lidza.NewServices()
	lidza.Provide(s, a)
	h := Throttle()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	codes := []int{}
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(lidza.WithServices(req.Context(), s)))
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || codes[3] != 429 {
		t.Fatalf("burst 2 then limited: %v", codes)
	}
	other := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	other.RemoteAddr = "10.0.0.2:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, other.WithContext(lidza.WithServices(other.Context(), s)))
	if rec.Code != 200 {
		t.Fatalf("another client limited: %d", rec.Code)
	}
}

// TestSlidingSession: a browser whose access token expired is renewed by
// the middleware from the refresh cookie, claims included; requests in
// flight with the old refresh token get an access token for a minute; a
// logged-out session is not renewed.
func TestSlidingSession(t *testing.T) {
	a := testAuth(t)
	a.cfg.AccessTTL = -time.Minute // this access token is born expired
	ctx := context.Background()
	tokens, err := a.Login(ctx, "user-1", map[string]any{"role": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(tokens.Access); err == nil {
		t.Fatal("expired token verified")
	}
	a.cfg.AccessTTL = time.Minute // the renewed ones are not
	s := lidza.NewServices()
	lidza.Provide(s, a)
	var seen *User
	h := Require()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = CurrentUser(r.Context()) }))
	call := func(access, refresh string) *httptest.ResponseRecorder {
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
	if rec := call(tokens.Access, ""); rec.Code != 401 {
		t.Fatalf("expired token without refresh cookie: %d", rec.Code)
	}
	rec := call(tokens.Access, tokens.Refresh)
	if rec.Code != 200 || seen == nil || seen.ID != "user-1" || seen.Claims["role"] != "admin" {
		t.Fatalf("renewed: %d %+v", rec.Code, seen)
	}
	var newAccess, newRefresh string
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case AccessCookie:
			newAccess = c.Value
		case RefreshCookie:
			newRefresh = c.Value
		}
		if c.Path != "/" {
			t.Errorf("cookie %s path %q", c.Name, c.Path)
		}
	}
	if newAccess == "" || newAccess == tokens.Access || newRefresh == "" || newRefresh == tokens.Refresh {
		t.Fatalf("cookies not rotated: %v", rec.Result().Cookies())
	}
	// A parallel request still carrying the old refresh token: an access
	// token, no rotation.
	rec = call("", tokens.Refresh)
	if rec.Code != 200 {
		t.Fatalf("grace: %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == RefreshCookie {
			t.Fatalf("grace refresh rotated the session again: %v", c)
		}
	}
	if seen == nil || seen.ID != "user-1" || seen.Claims != nil {
		t.Fatalf("grace without an access token: %+v (no claims to carry over)", seen)
	}
	if rec := call("", "nope"); rec.Code != 401 {
		t.Fatalf("bad refresh cookie: %d", rec.Code)
	}
	if err := a.RevokeAll(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	if rec := call(tokens.Access, newRefresh); rec.Code != 401 {
		t.Fatalf("revoked session renewed: %d", rec.Code)
	}
}
