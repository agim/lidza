package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/pkg/router"
)

type workspaceKey struct{}

// ErrChooseWorkspace is RequireWorkspace's 400 when the request names no
// workspace.
var ErrChooseWorkspace = router.ErrorCode(http.StatusBadRequest, "choose_workspace", "no workspace chosen: send the X-Workspace header, or switch to one (POST /api/v1/workspaces/{id}/current)")

// RequireWorkspace is middleware that authenticates like Require and then
// resolves the workspace the request is in (the X-Workspace header, else
// the lidza_workspace cookie): 400 when none is named, 404 when it does
// not exist or the signed-in user is not a member (an app-wide role
// counts), else the request goes on with WorkspaceID(ctx) set. The
// routes `lidza gen resource` writes for a model with a workspaceId
// field sit behind it.
func RequireWorkspace() func(http.Handler) http.Handler {
	authenticate := Require()
	return func(next http.Handler) http.Handler {
		resolve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := resolveWorkspace(r)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), workspaceKey{}, id)))
			case errors.Is(err, ErrChooseWorkspace), errors.Is(err, ErrNoWorkspace):
				router.WriteError(w, r, err)
			default:
				router.Error(w, http.StatusInternalServerError, "workspace check failed")
			}
		})
		return authenticate(resolve)
	}
}

// WorkspaceID is the workspace RequireWorkspace resolved for the
// request; "" outside it.
func WorkspaceID(ctx context.Context) string {
	id, _ := ctx.Value(workspaceKey{}).(string)
	return id
}

// resolveWorkspace reads and checks the request's workspace.
func resolveWorkspace(r *http.Request) (string, error) {
	id := r.Header.Get(WorkspaceHeader)
	if id == "" {
		if c, err := r.Cookie(WorkspaceCookie); err == nil {
			id = c.Value
		}
	}
	if id == "" {
		return "", ErrChooseWorkspace
	}
	if !validUUID(id) {
		return "", ErrNoWorkspace
	}
	u := CurrentUser(r.Context())
	if u == nil {
		return "", &router.HTTPError{Status: http.StatusUnauthorized, Message: "authentication required"}
	}
	var member bool
	err := From(r.Context()).pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM auth_member WHERE subject = $1 AND (scope = $2 OR scope = ''))
		AND EXISTS (SELECT 1 FROM auth_workspace WHERE id::text = $2)`, u.ID, id).Scan(&member)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !member {
		return "", ErrNoWorkspace
	}
	if err != nil {
		return "", err
	}
	return id, nil
}
