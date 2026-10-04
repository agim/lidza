package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/credentials"
)

// fakeGitHub is a local GitHub: the token endpoint (codes good, narrow,
// fail; refresh tokens r1 rotating to r2, revoked refused), /user, a
// repository listing that needs the token, and grant revocation.
type fakeGitHub struct {
	srv       *httptest.Server
	expiresIn int // seconds; 0 issues tokens that do not expire
	refreshes atomic.Int32
	mu        sync.Mutex
	revoked   []string
	verifiers []string
}

const ghClientID, ghClientSecret = "conn-client", "conn-secret"

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_id") != ghClientID || r.Form.Get("client_secret") != ghClientSecret {
			json.NewEncoder(w).Encode(map[string]string{"error": "incorrect_client_credentials"})
			return
		}
		reply := map[string]any{"token_type": "bearer"}
		switch {
		case r.Form.Get("grant_type") == "refresh_token":
			f.refreshes.Add(1)
			time.Sleep(50 * time.Millisecond) // concurrent callers overlap
			switch r.Form.Get("refresh_token") {
			case "r1":
				reply["access_token"], reply["refresh_token"], reply["scope"] = "gho_SECRETACCESS2", "r2", "repo,admin:repo_hook"
				reply["expires_in"] = 28800
			default:
				json.NewEncoder(w).Encode(map[string]string{"error": "bad_refresh_token"})
				return
			}
		case r.Form.Get("code") == "good":
			f.mu.Lock()
			f.verifiers = append(f.verifiers, r.Form.Get("code_verifier"))
			f.mu.Unlock()
			reply["access_token"], reply["scope"] = "gho_SECRETACCESS1", "repo,admin:repo_hook"
			if f.expiresIn > 0 {
				reply["expires_in"], reply["refresh_token"] = f.expiresIn, "r1"
			}
		case r.Form.Get("code") == "narrow":
			reply["access_token"], reply["scope"] = "gho_SECRETNARROW", "public_repo"
		default:
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer gho_SECRET") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"id": 4242, "login": "octo"}`))
	})
	mux.HandleFunc("GET /user/repos", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer gho_SECRETACCESS1" && auth != "Bearer gho_SECRETACCESS2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[{"full_name": "octo/private-app", "private": true}]`))
	})
	mux.HandleFunc("DELETE /applications/{id}/grant", func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		var body struct {
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if r.PathValue("id") != ghClientID || user != ghClientID || pass != ghClientSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.revoked = append(f.revoked, body.AccessToken)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) connector() *GitHubConnector {
	c := GitHubConnect(ghClientID, ghClientSecret)
	c.base, c.api = f.srv.URL, f.srv.URL
	return c
}

// connectApp mounts sign-in with the fake GitHub as a connector, on
// fresh auth_connection rows, with a master key for the sealing, and
// captures the default logger.
func connectApp(t *testing.T, f *fakeGitHub) (*Auth, string, *bytes.Buffer) {
	t.Helper()
	a := testAuth(t)
	ctx := context.Background()
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS auth_connection`)
	if _, err := a.pool.Exec(ctx, ConnectionTable); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credentials.EnvMasterKey, strings.Repeat("ab", 32))
	logs := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	srv, _ := signinServer(t, a, Options{Connectors: []Connector{f.connector()}, AfterConnect: "/settings"})
	return a, srv.URL + Prefix, logs
}

// user registers a signed-in browser and returns it with the subject.
func user(t *testing.T, api, email string) (*http.Client, string) {
	t.Helper()
	b := browser()
	code, out := call(t, b, "POST", api+"/register", map[string]string{"email": email, "password": "correct horse battery"})
	if code != 201 {
		t.Fatalf("register %s: %d %v", email, code, out)
	}
	return b, out["subject"].(string)
}

// start begins a connection and returns the provider's authorization
// URL.
func start(t *testing.T, b *http.Client, api string) *url.URL {
	t.Helper()
	res, err := b.Get(api + "/connect/github/start?redirect=/integrations")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("start: %d", res.StatusCode)
	}
	u, _ := url.Parse(res.Header.Get("Location"))
	return u
}

// back is the provider sending the browser back; it returns where the
// app lands.
func back(t *testing.T, b *http.Client, api string, q url.Values) *url.URL {
	t.Helper()
	res, err := b.Get(api + "/connect/github/callback?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("callback: %d", res.StatusCode)
	}
	u, _ := url.Parse(res.Header.Get("Location"))
	return u
}

// An authenticated user connects GitHub with repository and webhook
// scopes; the server then calls GitHub with the kept token, which the
// browser, the replies and the logs never see; another user cannot
// reach the connection; disconnecting revokes it at GitHub.
func TestConnectGitHub(t *testing.T) {
	f := newFakeGitHub(t)
	a, api, logs := connectApp(t, f)
	ctx := context.Background()
	alice, aliceID := user(t, api, "alice@example.com")

	authURL := start(t, alice, api)
	q := authURL.Query()
	if authURL.Path != "/login/oauth/authorize" || q.Get("scope") != "repo admin:repo_hook" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || !strings.HasSuffix(q.Get("redirect_uri"), "/api/v1/auth/connect/github/callback") {
		t.Fatalf("authorization URL: %s", authURL)
	}
	landed := back(t, alice, api, url.Values{"code": {"good"}, "state": {q.Get("state")}})
	if landed.Path != "/integrations" || landed.Query().Get("connected") != "github" {
		t.Fatalf("landed on %s", landed)
	}
	// PKCE: the verifier sent at the exchange answers the challenge.
	if len(f.verifiers) != 1 || s256(f.verifiers[0]) != q.Get("code_challenge") {
		t.Fatalf("PKCE verifier %v for challenge %s", f.verifiers, q.Get("code_challenge"))
	}

	// The list: the account and scopes, no token.
	res, _ := alice.Get(api + "/connections")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var list []Connection
	json.Unmarshal(body, &list)
	if res.StatusCode != 200 || len(list) != 1 || list[0].Provider != "github" || list[0].Subject != "4242" || list[0].Login != "octo" || strings.Join(list[0].Scopes, " ") != "repo admin:repo_hook" || list[0].Expiry != nil {
		t.Fatalf("connections: %d %s", res.StatusCode, body)
	}

	// The server calls GitHub as the user.
	conn, err := a.Connection(ctx, aliceID, "github")
	if err != nil {
		t.Fatal(err)
	}
	res, err = conn.Client().Get(f.srv.URL + "/user/repos")
	if err != nil {
		t.Fatal(err)
	}
	repos, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(repos), "octo/private-app") {
		t.Fatalf("repos as the user: %d %s", res.StatusCode, repos)
	}

	// Sealed at rest.
	var sealed string
	a.pool.QueryRow(ctx, `SELECT access_sealed FROM auth_connection WHERE owner = $1`, aliceID).Scan(&sealed)
	if sealed == "" || strings.Contains(sealed, "SECRET") {
		t.Fatalf("stored token not sealed: %q", sealed)
	}

	// Another user reaches nothing of it.
	bob, bobID := user(t, api, "bob@example.com")
	if _, err := a.Connection(ctx, bobID, "github"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("bob's connection: %v", err)
	}
	if code, _ := call(t, bob, "DELETE", api+"/connections/github", nil); code != 404 {
		t.Fatalf("bob deletes alice's connection: %d", code)
	}
	res, _ = bob.Get(api + "/connections")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("bob's list: %s", body)
	}

	// A grant prints, marshals and logs without its tokens.
	g, _ := (&pgConnections{a: a}).Get(ctx, aliceID, "github")
	slog.Info("grant", "grant", g)
	asJSON, _ := json.Marshal(g)
	for _, s := range []string{fmt.Sprintf("%v", g), fmt.Sprintf("%+v", g), fmt.Sprintf("%#v", g), string(asJSON), logs.String()} {
		if strings.Contains(s, "SECRET") {
			t.Fatalf("a token leaked: %s", s)
		}
	}

	// Disconnect: revoked at GitHub, forgotten here.
	if code, _ := call(t, alice, "DELETE", api+"/connections/github", nil); code != 200 && code != 204 {
		t.Fatalf("disconnect: %d", code)
	}
	if len(f.revoked) != 1 || f.revoked[0] != "gho_SECRETACCESS1" {
		t.Fatalf("revoked at GitHub: %v", f.revoked)
	}
	if _, err := a.Connection(ctx, aliceID, "github"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("after disconnect: %v", err)
	}
	if strings.Contains(logs.String(), "SECRET") {
		t.Fatalf("a token in the logs:\n%s", logs.String())
	}
}

// The round trip refuses what it must, and keeps nothing then.
func TestConnectRefusals(t *testing.T) {
	f := newFakeGitHub(t)
	a, api, _ := connectApp(t, f)
	ctx := context.Background()
	alice, aliceID := user(t, api, "alice@example.com")
	connected := func() bool {
		var n int
		a.pool.QueryRow(ctx, `SELECT count(*) FROM auth_connection`).Scan(&n)
		return n > 0
	}
	reason := func(u *url.URL) string { return u.Query().Get("connect_error") }

	// Denied consent.
	state := start(t, alice, api).Query().Get("state")
	if r := reason(back(t, alice, api, url.Values{"error": {"access_denied"}, "state": {state}})); r != "denied" {
		t.Fatalf("denied: %q", r)
	}
	// A state that is not this round trip's, and none at all.
	start(t, alice, api)
	if r := reason(back(t, alice, api, url.Values{"code": {"good"}, "state": {"forged"}})); r != "state" {
		t.Fatalf("forged state: %q", r)
	}
	if r := reason(back(t, browserWithSession(alice, api), api, url.Values{"code": {"good"}, "state": {state}})); r != "state" {
		t.Fatalf("no cookie: %q", r)
	}
	// Missing scopes: refused, and withdrawn at GitHub.
	state = start(t, alice, api).Query().Get("state")
	if r := reason(back(t, alice, api, url.Values{"code": {"narrow"}, "state": {state}})); r != "scopes" {
		t.Fatalf("missing scopes: %q", r)
	}
	if len(f.revoked) != 1 || f.revoked[0] != "gho_SECRETNARROW" {
		t.Fatalf("narrow grant not withdrawn: %v", f.revoked)
	}
	// A failed exchange.
	state = start(t, alice, api).Query().Get("state")
	if r := reason(back(t, alice, api, url.Values{"code": {"fail"}, "state": {state}})); r != "provider" {
		t.Fatalf("exchange failure: %q", r)
	}
	if connected() {
		t.Fatal("a refused round trip kept a connection")
	}

	// Replayed: the cookie and state of a finished round trip.
	state = start(t, alice, api).Query().Get("state")
	cookie := connectCookie(alice, api)
	if r := reason(back(t, alice, api, url.Values{"code": {"good"}, "state": {state}})); r != "" {
		t.Fatalf("good round trip: %q", r)
	}
	setConnectCookie(alice, api, cookie)
	if r := reason(back(t, alice, api, url.Values{"code": {"good"}, "state": {state}})); r != "state" {
		t.Fatalf("replayed callback: %q", r)
	}
	// Expired: a sealed trip past its time.
	expired, _ := a.seal(connectTrip{Provider: "github", State: "x", Owner: aliceID, Redirect: "/", Expires: time.Now().Add(-time.Minute).Unix()})
	setConnectCookie(alice, api, expired)
	if r := reason(back(t, alice, api, url.Values{"code": {"good"}, "state": {"x"}})); r != "state" {
		t.Fatalf("expired trip: %q", r)
	}
	// Another user's round trip: Alice's cookie and state in Bob's
	// browser.
	bob, _ := user(t, api, "bob@example.com")
	state = start(t, alice, api).Query().Get("state")
	setConnectCookie(bob, api, connectCookie(alice, api))
	if r := reason(back(t, bob, api, url.Values{"code": {"good"}, "state": {state}})); r != "state" {
		t.Fatalf("cross-user state: %q", r)
	}
	// Signed out: no round trip at all.
	res, _ := browser().Get(api + "/connect/github/start")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed-out start: %d", res.StatusCode)
	}
}

func connectCookie(b *http.Client, api string) string {
	u, _ := url.Parse(api + "/connect/github/callback")
	for _, c := range b.Jar.Cookies(u) {
		if c.Name == ConnectCookie {
			return c.Value
		}
	}
	return ""
}

func setConnectCookie(b *http.Client, api, value string) {
	u, _ := url.Parse(api + "/connect/")
	b.Jar.SetCookies(u, []*http.Cookie{{Name: ConnectCookie, Value: value, Path: Prefix + "/connect/"}})
}

// browserWithSession is b's session without its connect cookie.
func browserWithSession(b *http.Client, api string) *http.Client {
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(api)
	var keep []*http.Cookie
	for _, c := range b.Jar.Cookies(u) {
		if c.Name != ConnectCookie {
			keep = append(keep, &http.Cookie{Name: c.Name, Value: c.Value, Path: "/"})
		}
	}
	jar.SetCookies(u, keep)
	return &http.Client{Jar: jar, CheckRedirect: b.CheckRedirect}
}

// An expiring grant is renewed once for concurrent callers, its refresh
// token rotated; a refresh GitHub refuses marks the connection for
// reconnecting.
func TestConnectRefresh(t *testing.T) {
	f := newFakeGitHub(t)
	f.expiresIn = 30 // under the minute of margin: renewed at first use
	a, api, logs := connectApp(t, f)
	ctx := context.Background()
	alice, aliceID := user(t, api, "alice@example.com")
	state := start(t, alice, api).Query().Get("state")
	if r := back(t, alice, api, url.Values{"code": {"good"}, "state": {state}}).Query().Get("connect_error"); r != "" {
		t.Fatalf("connect: %q", r)
	}
	conn, err := a.Connection(ctx, aliceID, "github")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	tokens := make([]string, 5)
	errs := make([]error, 5)
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = conn.Token(ctx)
		}(i)
	}
	wg.Wait()
	for i := range tokens {
		if errs[i] != nil || tokens[i] != "gho_SECRETACCESS2" {
			t.Fatalf("caller %d: %q %v", i, tokens[i], errs[i])
		}
	}
	if n := f.refreshes.Load(); n != 1 {
		t.Fatalf("%d refreshes for concurrent callers", n)
	}
	g, _ := (&pgConnections{a: a}).Get(ctx, aliceID, "github")
	if g.RefreshToken() != "r2" || g.Expiry.Before(time.Now().Add(time.Hour)) {
		t.Fatalf("rotation not kept: %s refresh=%v expiry=%s", g, g.RefreshToken() == "r2", g.Expiry)
	}

	// Revoked at GitHub: the next renewal is refused.
	a.pool.Exec(ctx, `UPDATE auth_connection SET expires_at = now()`)
	(&pgConnections{a: a}).Put(ctx, aliceID, g.WithTokens(g.AccessToken(), "revoked"))
	a.pool.Exec(ctx, `UPDATE auth_connection SET expires_at = now()`)
	if _, err := conn.Token(ctx); !errors.Is(err, ErrReconnect) {
		t.Fatalf("refused refresh: %v", err)
	}
	if _, err := a.Connection(ctx, aliceID, "github"); !errors.Is(err, ErrReconnect) {
		t.Fatalf("connection after a refused refresh: %v", err)
	}
	res, _ := alice.Get(api + "/connections")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `"reconnect":true`) {
		t.Fatalf("list after a refused refresh: %s", body)
	}
	if strings.Contains(logs.String(), "SECRET") || strings.Contains(logs.String(), "r2") && strings.Contains(logs.String(), "refresh_token") {
		t.Fatalf("a token in the logs:\n%s", logs.String())
	}
}

// Sign-in with GitHub keeps its own scopes when GitHub is connectable
// too.
func TestSignInScopesUnchanged(t *testing.T) {
	u, _ := GitHub("id", "secret").AuthURL(context.Background(), AuthParams{RedirectURI: "https://app.example.com/cb", State: "s"})
	if !strings.Contains(u, "scope=read%3Auser+user%3Aemail") {
		t.Fatalf("sign-in scopes changed: %s", u)
	}
}

// AUTH_CONNECT builds the connectors; a missing secret is a warning.
func TestConnectorsFromEnv(t *testing.T) {
	cs, warnings := ConnectorsFromEnv(map[string]string{"AUTH_CONNECT": "github, calendar", "AUTH_CONNECT_GITHUB_CLIENT_ID": "id", "AUTH_CONNECT_GITHUB_CLIENT_SECRET": "s", "AUTH_CONNECT_GITHUB_SCOPES": "repo read:org"})
	if len(cs) != 1 || cs[0].Name() != "github" || strings.Join(cs[0].Scopes(), " ") != "repo read:org" {
		t.Fatalf("connectors: %v", cs)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "calendar") {
		t.Fatalf("warnings: %v", warnings)
	}
}

// An app booted without connectors configures one later (saved settings,
// then Reconfigure): its routes answer without a restart, a provider not
// configured is 404, and removing the configuration closes it again.
func TestConnectConfiguredLater(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS auth_connection`)
	if _, err := a.pool.Exec(ctx, ConnectionTable); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credentials.EnvMasterKey, strings.Repeat("ab", 32))
	t.Setenv("AUTH_CONNECT", "")
	srv, _ := signinServer(t, a, Options{Providers: []Provider{}})
	api := srv.URL + Prefix
	alice, _ := user(t, api, "alice@example.com")
	status := func(method, path string) int {
		req, _ := http.NewRequest(method, api+path, nil)
		req.Header.Set("Content-Type", "application/json")
		res, err := alice.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := status("GET", "/connect/github/start"); c != http.StatusNotFound {
		t.Fatalf("start before configuration: %d", c)
	}
	if c := status("GET", "/connections"); c != http.StatusOK {
		t.Fatalf("list before configuration: %d", c)
	}

	t.Setenv("AUTH_CONNECT", "github")
	t.Setenv("AUTH_CONNECT_GITHUB_CLIENT_ID", "Iv1.later")
	t.Setenv("AUTH_CONNECT_GITHUB_CLIENT_SECRET", "later-secret")
	if err := a.Reconfigure(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := alice.Get(api + "/connect/github/start")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || loc.Host != "github.com" || loc.Query().Get("client_id") != "Iv1.later" {
		t.Fatalf("start after configuration: %d %s", res.StatusCode, loc)
	}
	if c := status("GET", "/connect/gitlab/start"); c != http.StatusNotFound {
		t.Fatalf("a provider not configured: %d", c)
	}
	if c := status("DELETE", "/connections/github"); c != http.StatusNotFound {
		t.Fatalf("disconnect without a connection: %d", c)
	}
	// Signed out: no connection routes.
	if res, _ := browser().Get(api + "/connect/github/start"); res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed out: %v", res)
	}

	t.Setenv("AUTH_CONNECT", "")
	a.Reconfigure(ctx)
	if c := status("GET", "/connect/github/start"); c != http.StatusNotFound {
		t.Fatalf("start after removal: %d", c)
	}
}
