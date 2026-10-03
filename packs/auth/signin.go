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
	// NoVerifyEmail skips the "Verify your email" message registration
	// sends, for an app that sends its own (a welcome email from
	// OnSignUp) or does not verify addresses. The verify route stays: an
	// app that verifies later mails its own link with
	// IssueToken(ctx, PurposeVerifyEmail, profile.Subject, 0). With
	// RequireVerified and no link sent, password sign-in stays refused.
	NoVerifyEmail bool
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
	// OnSignUp runs once for every account the routes create: a
	// registration, or the first sign-in through a provider that makes a
	// new account (an identity that joins an existing account is not a
	// sign-up, and neither is CreateUser). It runs inside the transaction
	// that inserts the account, before the session opens: write the
	// app's own rows with tx (queries.New(tx)). An error rolls the
	// account back and fails the sign-up: registration replies with it
	// (a router.Errorf status as is, else 500), a provider sign-in lands
	// on FailurePath with ?error=signup.
	OnSignUp func(ctx context.Context, tx pgx.Tx, s SignUp) error
	// OnSignIn runs for every sign-in the routes make: a password, a
	// provider, and the one that follows a sign-up (after OnSignUp). It
	// runs before the session opens, so it suits work tied to the user
	// arriving (claiming invitations sent to their email, finishing a
	// join they started signed out). An error refuses the sign-in:
	// password sign-in and registration reply with it (a router.Errorf
	// status as is, else 500), a provider sign-in lands on FailurePath
	// with ?error=signin. Token refreshes are not sign-ins and do not run
	// it. SignIn carries the request (the client address, the app's own
	// cookies) and sets cookies on the reply (s.SetCookie), which go out
	// only when the sign-in succeeds.
	OnSignIn func(ctx context.Context, s SignIn) error
	// OnDeleteUser runs inside DeleteUser's transaction (the delete route
	// and the app's own calls), before the pack's rows go: delete or
	// anonymize the app's rows of the subject with tx. An error rolls the
	// whole deletion back. Setting it also serves POST
	// /api/v1/auth/delete, the signed-in user's "delete my account": an
	// app says what happens to its rows before users can delete
	// themselves (a no-op function when it keeps none).
	OnDeleteUser func(ctx context.Context, tx pgx.Tx, subject string) error
	// OnEvent runs after each account event the routes handle (the Event*
	// kinds: sign-ins and failed ones, sign-outs, password changes and
	// resets, verifications, sign-ups, deletions), for a security log. It
	// cannot refuse anything and runs on the request, so it stays quick
	// (an insert); the app logs its own errors.
	OnEvent func(ctx context.Context, e Event)
	// Client is the HTTP client the providers use (tests).
	Client *http.Client
}

// SignUp is a new account, as Options.OnSignUp sees it.
type SignUp struct {
	Profile Profile
	// Method is how the account signed up: "password" for a
	// registration, else the provider's name ("google").
	Method string
	// Identity is what the provider said about the user; nil for a
	// registration.
	Identity *Identity
	// Lang is the request's language (the i18n pack's locale, else the
	// first of Accept-Language; mail.Languages), for an app that keeps
	// the user's language; empty when the request names none.
	Lang string
}

// SignIn is a sign-in, as Options.OnSignIn sees it.
type SignIn struct {
	Profile Profile
	// Method is "password", or the provider's name ("google").
	Method string
	// Request is the request signing in: the login or register call, or
	// the provider's callback. Read the client address or the app's own
	// cookies from it (a pending action a visitor started signed out).
	Request *http.Request
	// Remember reports whether the session outlives the browser: false
	// for a sign-in with "remember": false (session cookies only).
	Remember bool
	// cookies collects what SetCookie adds; nil outside the routes.
	cookies *[]*http.Cookie
}

// SetCookie adds a cookie to the reply that signs the user in (a notice
// for the next page, clearing a cookie the app read from Request). The
// cookies go out only when the sign-in succeeds.
func (s SignIn) SetCookie(c *http.Cookie) {
	if s.cookies != nil {
		*s.cookies = append(*s.cookies, c)
	}
}

// MethodPassword is SignUp.Method for a registration with email and
// password, and the sign-in method SignInMethods names for a password.
const MethodPassword = "password"

// Prefix is where Mount registers the sign-in routes.
const Prefix = "/api/v1/auth"

// Mount registers the sign-in routes under /api/v1/auth: register,
// login, logout, session, me, verify, verify/resend, forgot, reset,
// password, delete,
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
		router.Route(r, "POST /api/v1/auth/login", s.authLogin, ThrottleSignIn())
		router.Route(r, "POST /api/v1/auth/verify", s.authVerify, Throttle())
		router.Route(r, "POST /api/v1/auth/verify/resend", s.authVerifyResend, Throttle(), Require())
		router.Route(r, "POST /api/v1/auth/forgot", s.authForgot, Throttle())
		router.Route(r, "POST /api/v1/auth/reset", s.authReset, Throttle())
		router.Route(r, "POST /api/v1/auth/password", s.authPassword, Require())
	}
	router.Route(r, "GET /api/v1/auth/session", s.authSession, Optional())
	router.Route(r, "GET /api/v1/auth/me", s.authMe, Require())
	router.Route(r, "POST /api/v1/auth/logout", s.authLogout, Require())
	if opt.OnDeleteUser != nil {
		router.Route(r, "POST /api/v1/auth/delete", s.authDelete, Throttle(), Require())
	}
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
	// Remember is the "remember me" box: false opens a session whose
	// cookies end with the browser, on this reply and every renewal.
	// Absent (null) remembers, as true does.
	Remember *bool `json:"remember,omitempty"`
}

// remembered reads a "remember me" field: absent means yes.
func remembered(b *bool) bool { return b == nil || *b }

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
	// Remember is Credentials.Remember for the session registration
	// opens: false ends it with the browser; absent remembers.
	Remember *bool `json:"remember,omitempty"`
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

// AccountDeletion confirms deleting the signed-in account: Password is
// the account's password, required when it has one. An account without
// a password (provider sign-in only) confirms by a sign-in within
// ReauthWindow instead.
type AccountDeletion struct {
	Password string `json:"password,omitempty"`
}

// ProviderLink is one sign-in button: its name, label and the URL to
// send the browser to (append ?redirect=/path to land elsewhere, and
// remember=false for a session that ends with the browser).
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
	return createUser(ctx, a.pool, NormalizeEmail(email), name, hash, nil)
}

// querier is a pool or a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func createUser(ctx context.Context, q querier, email, name, hash string, verifiedAt *time.Time) (Profile, error) {
	p := Profile{Subject: newUUID(), Email: email, Name: name, HasPassword: hash != "", VerifiedAt: verifiedAt}
	err := q.QueryRow(ctx, `INSERT INTO auth_user (subject, email, name, password_hash, verified_at) VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5) RETURNING created_at`,
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
		out = append(out, MethodPassword)
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
// joins; else a new account, created with its identity in one
// transaction with OnSignUp. The provider's name fills an empty profile.
func (s *signin) link(ctx context.Context, id Identity) (Profile, error) {
	a := From(ctx)
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
	insertIdentity := func(q querier, subject string) error {
		if _, err := q.Exec(ctx, `INSERT INTO auth_identity (id, provider, provider_subject, subject, email, name) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''))`,
			key, id.Provider, id.Subject, subject, email, id.Name); err != nil {
			return fmt.Errorf("auth: link identity: %w", err)
		}
		return nil
	}
	if p.Subject != "" {
		return p, insertIdentity(a.pool, p.Subject)
	}
	return s.signUp(ctx, func(tx pgx.Tx) (Profile, error) {
		var verified *time.Time
		if email != "" && id.EmailVerified {
			now := time.Now()
			verified = &now
		}
		// A savepoint: a taken address must not abort the transaction.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return Profile{}, err
		}
		p, err := createUser(ctx, sp, email, id.Name, "", verified)
		switch {
		case errors.Is(err, ErrEmailTaken):
			// The address belongs to a local account the provider does
			// not vouch for: a separate account without an address.
			if err := sp.Rollback(ctx); err != nil {
				return Profile{}, err
			}
			p, err = createUser(ctx, tx, "", id.Name, "", nil)
			if err != nil {
				return Profile{}, err
			}
		case err != nil:
			return Profile{}, err
		default:
			if err := sp.Commit(ctx); err != nil {
				return Profile{}, err
			}
		}
		return p, insertIdentity(tx, p.Subject)
	}, SignUp{Method: id.Provider, Identity: &id})
}

// signInError is an error of Options.OnSignIn; the typed routes reply
// with the wrapped error, a provider sign-in lands on ?error=signin.
type signInError struct{ err error }

func (e *signInError) Error() string { return e.err.Error() }
func (e *signInError) Unwrap() error { return e.err }

// signUpError is an error of Options.OnSignUp.
type signUpError struct{ err error }

func (e *signUpError) Error() string { return "auth: sign-up: " + e.err.Error() }
func (e *signUpError) Unwrap() error { return e.err }

// signUp creates an account with create inside a transaction and runs
// OnSignUp in it, so the account and the app's rows exist together or
// not at all.
func (s *signin) signUp(ctx context.Context, create func(tx pgx.Tx) (Profile, error), su SignUp) (Profile, error) {
	tx, err := From(ctx).pool.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	p, err := create(tx)
	if err != nil {
		return Profile{}, err
	}
	if s.opt.OnSignUp != nil {
		su.Profile = p
		if langs := mail.Languages(ctx); len(langs) > 0 {
			su.Lang = langs[0]
		}
		if err := s.opt.OnSignUp(ctx, tx, su); err != nil {
			return Profile{}, &signUpError{err}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, err
	}
	s.event(ctx, eventRequest(ctx), Event{Kind: EventSignedUp, Subject: p.Subject, Email: p.Email, Method: su.Method})
	return p, nil
}

// Handlers.

// session opens a session for p after OnSignIn, and sets its cookies and
// the ones OnSignIn added on req. r is the request signing in; remember
// is false for a session that ends with the browser.
func (s *signin) session(ctx context.Context, req interface{ SetCookie(*http.Cookie) }, r *http.Request, p Profile, provider string, withToken, remember bool) (SignedIn, error) {
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
	var appCookies []*http.Cookie
	if s.opt.OnSignIn != nil {
		method := provider
		if method == "" {
			method = MethodPassword
		}
		if err := s.opt.OnSignIn(ctx, SignIn{Profile: p, Method: method, Request: r, Remember: remember, cookies: &appCookies}); err != nil {
			return SignedIn{}, &signInError{err}
		}
	}
	tokens, err := a.LoginWith(ctx, p.Subject, claims, SessionOptions{SessionOnly: !remember})
	if err != nil {
		return SignedIn{}, err
	}
	for _, c := range appCookies {
		req.SetCookie(c)
	}
	for _, c := range a.Cookies(tokens) {
		req.SetCookie(c)
	}
	method := provider
	if method == "" {
		method = MethodPassword
	}
	s.event(ctx, r, Event{Kind: EventSignedIn, Subject: p.Subject, Email: p.Email, Method: method})
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
	ctx = withEventRequest(withRequestLanguage(ctx, req.Raw), req.Raw)
	p, err := s.signUp(ctx, func(tx pgx.Tx) (Profile, error) {
		return createUser(ctx, tx, email, strings.TrimSpace(req.Body.Name), hash, nil)
	}, SignUp{Method: MethodPassword})
	if err != nil {
		return SignedIn{}, err
	}
	if !s.opt.NoVerifyEmail {
		if err := s.sendLink(ctx, p, PurposeVerifyEmail); err != nil {
			return SignedIn{}, err
		}
	}
	req.Status(http.StatusCreated)
	return s.session(ctx, req, req.Raw, p, "", true, remembered(req.Body.Remember))
}

// ErrBadCredentials is the 401 of a wrong email or password; the reply
// does not say which.
var ErrBadCredentials = router.Errorf(http.StatusUnauthorized, "wrong email or password")

// ErrNotVerified is the 403 of a password sign-in before the email is
// verified, with Options.RequireVerified.
var ErrNotVerified = router.ErrorCode(http.StatusForbidden, "email_not_verified", "email not verified")

func (s *signin) authLogin(ctx context.Context, req *router.Request[Credentials]) (SignedIn, error) {
	a := From(ctx)
	email := NormalizeEmail(req.Body.Email)
	subject, hash, verified, err := a.passwordHash(ctx, email)
	failed := func(reason string) {
		s.event(ctx, req.Raw, Event{Kind: EventSignInFailed, Subject: subject, Email: email, Method: MethodPassword, Reason: reason})
	}
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (hash == "" || !CheckPassword(hash, req.Body.Password))) {
		failed("bad_credentials")
		return SignedIn{}, ErrBadCredentials
	}
	if err != nil {
		return SignedIn{}, err
	}
	if s.opt.RequireVerified && !verified {
		failed("not_verified")
		return SignedIn{}, ErrNotVerified
	}
	p, err := a.Profile(ctx, subject)
	if err != nil {
		return SignedIn{}, err
	}
	out, err := s.session(ctx, req, req.Raw, p, "", true, remembered(req.Body.Remember))
	var si *signInError
	switch {
	case errors.Is(err, ErrDisabled):
		failed("disabled")
	case errors.As(err, &si):
		failed("refused")
	}
	return out, err
}

func (s *signin) authLogout(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	u := CurrentUser(ctx)
	if err := From(ctx).Logout(ctx, u.SessionID); err != nil {
		return router.None{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventSignedOut, Subject: u.ID, Email: claimString(u.Claims, "email")})
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
	if err := a.MarkVerified(ctx, subject); err != nil {
		return router.None{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventEmailVerified, Subject: subject})
	return router.None{}, nil
}

// authVerifyResend emails the signed-in user a new verification link,
// for an unverified address; a verified one, or an app that does not
// verify addresses, gets 204 and no mail.
func (s *signin) authVerifyResend(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	req.Status(http.StatusNoContent)
	if s.opt.NoVerifyEmail {
		return router.None{}, nil
	}
	p, err := From(ctx).Profile(ctx, CurrentUser(ctx).ID)
	if err != nil {
		return router.None{}, err
	}
	if p.Verified() {
		return router.None{}, nil
	}
	return router.None{}, s.sendLink(withRequestLanguage(ctx, req.Raw), p, PurposeVerifyEmail)
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
	if err := s.sendLink(ctx, p, PurposeResetPassword); err != nil {
		return router.None{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventResetRequested, Subject: p.Subject, Email: p.Email})
	return router.None{}, nil
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
	if err := a.SetPassword(ctx, subject, req.Body.Password); err != nil {
		return router.None{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventPasswordReset, Subject: subject, Email: p.Email})
	return router.None{}, nil
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
			s.event(ctx, req.Raw, Event{Kind: EventPasswordCheckFailed, Subject: p.Subject, Email: p.Email})
			return router.None{}, router.ErrorCode(http.StatusForbidden, "wrong_password", "current password does not match")
		}
	}
	if err := a.ValidatePassword(req.Body.Password, p.Email); err != nil {
		return router.None{}, err
	}
	// Keep this session, and its "remember me": SetPassword ended them
	// all.
	remember := a.SessionRemembered(ctx, CurrentUser(ctx).SessionID)
	if err := a.SetPassword(ctx, p.Subject, req.Body.Password); err != nil {
		return router.None{}, err
	}
	tokens, err := a.LoginWith(ctx, p.Subject, CurrentUser(ctx).Claims, SessionOptions{SessionOnly: !remember})
	if err != nil {
		return router.None{}, err
	}
	for _, c := range a.Cookies(tokens) {
		req.SetCookie(c)
	}
	s.event(ctx, req.Raw, Event{Kind: EventPasswordChanged, Subject: p.Subject, Email: p.Email})
	return router.None{}, nil
}

// ReauthWindow is how recent the sign-in of an account without a
// password must be for the delete route: it stands in for the password.
const ReauthWindow = 10 * time.Minute

// ErrReauthenticate is the 403 of the delete route for an account
// without a password whose session is older than ReauthWindow: the
// client sends the user through the provider again (its start URL with
// ?redirect= back to the page), then repeats the request.
var ErrReauthenticate = router.ErrorCode(http.StatusForbidden, "reauthenticate", "sign in again to delete the account")

// authDelete deletes the signed-in user's account (DeleteUser, with
// OnDeleteUser) after a confirmation: the password when the account has
// one, else a sign-in within ReauthWindow. The cookies are cleared.
func (s *signin) authDelete(ctx context.Context, req *router.Request[AccountDeletion]) (router.None, error) {
	a := From(ctx)
	u := CurrentUser(ctx)
	p, err := a.Profile(ctx, u.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return router.None{}, router.Errorf(http.StatusUnauthorized, "account no longer exists")
	}
	if err != nil {
		return router.None{}, err
	}
	if p.HasPassword {
		_, hash, _, err := a.passwordHash(ctx, p.Email)
		if err != nil || !CheckPassword(hash, req.Body.Password) {
			s.event(ctx, req.Raw, Event{Kind: EventPasswordCheckFailed, Subject: p.Subject, Email: p.Email})
			return router.None{}, router.ErrorCode(http.StatusForbidden, "wrong_password", "password does not match")
		}
	} else {
		var created time.Time
		err := a.pool.QueryRow(ctx, `SELECT created_at FROM auth_session WHERE id = $1`, u.SessionID).Scan(&created)
		if err != nil || time.Since(created) > ReauthWindow {
			return router.None{}, ErrReauthenticate
		}
	}
	if err := a.DeleteUser(ctx, p.Subject); err != nil {
		return router.None{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventAccountDeleted, Subject: p.Subject, Email: p.Email})
	for _, c := range ClearedCookies() {
		req.SetCookie(c)
	}
	return router.None{}, nil
}

// withRequestLanguage records the request's Accept-Language for
// mail.Languages when the mail pack's middleware has not (the pack is
// not enabled), so SignUp.Lang has it.
func withRequestLanguage(ctx context.Context, r *http.Request) context.Context {
	if len(mail.Languages(ctx)) > 0 || r == nil {
		return ctx
	}
	return mail.WithAcceptLanguage(ctx, r.Header.Get("Accept-Language"))
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
// templates (Data: App, Link, Email, Name), in the request's language
// when the app has one for it (auth_verify.de.txt.tmpl, which may
// define "subject"), else a plain English text.
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
	if name, ok := m.TemplateFor(tmpl, mail.Languages(ctx)...); ok {
		msg.Template = name
		msg.Data = map[string]string{"App": app, "Link": link, "Email": p.Email, "Name": p.Name}
	} else {
		msg.Text = text
	}
	_, err = m.Send(ctx, msg)
	return err
}
