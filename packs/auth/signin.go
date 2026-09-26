package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/validate"
)

// Options configure Mount, the pack's own sign-in: local accounts (email
// and password in auth_user) by default, plus the sign-in providers
// (Google, GitHub, Microsoft, any OIDC issuer) named by AUTH_PROVIDERS
// and their credentials. Every field is optional.
type Options struct {
	// Providers are the external sign-ins. Nil reads AUTH_PROVIDERS and
	// the AUTH_<NAME>_CLIENT_ID and AUTH_<NAME>_CLIENT_SECRET credentials
	// (AUTH_<NAME>_ISSUER for an issuer the pack does not know), and
	// Reconfigure reloads them, so a provider added on the admin pages
	// works without a restart.
	Providers []Provider
	// NoLocal leaves out email and password: registration, login, the
	// reset links and the password route; users sign in with a provider.
	NoLocal bool
	// NoRegister keeps login but not self-registration: accounts come
	// from a provider or from the app (CreateUser).
	NoRegister bool
	// RequireVerified refuses password sign-in until the email is
	// verified through the link; a provider that vouches for the address
	// verifies it too.
	RequireVerified bool
	// VerifyPath and ResetPath are the frontend pages the emailed links
	// open, with ?token=...: "/verify" and "/reset".
	VerifyPath, ResetPath string
	// AfterSignIn is where a provider sign-in lands ("/"); the start
	// route's ?redirect= overrides it with another path of this app.
	AfterSignIn string
	// FailurePath is where a failed provider sign-in lands, with
	// ?error=<reason>: "/login".
	FailurePath string
	// Title names the app in the emails.
	Title string
	// Claims adds claims to every session (roles, a tenant); they ride in
	// the access token as auth.CurrentUser(ctx).Claims.
	Claims func(ctx context.Context, p Profile) (map[string]any, error)
	// Client is the HTTP client the providers use (tests).
	Client *http.Client
}

// Prefix is where Mount registers the sign-in routes.
const Prefix = "/api/v1/auth"

// Mount registers the sign-in routes under /api/v1/auth: register,
// login, logout, session, me, verify, forgot, reset, password,
// providers, and {provider}/start plus {provider}/callback for the
// external sign-ins. Sessions are the pack's usual ones: HttpOnly
// cookies that slide, and the access token in the reply for other
// clients. The app links its rows to auth.CurrentUser(ctx).ID.
func Mount(r *router.Router, opt Options) {
	s := newSignin(opt)
	mountedMu.Lock()
	mounted = s
	mountedMu.Unlock()
	if !opt.NoLocal {
		if !opt.NoRegister {
			router.Route(r, "POST /api/v1/auth/register", s.authRegister, Throttle())
		}
		router.Route(r, "POST /api/v1/auth/login", s.authLogin, Throttle())
		router.Route(r, "POST /api/v1/auth/verify", s.authVerify, Throttle())
		router.Route(r, "POST /api/v1/auth/forgot", s.authForgot, Throttle())
		router.Route(r, "POST /api/v1/auth/reset", s.authReset, Throttle())
		router.Route(r, "POST /api/v1/auth/password", s.authPassword, Require())
	}
	router.Route(r, "GET /api/v1/auth/session", s.authSession, Optional())
	router.Route(r, "GET /api/v1/auth/me", s.authMe, Require())
	router.Route(r, "POST /api/v1/auth/logout", s.authLogout, Require())
	router.Route(r, "GET /api/v1/auth/providers", s.authProviders)
	r.HandleFunc("GET /api/v1/auth/{provider}/start", s.start)
	r.HandleFunc("GET /api/v1/auth/{provider}/callback", s.callback)
}

// signin holds the options and the providers behind the routes.
type signin struct {
	opt   Options
	mu    sync.RWMutex
	provs []Provider
	fixed bool // Providers came from the options, not the environment
}

func newSignin(opt Options) *signin {
	if opt.VerifyPath == "" {
		opt.VerifyPath = "/verify"
	}
	if opt.ResetPath == "" {
		opt.ResetPath = "/reset"
	}
	if opt.AfterSignIn == "" {
		opt.AfterSignIn = "/"
	}
	if opt.FailurePath == "" {
		opt.FailurePath = "/login"
	}
	if opt.Client == nil {
		opt.Client = &http.Client{Timeout: 15 * time.Second}
	}
	s := &signin{opt: opt, provs: opt.Providers, fixed: opt.Providers != nil}
	if !s.fixed {
		s.reload()
	}
	return s
}

// reload reads the providers from the environment and the credentials.
func (s *signin) reload() {
	values, err := env.Values(".")
	if err != nil {
		slog.Warn("auth: providers not loaded", "err", err)
		return
	}
	provs, warnings := ProvidersFromEnv(values)
	for _, w := range warnings {
		slog.Warn("auth: " + w)
	}
	s.mu.Lock()
	s.provs = provs
	s.mu.Unlock()
}

func (s *signin) providers() []Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provs
}

func (s *signin) provider(name string) Provider {
	for _, p := range s.providers() {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// mounted is the sign-in of the app, for Reconfigure and the admin
// pages; one per process, set by Mount.
var (
	mountedMu sync.Mutex
	mounted   *signin
)

// Reconfigure reloads the sign-in providers from the environment and the
// credentials (lidza.Reconfigure calls it after the admin pages save).
func (a *Auth) Reconfigure(context.Context) error {
	mountedMu.Lock()
	s := mounted
	mountedMu.Unlock()
	if s != nil && !s.fixed {
		s.reload()
	}
	return nil
}

// Mounted reports whether the app mounted the pack's sign-in.
func Mounted() bool {
	mountedMu.Lock()
	defer mountedMu.Unlock()
	return mounted != nil
}

// ProviderNames lists the sign-in providers Mount serves, for the admin
// pages; nil when the app did not mount the sign-in.
func ProviderNames() []string {
	mountedMu.Lock()
	s := mounted
	mountedMu.Unlock()
	if s == nil {
		return nil
	}
	var out []string
	for _, p := range s.providers() {
		out = append(out, p.Name())
	}
	return out
}

// Types of the sign-in routes; the generated client has them.

// Credentials sign in a local account.
type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Validate implements validate.Validator.
func (c Credentials) Validate() error {
	var errs validate.Errors
	if !validate.Email(c.Email) {
		errs.Add("email", "email", "must be an email address")
	}
	if c.Password == "" {
		errs.Add("password", "required", "is required")
	}
	return errs.Result()
}

// Registration creates a local account.
type Registration struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

// Validate implements validate.Validator.
func (r Registration) Validate() error {
	var errs validate.Errors
	if !validate.Email(r.Email) {
		errs.Add("email", "email", "must be an email address")
	}
	if r.Password == "" {
		errs.Add("password", "required", "is required")
	}
	if len(r.Name) > 200 {
		errs.Add("name", "max", "must be at most 200 characters")
	}
	return errs.Result()
}

// SignedIn is the signed-in user as the sign-in routes reply.
type SignedIn struct {
	Subject  string `json:"subject"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Verified bool   `json:"verified"`
	// Providers are the ways this account signs in: "password" and the
	// linked providers' names.
	Providers []string `json:"providers"`
	// AccessToken is set by register and login for clients that send it
	// as a bearer token; browsers use the cookies.
	AccessToken string `json:"accessToken,omitempty"`
}

// SessionState is the reply of GET /api/v1/auth/session: the user, or
// null for a visitor.
type SessionState struct {
	User *SignedIn `json:"user"`
}

// TokenRequest carries the token from an emailed link.
type TokenRequest struct {
	Token string `json:"token"`
}

// Validate implements validate.Validator.
func (t TokenRequest) Validate() error {
	var errs validate.Errors
	if t.Token == "" {
		errs.Add("token", "required", "is required")
	}
	return errs.Result()
}

// EmailRequest asks for a reset link.
type EmailRequest struct {
	Email string `json:"email"`
}

// Validate implements validate.Validator.
func (e EmailRequest) Validate() error {
	var errs validate.Errors
	if !validate.Email(e.Email) {
		errs.Add("email", "email", "must be an email address")
	}
	return errs.Result()
}

// PasswordReset sets a new password from a reset link.
type PasswordReset struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// Validate implements validate.Validator.
func (p PasswordReset) Validate() error {
	var errs validate.Errors
	if p.Token == "" {
		errs.Add("token", "required", "is required")
	}
	if p.Password == "" {
		errs.Add("password", "required", "is required")
	}
	return errs.Result()
}

// PasswordChange sets a new password for the signed-in user; Current is
// empty for an account that had none (provider sign-in only).
type PasswordChange struct {
	Current  string `json:"current,omitempty"`
	Password string `json:"password"`
}

// Validate implements validate.Validator.
func (p PasswordChange) Validate() error {
	var errs validate.Errors
	if p.Password == "" {
		errs.Add("password", "required", "is required")
	}
	return errs.Result()
}

// ProviderLink is one sign-in button: its name, label and the URL to
// send the browser to (append ?redirect=/path to land elsewhere).
type ProviderLink struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ProviderList is the reply of GET /api/v1/auth/providers: what the
// sign-in page offers.
type ProviderList struct {
	Providers []ProviderLink `json:"providers"`
	// Local reports whether email and password sign in; Register whether
	// visitors may register.
	Local    bool `json:"local"`
	Register bool `json:"register"`
}

// Profile is a row of auth_user.
type Profile struct {
	Subject     string     `json:"subject"`
	Email       string     `json:"email,omitempty"`
	Name        string     `json:"name,omitempty"`
	HasPassword bool       `json:"hasPassword"`
	VerifiedAt  *time.Time `json:"verifiedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Verified reports whether the email was verified.
func (p Profile) Verified() bool { return p.VerifiedAt != nil }

// LinkedIdentity is a row of auth_identity.
type LinkedIdentity struct {
	Provider   string    `json:"provider"`
	Subject    string    `json:"providerSubject"`
	Email      string    `json:"email,omitempty"`
	Name       string    `json:"name,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}

// uniqueViolation is Postgres' code for a duplicate key.
const uniqueViolation = "23505"

// ErrEmailTaken is the 409 registration replies for a registered email.
var ErrEmailTaken = router.Errorf(http.StatusConflict, "email already registered")

// NormalizeEmail is how an address is stored and looked up: lowercased
// and trimmed, so a user signs in however they capitalize it.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// CreateUser adds a local account (an invited user, a seed). The
// password may be empty for an account that signs in through a provider
// only. The email must be free; ErrEmailTaken otherwise.
func (a *Auth) CreateUser(ctx context.Context, email, name, password string) (Profile, error) {
	hash := ""
	if password != "" {
		var err error
		if hash, err = HashPassword(password); err != nil {
			return Profile{}, err
		}
	}
	return a.createUser(ctx, NormalizeEmail(email), name, hash, nil)
}

func (a *Auth) createUser(ctx context.Context, email, name, hash string, verifiedAt *time.Time) (Profile, error) {
	p := Profile{Subject: newUUID(), Email: email, Name: name, HasPassword: hash != "", VerifiedAt: verifiedAt}
	err := a.pool.QueryRow(ctx, `INSERT INTO auth_user (subject, email, name, password_hash, verified_at) VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5) RETURNING created_at`,
		p.Subject, email, name, hash, verifiedAt).Scan(&p.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return Profile{}, ErrEmailTaken
	}
	if err != nil {
		return Profile{}, fmt.Errorf("auth: create user: %w", err)
	}
	return p, nil
}

const profileColumns = `subject, coalesce(email, ''), coalesce(name, ''), password_hash IS NOT NULL, verified_at, created_at`

func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	err := row.Scan(&p.Subject, &p.Email, &p.Name, &p.HasPassword, &p.VerifiedAt, &p.CreatedAt)
	return p, err
}

// Profile returns a user of the pack's own table; pgx.ErrNoRows when the
// subject is not one (an app with its own users table).
func (a *Auth) Profile(ctx context.Context, subject string) (Profile, error) {
	return scanProfile(a.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM auth_user WHERE subject = $1`, subject))
}

// ProfileByEmail looks a local account up by its address.
func (a *Auth) ProfileByEmail(ctx context.Context, email string) (Profile, error) {
	return scanProfile(a.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM auth_user WHERE email = $1`, NormalizeEmail(email)))
}

func (a *Auth) passwordHash(ctx context.Context, email string) (subject, hash string, verified bool, err error) {
	var h *string
	var v *time.Time
	err = a.pool.QueryRow(ctx, `SELECT subject, password_hash, verified_at FROM auth_user WHERE email = $1`, email).Scan(&subject, &h, &v)
	if h != nil {
		hash = *h
	}
	return subject, hash, v != nil, err
}

// SetPassword replaces a user's password and ends their other sessions.
func (a *Auth) SetPassword(ctx context.Context, subject, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := a.pool.Exec(ctx, `UPDATE auth_user SET password_hash = $2, updated_at = now() WHERE subject = $1`, subject, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return router.NotFound("user")
	}
	return a.RevokeAll(ctx, subject)
}

// MarkVerified records that the user's email was verified.
func (a *Auth) MarkVerified(ctx context.Context, subject string) error {
	_, err := a.pool.Exec(ctx, `UPDATE auth_user SET verified_at = coalesce(verified_at, now()), updated_at = now() WHERE subject = $1`, subject)
	return err
}

// Identities lists the provider accounts linked to a subject.
func (a *Auth) Identities(ctx context.Context, subject string) ([]LinkedIdentity, error) {
	rows, err := a.pool.Query(ctx, `SELECT provider, provider_subject, coalesce(email, ''), coalesce(name, ''), created_at, last_used_at FROM auth_identity WHERE subject = $1 ORDER BY created_at`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LinkedIdentity
	for rows.Next() {
		var li LinkedIdentity
		if err := rows.Scan(&li.Provider, &li.Subject, &li.Email, &li.Name, &li.CreatedAt, &li.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, li)
	}
	return out, rows.Err()
}

// SignInMethods names how a subject signs in: "password" when the local
// account has one, then the linked providers. Empty for a subject the
// pack's tables do not know.
func (a *Auth) SignInMethods(ctx context.Context, subject string) ([]string, error) {
	var out []string
	var hasPassword bool
	err := a.pool.QueryRow(ctx, `SELECT password_hash IS NOT NULL FROM auth_user WHERE subject = $1`, subject).Scan(&hasPassword)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if hasPassword {
		out = append(out, "password")
	}
	ids, err := a.Identities(ctx, subject)
	if err != nil {
		return nil, err
	}
	for _, li := range ids {
		out = append(out, li.Provider)
	}
	return out, nil
}

// link resolves a provider identity to a subject: the linked one; else a
// local account with the same verified address, which the identity
// joins; else a new account. The provider's name and picture fill an
// empty profile.
func (a *Auth) link(ctx context.Context, id Identity) (Profile, error) {
	key := id.Provider + ":" + id.Subject
	var subject string
	err := a.pool.QueryRow(ctx, `UPDATE auth_identity SET last_used_at = now(), email = coalesce(NULLIF($2, ''), email), name = coalesce(NULLIF($3, ''), name) WHERE id = $1 RETURNING subject`,
		key, id.Email, id.Name).Scan(&subject)
	switch {
	case err == nil:
		return a.Profile(ctx, subject)
	case !errors.Is(err, pgx.ErrNoRows):
		return Profile{}, fmt.Errorf("auth: identity: %w", err)
	}
	email := NormalizeEmail(id.Email)
	var p Profile
	if email != "" && id.EmailVerified {
		p, err = a.ProfileByEmail(ctx, email)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Profile{}, err
		}
		if err == nil {
			if err := a.MarkVerified(ctx, p.Subject); err != nil {
				return Profile{}, err
			}
		}
	}
	if p.Subject == "" {
		var verified *time.Time
		if email != "" && id.EmailVerified {
			now := time.Now()
			verified = &now
		}
		p, err = a.createUser(ctx, email, id.Name, "", verified)
		if errors.Is(err, ErrEmailTaken) {
			// The address belongs to a local account the provider does
			// not vouch for: a separate account without an address.
			p, err = a.createUser(ctx, "", id.Name, "", nil)
		}
		if err != nil {
			return Profile{}, err
		}
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO auth_identity (id, provider, provider_subject, subject, email, name) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''))`,
		key, id.Provider, id.Subject, p.Subject, email, id.Name); err != nil {
		return Profile{}, fmt.Errorf("auth: link identity: %w", err)
	}
	return p, nil
}

// Handlers.

func (s *signin) session(ctx context.Context, req interface{ SetCookie(*http.Cookie) }, p Profile, provider string, withToken bool) (SignedIn, error) {
	a := From(ctx)
	claims := map[string]any{}
	if p.Email != "" {
		claims["email"] = p.Email
	}
	if p.Name != "" {
		claims["name"] = p.Name
	}
	if provider != "" {
		claims["provider"] = provider
	}
	if s.opt.Claims != nil {
		extra, err := s.opt.Claims(ctx, p)
		if err != nil {
			return SignedIn{}, err
		}
		for k, v := range extra {
			claims[k] = v
		}
	}
	tokens, err := a.Login(ctx, p.Subject, claims)
	if err != nil {
		return SignedIn{}, err
	}
	for _, c := range a.Cookies(tokens) {
		req.SetCookie(c)
	}
	out, err := s.describe(ctx, p)
	if err != nil {
		return SignedIn{}, err
	}
	if withToken {
		out.AccessToken = tokens.Access
	}
	return out, nil
}

func (s *signin) describe(ctx context.Context, p Profile) (SignedIn, error) {
	methods, err := From(ctx).SignInMethods(ctx, p.Subject)
	if err != nil {
		return SignedIn{}, err
	}
	if methods == nil {
		methods = []string{}
	}
	return SignedIn{Subject: p.Subject, Email: p.Email, Name: p.Name, Verified: p.Verified(), Providers: methods}, nil
}

func (s *signin) authRegister(ctx context.Context, req *router.Request[Registration]) (SignedIn, error) {
	a := From(ctx)
	email := NormalizeEmail(req.Body.Email)
	if err := a.ValidatePassword(req.Body.Password, email); err != nil {
		return SignedIn{}, err
	}
	hash, err := HashPassword(req.Body.Password)
	if err != nil {
		return SignedIn{}, err
	}
	p, err := a.createUser(ctx, email, strings.TrimSpace(req.Body.Name), hash, nil)
	if err != nil {
		return SignedIn{}, err
	}
	if err := s.sendLink(ctx, p, PurposeVerifyEmail); err != nil {
		return SignedIn{}, err
	}
	req.Status(http.StatusCreated)
	return s.session(ctx, req, p, "", true)
}

// ErrBadCredentials is the 401 of a wrong email or password; the reply
// does not say which.
var ErrBadCredentials = router.Errorf(http.StatusUnauthorized, "wrong email or password")

// ErrNotVerified is the 403 of a password sign-in before the email is
// verified, with Options.RequireVerified.
var ErrNotVerified = router.Errorf(http.StatusForbidden, "email not verified")

func (s *signin) authLogin(ctx context.Context, req *router.Request[Credentials]) (SignedIn, error) {
	a := From(ctx)
	subject, hash, verified, err := a.passwordHash(ctx, NormalizeEmail(req.Body.Email))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (hash == "" || !CheckPassword(hash, req.Body.Password))) {
		return SignedIn{}, ErrBadCredentials
	}
	if err != nil {
		return SignedIn{}, err
	}
	if s.opt.RequireVerified && !verified {
		return SignedIn{}, ErrNotVerified
	}
	p, err := a.Profile(ctx, subject)
	if err != nil {
		return SignedIn{}, err
	}
	return s.session(ctx, req, p, "", true)
}

func (s *signin) authLogout(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	if err := From(ctx).Logout(ctx, CurrentUser(ctx).SessionID); err != nil {
		return router.None{}, err
	}
	for _, c := range ClearedCookies() {
		req.SetCookie(c)
	}
	return router.None{}, nil
}

// current reads the signed-in user's profile; an account the pack's
// table no longer has is a 401.
func (s *signin) current(ctx context.Context) (SignedIn, error) {
	p, err := From(ctx).Profile(ctx, CurrentUser(ctx).ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignedIn{}, router.Errorf(http.StatusUnauthorized, "account no longer exists")
	}
	if err != nil {
		return SignedIn{}, err
	}
	return s.describe(ctx, p)
}

func (s *signin) authSession(ctx context.Context, req *router.Request[router.None]) (SessionState, error) {
	if CurrentUser(ctx) == nil {
		return SessionState{}, nil
	}
	out, err := s.current(ctx)
	if err != nil {
		return SessionState{}, err
	}
	return SessionState{User: &out}, nil
}

func (s *signin) authMe(ctx context.Context, req *router.Request[router.None]) (SignedIn, error) {
	return s.current(ctx)
}

func (s *signin) authVerify(ctx context.Context, req *router.Request[TokenRequest]) (router.None, error) {
	a := From(ctx)
	subject, err := a.ConsumeToken(ctx, PurposeVerifyEmail, req.Body.Token)
	if err != nil {
		return router.None{}, err
	}
	return router.None{}, a.MarkVerified(ctx, subject)
}

// authForgot sends a reset link when the email is registered, and
// replies 204 either way so the reply does not reveal who is registered.
func (s *signin) authForgot(ctx context.Context, req *router.Request[EmailRequest]) (router.None, error) {
	p, err := From(ctx).ProfileByEmail(ctx, req.Body.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return router.None{}, nil
	}
	if err != nil {
		return router.None{}, err
	}
	return router.None{}, s.sendLink(ctx, p, PurposeResetPassword)
}

func (s *signin) authReset(ctx context.Context, req *router.Request[PasswordReset]) (router.None, error) {
	a := From(ctx)
	subject, err := a.ConsumeToken(ctx, PurposeResetPassword, req.Body.Token)
	if err != nil {
		return router.None{}, err
	}
	p, err := a.Profile(ctx, subject)
	if err != nil {
		return router.None{}, err
	}
	if err := a.ValidatePassword(req.Body.Password, p.Email); err != nil {
		return router.None{}, err
	}
	// The link proved the address.
	if err := a.MarkVerified(ctx, subject); err != nil {
		return router.None{}, err
	}
	return router.None{}, a.SetPassword(ctx, subject, req.Body.Password)
}

// authPassword changes the signed-in user's password: the current one
// must match when the account has one. Every other session ends.
func (s *signin) authPassword(ctx context.Context, req *router.Request[PasswordChange]) (router.None, error) {
	a := From(ctx)
	p, err := a.Profile(ctx, CurrentUser(ctx).ID)
	if err != nil {
		return router.None{}, err
	}
	if p.HasPassword {
		_, hash, _, err := a.passwordHash(ctx, p.Email)
		if err != nil || !CheckPassword(hash, req.Body.Current) {
			return router.None{}, router.Errorf(http.StatusForbidden, "current password does not match")
		}
	}
	if err := a.ValidatePassword(req.Body.Password, p.Email); err != nil {
		return router.None{}, err
	}
	if err := a.SetPassword(ctx, p.Subject, req.Body.Password); err != nil {
		return router.None{}, err
	}
	// Keep this session: SetPassword ended them all.
	tokens, err := a.Login(ctx, p.Subject, CurrentUser(ctx).Claims)
	if err != nil {
		return router.None{}, err
	}
	for _, c := range a.Cookies(tokens) {
		req.SetCookie(c)
	}
	return router.None{}, nil
}

func (s *signin) authProviders(ctx context.Context, req *router.Request[router.None]) (ProviderList, error) {
	out := ProviderList{Providers: []ProviderLink{}, Local: !s.opt.NoLocal, Register: !s.opt.NoLocal && !s.opt.NoRegister}
	for _, p := range s.providers() {
		out.Providers = append(out.Providers, ProviderLink{Name: p.Name(), Label: p.Label(), URL: Prefix + "/" + p.Name() + "/start"})
	}
	return out, nil
}

// sendLink emails the verification or reset link through the mail pack
// when it runs: the app's mail/auth_verify.* or mail/auth_reset.*
// templates (Data: App, Link, Email, Name), else a plain text.
func (s *signin) sendLink(ctx context.Context, p Profile, purpose string) error {
	m, ok := lidza.Optional[*mail.Mail](ctx)
	if !ok || p.Email == "" {
		return nil
	}
	token, err := From(ctx).IssueToken(ctx, purpose, p.Subject, 0)
	if err != nil {
		return err
	}
	app := s.opt.Title
	if app == "" {
		app = "the app"
	}
	var subject, tmpl, link, text string
	switch purpose {
	case PurposeVerifyEmail:
		subject, tmpl = "Verify your email", "auth_verify"
		link = m.Link(s.opt.VerifyPath + "?token=" + url.QueryEscape(token))
		text = fmt.Sprintf("Open this link to verify your email address for %s:\n\n%s\n\nIf you did not register, ignore this message.\n", app, link)
	default:
		subject, tmpl = "Reset your password", "auth_reset"
		link = m.Link(s.opt.ResetPath + "?token=" + url.QueryEscape(token))
		text = fmt.Sprintf("Open this link to set a new password for %s:\n\n%s\n\nIf you did not ask for it, ignore this message; your password stays as it is.\n", app, link)
	}
	msg := mail.Message{To: p.Email, Subject: subject}
	if hasTemplate(m, tmpl) {
		msg.Template = tmpl
		msg.Data = map[string]string{"App": app, "Link": link, "Email": p.Email, "Name": p.Name}
	} else {
		msg.Text = text
	}
	_, err = m.Send(ctx, msg)
	return err
}

func hasTemplate(m *mail.Mail, name string) bool {
	for _, t := range m.Templates() {
		if t == name {
			return true
		}
	}
	return false
}
