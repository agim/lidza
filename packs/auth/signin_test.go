package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/router"
)

// signinServer mounts the sign-in on a router with the pack in the
// context and serves it; the client keeps cookies and stops at
// redirects so the test reads them.
func signinServer(t *testing.T, a *Auth, opt Options) (*httptest.Server, *http.Client) {
	t.Helper()
	// The tests make many credential calls from one address: a relaxed
	// throttle on the instance the routes see (the same tables).
	relaxed, err := New(Config{Secret: testSecret, AccessTTL: time.Minute, RefreshTTL: time.Hour, LoginRPS: 1000, LoginBurst: 1000, MinPasswordLength: 10, TokenTTL: time.Hour}, a.pool)
	if err != nil {
		t.Fatal(err)
	}
	s := lidza.NewServices()
	lidza.Provide(s, relaxed)
	r := router.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(lidza.WithServices(req.Context(), s)))
		})
	})
	Mount(r, opt)
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return srv, client
}

func call(t *testing.T, client *http.Client, method, u string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	if len(raw) > 0 {
		json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out
}

func TestSignInLocal(t *testing.T) {
	a := testAuth(t)
	srv, client := signinServer(t, a, Options{})
	api := srv.URL + Prefix

	// A visitor.
	if code, out := call(t, client, "GET", api+"/session", nil); code != 200 || out["user"] != nil {
		t.Fatalf("visitor session: %d %v", code, out)
	}
	if code, out := call(t, client, "GET", api+"/providers", nil); code != 200 || out["local"] != true || out["register"] != true {
		t.Fatalf("providers: %d %v", code, out)
	}

	// Registration signs in: cookies for the browser, a token in the reply.
	code, out := call(t, client, "POST", api+"/register", map[string]string{"email": "Ana@Example.com", "password": "correct horse battery", "name": "Ana"})
	if code != 201 || out["email"] != "ana@example.com" || out["name"] != "Ana" || out["verified"] != false || out["accessToken"] == "" {
		t.Fatalf("register: %d %v", code, out)
	}
	subject := out["subject"].(string)
	if p, _ := out["providers"].([]any); len(p) != 1 || p[0] != "password" {
		t.Fatalf("providers after register: %v", out["providers"])
	}
	if code, out := call(t, client, "GET", api+"/me", nil); code != 200 || out["subject"] != subject {
		t.Fatalf("me: %d %v", code, out)
	}
	if code, out := call(t, client, "GET", api+"/session", nil); code != 200 || out["user"] == nil {
		t.Fatalf("session: %d %v", code, out)
	}
	// The first account is the admin candidate.
	if first, _ := a.FirstSubject(context.Background()); first != subject {
		t.Fatalf("first subject %q, want %q", first, subject)
	}

	// Refusals.
	if code, _ := call(t, client, "POST", api+"/register", map[string]string{"email": "ana@example.com", "password": "another long password"}); code != 409 {
		t.Fatalf("taken email: %d", code)
	}
	if code, out := call(t, client, "POST", api+"/register", map[string]string{"email": "not-an-email", "password": "x"}); code != 422 || out["error"] != "validation" {
		t.Fatalf("invalid registration: %d %v", code, out)
	}
	if code, _ := call(t, client, "POST", api+"/register", map[string]string{"email": "b@example.com", "password": "short"}); code != 422 {
		t.Fatalf("weak password accepted: %d", code)
	}

	// Sign out, then in.
	if code, _ := call(t, client, "POST", api+"/logout", nil); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, out := call(t, client, "GET", api+"/session", nil); code != 200 || out["user"] != nil {
		t.Fatalf("session after logout: %d %v", code, out)
	}
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "ana@example.com", "password": "wrong password here"}); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "nobody@example.com", "password": "wrong password here"}); code != 401 {
		t.Fatalf("unknown email: %d", code)
	}
	if code, out := call(t, client, "POST", api+"/login", map[string]string{"email": "ANA@example.com", "password": "correct horse battery"}); code != 200 || out["subject"] != subject {
		t.Fatalf("login: %d %v", code, out)
	}

	// Verification and reset links through the tokens (no mail pack here:
	// the routes still work with a token issued directly).
	tok, _ := a.IssueToken(context.Background(), PurposeVerifyEmail, subject, 0)
	if code, _ := call(t, client, "POST", api+"/verify", map[string]string{"token": tok}); code != 204 {
		t.Fatalf("verify: %d", code)
	}
	if code, out := call(t, client, "GET", api+"/me", nil); code != 200 || out["verified"] != true {
		t.Fatalf("verified: %d %v", code, out)
	}
	if code, _ := call(t, client, "POST", api+"/verify", map[string]string{"token": tok}); code != 401 {
		t.Fatalf("token reuse: %d", code)
	}
	if code, _ := call(t, client, "POST", api+"/forgot", map[string]string{"email": "nobody@example.com"}); code != 204 {
		t.Fatalf("forgot for an unknown address must not reveal it: %d", code)
	}
	reset, _ := a.IssueToken(context.Background(), PurposeResetPassword, subject, 0)
	if code, _ := call(t, client, "POST", api+"/reset", map[string]string{"token": reset, "password": "a brand new password"}); code != 204 {
		t.Fatalf("reset: %d", code)
	}
	// The reset ended every session.
	if code, _ := call(t, client, "GET", api+"/me", nil); code != 401 {
		t.Fatalf("session survived the reset: %d", code)
	}
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "ana@example.com", "password": "a brand new password"}); code != 200 {
		t.Fatalf("login with the new password: %d", code)
	}

	// Password change keeps this session.
	if code, _ := call(t, client, "POST", api+"/password", map[string]string{"current": "wrong", "password": "yet another password"}); code != 403 {
		t.Fatalf("change with a wrong current password: %d", code)
	}
	if code, _ := call(t, client, "POST", api+"/password", map[string]string{"current": "a brand new password", "password": "yet another password"}); code != 204 {
		t.Fatalf("change: %d", code)
	}
	if code, _ := call(t, client, "GET", api+"/me", nil); code != 200 {
		t.Fatalf("session after the change: %d", code)
	}

	// A disabled account cannot sign in.
	if err := a.Disable(context.Background(), subject); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "ana@example.com", "password": "yet another password"}); code != 403 {
		t.Fatalf("disabled login: %d", code)
	}
}

func TestSignInOptions(t *testing.T) {
	a := testAuth(t)
	srv, client := signinServer(t, a, Options{NoRegister: true, RequireVerified: true})
	api := srv.URL + Prefix
	if code, _ := call(t, client, "POST", api+"/register", map[string]string{"email": "x@example.com", "password": "correct horse battery"}); code != 404 {
		t.Fatalf("register with NoRegister: %d", code)
	}
	if _, err := a.CreateUser(context.Background(), "Invited@example.com", "Inv", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "invited@example.com", "password": "correct horse battery"}); code != 403 {
		t.Fatalf("unverified login with RequireVerified: %d", code)
	}
	p, _ := a.ProfileByEmail(context.Background(), "invited@example.com")
	a.MarkVerified(context.Background(), p.Subject)
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "invited@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("verified login: %d", code)
	}
	if code, out := call(t, client, "GET", api+"/providers", nil); code != 200 || out["register"] != false {
		t.Fatalf("providers: %d %v", code, out)
	}
}

// fakeIssuer is an OpenID Connect provider: discovery, an authorize
// endpoint that sends the browser back at once, a token endpoint that
// checks the PKCE verifier and signs an ID token, and its keys.
type fakeIssuer struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	user     map[string]any
	// seen records what authorize received.
	challenge, nonce string
	tokens           int
}

func newFakeIssuer(t *testing.T, clientID string) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: key, clientID: clientID, user: map[string]any{"sub": "u-1", "email": "ana@example.com", "email_verified": true, "name": "Ana"}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/keys"})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != clientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.challenge, f.nonce = q.Get("code_challenge"), q.Get("nonce")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=code-1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "code-1" || r.Form.Get("client_secret") != "secret" || s256(r.Form.Get("code_verifier")) != f.challenge {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		f.tokens++
		claims := jwt.MapClaims{"iss": f.srv.URL, "aud": clientID, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": f.nonce}
		for k, v := range f.user {
			claims[k] = v
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		signed, _ := tok.SignedString(f.key)
		json.NewEncoder(w).Encode(map[string]string{"id_token": signed, "access_token": "at", "token_type": "Bearer"})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		pub := f.key.PublicKey
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		}}})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// follow performs the round trip a browser would: start, the provider,
// the callback; it returns the callback's redirect target.
func follow(t *testing.T, client *http.Client, start string) (string, int) {
	t.Helper()
	res, err := client.Get(start)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 {
		return "", res.StatusCode
	}
	// The provider.
	res, err = client.Get(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("provider replied %d", res.StatusCode)
	}
	// The callback.
	res, err = client.Get(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.Header.Get("Location"), res.StatusCode
}

func TestSignInOIDC(t *testing.T) {
	a := testAuth(t)
	issuer := newFakeIssuer(t, "client-1")
	provider := OIDC("fake", "Fake", issuer.srv.URL, "client-1", "secret")
	srv, client := signinServer(t, a, Options{Providers: []Provider{provider}, AfterSignIn: "/home"})
	api := srv.URL + Prefix

	if code, out := call(t, client, "GET", api+"/providers", nil); code != 200 || !strings.Contains(fmt.Sprint(out["providers"]), "/api/v1/auth/fake/start") {
		t.Fatalf("providers: %d %v", code, out)
	}
	to, code := follow(t, client, api+"/fake/start?redirect=/after")
	if code != 302 || to != "/after" {
		t.Fatalf("callback: %d %q", code, to)
	}
	code, out := call(t, client, "GET", api+"/me", nil)
	if code != 200 || out["email"] != "ana@example.com" || out["verified"] != true || out["name"] != "Ana" {
		t.Fatalf("me after provider sign-in: %d %v", code, out)
	}
	subject := out["subject"].(string)
	if p := fmt.Sprint(out["providers"]); p != "[fake]" {
		t.Fatalf("providers: %s", p)
	}
	if u := CurrentUser(context.Background()); u != nil {
		t.Fatal("no user expected in a bare context")
	}

	// Signing in again lands on the same account, at the default path.
	call(t, client, "POST", api+"/logout", nil)
	if to, code := follow(t, client, api+"/fake/start"); code != 302 || to != "/home" {
		t.Fatalf("second callback: %d %q", code, to)
	}
	if code, out := call(t, client, "GET", api+"/me", nil); code != 200 || out["subject"] != subject {
		t.Fatalf("second sign-in: %d %v", code, out)
	}
	ids, err := a.Identities(context.Background(), subject)
	if err != nil || len(ids) != 1 || ids[0].Provider != "fake" || ids[0].Subject != "u-1" {
		t.Fatalf("identities: %v %v", ids, err)
	}

	// A local account with the same, vouched-for address is joined, not
	// duplicated; the local account becomes verified.
	call(t, client, "POST", api+"/logout", nil)
	local, err := a.CreateUser(context.Background(), "bo@example.com", "", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	issuer.user = map[string]any{"sub": "u-2", "email": "Bo@example.com", "email_verified": true, "name": "Bo"}
	if _, code := follow(t, client, api+"/fake/start"); code != 302 {
		t.Fatalf("callback: %d", code)
	}
	code, out = call(t, client, "GET", api+"/me", nil)
	if code != 200 || out["subject"] != local.Subject || out["verified"] != true || fmt.Sprint(out["providers"]) != "[password fake]" {
		t.Fatalf("joined account: %d %v", code, out)
	}

	// An address the provider does not vouch for stays apart.
	call(t, client, "POST", api+"/logout", nil)
	issuer.user = map[string]any{"sub": "u-3", "email": "bo@example.com", "email_verified": false, "name": "Impostor"}
	if _, code := follow(t, client, api+"/fake/start"); code != 302 {
		t.Fatalf("callback: %d", code)
	}
	if code, out := call(t, client, "GET", api+"/me", nil); code != 200 || out["subject"] == local.Subject || out["email"] != nil {
		t.Fatalf("unverified address joined a local account: %d %v", code, out)
	}

	// A disabled account is refused at the callback.
	call(t, client, "POST", api+"/logout", nil)
	a.Disable(context.Background(), subject)
	issuer.user = map[string]any{"sub": "u-1", "email": "ana@example.com", "email_verified": true}
	if to, code := follow(t, client, api+"/fake/start"); code != 302 || to != "/login?error=disabled" {
		t.Fatalf("disabled: %d %q", code, to)
	}

	// Without the cookie, or with another state, the callback refuses.
	bare := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ := bare.Get(api + "/fake/callback?code=code-1&state=x")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/login?error=state" {
		t.Fatalf("callback without state: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = client.Get(api + "/fake/start")
	res.Body.Close()
	res, _ = client.Get(api + "/fake/callback?code=code-1&state=other")
	res.Body.Close()
	if res.Header.Get("Location") != "/login?error=state" {
		t.Fatalf("callback with a wrong state: %q", res.Header.Get("Location"))
	}
	// Denied at the provider.
	res, _ = client.Get(api + "/fake/start")
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	res, _ = client.Get(api + "/fake/callback?error=access_denied&state=" + url.QueryEscape(loc.Query().Get("state")))
	res.Body.Close()
	if res.Header.Get("Location") != "/login?error=denied" {
		t.Fatalf("denied: %q", res.Header.Get("Location"))
	}
	// An unknown provider is a 404.
	res, _ = client.Get(api + "/nope/start")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("unknown provider: %d", res.StatusCode)
	}
}

func TestSignInGitHub(t *testing.T) {
	a := testAuth(t)
	mux := http.NewServeMux()
	var gh *httptest.Server
	mux.HandleFunc("GET /login/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=gh-code&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "gh-code" || r.Form.Get("client_secret") != "gh-secret" {
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "gh-token"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": "octo", "name": "Octo Cat"})
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"email": "old@example.com", "primary": false, "verified": true}, {"email": "octo@example.com", "primary": true, "verified": true}})
	})
	gh = httptest.NewServer(mux)
	t.Cleanup(gh.Close)
	provider := GitHub("gh-id", "gh-secret")
	provider.base, provider.api = gh.URL, gh.URL
	srv, client := signinServer(t, a, Options{Providers: []Provider{provider}})
	api := srv.URL + Prefix
	if to, code := follow(t, client, api+"/github/start"); code != 302 || to != "/" {
		t.Fatalf("callback: %d %q", code, to)
	}
	code, out := call(t, client, "GET", api+"/me", nil)
	if code != 200 || out["email"] != "octo@example.com" || out["name"] != "Octo Cat" || out["verified"] != true || fmt.Sprint(out["providers"]) != "[github]" {
		t.Fatalf("me: %d %v", code, out)
	}
	ids, _ := a.Identities(context.Background(), out["subject"].(string))
	if len(ids) != 1 || ids[0].Subject != "42" {
		t.Fatalf("identities: %+v", ids)
	}
}

func TestProvidersFromEnv(t *testing.T) {
	provs, warnings := ProvidersFromEnv(map[string]string{
		"AUTH_PROVIDERS":        "google, github,microsoft,okta,acme,missing",
		"AUTH_GOOGLE_CLIENT_ID": "g", "AUTH_GOOGLE_CLIENT_SECRET": "gs",
		"AUTH_GITHUB_CLIENT_ID": "h", "AUTH_GITHUB_CLIENT_SECRET": "hs",
		"AUTH_MICROSOFT_CLIENT_ID": "m", "AUTH_MICROSOFT_CLIENT_SECRET": "ms", "AUTH_MICROSOFT_TENANT": "contoso",
		"AUTH_OKTA_CLIENT_ID": "o", "AUTH_OKTA_CLIENT_SECRET": "os", "AUTH_OKTA_ISSUER": "https://acme.okta.com/oauth2/default", "AUTH_OKTA_LABEL": "Okta SSO",
		"AUTH_ACME_CLIENT_ID": "a", "AUTH_ACME_CLIENT_SECRET": "as",
	})
	var names []string
	for _, p := range provs {
		names = append(names, p.Name()+"="+p.Label())
	}
	if got := strings.Join(names, " "); got != "google=Google github=GitHub microsoft=Microsoft okta=Okta SSO" {
		t.Fatalf("providers: %s", got)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "AUTH_ACME_ISSUER") || !strings.Contains(warnings[1], "AUTH_MISSING_CLIENT_ID") {
		t.Fatalf("warnings: %v", warnings)
	}
	ms := provs[2].(*OIDCProvider)
	if ms.skipIssuer || !strings.Contains(ms.issuer, "contoso") {
		t.Fatalf("microsoft tenant: %+v", ms)
	}
	if Microsoft("", "m", "s").skipIssuer != true {
		t.Fatal("common tenant must skip the issuer check")
	}
	if p, _ := ProvidersFromEnv(map[string]string{}); len(p) != 0 {
		t.Fatal("no providers expected")
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/x": "/x", "//evil.com": "/", "https://evil.com": "/", "/\\evil": "/"} {
		if got := localPath(in, "/"); got != want {
			t.Errorf("localPath(%q) = %q, want %q", in, got, want)
		}
	}
}
