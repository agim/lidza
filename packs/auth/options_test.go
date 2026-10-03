package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/router"
)

// TestThrottleSignIn: the sign-in route has its own limit
// (AUTH_SIGNIN_RPS and AUTH_SIGNIN_BURST), apart from the other
// credential routes; unset, it uses AUTH_LOGIN_RPS and AUTH_LOGIN_BURST.
func TestThrottleSignIn(t *testing.T) {
	a := testAuth(t)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	passed := func(a *Auth, mw func(http.Handler) http.Handler) int {
		s := lidza.NewServices()
		lidza.Provide(s, a)
		h := mw(ok)
		n := 0
		for i := 0; i < 10; i++ {
			req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
			req.RemoteAddr = "10.0.0.1:1234"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req.WithContext(lidza.WithServices(req.Context(), s)))
			if rec.Code == 200 {
				n++
			}
		}
		return n
	}
	// Unset: the shared limit (burst 2).
	if n := passed(a, ThrottleSignIn()); n != 2 {
		t.Fatalf("sign-in without its own limit: %d passed, want 2", n)
	}
	own, err := New(Config{Secret: testSecret, LoginRPS: 1, LoginBurst: 2, SignInRPS: 1, SignInBurst: 6}, a.pool)
	if err != nil {
		t.Fatal(err)
	}
	if n := passed(own, ThrottleSignIn()); n != 6 {
		t.Fatalf("sign-in with its own burst: %d passed, want 6", n)
	}
	if n := passed(own, Throttle()); n != 2 {
		t.Fatalf("the other credential routes keep the shared limit: %d passed, want 2", n)
	}
	if n := passed(own, ThrottleSignInBy(func(*http.Request) string { return "one" })); n != 6 {
		t.Fatalf("keyed sign-in throttle: %d passed, want 6", n)
	}
}

// TestSessionOnly: a session opened without "remember me" sets session
// cookies (no Max-Age), and so does every renewal of it; a remembered
// one keeps AUTH_REFRESH_TTL on both.
func TestSessionOnly(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	s := lidza.NewServices()
	lidza.Provide(s, a)
	h := Require()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	renew := func(refresh string) []*http.Cookie {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
		req = req.WithContext(lidza.WithServices(req.Context(), s))
		req.AddCookie(&http.Cookie{Name: RefreshCookie, Value: refresh})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("renewal: %d", rec.Code)
		}
		return rec.Result().Cookies()
	}
	for _, sessionOnly := range []bool{true, false} {
		want := int(time.Hour.Seconds())
		if sessionOnly {
			want = 0
		}
		tokens, err := a.LoginWith(ctx, "user-1", nil, SessionOptions{SessionOnly: sessionOnly})
		if err != nil || tokens.SessionOnly != sessionOnly {
			t.Fatalf("LoginWith: %+v %v", tokens, err)
		}
		u, err := a.Verify(tokens.Access)
		if err != nil || a.SessionRemembered(ctx, u.SessionID) == sessionOnly {
			t.Fatalf("SessionRemembered, sessionOnly=%v: %v", sessionOnly, err)
		}
		cookies, renewed := a.Cookies(tokens), renew(tokens.Refresh)
		if len(cookies) != 2 || len(renewed) != 2 {
			t.Fatalf("cookies %v, renewed %v", cookies, renewed)
		}
		for _, c := range append(cookies, renewed...) {
			if c.MaxAge != want || (sessionOnly && (c.RawExpires != "" || strings.Contains(c.String(), "Max-Age"))) {
				t.Fatalf("sessionOnly=%v: cookie %s", sessionOnly, c.String())
			}
		}
	}
	// Login remembers, as before.
	tokens, err := a.Login(ctx, "user-2", nil)
	if err != nil || tokens.SessionOnly || a.Cookies(tokens)[0].MaxAge != int(time.Hour.Seconds()) {
		t.Fatalf("Login: %+v %v", tokens, err)
	}
}

// post sends a JSON body and returns the reply's status and cookies.
func post(t *testing.T, client *http.Client, u string, body any) (int, map[string]*http.Cookie) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", u, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	out := map[string]*http.Cookie{}
	for _, c := range res.Cookies() {
		out[c.Name] = c
	}
	return res.StatusCode, out
}

// TestRememberRoute: login and register take "remember"; false makes
// session cookies, absent or true remembers; a password change keeps
// the choice; ?remember=false on a provider's start does the same.
func TestRememberRoute(t *testing.T) {
	a := testAuth(t)
	issuer := newFakeIssuer(t, "client-1")
	srv, client := signinServer(t, a, Options{Providers: []Provider{OIDC("fake", "Fake", issuer.srv.URL, "client-1", "secret")}})
	api := srv.URL + Prefix
	session := func(cookies map[string]*http.Cookie) bool {
		t.Helper()
		c := cookies[AccessCookie]
		if c == nil || cookies[RefreshCookie] == nil {
			t.Fatalf("no session cookies: %v", cookies)
		}
		return c.MaxAge == 0 && cookies[RefreshCookie].MaxAge == 0
	}

	code, cookies := post(t, client, api+"/register", map[string]any{"email": "kim@example.com", "password": "correct horse battery", "remember": false})
	if code != 201 || !session(cookies) {
		t.Fatalf("register without remember: %d %v", code, cookies)
	}
	// A password change reopens the session, still without remember.
	if code, cookies := post(t, client, api+"/password", map[string]any{"current": "correct horse battery", "password": "another horse battery"}); code != 204 || !session(cookies) {
		t.Fatalf("password change: %d %v", code, cookies)
	}
	for _, tc := range []struct {
		body    map[string]any
		session bool
	}{
		{map[string]any{"remember": false}, true},
		{map[string]any{"remember": true}, false},
		{map[string]any{}, false},
	} {
		tc.body["email"], tc.body["password"] = "kim@example.com", "another horse battery"
		code, cookies := post(t, browser(), api+"/login", tc.body)
		if code != 200 || session(cookies) != tc.session {
			t.Fatalf("login %v: %d %v", tc.body, code, cookies)
		}
	}
	if code, cookies := post(t, browser(), api+"/register", map[string]any{"email": "lou@example.com", "password": "correct horse battery"}); code != 201 || session(cookies) {
		t.Fatalf("register remembers by default: %d %v", code, cookies)
	}

	// A provider: the callback's reply.
	callback := func(start string) map[string]*http.Cookie {
		t.Helper()
		c := browser()
		res, err := c.Get(start)
		if err != nil || res.StatusCode != 302 {
			t.Fatalf("start: %v %v", res, err)
		}
		res.Body.Close()
		res, err = c.Get(res.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		res, err = c.Get(res.Header.Get("Location"))
		if err != nil || res.StatusCode != 302 {
			t.Fatalf("callback: %v %v", res, err)
		}
		res.Body.Close()
		out := map[string]*http.Cookie{}
		for _, k := range res.Cookies() {
			out[k.Name] = k
		}
		return out
	}
	if !session(callback(api + "/fake/start?remember=false")) {
		t.Fatal("provider sign-in with remember=false remembered")
	}
	if session(callback(api + "/fake/start")) {
		t.Fatal("provider sign-in forgot by default")
	}
}

// TestSignInRequest: OnSignIn sees the request (its address and the
// app's cookies) and sets cookies on the reply, for a password sign-in,
// a registration and a provider callback; a refused sign-in sets none.
func TestSignInRequest(t *testing.T) {
	a := testAuth(t)
	issuer := newFakeIssuer(t, "client-1")
	var mu sync.Mutex
	var seen []string
	refuse := false
	opt := Options{
		Providers: []Provider{OIDC("fake", "Fake", issuer.srv.URL, "client-1", "secret")},
		OnSignIn: func(ctx context.Context, s SignIn) error {
			mu.Lock()
			defer mu.Unlock()
			pending := ""
			if c, err := s.Request.Cookie("pending"); err == nil {
				pending = c.Value
			}
			seen = append(seen, s.Method+" "+pending+" "+s.Request.RemoteAddr)
			s.SetCookie(&http.Cookie{Name: "notice", Value: "joined-2-teams", Path: "/"})
			s.SetCookie(&http.Cookie{Name: "pending", Value: "", Path: "/", MaxAge: -1})
			if refuse {
				return router.Errorf(http.StatusForbidden, "not now")
			}
			return nil
		},
	}
	srv, _ := signinServer(t, a, opt)
	api := srv.URL + Prefix
	u, _ := url.Parse(srv.URL)
	withPending := func() *http.Client {
		c := browser()
		c.Jar.SetCookies(u, []*http.Cookie{{Name: "pending", Value: "join-team-7", Path: "/"}})
		return c
	}
	check := func(what string, c *http.Client, method string) {
		t.Helper()
		mu.Lock()
		last := seen[len(seen)-1]
		mu.Unlock()
		if !strings.HasPrefix(last, method+" join-team-7 127.0.0.1:") {
			t.Fatalf("%s: hook saw %q", what, last)
		}
		jar := map[string]string{}
		for _, k := range c.Jar.Cookies(u) {
			jar[k.Name] = k.Value
		}
		if jar["notice"] != "joined-2-teams" || jar["pending"] != "" || jar[AccessCookie] == "" {
			t.Fatalf("%s: cookies %v", what, jar)
		}
	}

	c := withPending()
	if code, _ := post(t, c, api+"/register", map[string]any{"email": "max@example.com", "password": "correct horse battery"}); code != 201 {
		t.Fatalf("register: %d", code)
	}
	check("registration", c, MethodPassword)
	c = withPending()
	if code, _ := post(t, c, api+"/login", map[string]any{"email": "max@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	check("password sign-in", c, MethodPassword)
	c = withPending()
	if _, code := follow(t, c, api+"/fake/start"); code != 302 {
		t.Fatalf("provider: %d", code)
	}
	check("provider sign-in", c, "fake")

	// Refused: no session, and none of the hook's cookies.
	mu.Lock()
	refuse = true
	mu.Unlock()
	code, cookies := post(t, withPending(), api+"/login", map[string]any{"email": "max@example.com", "password": "correct horse battery"})
	if code != 403 || len(cookies) != 0 {
		t.Fatalf("refused sign-in: %d %v", code, cookies)
	}
}

// TestNoVerifyEmail: registration sends the verification link unless
// Options.NoVerifyEmail; the verify route still takes a link the app
// issues itself.
func TestNoVerifyEmail(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS mail_message`)
	if _, err := a.pool.Exec(ctx, mail.OutboxTable); err != nil {
		t.Fatal(err)
	}
	m, err := mail.New(mail.Config{Provider: "outbox", From: "app@example.com", AppURL: "https://app.example.com"}, a.pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	sent := func() int { return rows(t, a, `SELECT count(*) FROM mail_message`) }

	srv, _ := signinServerWith(t, a, Options{}, m)
	one := browser()
	if code, _ := post(t, one, srv.URL+Prefix+"/register", map[string]any{"email": "one@example.com", "password": "correct horse battery"}); code != 201 || sent() != 1 {
		t.Fatalf("default: %d, %d sent", code, sent())
	}
	// A new link on request, while the address is unverified.
	if code, _ := post(t, one, srv.URL+Prefix+"/verify/resend", map[string]any{}); code != 204 || sent() != 2 {
		t.Fatalf("resend: %d, %d sent", code, sent())
	}
	first, _ := a.ProfileByEmail(ctx, "one@example.com")
	if err := a.MarkVerified(ctx, first.Subject); err != nil {
		t.Fatal(err)
	}
	if code, _ := post(t, one, srv.URL+Prefix+"/verify/resend", map[string]any{}); code != 204 || sent() != 2 {
		t.Fatalf("resend when verified: %d, %d sent", code, sent())
	}
	var welcomed []string
	srv, _ = signinServerWith(t, a, Options{NoVerifyEmail: true, OnSignUp: func(ctx context.Context, _ pgx.Tx, s SignUp) error {
		welcomed = append(welcomed, s.Profile.Email)
		return nil
	}}, m)
	two := browser()
	if code, _ := post(t, two, srv.URL+Prefix+"/register", map[string]any{"email": "two@example.com", "password": "correct horse battery"}); code != 201 || sent() != 2 || len(welcomed) != 1 {
		t.Fatalf("NoVerifyEmail: %d, %d sent, welcomed %v", code, sent(), welcomed)
	}
	if code, _ := post(t, two, srv.URL+Prefix+"/verify/resend", map[string]any{}); code != 204 || sent() != 2 {
		t.Fatalf("resend with NoVerifyEmail: %d, %d sent", code, sent())
	}
	p, err := a.ProfileByEmail(ctx, "two@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.IssueToken(ctx, PurposeVerifyEmail, p.Subject, 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := post(t, browser(), srv.URL+Prefix+"/verify", map[string]any{"token": token}); code != 204 {
		t.Fatalf("verify an app-sent link: %d", code)
	}
	if p, _ := a.Profile(ctx, p.Subject); !p.Verified() {
		t.Fatal("not verified")
	}
}

// An app that has not run the migration adding auth_session.remember
// still signs people in and refreshes: a remembered session never
// needs the column.
func TestSignInBeforeRememberMigration(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `ALTER TABLE auth_session DROP COLUMN remember`); err != nil {
		t.Fatal(err)
	}
	tok, err := a.Login(ctx, "user-1", nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	next, err := a.Refresh(ctx, tok.Refresh, nil)
	if err != nil || next.SessionOnly {
		t.Fatalf("refresh: %+v %v", next, err)
	}
	var id string
	a.pool.QueryRow(ctx, `SELECT id FROM auth_session WHERE subject = 'user-1' LIMIT 1`).Scan(&id)
	if !a.SessionRemembered(ctx, id) {
		t.Fatal("a session without the column is not remembered")
	}
}
