package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/router"

	"notes/db/queries/gen"
	"notes/schema"
)

// uniqueViolation is Postgres' code for a duplicate key.
const uniqueViolation = "23505"

// Register creates a user from validated credentials and signs them in.
// A taken email is a 409 the client can show; any other error is a 500.
func Register(ctx context.Context, req *router.Request[schema.Credentials]) (schema.Session, error) {
	hash, err := auth.HashPassword(req.Body.Password)
	if err != nil {
		return schema.Session{}, err
	}
	user, err := queries.New(db.From(ctx)).CreateUser(ctx, queries.CreateUserParams{Email: req.Body.Email, PasswordHash: hash})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return schema.Session{}, router.Errorf(http.StatusConflict, "email already registered")
	}
	if err != nil {
		return schema.Session{}, err
	}
	req.Status(http.StatusCreated)
	return signIn(ctx, req, user)
}

// Login checks the password and opens a session. The reply does not say
// which of the two was wrong.
func Login(ctx context.Context, req *router.Request[schema.Credentials]) (schema.Session, error) {
	user, err := queries.New(db.From(ctx)).GetUserByEmail(ctx, req.Body.Email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !auth.CheckPassword(user.PasswordHash, req.Body.Password)) {
		return schema.Session{}, router.Errorf(http.StatusUnauthorized, "wrong email or password")
	}
	if err != nil {
		return schema.Session{}, err
	}
	return signIn(ctx, req, user)
}

// signIn opens the session: HttpOnly cookies for browsers, and the access
// token in the reply for clients that send it as a bearer token.
func signIn[In any](ctx context.Context, req *router.Request[In], user queries.AppUser) (schema.Session, error) {
	tokens, err := auth.From(ctx).Login(ctx, user.ID, map[string]any{"email": user.Email})
	if err != nil {
		return schema.Session{}, err
	}
	for _, c := range auth.From(ctx).Cookies(tokens) {
		req.SetCookie(c)
	}
	return schema.Session{UserID: user.ID, Email: user.Email, AccessToken: tokens.Access}, nil
}

// CurrentSession answers a visitor as well as a user: the route runs
// behind auth.Optional(), so CurrentUser is nil without a valid token.
func CurrentSession(ctx context.Context, req *router.Request[router.None]) (schema.SessionState, error) {
	u := auth.CurrentUser(ctx)
	if u == nil {
		return schema.SessionState{}, nil
	}
	email, _ := u.Claims["email"].(string)
	return schema.SessionState{User: &schema.Session{UserID: u.ID, Email: email}}, nil
}

// Me returns the signed-in user; auth.Require() put them in the context.
func Me(ctx context.Context, req *router.Request[router.None]) (schema.Session, error) {
	u := auth.CurrentUser(ctx)
	email, _ := u.Claims["email"].(string)
	return schema.Session{UserID: u.ID, Email: email}, nil
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
