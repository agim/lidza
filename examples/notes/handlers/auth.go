package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/router"

	"notes/db/queries/gen"
	"notes/schema"
)

// uniqueViolation is Postgres' code for a duplicate key.
const uniqueViolation = "23505"

// normalizeEmail is how an address is stored and looked up: lowercased
// and trimmed, so a user signs in however they capitalize it.
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// send mails one of the templates in mail/ through the mail pack: the
// outbox keeps a copy (tests read it), the configured provider delivers.
// The link is a path of this app; Link makes it absolute with APP_URL.
func send(ctx context.Context, to, subject, template, path string) error {
	m := mail.From(ctx)
	_, err := m.Send(ctx, mail.Message{To: to, Subject: subject, Template: template, Data: map[string]string{"App": "notes", "Link": m.Link(path)}})
	return err
}

// Register creates a user from validated credentials, applies the
// password policy, sends the verification link and signs the user in. A
// taken email is a 409 the client can show; any other error is a 500.
func Register(ctx context.Context, req *router.Request[schema.Credentials]) (schema.Session, error) {
	if err := auth.From(ctx).ValidatePassword(req.Body.Password, req.Body.Email); err != nil {
		return schema.Session{}, err
	}
	hash, err := auth.HashPassword(req.Body.Password)
	if err != nil {
		return schema.Session{}, err
	}
	user, err := queries.New(db.From(ctx)).CreateUser(ctx, queries.CreateUserParams{Email: normalizeEmail(req.Body.Email), PasswordHash: hash})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return schema.Session{}, router.Errorf(http.StatusConflict, "email already registered")
	}
	if err != nil {
		return schema.Session{}, err
	}
	token, err := auth.From(ctx).IssueToken(ctx, auth.PurposeVerifyEmail, user.Email, 0)
	if err != nil {
		return schema.Session{}, err
	}
	if err := send(ctx, user.Email, "Verify your email", "verify", "/verify?token="+token); err != nil {
		return schema.Session{}, err
	}
	req.Status(http.StatusCreated)
	return signIn(ctx, req, user)
}

// VerifyEmail consumes the token from the verification link.
func VerifyEmail(ctx context.Context, req *router.Request[schema.VerifyEmail]) (router.None, error) {
	email, err := auth.From(ctx).ConsumeToken(ctx, auth.PurposeVerifyEmail, req.Body.Token)
	if err != nil {
		return router.None{}, err
	}
	if _, err := queries.New(db.From(ctx)).MarkVerified(ctx, email); err != nil {
		return router.None{}, err
	}
	return router.None{}, nil
}

// ForgotPassword sends a reset link when the email is registered, and
// replies 204 either way so the reply does not reveal who is registered.
func ForgotPassword(ctx context.Context, req *router.Request[schema.ForgotPassword]) (router.None, error) {
	user, err := queries.New(db.From(ctx)).GetUserByEmail(ctx, normalizeEmail(req.Body.Email))
	if errors.Is(err, pgx.ErrNoRows) {
		return router.None{}, nil
	}
	if err != nil {
		return router.None{}, err
	}
	token, err := auth.From(ctx).IssueToken(ctx, auth.PurposeResetPassword, user.Email, 0)
	if err != nil {
		return router.None{}, err
	}
	return router.None{}, send(ctx, user.Email, "Reset your password", "reset", "/reset?token="+token)
}

// ResetPassword sets a new password from a reset link and ends every
// session of the user.
func ResetPassword(ctx context.Context, req *router.Request[schema.ResetPassword]) (router.None, error) {
	email, err := auth.From(ctx).ConsumeToken(ctx, auth.PurposeResetPassword, req.Body.Token)
	if err != nil {
		return router.None{}, err
	}
	if err := auth.From(ctx).ValidatePassword(req.Body.Password, email); err != nil {
		return router.None{}, err
	}
	hash, err := auth.HashPassword(req.Body.Password)
	if err != nil {
		return router.None{}, err
	}
	q := queries.New(db.From(ctx))
	if _, err := q.SetPassword(ctx, queries.SetPasswordParams{Email: email, PasswordHash: hash}); err != nil {
		return router.None{}, err
	}
	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		return router.None{}, err
	}
	return router.None{}, auth.From(ctx).RevokeAll(ctx, user.ID)
}

// Login checks the password and opens a session. The reply does not say
// which of the two was wrong.
func Login(ctx context.Context, req *router.Request[schema.Credentials]) (schema.Session, error) {
	user, err := queries.New(db.From(ctx)).GetUserByEmail(ctx, normalizeEmail(req.Body.Email))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !auth.CheckPassword(user.PasswordHash, req.Body.Password)) {
		return schema.Session{}, router.Errorf(http.StatusUnauthorized, "wrong email or password")
	}
	if err != nil {
		return schema.Session{}, err
	}
	return signIn(ctx, req, user)
}

// signIn opens the session: HttpOnly cookies for browsers, and the access
// token in the reply for clients that send it as a bearer token. The
// session slides: the auth pack renews an expired access cookie from the
// refresh cookie on any request, so no refresh route is needed.
func signIn[In any](ctx context.Context, req *router.Request[In], user queries.AppUser) (schema.Session, error) {
	tokens, err := auth.From(ctx).Login(ctx, user.ID, map[string]any{"email": user.Email})
	if err != nil {
		return schema.Session{}, err
	}
	for _, c := range auth.From(ctx).Cookies(tokens) {
		req.SetCookie(c)
	}
	return schema.Session{UserID: user.ID, Email: user.Email, Verified: user.VerifiedAt != nil, AccessToken: tokens.Access}, nil
}

// CurrentSession answers a visitor as well as a user: the route runs
// behind auth.Optional(), so CurrentUser is nil without a valid token.
func CurrentSession(ctx context.Context, req *router.Request[router.None]) (schema.SessionState, error) {
	u := auth.CurrentUser(ctx)
	if u == nil {
		return schema.SessionState{}, nil
	}
	session, err := sessionOf(ctx, u.ID)
	if err != nil {
		return schema.SessionState{}, err
	}
	return schema.SessionState{User: &session}, nil
}

// sessionOf reads the user from the database, not from the claims set at
// login: the verified flag is current the moment the emailed link is
// used, and a session renewed by the auth pack reads the same. An
// account deleted since login is a 401.
func sessionOf(ctx context.Context, userID string) (schema.Session, error) {
	user, err := queries.New(db.From(ctx)).GetUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return schema.Session{}, router.Errorf(http.StatusUnauthorized, "account no longer exists")
	}
	if err != nil {
		return schema.Session{}, err
	}
	return schema.Session{UserID: user.ID, Email: user.Email, Verified: user.VerifiedAt != nil}, nil
}

// Me returns the signed-in user; auth.Require() put them in the context.
func Me(ctx context.Context, req *router.Request[router.None]) (schema.Session, error) {
	return sessionOf(ctx, auth.CurrentUser(ctx).ID)
}

// Logout revokes the session and clears the cookies.
func Logout(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	if err := auth.From(ctx).Logout(ctx, auth.CurrentUser(ctx).SessionID); err != nil {
		return router.None{}, err
	}
	for _, c := range auth.ClearedCookies() {
		req.SetCookie(c)
	}
	return router.None{}, nil
}
