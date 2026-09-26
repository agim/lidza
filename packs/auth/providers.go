package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is what a provider says about the user who signed in.
type Identity struct {
	// Provider is the provider's name; Subject its stable id for the user.
	Provider, Subject string
	Email             string
	// EmailVerified is the provider vouching for the address: only then
	// does the identity join a local account with the same email.
	EmailVerified bool
	Name, Picture string
}

// AuthParams are the per-request values of one provider round trip.
type AuthParams struct {
	// RedirectURI is this app's callback, registered with the provider.
	RedirectURI string
	// State and Nonce bind the callback to the browser that started it;
	// CodeVerifier is the PKCE secret (its S256 challenge goes out).
	State, Nonce, CodeVerifier string
	Client                     *http.Client
}

// Provider is an external sign-in. The pack ships OIDC issuers (Google,
// Microsoft, any issuer with discovery) and GitHub; an app adds another
// by implementing it and listing it in Options.Providers.
type Provider interface {
	// Name is the route segment and the identity's provider ("google").
	Name() string
	// Label is the button text ("Google").
	Label() string
	// AuthURL is where the browser goes to sign in.
	AuthURL(ctx context.Context, p AuthParams) (string, error)
	// Exchange turns the callback's code into the identity, verifying
	// what the provider signed.
	Exchange(ctx context.Context, p AuthParams, code string) (Identity, error)
}

// ProvidersFromEnv builds the providers AUTH_PROVIDERS names (comma
// separated), each with AUTH_<NAME>_CLIENT_ID and
// AUTH_<NAME>_CLIENT_SECRET; google, github and microsoft
// (AUTH_MICROSOFT_TENANT, "common" by default) are known, any other
// name needs AUTH_<NAME>_ISSUER (an OIDC issuer with discovery) and may
// set AUTH_<NAME>_LABEL. A provider with a missing credential is left
// out and named in the warnings.
func ProvidersFromEnv(values map[string]string) ([]Provider, []string) {
	var out []Provider
	var warnings []string
	for _, name := range strings.Split(values["AUTH_PROVIDERS"], ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		key := "AUTH_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
		id, secret := values[key+"CLIENT_ID"], values[key+"CLIENT_SECRET"]
		if id == "" || secret == "" {
			warnings = append(warnings, fmt.Sprintf("provider %s: %sCLIENT_ID and %sCLIENT_SECRET are needed (lidza credentials set, or the admin pages)", name, key, key))
			continue
		}
		switch name {
		case "google":
			out = append(out, Google(id, secret))
		case "github":
			out = append(out, GitHub(id, secret))
		case "microsoft":
			out = append(out, Microsoft(values[key+"TENANT"], id, secret))
		default:
			issuer := values[key+"ISSUER"]
			if issuer == "" {
				warnings = append(warnings, fmt.Sprintf("provider %s: %sISSUER is needed (the OIDC issuer URL)", name, key))
				continue
			}
			label := values[key+"LABEL"]
			if label == "" {
				label = strings.ToUpper(name[:1]) + name[1:]
			}
			out = append(out, OIDC(name, label, issuer, id, secret))
		}
	}
	return out, warnings
}

// OIDCProvider is an OpenID Connect issuer with discovery: the
// authorization code flow with PKCE, the ID token verified against the
// issuer's keys (RS256 or ES256), its nonce and audience checked.
type OIDCProvider struct {
	name, label  string
	issuer       string
	clientID     string
	clientSecret string
	scopes       []string
	// skipIssuer accepts any issuer in the ID token (Microsoft's common
	// endpoint signs with the tenant's issuer, not the one discovered).
	skipIssuer bool

	mu     sync.Mutex
	disc   *discovery
	keys   map[string]any
	keysAt time.Time
}

type discovery struct {
	Issuer        string `json:"issuer"`
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	JWKS          string `json:"jwks_uri"`
	UserInfo      string `json:"userinfo_endpoint"`
}

// OIDC returns a provider for an issuer (https://accounts.google.com,
// https://login.example.com/realms/app) that publishes
// /.well-known/openid-configuration. The scopes default to openid,
// email and profile.
func OIDC(name, label, issuer, clientID, clientSecret string, scopes ...string) *OIDCProvider {
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	return &OIDCProvider{name: name, label: label, issuer: strings.TrimSuffix(issuer, "/"), clientID: clientID, clientSecret: clientSecret, scopes: scopes}
}

// Google is sign-in with Google: an OAuth client of type "Web
// application" in Google Cloud, with the app's callback
// (<APP_URL>/api/v1/auth/google/callback) among its redirect URIs.
func Google(clientID, clientSecret string) *OIDCProvider {
	return OIDC("google", "Google", "https://accounts.google.com", clientID, clientSecret)
}

// Microsoft is sign-in with a Microsoft account or Entra ID: an app
// registration with the callback as a Web redirect URI. The tenant is
// "common" (any account), "organizations", "consumers" or a tenant id.
func Microsoft(tenant, clientID, clientSecret string) *OIDCProvider {
	if tenant == "" {
		tenant = "common"
	}
	p := OIDC("microsoft", "Microsoft", "https://login.microsoftonline.com/"+tenant+"/v2.0", clientID, clientSecret)
	switch tenant {
	case "common", "organizations", "consumers":
		p.skipIssuer = true
	}
	return p
}

// Name implements Provider.
func (o *OIDCProvider) Name() string { return o.name }

// Label implements Provider.
func (o *OIDCProvider) Label() string { return o.label }

func (o *OIDCProvider) discover(ctx context.Context, client *http.Client) (*discovery, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.disc != nil {
		return o.disc, nil
	}
	var d discovery
	if err := getJSON(ctx, client, o.issuer+"/.well-known/openid-configuration", "", &d); err != nil {
		return nil, fmt.Errorf("%s: discovery: %w", o.name, err)
	}
	if d.Authorization == "" || d.Token == "" || d.JWKS == "" {
		return nil, fmt.Errorf("%s: discovery document lacks the endpoints", o.name)
	}
	o.disc = &d
	return o.disc, nil
}

// AuthURL implements Provider.
func (o *OIDCProvider) AuthURL(ctx context.Context, p AuthParams) (string, error) {
	d, err := o.discover(ctx, p.Client)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.clientID},
		"redirect_uri":          {p.RedirectURI},
		"scope":                 {strings.Join(o.scopes, " ")},
		"state":                 {p.State},
		"nonce":                 {p.Nonce},
		"code_challenge":        {s256(p.CodeVerifier)},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.Authorization, "?") {
		sep = "&"
	}
	return d.Authorization + sep + q.Encode(), nil
}

// Exchange implements Provider.
func (o *OIDCProvider) Exchange(ctx context.Context, p AuthParams, code string) (Identity, error) {
	d, err := o.discover(ctx, p.Client)
	if err != nil {
		return Identity{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.RedirectURI},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
		"code_verifier": {p.CodeVerifier},
	}
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := postForm(ctx, p.Client, d.Token, form, &tok); err != nil {
		return Identity{}, fmt.Errorf("%s: token: %w", o.name, err)
	}
	if tok.Error != "" {
		return Identity{}, fmt.Errorf("%s: token: %s %s", o.name, tok.Error, tok.ErrorDesc)
	}
	if tok.IDToken == "" {
		return Identity{}, fmt.Errorf("%s: token reply has no id_token", o.name)
	}
	claims, err := o.verifyIDToken(ctx, p.Client, d, tok.IDToken, p.Nonce)
	if err != nil {
		return Identity{}, fmt.Errorf("%s: id_token: %w", o.name, err)
	}
	id := Identity{Provider: o.name, Subject: str(claims["sub"]), Email: str(claims["email"]), Name: str(claims["name"]), Picture: str(claims["picture"])}
	if v, ok := claims["email_verified"].(bool); ok {
		id.EmailVerified = v
	} else if s, ok := claims["email_verified"].(string); ok {
		id.EmailVerified = s == "true"
	}
	if id.Email == "" && d.UserInfo != "" && tok.AccessToken != "" {
		var info map[string]any
		if err := getJSON(ctx, p.Client, d.UserInfo, tok.AccessToken, &info); err == nil {
			id.Email = str(info["email"])
			if v, ok := info["email_verified"].(bool); ok {
				id.EmailVerified = v
			}
			if id.Name == "" {
				id.Name = str(info["name"])
			}
		}
	}
	if id.Subject == "" {
		return Identity{}, fmt.Errorf("%s: id_token has no sub", o.name)
	}
	return id, nil
}

func (o *OIDCProvider) verifyIDToken(ctx context.Context, client *http.Client, d *discovery, raw, nonce string) (jwt.MapClaims, error) {
	keyOf := func(t *jwt.Token) (any, error) {
		switch t.Method.Alg() {
		case "RS256", "ES256":
		default:
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		key, err := o.key(ctx, client, d.JWKS, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}
	opts := []jwt.ParserOption{jwt.WithAudience(o.clientID), jwt.WithExpirationRequired(), jwt.WithLeeway(time.Minute)}
	if !o.skipIssuer {
		opts = append(opts, jwt.WithIssuer(d.Issuer))
	}
	parsed, err := jwt.Parse(raw, keyOf, opts...)
	if err != nil {
		return nil, err
	}
	claims, _ := parsed.Claims.(jwt.MapClaims)
	if str(claims["nonce"]) != nonce {
		return nil, errors.New("nonce does not match")
	}
	return claims, nil
}

// key returns the issuer's key kid, fetching the key set when it is
// unknown (at most once a minute).
func (o *OIDCProvider) key(ctx context.Context, client *http.Client, jwksURL, kid string) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if k, ok := o.keys[kid]; ok {
		return k, nil
	}
	if o.keys != nil && time.Since(o.keysAt) < time.Minute {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := getJSON(ctx, client, jwksURL, "", &set); err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	keys := map[string]any{}
	for _, jwk := range set.Keys {
		k, err := parseJWK(jwk)
		if err != nil {
			continue
		}
		keys[str(jwk["kid"])] = k
	}
	o.keys, o.keysAt = keys, time.Now()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	if kid == "" && len(keys) == 1 {
		for _, k := range keys {
			return k, nil
		}
	}
	return nil, fmt.Errorf("unknown key id %q", kid)
}

// parseJWK builds a public key from a JSON Web Key (RSA or P-256).
func parseJWK(jwk map[string]any) (any, error) {
	decode := func(name string) (*big.Int, error) {
		b, err := base64.RawURLEncoding.DecodeString(str(jwk[name]))
		if err != nil {
			return nil, err
		}
		return new(big.Int).SetBytes(b), nil
	}
	switch str(jwk["kty"]) {
	case "RSA":
		n, err := decode("n")
		if err != nil {
			return nil, err
		}
		e, err := decode("e")
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		if str(jwk["crv"]) != "P-256" {
			return nil, errors.New("unsupported curve")
		}
		x, err := base64.RawURLEncoding.DecodeString(str(jwk["x"]))
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(str(jwk["y"]))
		if err != nil {
			return nil, err
		}
		if len(x) != 32 || len(y) != 32 {
			return nil, errors.New("malformed P-256 point")
		}
		point := append(append([]byte{4}, x...), y...)
		return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	}
	return nil, errors.New("unsupported key type")
}

// GitHubProvider is sign-in with GitHub: OAuth without OIDC, the user
// and the primary verified email read from the API with the token.
type GitHubProvider struct {
	clientID, clientSecret string
	// base and api are overridable for tests.
	base, api string
}

// GitHub is sign-in with GitHub: an OAuth App (Settings, Developer
// settings) whose callback URL is <APP_URL>/api/v1/auth/github/callback.
func GitHub(clientID, clientSecret string) *GitHubProvider {
	return &GitHubProvider{clientID: clientID, clientSecret: clientSecret, base: "https://github.com", api: "https://api.github.com"}
}

// Name implements Provider.
func (g *GitHubProvider) Name() string { return "github" }

// Label implements Provider.
func (g *GitHubProvider) Label() string { return "GitHub" }

// AuthURL implements Provider.
func (g *GitHubProvider) AuthURL(ctx context.Context, p AuthParams) (string, error) {
	q := url.Values{"client_id": {g.clientID}, "redirect_uri": {p.RedirectURI}, "scope": {"read:user user:email"}, "state": {p.State}}
	return g.base + "/login/oauth/authorize?" + q.Encode(), nil
}

// Exchange implements Provider.
func (g *GitHubProvider) Exchange(ctx context.Context, p AuthParams, code string) (Identity, error) {
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	form := url.Values{"client_id": {g.clientID}, "client_secret": {g.clientSecret}, "code": {code}, "redirect_uri": {p.RedirectURI}}
	if err := postForm(ctx, p.Client, g.base+"/login/oauth/access_token", form, &tok); err != nil {
		return Identity{}, fmt.Errorf("github: token: %w", err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return Identity{}, fmt.Errorf("github: token: %s %s", tok.Error, tok.ErrorDesc)
	}
	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := getJSON(ctx, p.Client, g.api+"/user", tok.AccessToken, &user); err != nil {
		return Identity{}, fmt.Errorf("github: user: %w", err)
	}
	id := Identity{Provider: "github", Subject: strconv.FormatInt(user.ID, 10), Name: user.Name, Picture: user.AvatarURL}
	if id.Name == "" {
		id.Name = user.Login
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, p.Client, g.api+"/user/emails", tok.AccessToken, &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				id.Email, id.EmailVerified = e.Email, true
			}
		}
	}
	if id.Email == "" {
		id.Email = user.Email
	}
	if id.Subject == "0" {
		return Identity{}, errors.New("github: user reply has no id")
	}
	return id, nil
}

// HTTP helpers.

func getJSON(ctx context.Context, client *http.Client, u, bearer string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return doJSON(client, req, dst)
}

func postForm(ctx context.Context, client *http.Client, u string, form url.Values, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return doJSON(client, req, dst)
}

func doJSON(client *http.Client, req *http.Request, dst any) error {
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("%s: %d %s", req.URL.Host, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, dst)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newUUID returns a random UUID (version 4) as text, the subject of a
// new account.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// The provider round trip: start sets a signed cookie with the state,
// the nonce, the PKCE verifier and where to land, and sends the browser
// to the provider; callback checks the cookie, exchanges the code,
// links the identity and opens the session.

// StateCookie holds the round trip between start and callback.
const StateCookie = "lidza_signin"

type roundTrip struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Redirect string `json:"r"`
	Expires  int64  `json:"e"`
}

func (a *Auth) sealTrip(t roundTrip) (string, error) {
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.Secret))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (a *Auth) openTrip(value string) (roundTrip, error) {
	i := strings.IndexByte(value, '.')
	if i < 0 {
		return roundTrip{}, errors.New("malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value[:i])
	if err != nil {
		return roundTrip{}, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(value[i+1:])
	if err != nil {
		return roundTrip{}, err
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.Secret))
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return roundTrip{}, errors.New("bad signature")
	}
	var t roundTrip
	if err := json.Unmarshal(payload, &t); err != nil {
		return roundTrip{}, err
	}
	if time.Now().Unix() > t.Expires {
		return roundTrip{}, errors.New("expired")
	}
	return t, nil
}

// redirectURI is the callback the provider sends the browser back to:
// APP_URL when set, else the request's own origin.
func (s *signin) redirectURI(r *http.Request, provider string) string {
	a := From(r.Context())
	base := strings.TrimRight(a.cfg.AppURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + Prefix + "/" + provider + "/callback"
}

// localPath keeps a redirect inside the app: a path, never another
// origin.
func localPath(p, fallback string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return fallback
	}
	return p
}

func (s *signin) fail(w http.ResponseWriter, r *http.Request, reason string, err error) {
	if err != nil {
		slog.Warn("auth: provider sign-in failed", "reason", reason, "err", err)
	}
	sep := "?"
	if strings.Contains(s.opt.FailurePath, "?") {
		sep = "&"
	}
	http.Redirect(w, r, s.opt.FailurePath+sep+"error="+url.QueryEscape(reason), http.StatusFound)
}

func (s *signin) start(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p := s.provider(name)
	if p == nil {
		http.NotFound(w, r)
		return
	}
	a := From(r.Context())
	trip := roundTrip{Provider: name, State: randomID(), Nonce: randomID(), Verifier: randomID() + randomID(), Redirect: localPath(r.URL.Query().Get("redirect"), s.opt.AfterSignIn), Expires: time.Now().Add(10 * time.Minute).Unix()}
	sealed, err := a.sealTrip(trip)
	if err != nil {
		s.fail(w, r, "provider", err)
		return
	}
	params := AuthParams{RedirectURI: s.redirectURI(r, name), State: trip.State, Nonce: trip.Nonce, CodeVerifier: trip.Verifier, Client: s.opt.Client}
	to, err := p.AuthURL(r.Context(), params)
	if err != nil {
		s.fail(w, r, "provider", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: sealed, Path: Prefix + "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *signin) callback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p := s.provider(name)
	if p == nil {
		http.NotFound(w, r)
		return
	}
	a := From(r.Context())
	clear := &http.Cookie{Name: StateCookie, Value: "", Path: Prefix + "/", HttpOnly: true, MaxAge: -1}
	http.SetCookie(w, clear)
	c, err := r.Cookie(StateCookie)
	if err != nil {
		s.fail(w, r, "state", errors.New("no state cookie"))
		return
	}
	trip, err := a.openTrip(c.Value)
	if err == nil && (trip.Provider != name || trip.State == "" || trip.State != r.URL.Query().Get("state")) {
		err = errors.New("state does not match the cookie")
	}
	if err != nil {
		s.fail(w, r, "state", err)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		reason := "provider"
		if e == "access_denied" {
			reason = "denied"
		}
		s.fail(w, r, reason, errors.New(e))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.fail(w, r, "provider", errors.New("no code"))
		return
	}
	params := AuthParams{RedirectURI: s.redirectURI(r, name), State: trip.State, Nonce: trip.Nonce, CodeVerifier: trip.Verifier, Client: s.opt.Client}
	id, err := p.Exchange(r.Context(), params, code)
	if err != nil {
		s.fail(w, r, "provider", err)
		return
	}
	prof, err := a.link(r.Context(), id)
	if err != nil {
		s.fail(w, r, "provider", err)
		return
	}
	if _, err := s.session(r.Context(), cookieSetter{w}, prof, name, false); err != nil {
		if errors.Is(err, ErrDisabled) {
			s.fail(w, r, "disabled", nil)
			return
		}
		s.fail(w, r, "provider", err)
		return
	}
	http.Redirect(w, r, trip.Redirect, http.StatusFound)
}

// cookieSetter adapts a ResponseWriter to what session sets cookies on.
type cookieSetter struct{ w http.ResponseWriter }

func (c cookieSetter) SetCookie(k *http.Cookie) { http.SetCookie(c.w, k) }
