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
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/router"
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
}

// Cookie names.
const (
	AccessCookie  = "lidza_access"
	RefreshCookie = "lidza_refresh"
)

// Auth is the running pack.
type Auth struct {
	cfg  Config
	pool *pgxpool.Pool
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
	*a = *built
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

// Refresh rotates a session: the refresh token is replaced, a new access
// token issued. A revoked or expired session fails.
func (a *Auth) Refresh(ctx context.Context, refreshToken string, claims map[string]any) (Tokens, error) {
	var sessionID, subject string
	err := a.pool.QueryRow(ctx, `SELECT id, subject FROM auth_session WHERE refresh_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		hashToken(refreshToken)).Scan(&sessionID, &subject)
	if err != nil {
		return Tokens{}, router.Errorf(http.StatusUnauthorized, "session expired")
	}
	refresh := randomID()
	if _, err := a.pool.Exec(ctx, `UPDATE auth_session SET refresh_hash = $1 WHERE id = $2`, hashToken(refresh), sessionID); err != nil {
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
// a typed handler adds them with req.SetCookie.
func (a *Auth) Cookies(t Tokens) []*http.Cookie {
	return []*http.Cookie{
		{Name: AccessCookie, Value: t.Access, Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(a.cfg.AccessTTL.Seconds())},
		{Name: RefreshCookie, Value: t.Refresh, Path: "/api/v1/auth", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.cfg.RefreshTTL.Seconds())},
	}
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
		{Name: RefreshCookie, Value: "", Path: "/api/v1/auth", HttpOnly: true, MaxAge: -1},
	}
}

type userKey struct{}

// CurrentUser returns the user Require put in the context, or nil.
func CurrentUser(ctx context.Context) *User {
	u, _ := ctx.Value(userKey{}).(*User)
	return u
}

// Require is middleware that authenticates every request under it from
// the Authorization: Bearer header or the access cookie and replies 401
// otherwise. A cookie-authenticated state-changing request must carry
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
			if token == "" {
				if required {
					router.Error(w, http.StatusUnauthorized, "authentication required")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			u, err := a.Verify(token)
			if err != nil {
				router.Error(w, http.StatusUnauthorized, "invalid or expired token")
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
