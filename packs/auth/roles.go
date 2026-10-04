package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/pkg/router"
)

// Roles maps each of the app's roles to the permissions it grants. The
// app defines them in code; memberships (who holds a role, and where)
// live in auth_member:
//
//	var roles = auth.Roles{
//		"admin":  {"*"},
//		"editor": {"posts.read", "posts.write"},
//		"viewer": {"posts.read"},
//	}
//
// A permission is a name ("posts.write"), "prefix.*" for every name
// under a prefix, or "*" for all. A membership has a scope: a team, a
// workspace, a project id, whatever the app groups by; AppWide ("")
// holds in every scope. Every check reads the database, so a role
// revoked, or an account deleted, is refused on its next request even
// with a session still valid. A role in the database that the map no
// longer names grants nothing: unknown means denied.
type Roles map[string][]string

// AppWide is the scope of a membership that holds in every scope.
const AppWide = ""

// Membership is one role a subject holds in a scope.
type Membership struct {
	Subject   string    `json:"subject"`
	Scope     string    `json:"scope"`
	Role      string    `json:"role"`
	GrantedBy string    `json:"grantedBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrUnknownRole is Grant's error for a role the map does not name.
var ErrUnknownRole = errors.New("auth: unknown role")

// ErrForbidden is Check's error when the user lacks the permission; as a
// handler's error it replies 403.
var ErrForbidden = &router.HTTPError{Status: http.StatusForbidden, Message: "forbidden", Code: "forbidden"}

// maxScope bounds a scope, a role and a subject as stored.
const maxScope = 200

// Grant gives subject the role in scope (AppWide for every scope). It is
// idempotent. The signed-in user, when there is one, is recorded as the
// granter.
func (r Roles) Grant(ctx context.Context, subject, scope, role string) error {
	if _, ok := r[role]; !ok {
		return fmt.Errorf("%w %q", ErrUnknownRole, role)
	}
	if err := checkMemberArgs(subject, scope); err != nil {
		return err
	}
	var by *string
	if u := CurrentUser(ctx); u != nil {
		by = &u.ID
	}
	_, err := From(ctx).pool.Exec(ctx, `INSERT INTO auth_member (subject, scope, role, granted_by) VALUES ($1, $2, $3, $4) ON CONFLICT (subject, scope, role) DO NOTHING`, subject, scope, role, by)
	if err != nil {
		return fmt.Errorf("auth: grant: %w", err)
	}
	return nil
}

// Revoke takes the role in scope from subject; one it does not hold is
// not an error. The next check refuses at once.
func (r Roles) Revoke(ctx context.Context, subject, scope, role string) error {
	_, err := From(ctx).pool.Exec(ctx, `DELETE FROM auth_member WHERE subject = $1 AND scope = $2 AND role = $3`, subject, scope, role)
	if err != nil {
		return fmt.Errorf("auth: revoke: %w", err)
	}
	return nil
}

// RevokeAll takes every role subject holds in scope (a member leaving a
// team).
func (r Roles) RevokeAll(ctx context.Context, subject, scope string) error {
	_, err := From(ctx).pool.Exec(ctx, `DELETE FROM auth_member WHERE subject = $1 AND scope = $2`, subject, scope)
	if err != nil {
		return fmt.Errorf("auth: revoke: %w", err)
	}
	return nil
}

// Of lists the roles subject holds in scope, its app-wide ones included,
// that the map names.
func (r Roles) Of(ctx context.Context, subject, scope string) ([]string, error) {
	if subject == "" {
		return nil, nil
	}
	rows, err := From(ctx).pool.Query(ctx, `SELECT DISTINCT role FROM auth_member WHERE subject = $1 AND (scope = $2 OR scope = '') ORDER BY role`, subject, scope)
	if err != nil {
		return nil, fmt.Errorf("auth: roles: %w", err)
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("auth: roles: %w", err)
	}
	out := held[:0]
	for _, role := range held {
		if _, ok := r[role]; ok {
			out = append(out, role)
		}
	}
	return out, nil
}

// Can reports whether subject holds a permission in scope. An error
// (the database unreachable) is never a yes.
func (r Roles) Can(ctx context.Context, subject, scope, permission string) (bool, error) {
	roles, err := r.Of(ctx, subject, scope)
	if err != nil {
		return false, err
	}
	for _, role := range roles {
		for _, p := range r[role] {
			if grants(p, permission) {
				return true, nil
			}
		}
	}
	return false, nil
}

// grants reports whether a role's permission p covers want.
func grants(p, want string) bool {
	switch {
	case p == "*" || p == want:
		return true
	case strings.HasSuffix(p, ".*"):
		return strings.HasPrefix(want, strings.TrimSuffix(p, "*"))
	}
	return false
}

// Check is Can for the signed-in user of ctx, as an error for a handler
// or a job: nil when allowed, ErrForbidden when not (a 403 from a typed
// handler), 401 without a user, and the database's error otherwise.
func (r Roles) Check(ctx context.Context, scope, permission string) error {
	u := CurrentUser(ctx)
	if u == nil {
		return &router.HTTPError{Status: http.StatusUnauthorized, Message: "authentication required"}
	}
	ok, err := r.Can(ctx, u.ID, scope, permission)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

// Require is middleware that authenticates like auth.Require and then
// lets through only a user holding permission in the request's scope:
// scope reads it from the request (a path value, r.PathValue("team")),
// nil for AppWide. It replies 401 without a valid session, 403 without
// the permission, 500 when the check cannot run: never through.
func (r Roles) Require(permission string, scope func(*http.Request) string) func(http.Handler) http.Handler {
	authenticate := Require()
	return func(next http.Handler) http.Handler {
		check := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			s := AppWide
			if scope != nil {
				s = scope(req)
			}
			err := r.Check(req.Context(), s, permission)
			switch {
			case err == nil:
				next.ServeHTTP(w, req)
			case errors.Is(err, ErrForbidden):
				router.Error(w, http.StatusForbidden, "forbidden")
			default:
				slog.ErrorContext(req.Context(), "auth: permission check failed", "permission", permission, "err", err)
				router.Error(w, http.StatusInternalServerError, "permission check failed")
			}
		})
		return authenticate(check)
	}
}

// Members lists the memberships of scope (AppWide lists the app-wide
// ones), by subject then role, up to limit (at most 500) after the
// cursor; next is the cursor of the following page, "" at the end.
func (r Roles) Members(ctx context.Context, scope, cursor string, limit int) (page []Membership, next string, err error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	afterSubject, afterRole, _ := strings.Cut(cursor, "\x00")
	rows, err := From(ctx).pool.Query(ctx, `SELECT subject, scope, role, coalesce(granted_by, ''), created_at FROM auth_member
		WHERE scope = $1 AND (subject, role) > ($2, $3) ORDER BY subject, role LIMIT $4`, scope, afterSubject, afterRole, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("auth: members: %w", err)
	}
	page, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Membership, error) {
		var m Membership
		return m, row.Scan(&m.Subject, &m.Scope, &m.Role, &m.GrantedBy, &m.CreatedAt)
	})
	if err != nil {
		return nil, "", fmt.Errorf("auth: members: %w", err)
	}
	if len(page) > limit {
		page = page[:limit]
		last := page[limit-1]
		next = last.Subject + "\x00" + last.Role
	}
	return page, next, nil
}

// Scopes lists the scopes where subject holds a role (its teams), the
// app-wide one as "".
func (r Roles) Scopes(ctx context.Context, subject string) ([]string, error) {
	rows, err := From(ctx).pool.Query(ctx, `SELECT DISTINCT scope FROM auth_member WHERE subject = $1 ORDER BY scope LIMIT 1000`, subject)
	if err != nil {
		return nil, fmt.Errorf("auth: scopes: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("auth: scopes: %w", err)
	}
	return slices.Clip(out), nil
}

func checkMemberArgs(subject, scope string) error {
	switch {
	case subject == "":
		return errors.New("auth: grant: no subject")
	case len(subject) > maxScope || len(scope) > maxScope:
		return fmt.Errorf("auth: grant: subject and scope are at most %d bytes", maxScope)
	}
	return nil
}
