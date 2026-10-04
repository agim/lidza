package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHubConnector connects a GitHub account through an OAuth App, for the
// server to call the GitHub API as the user: repositories, private ones
// included, and their webhooks with the default scopes.
type GitHubConnector struct {
	clientID, clientSecret string
	scopes                 []string
	// base and api are overridable for tests.
	base, api string
}

// GitHubDefaultScopes read and write the user's repositories (private
// ones included) and manage their webhooks.
var GitHubDefaultScopes = []string{"repo", "admin:repo_hook"}

// GitHubConnect connects GitHub with an OAuth App (Settings, Developer
// settings, OAuth Apps). Its authorization callback URL must cover
// <APP_URL>/api/v1/auth/connect/github/callback: GitHub accepts a
// redirect under the registered path, so <APP_URL>/api/v1/auth serves
// both this and GitHub sign-in from one app, or register an app of its
// own. Without scopes it asks for GitHubDefaultScopes; a grant missing
// one is refused. OAuth App tokens do not expire. A GitHub App's
// installation token, narrower (chosen repositories, fine-grained
// permissions, an hour), is the other way to reach repositories; the app
// mints those itself, they are not a user's connection.
func GitHubConnect(clientID, clientSecret string, scopes ...string) *GitHubConnector {
	if len(scopes) == 0 {
		scopes = GitHubDefaultScopes
	}
	return &GitHubConnector{clientID: clientID, clientSecret: clientSecret, scopes: scopes, base: "https://github.com", api: "https://api.github.com"}
}

// Name implements Connector.
func (g *GitHubConnector) Name() string { return "github" }

// Scopes implements Connector.
func (g *GitHubConnector) Scopes() []string { return g.scopes }

// AuthURL implements Connector.
func (g *GitHubConnector) AuthURL(ctx context.Context, p AuthParams) (string, error) {
	q := url.Values{"client_id": {g.clientID}, "redirect_uri": {p.RedirectURI}, "scope": {strings.Join(g.scopes, " ")}, "state": {p.State},
		"code_challenge": {s256(p.CodeVerifier)}, "code_challenge_method": {"S256"}, "allow_signup": {"false"}}
	return g.base + "/login/oauth/authorize?" + q.Encode(), nil
}

// Exchange implements Connector.
func (g *GitHubConnector) Exchange(ctx context.Context, p AuthParams, code string) (Grant, error) {
	form := url.Values{"client_id": {g.clientID}, "client_secret": {g.clientSecret}, "code": {code}, "redirect_uri": {p.RedirectURI}, "code_verifier": {p.CodeVerifier}}
	tok, err := tokenRequest(ctx, p.Client, g.base+"/login/oauth/access_token", form, "", "")
	if err != nil {
		return Grant{}, fmt.Errorf("github: token: %w", err)
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := getJSON(ctx, p.Client, g.api+"/user", tok.AccessToken, &user); err != nil {
		return Grant{}, fmt.Errorf("github: user: %w", err)
	}
	if user.ID == 0 {
		return Grant{}, fmt.Errorf("github: user reply has no id")
	}
	return tok.grant("github", strconv.FormatInt(user.ID, 10), user.Login, splitScopes(tok.Scope, g.scopes)), nil
}

// Refresh implements Connector: OAuth App tokens do not expire; a grant
// with a refresh token (expiring user tokens) is renewed.
func (g *GitHubConnector) Refresh(ctx context.Context, client *http.Client, gr Grant) (Grant, error) {
	if gr.RefreshToken() == "" {
		return Grant{}, ErrReconnect
	}
	form := url.Values{"client_id": {g.clientID}, "client_secret": {g.clientSecret}, "grant_type": {"refresh_token"}, "refresh_token": {gr.RefreshToken()}}
	tok, err := tokenRequest(ctx, client, g.base+"/login/oauth/access_token", form, "", "")
	if err != nil {
		return Grant{}, fmt.Errorf("github: refresh: %w", err)
	}
	return tok.grant("github", gr.Subject, gr.Login, splitScopes(tok.Scope, gr.Scopes)), nil
}

// Revoke implements Connector: the OAuth App's grant is deleted, which
// revokes every token of it for the user.
func (g *GitHubConnector) Revoke(ctx context.Context, client *http.Client, gr Grant) error {
	body, _ := json.Marshal(map[string]string{"access_token": gr.AccessToken()})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.api+"/applications/"+url.PathEscape(g.clientID)+"/grant", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(g.clientID, g.clientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	return revokeDo(client, req, http.StatusNotFound, http.StatusUnprocessableEntity)
}

// OAuth2Config describes a provider that speaks OAuth 2.0 (RFC 6749):
// a calendar, a storage service, another code host.
type OAuth2Config struct {
	// Name is the route segment and the grant's provider ("calendar").
	Name                   string
	ClientID, ClientSecret string
	// AuthURL and TokenURL are the authorization and token endpoints;
	// RevokeURL the revocation endpoint (RFC 7009), empty when there is
	// none.
	AuthURL, TokenURL, RevokeURL string
	// UserURL answers the account for the access token (a userinfo
	// endpoint); SubjectField and LoginField name its fields ("sub" and
	// "email" by default, "id" when there is no "sub").
	UserURL, SubjectField, LoginField string
	// Scopes are asked for; a grant missing one is refused.
	Scopes []string
	// PKCE sends an S256 challenge (most providers accept it).
	PKCE bool
	// Params are added to the authorization URL (access_type=offline
	// and prompt=consent where a provider gives refresh tokens only so).
	Params url.Values
}

// OAuth2Connector is a Connector for an OAuth2Config.
type OAuth2Connector struct{ cfg OAuth2Config }

// OAuth2Connect connects a provider described by cfg.
func OAuth2Connect(cfg OAuth2Config) *OAuth2Connector {
	if cfg.SubjectField == "" {
		cfg.SubjectField = "sub"
	}
	if cfg.LoginField == "" {
		cfg.LoginField = "email"
	}
	return &OAuth2Connector{cfg: cfg}
}

// Name implements Connector.
func (o *OAuth2Connector) Name() string { return o.cfg.Name }

// Scopes implements Connector.
func (o *OAuth2Connector) Scopes() []string { return o.cfg.Scopes }

// AuthURL implements Connector.
func (o *OAuth2Connector) AuthURL(ctx context.Context, p AuthParams) (string, error) {
	q := url.Values{"response_type": {"code"}, "client_id": {o.cfg.ClientID}, "redirect_uri": {p.RedirectURI}, "scope": {strings.Join(o.cfg.Scopes, " ")}, "state": {p.State}}
	if o.cfg.PKCE {
		q.Set("code_challenge", s256(p.CodeVerifier))
		q.Set("code_challenge_method", "S256")
	}
	for k, vs := range o.cfg.Params {
		q[k] = vs
	}
	sep := "?"
	if strings.Contains(o.cfg.AuthURL, "?") {
		sep = "&"
	}
	return o.cfg.AuthURL + sep + q.Encode(), nil
}

// Exchange implements Connector.
func (o *OAuth2Connector) Exchange(ctx context.Context, p AuthParams, code string) (Grant, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.RedirectURI}}
	if o.cfg.PKCE {
		form.Set("code_verifier", p.CodeVerifier)
	}
	tok, err := tokenRequest(ctx, p.Client, o.cfg.TokenURL, form, o.cfg.ClientID, o.cfg.ClientSecret)
	if err != nil {
		return Grant{}, fmt.Errorf("%s: token: %w", o.cfg.Name, err)
	}
	subject, login := "", ""
	if o.cfg.UserURL != "" {
		var user map[string]any
		if err := getJSON(ctx, p.Client, o.cfg.UserURL, tok.AccessToken, &user); err != nil {
			return Grant{}, fmt.Errorf("%s: user: %w", o.cfg.Name, err)
		}
		subject, login = field(user, o.cfg.SubjectField), field(user, o.cfg.LoginField)
		if subject == "" {
			subject = field(user, "id")
		}
	}
	if subject == "" {
		return Grant{}, fmt.Errorf("%s: no account id (UserURL, SubjectField)", o.cfg.Name)
	}
	return tok.grant(o.cfg.Name, subject, login, splitScopes(tok.Scope, o.cfg.Scopes)), nil
}

// Refresh implements Connector.
func (o *OAuth2Connector) Refresh(ctx context.Context, client *http.Client, g Grant) (Grant, error) {
	if g.RefreshToken() == "" {
		return Grant{}, ErrReconnect
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {g.RefreshToken()}}
	tok, err := tokenRequest(ctx, client, o.cfg.TokenURL, form, o.cfg.ClientID, o.cfg.ClientSecret)
	if err != nil {
		return Grant{}, fmt.Errorf("%s: refresh: %w", o.cfg.Name, err)
	}
	return tok.grant(o.cfg.Name, g.Subject, g.Login, splitScopes(tok.Scope, g.Scopes)), nil
}

// Revoke implements Connector: the refresh token when there is one
// (which revokes the grant at most providers), else the access token.
func (o *OAuth2Connector) Revoke(ctx context.Context, client *http.Client, g Grant) error {
	if o.cfg.RevokeURL == "" {
		return nil
	}
	token, hint := g.RefreshToken(), "refresh_token"
	if token == "" {
		token, hint = g.AccessToken(), "access_token"
	}
	form := url.Values{"token": {token}, "token_type_hint": {hint}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(o.cfg.ClientID), url.QueryEscape(o.cfg.ClientSecret))
	return revokeDo(client, req)
}

// tokenReply is a token endpoint's answer (RFC 6749 section 5).
type tokenReply struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func (t tokenReply) grant(provider, subject, login string, scopes []string) Grant {
	g := Grant{Provider: provider, Subject: subject, Login: login, Scopes: scopes, TokenType: strings.ToLower(t.TokenType)}
	if g.TokenType == "" {
		g.TokenType = "bearer"
	}
	if t.ExpiresIn > 0 {
		g.Expiry = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return g.WithTokens(t.AccessToken, t.RefreshToken)
}

// tokenRequest posts to a token endpoint, with the client's credentials
// as HTTP basic auth when id is set (else in the form, as GitHub wants).
// An error names the endpoint's error code, never the body, which may
// echo what was sent; invalid_grant and GitHub's bad_refresh_token are
// ErrReconnect.
func tokenRequest(ctx context.Context, client *http.Client, endpoint string, form url.Values, id, secret string) (tokenReply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenReply{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if id != "" {
		req.SetBasicAuth(url.QueryEscape(id), url.QueryEscape(secret))
	}
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return tokenReply{}, err
	}
	defer res.Body.Close()
	var t tokenReply
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	jsonErr := json.Unmarshal(body, &t)
	switch {
	case t.Error == "invalid_grant" || t.Error == "bad_refresh_token" || t.Error == "bad_verification_code":
		return tokenReply{}, fmt.Errorf("%w (%s)", ErrReconnect, t.Error)
	case t.Error != "":
		return tokenReply{}, fmt.Errorf("%s: %s %s", req.URL.Host, t.Error, t.ErrorDesc)
	case res.StatusCode >= 400:
		return tokenReply{}, fmt.Errorf("%s: %d", req.URL.Host, res.StatusCode)
	case jsonErr != nil:
		return tokenReply{}, fmt.Errorf("%s: unreadable token reply", req.URL.Host)
	case t.AccessToken == "":
		return tokenReply{}, fmt.Errorf("%s: no access token", req.URL.Host)
	}
	return t, nil
}

// revokeDo sends a revocation; a 2xx, or one of gone (statuses meaning
// the grant is already gone), is done.
func revokeDo(client *http.Client, req *http.Request, gone ...int) error {
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode/100 == 2 {
		return nil
	}
	for _, s := range gone {
		if res.StatusCode == s {
			return nil
		}
	}
	return fmt.Errorf("%s: revoke: %d", req.URL.Host, res.StatusCode)
}

// splitScopes reads a granted scope list (space or comma separated);
// empty means the provider granted what was asked.
func splitScopes(granted string, asked []string) []string {
	fields := strings.FieldsFunc(granted, func(r rune) bool { return r == ' ' || r == ',' })
	if len(fields) == 0 {
		return append([]string(nil), asked...)
	}
	return fields
}

func field(m map[string]any, name string) string {
	switch v := m[name].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	}
	return ""
}

// ConnectorsFromEnv builds the connectors AUTH_CONNECT names (comma
// separated), each with AUTH_CONNECT_<NAME>_CLIENT_ID and
// AUTH_CONNECT_<NAME>_CLIENT_SECRET and, optionally,
// AUTH_CONNECT_<NAME>_SCOPES (space separated). github is known; another
// provider is an OAuth2Connect in Options.Connectors. One with a missing
// credential is left out and named in the warnings.
func ConnectorsFromEnv(values map[string]string) ([]Connector, []string) {
	var out []Connector
	var warnings []string
	for _, name := range strings.Split(values["AUTH_CONNECT"], ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		prefix := "AUTH_CONNECT_" + strings.ToUpper(name) + "_"
		id, secret := values[prefix+"CLIENT_ID"], values[prefix+"CLIENT_SECRET"]
		if id == "" || secret == "" {
			warnings = append(warnings, fmt.Sprintf("connector %s left out: %sCLIENT_ID and %sCLIENT_SECRET are needed", name, prefix, prefix))
			continue
		}
		scopes := strings.Fields(values[prefix+"SCOPES"])
		switch name {
		case "github":
			out = append(out, GitHubConnect(id, secret, scopes...))
		default:
			warnings = append(warnings, fmt.Sprintf("connector %s left out: only github is known by name; give others as auth.OAuth2Connect in Options.Connectors", name))
		}
	}
	return out, warnings
}
