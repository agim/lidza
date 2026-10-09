package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/router"
)

var workspaceRoles = Roles{
	"owner":  {"*"},
	"admin":  {"members.*", "workspace.update", "posts.*"},
	"member": {"members.read", "posts.*"},
}

// teamServer is the sign-in routes and the workspace routes on one
// router, with the mail pack writing to its outbox.
func teamServer(t *testing.T, teams *Teams) (*httptest.Server, *Auth) {
	t.Helper()
	a := testAuth(t)
	ctx := context.Background()
	for _, tbl := range []string{"auth_member", "auth_workspace", "auth_invite", "mail_message"} {
		a.pool.Exec(ctx, `DROP TABLE IF EXISTS `+tbl)
	}
	if _, err := a.pool.Exec(ctx, MemberTable+WorkspaceTable+InviteTable+mail.OutboxTable); err != nil {
		t.Fatal(err)
	}
	a.cfg.LoginRPS, a.cfg.LoginBurst = 1000, 1000
	m, err := mail.New(mail.Config{Provider: "outbox", From: "app@example.com", AppURL: "https://app.example.com"}, a.pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := lidza.NewServices()
	lidza.Provide(s, a)
	lidza.Provide(s, m)
	r := router.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(lidza.WithServices(req.Context(), s)))
		})
	})
	Mount(r, Options{NoVerifyEmail: true})
	teams.Mount(r)
	// A route of a workspace-scoped resource, as gen resource registers it.
	scoped := r.Group("/api/v1/scoped", RequireWorkspace())
	router.Route(scoped, "GET /api/v1/scoped", func(ctx context.Context, _ *router.Request[router.None]) (map[string]string, error) {
		return map[string]string{"workspace": WorkspaceID(ctx)}, nil
	})
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	return srv, a
}

type teamClient struct {
	t   *testing.T
	c   *http.Client
	url string
}

func (tc teamClient) do(method, path string, body any) (int, string) {
	tc.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, tc.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := tc.c.Do(req)
	if err != nil {
		tc.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func signUp(t *testing.T, srv *httptest.Server, email string) teamClient {
	t.Helper()
	tc := teamClient{t: t, c: browser(), url: srv.URL}
	if code, body := tc.do("POST", "/api/v1/auth/register", map[string]string{"email": email, "password": "correct horse battery", "name": strings.Split(email, "@")[0]}); code >= 300 {
		t.Fatalf("register %s: %d %s", email, code, body)
	}
	return tc
}

var linkToken = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// lastInviteToken reads the token from the newest invitation mail to
// email.
func lastInviteToken(t *testing.T, a *Auth, email string) string {
	t.Helper()
	var text string
	if err := a.pool.QueryRow(context.Background(), `SELECT text FROM mail_message WHERE recipient = $1 AND subject LIKE 'Join %' ORDER BY created_at DESC LIMIT 1`, email).Scan(&text); err != nil {
		t.Fatalf("no invitation mail to %s: %v", email, err)
	}
	m := linkToken.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no link in %q", text)
	}
	return m[1]
}

func jsonField(t *testing.T, body, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not an object: %s", body)
	}
	s, _ := m[name].(string)
	return s
}

// TestTeams: a workspace's creator owns it; an invitation reaches only
// its address, once; a stranger cannot see the workspace; nobody grants
// more than they hold; the last owner stays; members leave; deletion
// takes the memberships and invitations with it.
func TestTeams(t *testing.T) {
	var events []string
	var mu sync.Mutex
	teams := &Teams{Roles: workspaceRoles, Title: "Shop", OnEvent: func(_ context.Context, e TeamEvent) {
		mu.Lock()
		events = append(events, e.Kind)
		mu.Unlock()
	}}
	srv, a := teamServer(t, teams)
	ann := signUp(t, srv, "ann@example.com")
	bob := signUp(t, srv, "bob@example.com")
	eve := signUp(t, srv, "eve@example.com")

	code, body := ann.do("POST", "/api/v1/workspaces", map[string]string{"name": "Acme"})
	if code != 200 || !strings.Contains(body, `"roles":["owner"]`) {
		t.Fatalf("create: %d %s", code, body)
	}
	ws := jsonField(t, body, "id")
	if code, body := ann.do("GET", "/api/v1/workspaces", nil); code != 200 || !strings.Contains(body, ws) {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, _ := eve.do("GET", "/api/v1/workspaces/"+ws, nil); code != 404 {
		t.Fatalf("a stranger sees the workspace: %d", code)
	}
	if code, body := eve.do("GET", "/api/v1/workspaces", nil); code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("a stranger's list: %d %s", code, body)
	}

	// Invite Bob as a member; the mail carries the link, the reply never the token.
	code, body = ann.do("POST", "/api/v1/workspaces/"+ws+"/invites", map[string]string{"email": "Bob@Example.com", "role": "member"})
	if code != 200 || strings.Contains(body, "token") {
		t.Fatalf("invite: %d %s", code, body)
	}
	token := lastInviteToken(t, a, "bob@example.com")
	if code, body := bob.do("POST", "/api/v1/invites/preview", map[string]string{"token": token}); code != 200 || jsonField(t, body, "name") != "Acme" || jsonField(t, body, "role") != "member" {
		t.Fatalf("preview: %d %s", code, body)
	}
	// Eve has the link but not the address.
	if code, body := eve.do("POST", "/api/v1/invites/accept", map[string]string{"token": token}); code != 403 || !strings.Contains(body, "invite_address") {
		t.Fatalf("accepted by another address: %d %s", code, body)
	}
	if code, body := bob.do("POST", "/api/v1/invites/accept", map[string]string{"token": token}); code != 200 || !strings.Contains(body, `"roles":["member"]`) {
		t.Fatalf("accept: %d %s", code, body)
	}
	if code, _ := bob.do("POST", "/api/v1/invites/accept", map[string]string{"token": token}); code != 410 {
		t.Fatalf("token used twice: %d", code)
	}
	if code, body := bob.do("GET", "/api/v1/workspaces/"+ws+"/members", nil); code != 200 || !strings.Contains(body, "ann@example.com") || !strings.Contains(body, "bob@example.com") {
		t.Fatalf("members: %d %s", code, body)
	}

	// A member cannot invite, rename or manage; nor grant above itself.
	if code, _ := bob.do("POST", "/api/v1/workspaces/"+ws+"/invites", map[string]string{"email": "x@example.com", "role": "member"}); code != 403 {
		t.Fatalf("member invited: %d", code)
	}
	if code, _ := bob.do("PATCH", "/api/v1/workspaces/"+ws, map[string]string{"name": "Mine"}); code != 403 {
		t.Fatalf("member renamed: %d", code)
	}
	bobID := jsonField(t, mustProfile(t, a, "bob@example.com"), "subject")
	annID := jsonField(t, mustProfile(t, a, "ann@example.com"), "subject")
	if code, _ := bob.do("PUT", "/api/v1/workspaces/"+ws+"/members/"+bobID, map[string]string{"role": "owner"}); code != 403 {
		t.Fatalf("member promoted itself: %d", code)
	}

	// Ann makes Bob an admin; an admin may invite members but not owners.
	if code, body := ann.do("PUT", "/api/v1/workspaces/"+ws+"/members/"+bobID, map[string]string{"role": "admin"}); code != 200 {
		t.Fatalf("promote: %d %s", code, body)
	}
	if code, body := bob.do("POST", "/api/v1/workspaces/"+ws+"/invites", map[string]string{"email": "eve@example.com", "role": "owner"}); code != 403 || !strings.Contains(body, "escalation") {
		t.Fatalf("admin invited an owner: %d %s", code, body)
	}
	if code, _ := bob.do("PUT", "/api/v1/workspaces/"+ws+"/members/"+annID, map[string]string{"role": "member"}); code != 403 {
		t.Fatalf("admin demoted the owner: %d", code)
	}

	// The last owner stays.
	if code, body := ann.do("DELETE", "/api/v1/workspaces/"+ws+"/members/"+annID, nil); code != 409 || !strings.Contains(body, "last_owner") {
		t.Fatalf("last owner left: %d %s", code, body)
	}
	if code, _ := ann.do("PUT", "/api/v1/workspaces/"+ws+"/members/"+annID, map[string]string{"role": "admin"}); code != 409 {
		t.Fatalf("last owner demoted: %d", code)
	}

	// Revoked and replaced invitations do not work.
	ann.do("POST", "/api/v1/workspaces/"+ws+"/invites", map[string]string{"email": "eve@example.com", "role": "member"})
	first := lastInviteToken(t, a, "eve@example.com")
	ann.do("POST", "/api/v1/workspaces/"+ws+"/invites", map[string]string{"email": "eve@example.com", "role": "member"})
	second := lastInviteToken(t, a, "eve@example.com")
	if code, _ := eve.do("POST", "/api/v1/invites/accept", map[string]string{"token": first}); code != 410 {
		t.Fatalf("replaced invitation accepted: %d", code)
	}
	_, list := ann.do("GET", "/api/v1/workspaces/"+ws+"/invites", nil)
	var open []Invite
	json.Unmarshal([]byte(list), &open)
	if len(open) != 1 {
		t.Fatalf("open invitations: %s", list)
	}
	if code, _ := ann.do("DELETE", "/api/v1/workspaces/"+ws+"/invites/"+open[0].ID, nil); code != 200 && code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := eve.do("POST", "/api/v1/invites/accept", map[string]string{"token": second}); code != 410 {
		t.Fatalf("revoked invitation accepted: %d", code)
	}

	// Bob leaves; then the workspace goes, with what it held.
	if code, _ := bob.do("DELETE", "/api/v1/workspaces/"+ws+"/members/"+bobID, nil); code != 200 && code != 204 {
		t.Fatalf("leave: %d", code)
	}
	if code, _ := bob.do("GET", "/api/v1/workspaces/"+ws, nil); code != 404 {
		t.Fatalf("still in after leaving: %d", code)
	}
	if code, _ := ann.do("DELETE", "/api/v1/workspaces/"+ws, nil); code != 200 && code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var left int
	a.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM auth_member WHERE scope = $1) + (SELECT count(*) FROM auth_invite WHERE workspace = $1)`, ws).Scan(&left)
	if left != 0 {
		t.Fatalf("%d rows left after delete", left)
	}
	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()
	for _, want := range []string{TeamCreated, TeamInviteSent, TeamJoined, TeamRoleChanged, TeamInviteRevoked, TeamMemberLeft, TeamDeleted} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s event in %s", want, got)
		}
	}
}

// TestTeamsCurrent: the current workspace comes from the header or the
// cookie the switch route sets, and only a member's counts.
func TestTeamsCurrent(t *testing.T) {
	teams := &Teams{Roles: workspaceRoles}
	srv, _ := teamServer(t, teams)
	ann := signUp(t, srv, "ann@example.com")
	eve := signUp(t, srv, "eve@example.com")
	_, body := ann.do("POST", "/api/v1/workspaces", map[string]string{"name": "Acme"})
	ws := jsonField(t, body, "id")
	r := httptest.NewRequest("GET", "/", nil)
	if id, err := teams.Current(r); id != "" || err != nil {
		t.Fatalf("none named: %q %v", id, err)
	}
	if code, _ := ann.do("POST", "/api/v1/workspaces/"+ws+"/current", nil); code != 200 {
		t.Fatalf("switch: %d", code)
	}
	if code, _ := eve.do("POST", "/api/v1/workspaces/"+ws+"/current", nil); code != 404 {
		t.Fatalf("a stranger switched in: %d", code)
	}

	// RequireWorkspace: the cookie the switch set, the header, a stranger, none.
	if code, body := ann.do("GET", "/api/v1/scoped", nil); code != 200 || jsonField(t, body, "workspace") != ws {
		t.Fatalf("by cookie: %d %s", code, body)
	}
	header := func(c teamClient, id string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/v1/scoped", nil)
		if id != "" {
			req.Header.Set(WorkspaceHeader, id)
		}
		res, err := c.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := header(eve, ws); code != 404 {
		t.Fatalf("a stranger's header: %d %s", code, body)
	}
	if code, body := header(eve, ""); code != 400 || !strings.Contains(body, "choose_workspace") {
		t.Fatalf("none chosen: %d %s", code, body)
	}
	if code, _ := header(eve, "not-a-uuid"); code != 404 {
		t.Fatalf("a malformed id: %d", code)
	}
	if code, body := header(teamClient{t: t, c: http.DefaultClient}, ws); code != 401 {
		t.Fatalf("signed out: %d %s", code, body)
	}
}

// TestTeamsDeleteHook: OnDelete runs in the deleting transaction; its
// error keeps the workspace.
func TestTeamsDeleteHook(t *testing.T) {
	refuse := true
	teams := &Teams{Roles: workspaceRoles, OnDelete: func(ctx context.Context, tx pgx.Tx, id string) error {
		if refuse {
			return router.ErrorCode(http.StatusConflict, "busy", "not now")
		}
		return nil
	}}
	srv, _ := teamServer(t, teams)
	ann := signUp(t, srv, "ann@example.com")
	_, body := ann.do("POST", "/api/v1/workspaces", map[string]string{"name": "Acme"})
	ws := jsonField(t, body, "id")
	if code, _ := ann.do("DELETE", "/api/v1/workspaces/"+ws, nil); code != 409 {
		t.Fatalf("hook error ignored: %d", code)
	}
	if code, _ := ann.do("GET", "/api/v1/workspaces/"+ws, nil); code != 200 {
		t.Fatalf("workspace gone after a refused delete: %d", code)
	}
	refuse = false
	if code, _ := ann.do("DELETE", "/api/v1/workspaces/"+ws, nil); code != 200 && code != 204 {
		t.Fatalf("delete: %d", code)
	}
}

func mustProfile(t *testing.T, a *Auth, email string) string {
	t.Helper()
	p, err := a.ProfileByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	return string(b)
}
