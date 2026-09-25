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
	if _, err := pool.Exec(ctx, SessionTable); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Secret: testSecret, AccessTTL: time.Minute, RefreshTTL: time.Hour}, pool)
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
	if _, err := a.Refresh(ctx, tokens.Refresh, nil); err == nil {
		t.Fatal("old refresh token still valid after rotation")
	}
	if err := a.Logout(ctx, u.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refresh(ctx, rotated.Refresh, nil); err == nil {
		t.Fatal("refresh after logout")
	}

	// Middleware: bearer, cookie, CSRF rule, missing.
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
	if len(rec.Result().Cookies()) != 2 || !rec.Result().Cookies()[0].HttpOnly || len(ClearedCookies()) != 2 {
		t.Fatal("cookies")
	}
}
