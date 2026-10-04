package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/router"
)

// Connections: a signed-in user connects an external account (a code
// host, a calendar, a storage service) so the server calls its API on
// the user's behalf. This is not sign-in: the account is not an
// identity of the user's and opens no session; the grant (its access
// token, refresh token, scopes and expiry) is kept, sealed with the
// master key, for the server to use. The browser never sees a token.
//
// Mount serves, when Options.Connectors (or AUTH_CONNECT) names any:
//
//	GET    /api/v1/auth/connect/{provider}/start     the signed-in user starts; ?redirect=/path
//	GET    /api/v1/auth/connect/{provider}/callback  the provider returns here
//	GET    /api/v1/auth/connections                  the user's connections, no secrets
//	DELETE /api/v1/auth/connections/{provider}       revoke at the provider and forget
//
// The server uses a connection with auth.From(ctx).Connection(ctx,
// owner, "github"): Client gives an *http.Client that sends the token
// and refreshes it when it expires.

// ErrNotConnected is returned for a provider the owner has not
// connected.
var ErrNotConnected = errors.New("auth: not connected")

// ErrReconnect is returned when the grant no longer works (revoked at
// the provider, expired without a refresh token, a refresh refused):
// the user connects again. The connection lists with Reconnect set.
var ErrReconnect = errors.New("auth: the connection must be made again")

// Grant is what a provider granted: the account it belongs to, the
// scopes, and the tokens. The tokens are unexported: a Grant marshals
// to JSON, prints and logs without them; AccessToken and RefreshToken
// read them, for a ConnectionStore or a Connector of the app's.
type Grant struct {
	// Provider is the connector's name ("github").
	Provider string
	// Subject is the provider's stable id of the account; Login its
	// readable name (a username, an address).
	Subject, Login string
	// Scopes are the scopes the provider granted.
	Scopes []string
	// TokenType is the access token's type ("bearer").
	TokenType string
	// Expiry is when the access token expires; zero when it does not.
	Expiry time.Time

	accessToken, refreshToken string
}

// WithTokens returns g carrying an access token and a refresh token
// (empty when the provider issues none).
func (g Grant) WithTokens(access, refresh string) Grant {
	g.accessToken, g.refreshToken = access, refresh
	return g
}

// AccessToken is the token sent to the provider's API.
func (g Grant) AccessToken() string { return g.accessToken }

// RefreshToken renews the access token; empty when the provider issues
// none.
func (g Grant) RefreshToken() string { return g.refreshToken }

// String names the grant without its tokens.
func (g Grant) String() string {
	return fmt.Sprintf("auth.Grant{%s %s %s scopes=%s}", g.Provider, g.Subject, g.Login, strings.Join(g.Scopes, " "))
}

// GoString is String, so %#v does not print the tokens either.
func (g Grant) GoString() string { return g.String() }

// LogValue keeps the tokens out of structured logs.
func (g Grant) LogValue() slog.Value {
	return slog.GroupValue(slog.String("provider", g.Provider), slog.String("subject", g.Subject), slog.String("login", g.Login), slog.String("scopes", strings.Join(g.Scopes, " ")))
}

// expiring reports whether the access token expires within margin.
func (g Grant) expiring(now time.Time, margin time.Duration) bool {
	return !g.Expiry.IsZero() && !now.Add(margin).Before(g.Expiry)
}

// Connection is a connected account as the user and the app see it:
// no token.
type Connection struct {
	Provider string   `json:"provider"`
	Subject  string   `json:"subject"`
	Login    string   `json:"login,omitempty"`
	Scopes   []string `json:"scopes"`
	// Expiry is the access token's; nil when it does not expire.
	Expiry *time.Time `json:"expiry,omitempty"`
	// Reconnect is set when the grant stopped working: connect again.
	Reconnect   bool      `json:"reconnect,omitempty"`
	ConnectedAt time.Time `json:"connectedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Connector is an external API the user connects. The pack ships GitHub
// (GitHubConnect) and any OAuth 2.0 provider (OAuth2Connect).
type Connector interface {
	// Name is the route segment and the grant's provider ("github").
	Name() string
	// Scopes are the scopes asked for; a grant missing one is refused.
	Scopes() []string
	// AuthURL is where the browser goes to grant access; p carries the
	// callback, the state and the PKCE verifier.
	AuthURL(ctx context.Context, p AuthParams) (string, error)
	// Exchange turns the callback's code into the grant.
	Exchange(ctx context.Context, p AuthParams, code string) (Grant, error)
	// Refresh renews the access token with the refresh token: the new
	// grant (a rotated refresh token included), or ErrReconnect when the
	// provider refuses it.
	Refresh(ctx context.Context, client *http.Client, g Grant) (Grant, error)
	// Revoke withdraws the grant at the provider; nil where the provider
	// has no revocation.
	Revoke(ctx context.Context, client *http.Client, g Grant) error
}

// ConnectionStore keeps grants, each under its owner (the signed-in
// user's id) and provider; every method is scoped to the owner, so one
// user cannot reach another's. The pack's store keeps them in Postgres
// (auth_connection) with the tokens sealed with the master key; an app
// with workspaces may wrap it to add its own checks.
type ConnectionStore interface {
	Put(ctx context.Context, owner string, g Grant) error
	// Get returns ErrNotConnected for a provider the owner has not
	// connected, ErrReconnect for one marked so.
	Get(ctx context.Context, owner, provider string) (Grant, error)
	List(ctx context.Context, owner string) ([]Connection, error)
	// MarkReconnect flags the connection and drops its tokens.
	MarkReconnect(ctx context.Context, owner, provider string) error
	Delete(ctx context.Context, owner, provider string) error
}

// PurposeConnect is the one-time token purpose of a connect round trip.
const PurposeConnect = "connect"

// ConnectCookie holds a connect round trip between start and callback.
const ConnectCookie = "lidza_connect"

// connectTrip is the sealed cookie of a connect round trip.
type connectTrip struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Redirect string `json:"r"`
	Owner    string `json:"o"`
	Expires  int64  `json:"e"`
}

func (s *signin) connectors() []Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns
}

func (s *signin) connector(name string) Connector {
	for _, c := range s.connectors() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func (s *signin) connectRedirectURI(r *http.Request, provider string) string {
	return strings.TrimSuffix(s.redirectURI(r, provider), "/"+provider+"/callback") + "/connect/" + provider + "/callback"
}

// connectDone lands on to with ?connected=<provider>, or with
// ?connect_error=<reason>&provider=<provider>.
func connectDone(w http.ResponseWriter, r *http.Request, to, provider, reason string, err error) {
	q := url.Values{}
	if reason == "" {
		q.Set("connected", provider)
	} else {
		q.Set("connect_error", reason)
		q.Set("provider", provider)
		if err != nil {
			slog.Warn("auth: connect failed", "provider", provider, "reason", reason, "err", err)
		}
	}
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	http.Redirect(w, r, to+sep+q.Encode(), http.StatusFound)
}

func (s *signin) connectStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	c := s.connector(name)
	u := CurrentUser(r.Context())
	if c == nil || u == nil {
		http.NotFound(w, r)
		return
	}
	a := From(r.Context())
	to := localPath(r.URL.Query().Get("redirect"), s.opt.AfterConnect)
	if s.opt.ConnectAuthorize != nil {
		if err := s.opt.ConnectAuthorize(r.Context(), u, name); err != nil {
			connectDone(w, r, to, name, "refused", err)
			return
		}
	}
	// The state is a one-time token bound to the user and the provider;
	// the cookie binds it to this browser.
	state, err := a.IssueToken(r.Context(), PurposeConnect, u.ID+" "+name, 10*time.Minute)
	if err != nil {
		connectDone(w, r, to, name, "provider", err)
		return
	}
	trip := connectTrip{Provider: name, State: state, Verifier: randomID() + randomID(), Redirect: to, Owner: u.ID, Expires: time.Now().Add(10 * time.Minute).Unix()}
	sealed, err := a.seal(trip)
	if err != nil {
		connectDone(w, r, to, name, "provider", err)
		return
	}
	authURL, err := c.AuthURL(r.Context(), AuthParams{RedirectURI: s.connectRedirectURI(r, name), State: state, CodeVerifier: trip.Verifier, Client: s.opt.Client})
	if err != nil {
		connectDone(w, r, to, name, "provider", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: ConnectCookie, Value: sealed, Path: Prefix + "/connect/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *signin) connectCallback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	c := s.connector(name)
	u := CurrentUser(r.Context())
	if c == nil || u == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	a := From(ctx)
	http.SetCookie(w, &http.Cookie{Name: ConnectCookie, Value: "", Path: Prefix + "/connect/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	fail := func(to, reason string, err error) {
		s.event(ctx, r, Event{Kind: EventConnectFailed, Subject: u.ID, Method: name, Reason: reason})
		connectDone(w, r, to, name, reason, err)
	}
	var trip connectTrip
	cookie, err := r.Cookie(ConnectCookie)
	if err == nil {
		err = a.open(cookie.Value, &trip)
	}
	switch {
	case err != nil:
		fail(s.opt.AfterConnect, "state", err)
		return
	case trip.Provider != name || trip.State == "" || trip.State != r.URL.Query().Get("state"):
		fail(s.opt.AfterConnect, "state", errors.New("state does not match the cookie"))
		return
	case trip.Owner != u.ID:
		fail(s.opt.AfterConnect, "state", errors.New("started by another user"))
		return
	}
	// One use: a replayed callback finds the token spent.
	if subject, err := a.ConsumeToken(ctx, PurposeConnect, trip.State); err != nil || subject != u.ID+" "+name {
		fail(trip.Redirect, "state", errors.New("state used, expired or not this user's"))
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		reason := "provider"
		if e == "access_denied" {
			reason = "denied"
		}
		fail(trip.Redirect, reason, errors.New(e))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail(trip.Redirect, "provider", errors.New("no code"))
		return
	}
	g, err := c.Exchange(ctx, AuthParams{RedirectURI: s.connectRedirectURI(r, name), State: trip.State, CodeVerifier: trip.Verifier, Client: s.opt.Client}, code)
	if err != nil {
		fail(trip.Redirect, "provider", err)
		return
	}
	if missing := missingScopes(c.Scopes(), g.Scopes); len(missing) > 0 {
		// Not kept: withdraw what was granted.
		c.Revoke(ctx, s.opt.Client, g)
		fail(trip.Redirect, "scopes", fmt.Errorf("granted %v, missing %v", g.Scopes, missing))
		return
	}
	g.Provider = name
	if err := s.store(a).Put(ctx, u.ID, g); err != nil {
		fail(trip.Redirect, "provider", err)
		return
	}
	s.event(ctx, r, Event{Kind: EventConnected, Subject: u.ID, Method: name})
	connectDone(w, r, trip.Redirect, name, "", nil)
}

// missingScopes lists the wanted scopes not granted.
func missingScopes(want, got []string) []string {
	var missing []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// connectionsList answers GET /api/v1/auth/connections.
func (s *signin) connectionsList(ctx context.Context, req *router.Request[router.None]) ([]Connection, error) {
	u := CurrentUser(ctx)
	list, err := s.store(From(ctx)).List(ctx, u.ID)
	if list == nil {
		list = []Connection{}
	}
	return list, err
}

// connectionsDelete answers DELETE /api/v1/auth/connections/{provider}:
// the grant is revoked at the provider (when it can be) and forgotten.
func (s *signin) connectionsDelete(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	var out router.None
	u := CurrentUser(ctx)
	name := req.Param("provider")
	store := s.store(From(ctx))
	g, err := store.Get(ctx, u.ID, name)
	switch {
	case errors.Is(err, ErrNotConnected):
		return out, router.NotFound("connection")
	case err != nil && !errors.Is(err, ErrReconnect):
		return out, err
	}
	if c := s.connector(name); c != nil && err == nil {
		if rerr := c.Revoke(ctx, s.opt.Client, g); rerr != nil {
			slog.Warn("auth: revoke at the provider failed; the connection is forgotten here", "provider", name, "err", rerr)
		}
	}
	if err := store.Delete(ctx, u.ID, name); err != nil {
		return out, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventDisconnected, Subject: u.ID, Method: name})
	return out, nil
}

// store is the connection store the routes and Connection use: the
// app's (Options.Connections) or the pack's in Postgres.
func (s *signin) store(a *Auth) ConnectionStore {
	if s != nil && s.opt.Connections != nil {
		return s.opt.Connections
	}
	return &pgConnections{a: a}
}

// The server side.

// Conn is one owner's connection to one provider, for the server: Token
// and Client renew the access token when it expires.
type Conn struct {
	owner, provider string
	store           ConnectionStore
	connector       Connector
	client          *http.Client
}

// Connection returns owner's connection to provider. It fails with
// ErrNotConnected when there is none, ErrReconnect when it must be made
// again. The owner is the app's to decide: the signed-in user
// (auth.CurrentUser(ctx).ID), or one an app's own check allows.
func (a *Auth) Connection(ctx context.Context, owner, provider string) (*Conn, error) {
	mountedMu.Lock()
	s := mounted
	mountedMu.Unlock()
	if s == nil {
		return nil, errors.New("auth: connections need auth.Mount")
	}
	c := s.connector(provider)
	if c == nil {
		return nil, fmt.Errorf("auth: no connector %q (Options.Connectors, AUTH_CONNECT)", provider)
	}
	store := s.store(a)
	if _, err := store.Get(ctx, owner, provider); err != nil {
		return nil, err
	}
	return &Conn{owner: owner, provider: provider, store: store, connector: c, client: s.opt.Client}, nil
}

// refreshes bounds the refreshes in flight on this node; refreshTimeout
// bounds each.
var (
	refreshes      = make(chan struct{}, 8)
	refreshTimeout = 15 * time.Second
	refreshMu      sync.Mutex
	refreshing     = map[string]*refreshCall{}
)

type refreshCall struct {
	done  chan struct{}
	grant Grant
	err   error
}

// Token returns a valid access token, renewing it first when it expires
// within a minute. Concurrent callers for one connection share a
// renewal.
func (c *Conn) Token(ctx context.Context) (string, error) {
	g, err := c.store.Get(ctx, c.owner, c.provider)
	if err != nil {
		return "", err
	}
	if !g.expiring(time.Now(), time.Minute) {
		return g.AccessToken(), nil
	}
	if g.RefreshToken() == "" {
		c.store.MarkReconnect(ctx, c.owner, c.provider)
		return "", ErrReconnect
	}
	g, err = c.refresh(ctx, g)
	if err != nil {
		return "", err
	}
	return g.AccessToken(), nil
}

func (c *Conn) refresh(ctx context.Context, g Grant) (Grant, error) {
	key := c.owner + " " + c.provider
	refreshMu.Lock()
	if call, ok := refreshing[key]; ok {
		refreshMu.Unlock()
		select {
		case <-call.done:
			return call.grant, call.err
		case <-ctx.Done():
			return Grant{}, ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	refreshing[key] = call
	refreshMu.Unlock()
	defer func() {
		refreshMu.Lock()
		delete(refreshing, key)
		refreshMu.Unlock()
		close(call.done)
	}()

	select {
	case refreshes <- struct{}{}:
		defer func() { <-refreshes }()
	case <-ctx.Done():
		call.err = ctx.Err()
		return Grant{}, call.err
	}
	rctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	next, err := c.connector.Refresh(rctx, c.client, g)
	switch {
	case errors.Is(err, ErrReconnect):
		c.store.MarkReconnect(ctx, c.owner, c.provider)
		call.err = ErrReconnect
	case err != nil:
		call.err = fmt.Errorf("auth: refresh %s: %w", c.provider, err)
	default:
		next.Provider = c.provider
		if next.Subject == "" {
			next.Subject, next.Login = g.Subject, g.Login
		}
		if len(next.Scopes) == 0 {
			next.Scopes = g.Scopes
		}
		if next.RefreshToken() == "" {
			next = next.WithTokens(next.AccessToken(), g.RefreshToken())
		}
		if err := c.store.Put(ctx, c.owner, next); err != nil {
			call.err = err
		} else {
			call.grant = next
		}
	}
	return call.grant, call.err
}

// Client is an *http.Client that sends the connection's access token
// with every request, renewed when it expires. A reply of 401 means the
// grant may be gone: Token's ErrReconnect, or the provider's answer.
func (c *Conn) Client() *http.Client {
	base := c.client
	if base == nil {
		base = http.DefaultClient
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{Timeout: base.Timeout, Transport: bearerTransport{conn: c, next: transport}}
}

type bearerTransport struct {
	conn *Conn
	next http.RoundTripper
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := b.conn.Token(req.Context())
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token)
	return b.next.RoundTrip(req)
}

// The pack's store: auth_connection, the tokens sealed with the master
// key (config/master.key or LIDZA_MASTER_KEY).

type pgConnections struct{ a *Auth }

func (p *pgConnections) key() ([]byte, error) {
	key, err := credentials.Key(".")
	if err != nil {
		return nil, fmt.Errorf("auth: connections are sealed with the master key: %w", err)
	}
	return key, nil
}

func (p *pgConnections) Put(ctx context.Context, owner string, g Grant) error {
	key, err := p.key()
	if err != nil {
		return err
	}
	access, err := credentials.Encrypt(key, []byte(g.AccessToken()))
	if err != nil {
		return err
	}
	refresh := ""
	if g.RefreshToken() != "" {
		if refresh, err = credentials.Encrypt(key, []byte(g.RefreshToken())); err != nil {
			return err
		}
	}
	var expires *time.Time
	if !g.Expiry.IsZero() {
		expires = &g.Expiry
	}
	_, err = p.a.pool.Exec(ctx, `INSERT INTO auth_connection (id, owner, provider, subject, login, scopes, token_type, access_sealed, refresh_sealed, expires_at, reconnect)
VALUES ($10 || ':' || $1, $1, $2, $3, $4, $5, $6, $7, $8, $9, false)
ON CONFLICT (owner, provider) DO UPDATE SET subject = EXCLUDED.subject, login = EXCLUDED.login, scopes = EXCLUDED.scopes, token_type = EXCLUDED.token_type,
  access_sealed = EXCLUDED.access_sealed, refresh_sealed = EXCLUDED.refresh_sealed, expires_at = EXCLUDED.expires_at, reconnect = false, updated_at = now()`,
		owner, g.Provider, g.Subject, g.Login, strings.Join(g.Scopes, " "), g.TokenType, access, refresh, expires, g.Provider)
	if err != nil {
		return fmt.Errorf("auth: save connection: %w", err)
	}
	return nil
}

func (p *pgConnections) Get(ctx context.Context, owner, provider string) (Grant, error) {
	var g Grant
	var scopes, access, refresh string
	var expires *time.Time
	var reconnect bool
	err := p.a.pool.QueryRow(ctx, `SELECT subject, login, scopes, token_type, access_sealed, refresh_sealed, expires_at, reconnect FROM auth_connection WHERE owner = $1 AND provider = $2`, owner, provider).
		Scan(&g.Subject, &g.Login, &scopes, &g.TokenType, &access, &refresh, &expires, &reconnect)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, ErrNotConnected
	}
	if err != nil {
		return Grant{}, fmt.Errorf("auth: read connection: %w", err)
	}
	if reconnect {
		return Grant{}, ErrReconnect
	}
	key, err := p.key()
	if err != nil {
		return Grant{}, err
	}
	at, err := credentials.Decrypt(key, access)
	if err != nil {
		return Grant{}, fmt.Errorf("auth: open connection (another master key?): %w", err)
	}
	var rt []byte
	if refresh != "" {
		if rt, err = credentials.Decrypt(key, refresh); err != nil {
			return Grant{}, fmt.Errorf("auth: open connection (another master key?): %w", err)
		}
	}
	g.Provider, g.Scopes = provider, strings.Fields(scopes)
	if expires != nil {
		g.Expiry = *expires
	}
	return g.WithTokens(string(at), string(rt)), nil
}

func (p *pgConnections) List(ctx context.Context, owner string) ([]Connection, error) {
	rows, err := p.a.pool.Query(ctx, `SELECT provider, subject, login, scopes, expires_at, reconnect, created_at, updated_at FROM auth_connection WHERE owner = $1 ORDER BY provider`, owner)
	if err != nil {
		return nil, fmt.Errorf("auth: list connections: %w", err)
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		var c Connection
		var scopes string
		if err := rows.Scan(&c.Provider, &c.Subject, &c.Login, &scopes, &c.Expiry, &c.Reconnect, &c.ConnectedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Scopes = strings.Fields(scopes)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *pgConnections) MarkReconnect(ctx context.Context, owner, provider string) error {
	_, err := p.a.pool.Exec(ctx, `UPDATE auth_connection SET reconnect = true, access_sealed = '', refresh_sealed = '', updated_at = now() WHERE owner = $1 AND provider = $2`, owner, provider)
	return err
}

func (p *pgConnections) Delete(ctx context.Context, owner, provider string) error {
	_, err := p.a.pool.Exec(ctx, `DELETE FROM auth_connection WHERE owner = $1 AND provider = $2`, owner, provider)
	return err
}
