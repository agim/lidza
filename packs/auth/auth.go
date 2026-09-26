// Package auth is the official authentication pack: password hashing
// (argon2id), signed access tokens (JWT HS256, short-lived), refresh
// sessions in Postgres (revocable, survive restarts), cookies for
// browsers and a bearer header for other clients, and the middleware that
// puts the user in the request context. The app owns its users table and
// the lookup; the pack owns everything from "password matches" onward.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/validate"
)

// Config comes from the environment.
type Config struct {
	// Secret signs access tokens; at least 32 bytes, the same on every
	// node. Rotating it logs everyone out.
	Secret string `env:"AUTH_SECRET" required:"true"`
	// AccessTTL bounds an access token; RefreshTTL a session.
	AccessTTL  time.Duration `env:"AUTH_ACCESS_TTL" default:"15m"`
	RefreshTTL time.Duration `env:"AUTH_REFRESH_TTL" default:"720h"`
	// CookieSecure marks cookies Secure; set it in production behind TLS.
	CookieSecure bool `env:"AUTH_COOKIE_SECURE"`
	// Issuer is the JWT iss claim.
	Issuer string `env:"AUTH_ISSUER" default:"lidza"`
	// LoginRPS and LoginBurst bound credential attempts per client
	// address on the routes wrapped with Throttle (login, register,
	// password reset).
	LoginRPS   float64 `env:"AUTH_LOGIN_RPS" default:"1"`
	LoginBurst int     `env:"AUTH_LOGIN_BURST" default:"5"`
	// MinPasswordLength is the floor ValidatePassword applies.
	MinPasswordLength int `env:"AUTH_MIN_PASSWORD" default:"10"`
	// TokenTTL bounds the one-time tokens IssueToken creates (email
	// verification, password reset) unless the call says otherwise.
	TokenTTL time.Duration `env:"AUTH_TOKEN_TTL" default:"1h"`
}

// Cookie names.
const (
	AccessCookie  = "lidza_access"
	RefreshCookie = "lidza_refresh"
)

// Auth is the running pack.
type Auth struct {
	cfg     Config
	pool    *pgxpool.Pool
	firstMu sync.Mutex
	first   string
}

// Pack returns the pack for packs.go. It needs the db pack started first.
func Pack() lidza.Pack { return &Auth{} }

// New builds the pack outside the lifecycle (tests).
func New(cfg Config, pool *pgxpool.Pool) (*Auth, error) {
	if len(cfg.Secret) < 32 {
		return nil, errors.New("AUTH_SECRET must be at least 32 bytes")
	}
	if cfg.AccessTTL <= 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = 30 * 24 * time.Hour
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "lidza"
	}
	return &Auth{cfg: cfg, pool: pool}, nil
}

// From returns the pack from a request context.
func From(ctx context.Context) *Auth { return lidza.Service[*Auth](ctx) }

// Name implements lidza.Pack.
func (a *Auth) Name() string { return "lidza/auth" }

// Start reads the configuration and takes the pool from the db pack.
func (a *Auth) Start(ctx context.Context, s *lidza.Services) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	pool, ok := s.Lookup(poolType())
	if !ok {
		return errors.New("auth needs the db pack: list \"lidza/db\" before \"lidza/auth\" in lidza.json")
	}
	built, err := New(cfg, pool.(*pgxpool.Pool))
	if err != nil {
		return err
	}
	a.cfg, a.pool = built.cfg, built.pool
	lidza.Provide(s, a)
	return nil
}

// Stop implements lidza.Pack.
func (a *Auth) Stop(context.Context) error { return nil }

// User is the authenticated principal.
type User struct {
	// ID is the subject the app passed to Login.
	ID string `json:"id"`
	// Claims are what the app attached at login (roles, name).
	Claims map[string]any `json:"claims,omitempty"`
	// SessionID identifies the refresh session; empty for bearer tokens
	// issued without one.
	SessionID string `json:"sessionId,omitempty"`
}

// Tokens are what Login and Refresh return.
type Tokens struct {
	Access    string    `json:"accessToken"`
	Refresh   string    `json:"refreshToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Login opens a session for subject with claims and returns its tokens.
// The app verifies the credentials first (CheckPassword).
func (a *Auth) Login(ctx context.Context, subject string, claims map[string]any) (Tokens, error) {
	if err := a.seen(ctx, subject, claims); err != nil {
		return Tokens{}, err
	}
	sessionID := randomID()
	refresh := randomID()
	expires := time.Now().Add(a.cfg.RefreshTTL)
	_, err := a.pool.Exec(ctx, `INSERT INTO auth_session (id, subject, refresh_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		sessionID, subject, hashToken(refresh), expires)
	if err != nil {
		return Tokens{}, fmt.Errorf("auth: create session: %w", err)
	}
	return a.tokens(subject, claims, sessionID, refresh)
}

// RefreshGrace is how long the refresh token a Refresh just replaced
// keeps working: requests a browser sent in parallel with an expired
// access token all carry the old refresh cookie, and the first one to
// arrive rotates it. Within the grace the others get a new access token
// only (Tokens.Refresh empty); the session is not rotated again.
const RefreshGrace = time.Minute

// Refresh rotates a session: the refresh token is replaced, a new access
// token issued. A revoked or expired session fails. The token Refresh
// replaced stays valid for RefreshGrace, for an access token only.
func (a *Auth) Refresh(ctx context.Context, refreshToken string, claims map[string]any) (Tokens, error) {
	var sessionID, subject string
	var current bool
	err := a.pool.QueryRow(ctx, `SELECT id, subject, refresh_hash = $1 FROM auth_session
		WHERE (refresh_hash = $1 OR (prev_refresh_hash = $1 AND rotated_at > now() - $2::interval))
		AND revoked_at IS NULL AND expires_at > now()`,
		hashToken(refreshToken), fmt.Sprintf("%d seconds", int(RefreshGrace.Seconds()))).Scan(&sessionID, &subject, &current)
	if err != nil {
		return Tokens{}, router.Errorf(http.StatusUnauthorized, "session expired")
	}
	if err := a.seen(ctx, subject, claims); err != nil {
		return Tokens{}, err
	}
	if !current {
		return a.tokens(subject, claims, sessionID, "")
	}
	refresh := randomID()
	if _, err := a.pool.Exec(ctx, `UPDATE auth_session SET prev_refresh_hash = refresh_hash, rotated_at = now(), refresh_hash = $1 WHERE id = $2`, hashToken(refresh), sessionID); err != nil {
		return Tokens{}, err
	}
	return a.tokens(subject, claims, sessionID, refresh)
}

// Logout revokes the session of the current user.
func (a *Auth) Logout(ctx context.Context, sessionID string) error {
	_, err := a.pool.Exec(ctx, `UPDATE auth_session SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, sessionID)
	return err
}

// RevokeAll ends every session of a subject (password change, compromise).
func (a *Auth) RevokeAll(ctx context.Context, subject string) error {
	_, err := a.pool.Exec(ctx, `UPDATE auth_session SET revoked_at = now() WHERE subject = $1 AND revoked_at IS NULL`, subject)
	return err
}

// Token purposes for IssueToken and ConsumeToken.
const (
	PurposeVerifyEmail   = "verify_email"
	PurposeResetPassword = "reset_password"
)

// IssueToken creates a one-time token for a purpose and subject (an
// email address, a user id), valid for ttl (the configured TokenTTL when
// zero). Only its hash is stored; the token goes to the user, in a link
// the app sends. Issuing a new token for the same purpose and subject
// invalidates the earlier ones.
func (a *Auth) IssueToken(ctx context.Context, purpose, subject string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = a.cfg.TokenTTL
	}
	token := randomID() + randomID()
	if _, err := a.pool.Exec(ctx, `UPDATE auth_token SET used_at = now() WHERE purpose = $1 AND subject = $2 AND used_at IS NULL`, purpose, subject); err != nil {
		return "", fmt.Errorf("auth: invalidate tokens: %w", err)
	}
	_, err := a.pool.Exec(ctx, `INSERT INTO auth_token (id, purpose, subject, hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		randomID(), purpose, subject, hashToken(token), time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("auth: create token: %w", err)
	}
	return token, nil
}

// ConsumeToken marks a token used and returns its subject. An unknown,
// used or expired token is a 401 the client can show; the message does
// not say which.
func (a *Auth) ConsumeToken(ctx context.Context, purpose, token string) (string, error) {
	var subject string
	err := a.pool.QueryRow(ctx, `UPDATE auth_token SET used_at = now() WHERE purpose = $1 AND hash = $2 AND used_at IS NULL AND expires_at > now() RETURNING subject`,
		purpose, hashToken(token)).Scan(&subject)
	if err != nil {
		return "", router.Errorf(http.StatusUnauthorized, "invalid or expired token")
	}
	return subject, nil
}

// Throttle is middleware for the routes that take credentials (login,
// register, password reset): a token bucket per client address with
// AUTH_LOGIN_RPS and AUTH_LOGIN_BURST, replying 429 beyond it. Wrap the
// route: router.Route(r, "POST /api/v1/auth/login", login, auth.Throttle()).
// Behind a proxy that sets X-Forwarded-For, key on it with ThrottleBy.
func Throttle() middleware.Middleware {
	return ThrottleBy(nil)
}

// ThrottleBy is Throttle with a key function (nil: the client address).
func ThrottleBy(key func(r *http.Request) string) middleware.Middleware {
	var once sync.Once
	var limited http.Handler
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() {
				cfg := From(r.Context()).cfg
				limited = middleware.RateLimit(middleware.RateLimitOptions{RPS: cfg.LoginRPS, Burst: cfg.LoginBurst, Key: key})(next)
			})
			limited.ServeHTTP(w, r)
		})
	}
}

// ValidatePassword applies the password policy: at least
// AUTH_MIN_PASSWORD characters, not among the most common passwords, not
// the email address or its local part. The error is a field error on
// "password", so a handler returns it as is for a 422.
func (a *Auth) ValidatePassword(password, email string) error {
	problem := ""
	switch lower := strings.ToLower(password); {
	case len([]rune(password)) < a.cfg.MinPasswordLength:
		problem = fmt.Sprintf("at least %d characters", a.cfg.MinPasswordLength)
	case commonPasswords[lower]:
		problem = "too common"
	case email != "" && (lower == strings.ToLower(email) || lower == strings.ToLower(strings.SplitN(email, "@", 2)[0])):
		problem = "must not be the email address"
	}
	if problem == "" {
		return nil
	}
	return &validate.Errors{Fields: []validate.FieldError{{Field: "password", Rule: "policy", Message: problem}}}
}

// commonPasswords are the most used passwords of the last years' breach
// lists, lowercased; anything here is refused whatever its length.
var commonPasswords = map[string]bool{}

func init() {
	for _, p := range strings.Fields(commonPasswordList) {
		commonPasswords[p] = true
	}
}

const commonPasswordList = `123456 123456789 12345678 password qwerty 1234567890 1234567 111111 123123 abc123 1q2w3e4r
password1 password123 iloveyou 12345678910 admin123 letmein welcome monkey dragon sunshine princess football
qwerty123 1qaz2wsx passw0rd baseball master superman trustno1 whatever shadow michael jennifer 123qwe 654321
qwertyuiop 000000 1234567891 zaq12wsx 1q2w3e4r5t asdfghjkl 987654321 password12 charlie donald access
qazwsx starwars hello123 welcome1 adminadmin administrator changeme secret123 p@ssw0rd passw0rd1
liverpool computer internet 123abc 1234qwer qwer1234 pokemon batman freedom mustang jordan23
11111111 12341234 aaaaaaaa abcd1234 letmein1 password! password2 welcome123 summer2023 winter2023`

func (a *Auth) tokens(subject string, claims map[string]any, sessionID, refresh string) (Tokens, error) {
	expires := time.Now().Add(a.cfg.AccessTTL)
	mc := jwt.MapClaims{"sub": subject, "iss": a.cfg.Issuer, "sid": sessionID, "iat": time.Now().Unix(), "exp": expires.Unix()}
	if len(claims) > 0 {
		mc["app"] = claims
	}
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, mc).SignedString([]byte(a.cfg.Secret))
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{Access: access, Refresh: refresh, ExpiresAt: expires}, nil
}

// Verify parses an access token.
func (a *Auth) Verify(token string) (*User, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(a.cfg.Secret), nil
	}, jwt.WithIssuer(a.cfg.Issuer), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	mc, _ := parsed.Claims.(jwt.MapClaims)
	u := &User{ID: fmt.Sprint(mc["sub"])}
	if sid, ok := mc["sid"].(string); ok {
		u.SessionID = sid
	}
	if app, ok := mc["app"].(map[string]any); ok {
		u.Claims = app
	}
	return u, nil
}

// Cookies returns the tokens as HttpOnly cookies for a browser client;
// a typed handler adds them with req.SetCookie. The access cookie lives
// as long as the session, not the token: once the token inside expires,
// Require and Optional renew it from the refresh cookie and set both
// again (sessions slide; no refresh route or client code is needed).
// A Tokens without Refresh (a Refresh inside RefreshGrace) sets the
// access cookie only.
func (a *Auth) Cookies(t Tokens) []*http.Cookie {
	cookies := []*http.Cookie{
		{Name: AccessCookie, Value: t.Access, Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(a.cfg.RefreshTTL.Seconds())},
	}
	if t.Refresh != "" {
		cookies = append(cookies, &http.Cookie{Name: RefreshCookie, Value: t.Refresh, Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.cfg.RefreshTTL.Seconds())})
	}
	return cookies
}

// SetCookies writes the token cookies on a raw ResponseWriter.
func (a *Auth) SetCookies(w http.ResponseWriter, t Tokens) {
	for _, c := range a.Cookies(t) {
		http.SetCookie(w, c)
	}
}

// ClearedCookies expire the token cookies; use on logout.
func ClearedCookies() []*http.Cookie {
	return []*http.Cookie{
		{Name: AccessCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1},
		{Name: RefreshCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1},
		// The refresh cookie's path before v0.1.3.
		{Name: RefreshCookie, Value: "", Path: "/api/v1/auth", HttpOnly: true, MaxAge: -1},
	}
}

type userKey struct{}

// WithUser puts a user in the context, for a test or a middleware that
// authenticates another way; Require and Optional do it for tokens.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// FirstSubject returns the subject of the earliest session ever opened:
// the first user to sign in, the app's first account. Empty until
// someone has. The answer is cached once known; sessions are revoked,
// never deleted.
func (a *Auth) FirstSubject(ctx context.Context) (string, error) {
	a.firstMu.Lock()
	defer a.firstMu.Unlock()
	if a.first != "" {
		return a.first, nil
	}
	var subject string
	err := a.pool.QueryRow(ctx, `SELECT subject FROM auth_session ORDER BY created_at, id LIMIT 1`).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	a.first = subject
	return subject, nil
}

// CurrentUser returns the user Require put in the context, or nil.
func CurrentUser(ctx context.Context) *User {
	u, _ := ctx.Value(userKey{}).(*User)
	return u
}

// Require is middleware that authenticates every request under it from
// the Authorization: Bearer header or the access cookie and replies 401
// otherwise, also when the token's session has been ended (logout,
// RevokeAll). A cookie-authenticated state-changing request must carry
// Content-Type: application/json (the generated clients always do), which
// a cross-site form cannot send, or a Sec-Fetch-Site header saying it is
// same-origin; so the cookie cannot be ridden by another origin (CSRF).
func Require() func(http.Handler) http.Handler {
	return guard(true)
}

// Optional is middleware for routes that serve both visitors and users:
// a request with a valid token gets its user (CurrentUser), one without
// a token continues anonymously (CurrentUser is nil), and an invalid
// token or a cross-site cookie is refused as with Require.
func Optional() func(http.Handler) http.Handler {
	return guard(false)
}

func guard(required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := From(r.Context())
			token, fromCookie := bearer(r)
			var u *User
			var err error
			if token != "" {
				u, err = a.Verify(token)
			}
			// A browser whose access token is missing or expired but whose
			// refresh cookie is valid gets a new one on this request: the
			// session slides without a refresh route. Bearer clients refresh
			// explicitly.
			if u == nil && (token == "" || fromCookie) {
				if c, cerr := r.Cookie(RefreshCookie); cerr == nil && c.Value != "" {
					if renewed, rerr := a.Refresh(r.Context(), c.Value, a.expiredClaims(token)); rerr == nil {
						a.SetCookies(w, renewed)
						token, fromCookie = renewed.Access, true
						u, err = a.Verify(token)
					}
				}
			}
			if token == "" {
				if required {
					router.Error(w, http.StatusUnauthorized, "authentication required")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				router.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			if u.SessionID != "" && !a.sessionActive(r.Context(), u.SessionID) {
				router.Error(w, http.StatusUnauthorized, "session ended")
				return
			}
			if fromCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions &&
				!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") && !sameOrigin(r) {
				router.Error(w, http.StatusForbidden, "cookie sessions must send Content-Type: application/json")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
		})
	}
}

// expiredClaims returns the app claims of an access token whose signature
// is valid but whose time is up, so a renewed token carries them on; nil
// for no token or a bad one.
func (a *Auth) expiredClaims(token string) map[string]any {
	if token == "" {
		return nil
	}
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(a.cfg.Secret), nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		return nil
	}
	mc, _ := parsed.Claims.(jwt.MapClaims)
	app, _ := mc["app"].(map[string]any)
	return app
}

// sessionActive reports whether the session behind an access token is
// still open: one primary-key lookup per authenticated request, so a
// logout, a password reset or RevokeAll takes effect at once instead of
// when the access token expires.
func (a *Auth) sessionActive(ctx context.Context, sessionID string) bool {
	var one int
	err := a.pool.QueryRow(ctx, `SELECT 1 FROM auth_session s WHERE s.id = $1 AND s.revoked_at IS NULL AND s.expires_at > now()
		AND NOT EXISTS (SELECT 1 FROM auth_account a WHERE a.subject = s.subject AND a.disabled_at IS NOT NULL)`, sessionID).Scan(&one)
	return err == nil
}

// sameOrigin reports whether the browser declared the request same-origin
// (or user-initiated) through Sec-Fetch-Site.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	}
	return false
}

func bearer(r *http.Request) (token string, fromCookie bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer "), false
	}
	if c, err := r.Cookie(AccessCookie); err == nil {
		return c.Value, true
	}
	return "", false
}

// RefreshTokenFrom reads the refresh token from the JSON body field
// "refreshToken" or the refresh cookie.
func RefreshTokenFrom(r *http.Request, body string) string {
	if body != "" {
		return body
	}
	if c, err := r.Cookie(RefreshCookie); err == nil {
		return c.Value
	}
	return ""
}

// HashPassword returns an argon2id hash in the encoded form
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const mem, iters, par, keyLen = 64 * 1024, 3, 2, 32
	hash := argon2.IDKey([]byte(password), salt, iters, mem, par, keyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", mem, iters, par, enc.EncodeToString(salt), enc.EncodeToString(hash)), nil
}

// CheckPassword reports whether password matches the encoded hash.
func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, iters uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iters, &par); err != nil {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iters, mem, par, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	sum := argon2.IDKey([]byte(t), []byte("lidza-refresh"), 1, 8*1024, 1, 32)
	return hex.EncodeToString(sum)
}

// poolType is the service key of the db pack's pool.
func poolType() reflectType { return typeOf[*pgxpool.Pool]() }

// Ensure the db pack's type stays in the import graph so packs.go order
// mistakes surface at compile time, not at runtime.
var _ = db.From
