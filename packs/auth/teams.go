package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/validate"
)

// Teams are workspaces: groups of accounts that share the app's data, on
// top of Roles. A workspace's id is the scope of its memberships, so
// Roles.Check(ctx, workspaceID, "posts.write") is how a handler asks.
// Its creator gets the Owner role; members invite others by email, with
// a one-time link bound to that address; members with the permission
// change roles and remove members. The app's Roles map names the
// permissions the workspace routes ask for:
//
//	var roles = auth.Roles{
//		"owner":  {"*"},
//		"admin":  {"members.*", "workspace.update", "posts.*"},
//		"member": {"members.read", "posts.*"},
//	}
//	teams := auth.Teams{Roles: roles}
//	teams.Mount(r) // in routes.go, after auth.Mount
//
// A role is granted only by someone holding every permission it grants,
// so nobody invites or promotes above themselves; the last owner of a
// workspace cannot leave, be removed or be demoted.
type Teams struct {
	Roles Roles
	// Owner is the role a workspace's creator gets: "owner" when empty.
	// It must be in Roles.
	Owner string
	// InviteTTL bounds an invitation; 7 days when zero.
	InviteTTL time.Duration
	// InvitePath is the app's page the emailed link opens, with
	// ?token=...: it posts the token to /api/v1/invites/accept, signed
	// in. "/invite" when empty.
	InvitePath string
	// Title names the app in the invitation email.
	Title string
	// MaxPendingInvites bounds the open invitations of one workspace
	// (100 when zero), so nobody turns the app into a mailer.
	MaxPendingInvites int
	// OnEvent reports what happened, for an audit log
	// (audit.Record): a workspace created, renamed or deleted, an
	// invitation sent, accepted or revoked, a role changed, a member
	// removed or gone. It runs after the change; a panic is logged.
	OnEvent func(ctx context.Context, e TeamEvent)
	// OnDelete runs in the transaction that deletes a workspace, before
	// its memberships and invitations go: delete the app's rows of that
	// workspace there. Its error rolls the deletion back.
	OnDelete func(ctx context.Context, tx pgx.Tx, workspaceID string) error
}

// The permissions the workspace routes ask for, in the workspace's scope.
const (
	PermMembersRead     = "members.read"     // list members and invitations
	PermMembersInvite   = "members.invite"   // invite, revoke an invitation
	PermMembersManage   = "members.manage"   // change a role, remove a member
	PermWorkspaceUpdate = "workspace.update" // rename
	PermWorkspaceDelete = "workspace.delete" // delete with everything in it
)

// Team events, in TeamEvent.Kind.
const (
	TeamCreated       = "workspace_created"
	TeamRenamed       = "workspace_renamed"
	TeamDeleted       = "workspace_deleted"
	TeamInviteSent    = "invite_sent"
	TeamInviteRevoked = "invite_revoked"
	TeamJoined        = "member_joined"
	TeamRoleChanged   = "role_changed"
	TeamMemberRemoved = "member_removed"
	TeamMemberLeft    = "member_left"
)

// TeamEvent is one change to a workspace; Actor is the signed-in user.
type TeamEvent struct {
	Kind      string
	Workspace string
	Actor     string
	// Subject is the member concerned; Email the invited address.
	Subject string
	Email   string
	Role    string
}

// WorkspaceTable and InviteTable are the DDL of the workspaces and their
// invitations (models AuthWorkspace and AuthInvite in the schema
// fragment). Tests create them directly.
const (
	WorkspaceTable = `CREATE TABLE IF NOT EXISTS auth_workspace (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);`
	InviteTable = `CREATE TABLE IF NOT EXISTS auth_invite (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace text NOT NULL,
  email text NOT NULL,
  role text NOT NULL,
  token_hash text NOT NULL UNIQUE,
  invited_by text NOT NULL,
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  accepted_by text,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS auth_invite_workspace_idx ON auth_invite (workspace);`
)

// WorkspaceHeader and WorkspaceCookie carry the workspace a request is
// in (Current): the header for API clients, the cookie the switch route
// sets for the browser.
const (
	WorkspaceHeader = "X-Workspace"
	WorkspaceCookie = "lidza_workspace"
)

// Workspace is a workspace as the routes return it; Roles are the
// signed-in member's.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	Roles     []string  `json:"roles,omitempty"`
}

// Member is one account in a workspace, with its roles there.
type Member struct {
	Subject string   `json:"subject"`
	Email   string   `json:"email,omitempty"`
	Name    string   `json:"name,omitempty"`
	Roles   []string `json:"roles"`
}

// Invite is an open, accepted or revoked invitation; never its token.
type Invite struct {
	ID         string     `json:"id"`
	Workspace  string     `json:"workspace"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	InvitedBy  string     `json:"invitedBy"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// WorkspaceInput names a workspace (create, rename).
type WorkspaceInput struct {
	Name string `json:"name"`
}

// Validate implements validate.Validator.
func (in WorkspaceInput) Validate() error {
	var errs validate.Errors
	n := strings.TrimSpace(in.Name)
	switch {
	case n == "":
		errs.Add("name", "required", "is required")
	case utf8.RuneCountInString(n) > 100:
		errs.Add("name", "max", "at most 100 characters")
	}
	return errs.Result()
}

// InviteInput invites an address with a role.
type InviteInput struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Validate implements validate.Validator.
func (in InviteInput) Validate() error {
	var errs validate.Errors
	if e := strings.TrimSpace(in.Email); e == "" {
		errs.Add("email", "required", "is required")
	} else if !strings.Contains(e, "@") || len(e) > 254 {
		errs.Add("email", "email", "an email address")
	}
	if in.Role == "" {
		errs.Add("role", "required", "is required")
	}
	return errs.Result()
}

// RoleInput sets a member's role.
type RoleInput struct {
	Role string `json:"role"`
}

// InviteToken is the token from an invitation link.
type InviteToken struct {
	Token string `json:"token"`
}

// InvitePreview is what an invitation link shows before it is accepted.
type InvitePreview struct {
	Workspace string    `json:"workspace"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Errors the workspace routes answer with.
var (
	ErrLastOwner     = router.ErrorCode(http.StatusConflict, "last_owner", "a workspace keeps at least one owner")
	ErrInviteGone    = router.ErrorCode(http.StatusGone, "invite_gone", "this invitation was used, revoked or has expired")
	ErrInviteAddress = router.ErrorCode(http.StatusForbidden, "invite_address", "this invitation is for another email address: sign in with that address")
	ErrNoWorkspace   = router.ErrorCode(http.StatusNotFound, "no_workspace", "no such workspace")
	ErrEscalation    = router.ErrorCode(http.StatusForbidden, "escalation", "you cannot grant a role with permissions you do not hold")
	ErrTooManyInvite = router.ErrorCode(http.StatusTooManyRequests, "too_many_invites", "too many open invitations for this workspace")
)

func (t *Teams) owner() string {
	if t.Owner == "" {
		return "owner"
	}
	return t.Owner
}

func (t *Teams) inviteTTL() time.Duration {
	if t.InviteTTL <= 0 {
		return 7 * 24 * time.Hour
	}
	return t.InviteTTL
}

func (t *Teams) event(ctx context.Context, e TeamEvent) {
	if t.OnEvent == nil {
		return
	}
	if u := CurrentUser(ctx); u != nil && e.Actor == "" {
		e.Actor = u.ID
	}
	defer func() {
		if v := recover(); v != nil {
			slog.Error("auth: Teams.OnEvent panicked", "kind", e.Kind, "panic", v)
		}
	}()
	t.OnEvent(ctx, e)
}

// Mount registers the workspace routes, behind sign-in:
//
//	GET    /api/v1/workspaces                        the signed-in user's workspaces
//	POST   /api/v1/workspaces                        create one (its creator the owner)
//	GET    /api/v1/workspaces/{id}                   one, with the user's roles
//	PATCH  /api/v1/workspaces/{id}                   rename (workspace.update)
//	DELETE /api/v1/workspaces/{id}                   delete (workspace.delete)
//	POST   /api/v1/workspaces/{id}/current           make it the browser's current one
//	GET    /api/v1/workspaces/{id}/members           members (members.read)
//	PUT    /api/v1/workspaces/{id}/members/{subject} set a member's role (members.manage)
//	DELETE /api/v1/workspaces/{id}/members/{subject} remove a member (members.manage), or leave (self)
//	GET    /api/v1/workspaces/{id}/invites           open invitations (members.read)
//	POST   /api/v1/workspaces/{id}/invites           invite an address (members.invite)
//	DELETE /api/v1/workspaces/{id}/invites/{invite}  revoke (members.invite)
//	POST   /api/v1/invites/preview                   what a token invites to (signed in)
//	POST   /api/v1/invites/accept                    accept it (signed in as the invited address)
//
// The Owner role must be in Roles.
func (t *Teams) Mount(r *router.Router) {
	if _, ok := t.Roles[t.owner()]; !ok {
		panic(fmt.Sprintf("auth.Teams: the owner role %q is not in Roles", t.owner()))
	}
	router.Route(r, "GET /api/v1/workspaces", t.workspacesList, Require())
	router.Route(r, "POST /api/v1/workspaces", t.workspaceCreate, Throttle(), Require())
	router.Route(r, "GET /api/v1/workspaces/{id}", t.workspaceGet, Require())
	router.Route(r, "PATCH /api/v1/workspaces/{id}", t.workspaceRename, Require())
	router.Route(r, "DELETE /api/v1/workspaces/{id}", t.workspaceDelete, Require())
	router.Route(r, "POST /api/v1/workspaces/{id}/current", t.workspaceSwitch, Require())
	router.Route(r, "GET /api/v1/workspaces/{id}/members", t.workspaceMembers, Require())
	router.Route(r, "PUT /api/v1/workspaces/{id}/members/{subject}", t.workspaceSetRole, Require())
	router.Route(r, "DELETE /api/v1/workspaces/{id}/members/{subject}", t.workspaceRemoveMember, Require())
	router.Route(r, "GET /api/v1/workspaces/{id}/invites", t.workspaceInvites, Require())
	router.Route(r, "POST /api/v1/workspaces/{id}/invites", t.workspaceInvite, Throttle(), Require())
	router.Route(r, "DELETE /api/v1/workspaces/{id}/invites/{invite}", t.workspaceRevokeInvite, Require())
	router.Route(r, "POST /api/v1/invites/preview", t.invitePreview, Throttle(), Require())
	router.Route(r, "POST /api/v1/invites/accept", t.inviteAccept, Throttle(), Require())
}

// workspace loads a workspace the signed-in user belongs to (any role
// there, or an app-wide one); ErrNoWorkspace otherwise, so a stranger
// cannot tell a workspace exists.
func (t *Teams) workspace(ctx context.Context, id string) (Workspace, error) {
	u := CurrentUser(ctx)
	var w Workspace
	if !validUUID(id) {
		return w, ErrNoWorkspace
	}
	err := From(ctx).pool.QueryRow(ctx, `SELECT id::text, name, created_by, created_at FROM auth_workspace WHERE id = $1`, id).Scan(&w.ID, &w.Name, &w.CreatedBy, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNoWorkspace
	}
	if err != nil {
		return w, err
	}
	roles, err := t.Roles.Of(ctx, u.ID, w.ID)
	if err != nil {
		return w, err
	}
	if len(roles) == 0 {
		return w, ErrNoWorkspace
	}
	w.Roles = roles
	return w, nil
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'):
			return false
		}
	}
	return true
}

// Current is the workspace a request is in: the X-Workspace header, else
// the lidza_workspace cookie (the switch route sets it), checked against
// the signed-in user's memberships. "" with no workspace named;
// ErrNoWorkspace (404) for one the user is not in. Handlers scope rows
// with it, and check permissions with Roles.Check(ctx, id, ...).
func (t *Teams) Current(r *http.Request) (string, error) {
	id := r.Header.Get(WorkspaceHeader)
	if id == "" {
		if c, err := r.Cookie(WorkspaceCookie); err == nil {
			id = c.Value
		}
	}
	if id == "" {
		return "", nil
	}
	if CurrentUser(r.Context()) == nil {
		return "", &router.HTTPError{Status: http.StatusUnauthorized, Message: "authentication required"}
	}
	if _, err := t.workspace(r.Context(), id); err != nil {
		return "", err
	}
	return id, nil
}

func (t *Teams) workspacesList(ctx context.Context, _ *router.Request[router.None]) ([]Workspace, error) {
	u := CurrentUser(ctx)
	rows, err := From(ctx).pool.Query(ctx, `SELECT w.id::text, w.name, w.created_by, w.created_at FROM auth_workspace w
		WHERE w.id::text IN (SELECT scope FROM auth_member WHERE subject = $1 AND scope <> '') ORDER BY w.name, w.id LIMIT 500`, u.ID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Workspace, error) {
		var w Workspace
		return w, row.Scan(&w.ID, &w.Name, &w.CreatedBy, &w.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Roles, err = t.Roles.Of(ctx, u.ID, out[i].ID); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []Workspace{}
	}
	return out, nil
}

func (t *Teams) workspaceCreate(ctx context.Context, req *router.Request[WorkspaceInput]) (Workspace, error) {
	u := CurrentUser(ctx)
	a := From(ctx)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback(ctx)
	var w Workspace
	if err := tx.QueryRow(ctx, `INSERT INTO auth_workspace (name, created_by) VALUES ($1, $2) RETURNING id::text, name, created_by, created_at`,
		strings.TrimSpace(req.Body.Name), u.ID).Scan(&w.ID, &w.Name, &w.CreatedBy, &w.CreatedAt); err != nil {
		return Workspace{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth_member (subject, scope, role, granted_by) VALUES ($1, $2, $3, $1)`, u.ID, w.ID, t.owner()); err != nil {
		return Workspace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, err
	}
	w.Roles = []string{t.owner()}
	t.event(ctx, TeamEvent{Kind: TeamCreated, Workspace: w.ID})
	return w, nil
}

func (t *Teams) workspaceGet(ctx context.Context, req *router.Request[router.None]) (Workspace, error) {
	return t.workspace(ctx, req.Param("id"))
}

// allowed loads the workspace and checks the signed-in user's permission
// there: 404 for a stranger, 403 for a member without it.
func (t *Teams) allowed(ctx context.Context, id, permission string) (Workspace, error) {
	w, err := t.workspace(ctx, id)
	if err != nil {
		return w, err
	}
	return w, t.Roles.Check(ctx, w.ID, permission)
}

func (t *Teams) workspaceRename(ctx context.Context, req *router.Request[WorkspaceInput]) (Workspace, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermWorkspaceUpdate)
	if err != nil {
		return w, err
	}
	w.Name = strings.TrimSpace(req.Body.Name)
	if _, err := From(ctx).pool.Exec(ctx, `UPDATE auth_workspace SET name = $2, updated_at = now() WHERE id = $1`, w.ID, w.Name); err != nil {
		return w, err
	}
	t.event(ctx, TeamEvent{Kind: TeamRenamed, Workspace: w.ID})
	return w, nil
}

func (t *Teams) workspaceDelete(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermWorkspaceDelete)
	if err != nil {
		return router.None{}, err
	}
	tx, err := From(ctx).pool.Begin(ctx)
	if err != nil {
		return router.None{}, err
	}
	defer tx.Rollback(ctx)
	if t.OnDelete != nil {
		if err := t.OnDelete(ctx, tx, w.ID); err != nil {
			return router.None{}, err
		}
	}
	for _, q := range []string{`DELETE FROM auth_member WHERE scope = $1`, `DELETE FROM auth_invite WHERE workspace = $1`, `DELETE FROM auth_workspace WHERE id::text = $1`} {
		if _, err := tx.Exec(ctx, q, w.ID); err != nil {
			return router.None{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return router.None{}, err
	}
	t.event(ctx, TeamEvent{Kind: TeamDeleted, Workspace: w.ID})
	return router.None{}, nil
}

func (t *Teams) workspaceSwitch(ctx context.Context, req *router.Request[router.None]) (Workspace, error) {
	w, err := t.workspace(ctx, req.Param("id"))
	if err != nil {
		return w, err
	}
	req.SetCookie(&http.Cookie{Name: WorkspaceCookie, Value: w.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: From(ctx).cfg.CookieSecure, MaxAge: 365 * 24 * 3600})
	return w, nil
}

func (t *Teams) workspaceMembers(ctx context.Context, req *router.Request[router.None]) ([]Member, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermMembersRead)
	if err != nil {
		return nil, err
	}
	a := From(ctx)
	rows, err := a.pool.Query(ctx, `SELECT subject, array_agg(role ORDER BY role) FROM auth_member WHERE scope = $1 GROUP BY subject ORDER BY subject LIMIT 1000`, w.ID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Member, error) {
		var m Member
		return m, row.Scan(&m.Subject, &m.Roles)
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if p, err := a.Profile(ctx, out[i].Subject); err == nil {
			out[i].Email, out[i].Name = p.Email, p.Name
		}
	}
	if out == nil {
		out = []Member{}
	}
	return out, nil
}

// grantable reports whether the signed-in user holds every permission
// role grants in the workspace: nobody hands out more than they have.
func (t *Teams) grantable(ctx context.Context, workspace, role string) error {
	perms, ok := t.Roles[role]
	if !ok {
		return router.ErrorCode(http.StatusUnprocessableEntity, "unknown_role", "no such role")
	}
	u := CurrentUser(ctx)
	for _, p := range perms {
		can, err := t.Roles.Can(ctx, u.ID, workspace, p)
		if err != nil {
			return err
		}
		if !can {
			return ErrEscalation
		}
	}
	return nil
}

// owners counts the members holding the owner role in the workspace.
func (t *Teams) owners(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, workspace string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM auth_member WHERE scope = $1 AND role = $2`, workspace, t.owner()).Scan(&n)
	return n, err
}

func (t *Teams) workspaceSetRole(ctx context.Context, req *router.Request[RoleInput]) (Member, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermMembersManage)
	if err != nil {
		return Member{}, err
	}
	subject := req.Param("subject")
	if err := t.grantable(ctx, w.ID, req.Body.Role); err != nil {
		return Member{}, err
	}
	a := From(ctx)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return Member{}, err
	}
	defer tx.Rollback(ctx)
	// The workspace's rows lock: two demotions at once cannot both pass
	// the last-owner check.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM auth_workspace WHERE id::text = $1 FOR UPDATE`, w.ID); err != nil {
		return Member{}, err
	}
	var current []string
	rows, err := tx.Query(ctx, `SELECT role FROM auth_member WHERE scope = $1 AND subject = $2`, w.ID, subject)
	if err != nil {
		return Member{}, err
	}
	if current, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return Member{}, err
	}
	if len(current) == 0 {
		return Member{}, router.NotFound("no such member")
	}
	for _, r := range current {
		if r != req.Body.Role {
			// Taking a role away needs the same standing as granting it.
			if _, known := t.Roles[r]; known {
				if err := t.grantable(ctx, w.ID, r); err != nil {
					return Member{}, err
				}
			}
		}
	}
	if req.Body.Role != t.owner() && containsString(current, t.owner()) {
		n, err := t.owners(ctx, tx, w.ID)
		if err != nil {
			return Member{}, err
		}
		if n <= 1 {
			return Member{}, ErrLastOwner
		}
	}
	u := CurrentUser(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM auth_member WHERE scope = $1 AND subject = $2`, w.ID, subject); err != nil {
		return Member{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth_member (subject, scope, role, granted_by) VALUES ($1, $2, $3, $4)`, subject, w.ID, req.Body.Role, u.ID); err != nil {
		return Member{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Member{}, err
	}
	t.event(ctx, TeamEvent{Kind: TeamRoleChanged, Workspace: w.ID, Subject: subject, Role: req.Body.Role})
	m := Member{Subject: subject, Roles: []string{req.Body.Role}}
	if p, err := a.Profile(ctx, subject); err == nil {
		m.Email, m.Name = p.Email, p.Name
	}
	return m, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (t *Teams) workspaceRemoveMember(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	u := CurrentUser(ctx)
	subject := req.Param("subject")
	self := subject == u.ID
	w, err := t.workspace(ctx, req.Param("id"))
	if err != nil {
		return router.None{}, err
	}
	if !self {
		if err := t.Roles.Check(ctx, w.ID, PermMembersManage); err != nil {
			return router.None{}, err
		}
	}
	a := From(ctx)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return router.None{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM auth_workspace WHERE id::text = $1 FOR UPDATE`, w.ID); err != nil {
		return router.None{}, err
	}
	rows, err := tx.Query(ctx, `SELECT role FROM auth_member WHERE scope = $1 AND subject = $2`, w.ID, subject)
	if err != nil {
		return router.None{}, err
	}
	current, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return router.None{}, err
	}
	if len(current) == 0 {
		return router.None{}, router.NotFound("no such member")
	}
	if !self {
		for _, r := range current {
			if _, known := t.Roles[r]; known {
				if err := t.grantable(ctx, w.ID, r); err != nil {
					return router.None{}, err
				}
			}
		}
	}
	if containsString(current, t.owner()) {
		n, err := t.owners(ctx, tx, w.ID)
		if err != nil {
			return router.None{}, err
		}
		if n <= 1 {
			return router.None{}, ErrLastOwner
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM auth_member WHERE scope = $1 AND subject = $2`, w.ID, subject); err != nil {
		return router.None{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return router.None{}, err
	}
	kind := TeamMemberRemoved
	if self {
		kind = TeamMemberLeft
	}
	t.event(ctx, TeamEvent{Kind: kind, Workspace: w.ID, Subject: subject})
	return router.None{}, nil
}

const inviteColumns = `id::text, workspace, email, role, invited_by, expires_at, accepted_at, revoked_at, created_at`

func scanInvite(row pgx.CollectableRow) (Invite, error) {
	var i Invite
	return i, row.Scan(&i.ID, &i.Workspace, &i.Email, &i.Role, &i.InvitedBy, &i.ExpiresAt, &i.AcceptedAt, &i.RevokedAt, &i.CreatedAt)
}

func (t *Teams) workspaceInvites(ctx context.Context, req *router.Request[router.None]) ([]Invite, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermMembersRead)
	if err != nil {
		return nil, err
	}
	rows, err := From(ctx).pool.Query(ctx, `SELECT `+inviteColumns+` FROM auth_invite
		WHERE workspace = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now() ORDER BY created_at DESC LIMIT 500`, w.ID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanInvite)
	if out == nil {
		out = []Invite{}
	}
	return out, err
}

func inviteHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (t *Teams) workspaceInvite(ctx context.Context, req *router.Request[InviteInput]) (Invite, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermMembersInvite)
	if err != nil {
		return Invite{}, err
	}
	if err := t.grantable(ctx, w.ID, req.Body.Role); err != nil {
		return Invite{}, err
	}
	a := From(ctx)
	max := t.MaxPendingInvites
	if max <= 0 {
		max = 100
	}
	var open int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM auth_invite WHERE workspace = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()`, w.ID).Scan(&open); err != nil {
		return Invite{}, err
	}
	if open >= max {
		return Invite{}, ErrTooManyInvite
	}
	email := NormalizeEmail(req.Body.Email)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Invite{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	u := CurrentUser(ctx)
	// A new invitation to the same address replaces an open one.
	if _, err := a.pool.Exec(ctx, `UPDATE auth_invite SET revoked_at = now() WHERE workspace = $1 AND email = $2 AND accepted_at IS NULL AND revoked_at IS NULL`, w.ID, email); err != nil {
		return Invite{}, err
	}
	rows, err := a.pool.Query(ctx, `INSERT INTO auth_invite (workspace, email, role, token_hash, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+inviteColumns, w.ID, email, req.Body.Role, inviteHash(token), u.ID, time.Now().Add(t.inviteTTL()))
	if err != nil {
		return Invite{}, err
	}
	inv, err := pgx.CollectExactlyOneRow(rows, scanInvite)
	if err != nil {
		return Invite{}, err
	}
	if err := t.sendInvite(ctx, w, inv, token); err != nil {
		return Invite{}, err
	}
	t.event(ctx, TeamEvent{Kind: TeamInviteSent, Workspace: w.ID, Email: email, Role: req.Body.Role})
	return inv, nil
}

// sendInvite emails the link through the mail pack: the app's
// mail/auth_invite.* template when it has one (Data: App, Link,
// Workspace, Inviter, Email, Role), else plain text. Without the mail
// pack nothing is sent; the invitation stands and can be resent.
func (t *Teams) sendInvite(ctx context.Context, w Workspace, inv Invite, token string) error {
	m, ok := lidza.Optional[*mail.Mail](ctx)
	if !ok {
		return nil
	}
	path := t.InvitePath
	if path == "" {
		path = "/invite"
	}
	link := m.Link(path + "?token=" + url.QueryEscape(token))
	app := t.Title
	if app == "" {
		app = "the app"
	}
	inviter := ""
	if u := CurrentUser(ctx); u != nil {
		if p, err := From(ctx).Profile(ctx, u.ID); err == nil {
			inviter = p.Name
			if inviter == "" {
				inviter = p.Email
			}
		}
	}
	lead := "You are invited"
	if inviter != "" {
		lead = inviter + " invited you"
	}
	msg := mail.Message{To: inv.Email, Subject: fmt.Sprintf("Join %s on %s", w.Name, app)}
	if name, ok := m.TemplateFor("auth_invite", mail.Languages(ctx)...); ok {
		msg.Template = name
		msg.Data = map[string]string{"App": app, "Link": link, "Workspace": w.Name, "Inviter": inviter, "Email": inv.Email, "Role": inv.Role}
	} else {
		msg.Text = fmt.Sprintf("%s to join %s on %s as %s.\n\nOpen this link to accept (signed in as %s):\n\n%s\n\nIt works once and expires on %s.\n",
			lead, w.Name, app, inv.Role, inv.Email, link, inv.ExpiresAt.UTC().Format("2 January 2006"))
	}
	_, err := m.Send(ctx, msg)
	return err
}

func (t *Teams) workspaceRevokeInvite(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	w, err := t.allowed(ctx, req.Param("id"), PermMembersInvite)
	if err != nil {
		return router.None{}, err
	}
	var email string
	err = From(ctx).pool.QueryRow(ctx, `UPDATE auth_invite SET revoked_at = now() WHERE id::text = $1 AND workspace = $2 AND accepted_at IS NULL AND revoked_at IS NULL RETURNING email`,
		req.Param("invite"), w.ID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return router.None{}, router.NotFound("no such open invitation")
	}
	if err != nil {
		return router.None{}, err
	}
	t.event(ctx, TeamEvent{Kind: TeamInviteRevoked, Workspace: w.ID, Email: email})
	return router.None{}, nil
}

// openInvite finds the open invitation a token names.
func (t *Teams) openInvite(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, token string, lock bool) (Invite, error) {
	sql := `SELECT ` + inviteColumns + ` FROM auth_invite WHERE token_hash = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()`
	if lock {
		sql += ` FOR UPDATE`
	}
	rows, err := q.Query(ctx, sql, inviteHash(strings.TrimSpace(token)))
	if err != nil {
		return Invite{}, err
	}
	inv, err := pgx.CollectExactlyOneRow(rows, scanInvite)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, ErrInviteGone
	}
	return inv, err
}

func (t *Teams) invitePreview(ctx context.Context, req *router.Request[InviteToken]) (InvitePreview, error) {
	inv, err := t.openInvite(ctx, From(ctx).pool, req.Body.Token, false)
	if err != nil {
		return InvitePreview{}, err
	}
	var name string
	if err := From(ctx).pool.QueryRow(ctx, `SELECT name FROM auth_workspace WHERE id::text = $1`, inv.Workspace).Scan(&name); err != nil {
		return InvitePreview{}, ErrInviteGone
	}
	return InvitePreview{Workspace: inv.Workspace, Name: name, Email: inv.Email, Role: inv.Role, ExpiresAt: inv.ExpiresAt}, nil
}

// signedInEmail is the signed-in account's address: its profile's, else
// the session's email claim.
func signedInEmail(ctx context.Context) string {
	u := CurrentUser(ctx)
	if p, err := From(ctx).Profile(ctx, u.ID); err == nil && p.Email != "" {
		return NormalizeEmail(p.Email)
	}
	if e, _ := u.Claims["email"].(string); e != "" {
		return NormalizeEmail(e)
	}
	return ""
}

func (t *Teams) inviteAccept(ctx context.Context, req *router.Request[InviteToken]) (Workspace, error) {
	u := CurrentUser(ctx)
	a := From(ctx)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback(ctx)
	inv, err := t.openInvite(ctx, tx, req.Body.Token, true)
	if err != nil {
		return Workspace{}, err
	}
	if signedInEmail(ctx) != inv.Email {
		return Workspace{}, ErrInviteAddress
	}
	if _, ok := t.Roles[inv.Role]; !ok {
		return Workspace{}, ErrInviteGone
	}
	if _, err := tx.Exec(ctx, `UPDATE auth_invite SET accepted_at = now(), accepted_by = $2 WHERE id::text = $1`, inv.ID, u.ID); err != nil {
		return Workspace{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth_member (subject, scope, role, granted_by) VALUES ($1, $2, $3, $4) ON CONFLICT (subject, scope, role) DO NOTHING`,
		u.ID, inv.Workspace, inv.Role, inv.InvitedBy); err != nil {
		return Workspace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, err
	}
	t.event(ctx, TeamEvent{Kind: TeamJoined, Workspace: inv.Workspace, Subject: u.ID, Email: inv.Email, Role: inv.Role})
	return t.workspace(ctx, inv.Workspace)
}

// WorkspaceSummary is one workspace as the admin pages list it.
type WorkspaceSummary struct {
	ID        string
	Name      string
	CreatedAt time.Time
	Members   int
	Invites   int
	// Owners are the owner role's members, by email when known.
	Owners []string
}

// HasWorkspaces reports whether the app keeps workspaces (the
// auth_workspace table exists): the admin pages list them then.
func (a *Auth) HasWorkspaces(ctx context.Context) bool {
	var exists bool
	err := a.pool.QueryRow(ctx, `SELECT to_regclass('auth_workspace') IS NOT NULL`).Scan(&exists)
	return err == nil && exists
}

// Workspaces lists workspaces whose name contains q (case-insensitive),
// newest first, with their member and open-invitation counts and the
// holders of owner (the Teams.Owner role; "owner" when empty).
func (a *Auth) Workspaces(ctx context.Context, q, owner string, limit, offset int) ([]WorkspaceSummary, int, error) {
	if owner == "" {
		owner = "owner"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var total int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM auth_workspace WHERE $1 = '' OR position(lower($1) in lower(name)) > 0`, q).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := a.pool.Query(ctx, `SELECT w.id::text, w.name, w.created_at,
		(SELECT count(DISTINCT subject) FROM auth_member m WHERE m.scope = w.id::text),
		(SELECT count(*) FROM auth_invite i WHERE i.workspace = w.id::text AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()),
		COALESCE((SELECT array_agg(m.subject ORDER BY m.created_at) FROM auth_member m WHERE m.scope = w.id::text AND m.role = $4), '{}')
		FROM auth_workspace w WHERE $1 = '' OR position(lower($1) in lower(w.name)) > 0
		ORDER BY w.created_at DESC, w.id LIMIT $2 OFFSET $3`, q, limit, offset, owner)
	if err != nil {
		return nil, 0, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (WorkspaceSummary, error) {
		var s WorkspaceSummary
		return s, row.Scan(&s.ID, &s.Name, &s.CreatedAt, &s.Members, &s.Invites, &s.Owners)
	})
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		for j, subject := range out[i].Owners {
			if p, err := a.Profile(ctx, subject); err == nil && p.Email != "" {
				out[i].Owners[j] = p.Email
			}
		}
	}
	return out, total, nil
}
