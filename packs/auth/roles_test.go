package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/router"
)

var teamRoles = Roles{
	"admin":    {"*"},
	"deployer": {"deploy.*"},
	"viewer":   {"deploy.read"},
}

// rolesApp is an app with sign-in and three routes per team, each behind
// a permission.
func rolesApp(t *testing.T) (*Auth, string, context.Context) {
	t.Helper()
	a := testAuth(t)
	ctx := context.Background()
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS auth_member`)
	if _, err := a.pool.Exec(ctx, MemberTable); err != nil {
		t.Fatal(err)
	}
	relaxed, err := New(Config{Secret: testSecret, AccessTTL: time.Minute, RefreshTTL: time.Hour, LoginRPS: 1000, LoginBurst: 1000, MinPasswordLength: 10, TokenTTL: time.Hour}, a.pool)
	if err != nil {
		t.Fatal(err)
	}
	s := lidza.NewServices()
	lidza.Provide(s, relaxed)
	r := router.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(lidza.WithServices(req.Context(), s)))
		})
	})
	Mount(r, Options{Providers: []Provider{}})
	team := func(r *http.Request) string { return r.PathValue("team") }
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.Handle("GET /api/v1/teams/{team}/deploys", teamRoles.Require("deploy.read", team)(ok))
	r.Handle("POST /api/v1/teams/{team}/deploys", teamRoles.Require("deploy.run", team)(ok))
	r.Handle("POST /api/v1/teams/{team}/members", teamRoles.Require("team.manage", team)(ok))
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	return a, srv.URL, lidza.WithServices(ctx, s)
}

func TestRoles(t *testing.T) {
	a, base, ctx := rolesApp(t)
	api := base + Prefix
	viewer, viewerID := user(t, api, "viewer@example.com")
	deployer, deployerID := user(t, api, "deployer@example.com")
	admin, adminID := user(t, api, "admin@example.com")
	owner, ownerID := user(t, api, "owner@example.com")
	stranger, _ := user(t, api, "stranger@example.com")
	for _, g := range []struct{ subject, scope, role string }{
		{viewerID, "a", "viewer"}, {deployerID, "a", "deployer"}, {adminID, "a", "admin"}, {ownerID, AppWide, "admin"},
	} {
		if err := teamRoles.Grant(ctx, g.subject, g.scope, g.role); err != nil {
			t.Fatal(err)
		}
	}
	status := func(c *http.Client, method, path string) int {
		t.Helper()
		req, _ := http.NewRequest(method, base+"/api/v1/teams/"+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	want := func(c *http.Client, who, method, path string, code int) {
		t.Helper()
		if got := status(c, method, path); got != code {
			t.Errorf("%s %s %s: %d, want %d", who, method, path, got, code)
		}
	}
	want(viewer, "viewer", "GET", "a/deploys", 204)
	want(viewer, "viewer", "POST", "a/deploys", 403)
	want(viewer, "viewer", "POST", "a/members", 403)
	want(deployer, "deployer", "POST", "a/deploys", 204)
	want(deployer, "deployer", "POST", "a/members", 403)
	want(admin, "admin", "POST", "a/members", 204)
	// A team's roles stop at the team; an app-wide one holds everywhere.
	want(admin, "team a's admin", "GET", "b/deploys", 403)
	want(owner, "app-wide admin", "POST", "b/members", 204)
	// No membership, no session.
	want(stranger, "stranger", "GET", "a/deploys", 403)
	want(browser(), "signed out", "GET", "a/deploys", 401)

	// Revoked: refused on the next request, the session still valid.
	if err := teamRoles.Revoke(ctx, deployerID, "a", "deployer"); err != nil {
		t.Fatal(err)
	}
	want(deployer, "revoked deployer", "POST", "a/deploys", 403)
	// A role the map no longer names grants nothing.
	if _, err := a.pool.Exec(ctx, `INSERT INTO auth_member (subject, scope, role) VALUES ($1, 'a', 'retired')`, deployerID); err != nil {
		t.Fatal(err)
	}
	want(deployer, "retired role", "GET", "a/deploys", 403)
	if roles, err := teamRoles.Of(ctx, deployerID, "a"); err != nil || len(roles) != 0 {
		t.Fatalf("roles of the revoked deployer: %v %v", roles, err)
	}

	// A deleted account loses its memberships with it.
	if err := a.DeleteUser(ctx, viewerID); err != nil {
		t.Fatal(err)
	}
	var left int
	a.pool.QueryRow(ctx, `SELECT count(*) FROM auth_member WHERE subject = $1`, viewerID).Scan(&left)
	if left != 0 {
		t.Fatalf("%d memberships left after deletion", left)
	}

	// The check fails closed: no table, no access.
	if _, err := a.pool.Exec(ctx, `ALTER TABLE auth_member RENAME TO auth_member_away`); err != nil {
		t.Fatal(err)
	}
	want(admin, "admin, database failing", "POST", "a/members", 500)
	if ok, err := teamRoles.Can(ctx, adminID, "a", "deploy.read"); ok || err == nil {
		t.Fatalf("Can with the database failing: %v %v", ok, err)
	}
	a.pool.Exec(ctx, `ALTER TABLE auth_member_away RENAME TO auth_member`)
}

func TestRolesAPI(t *testing.T) {
	a, _, ctx := rolesApp(t)
	if err := teamRoles.Grant(ctx, "u1", "a", "owner"); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("unknown role: %v", err)
	}
	if err := teamRoles.Grant(ctx, "", "a", "viewer"); err == nil {
		t.Fatal("empty subject granted")
	}
	for i, s := range []string{"u3", "u1", "u2", "u4", "u5"} {
		role := []string{"viewer", "deployer"}[i%2]
		if err := teamRoles.Grant(ctx, s, "a", role); err != nil {
			t.Fatal(err)
		}
	}
	teamRoles.Grant(ctx, "u1", "a", "viewer")
	teamRoles.Grant(ctx, "u1", "a", "viewer") // idempotent
	teamRoles.Grant(ctx, "u1", "b", "admin")
	var all []string
	cursor := ""
	for pages := 0; ; pages++ {
		page, next, err := teamRoles.Members(ctx, "a", cursor, 2)
		if err != nil || pages > 5 {
			t.Fatal(err, pages)
		}
		for _, m := range page {
			all = append(all, m.Subject+":"+m.Role)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if got := strings.Join(all, ","); got != "u1:deployer,u1:viewer,u2:viewer,u3:viewer,u4:deployer,u5:viewer" {
		t.Fatalf("members: %s", got)
	}
	if scopes, err := teamRoles.Scopes(ctx, "u1"); err != nil || strings.Join(scopes, ",") != "a,b" {
		t.Fatalf("scopes: %v %v", scopes, err)
	}
	if err := teamRoles.RevokeAll(ctx, "u1", "a"); err != nil {
		t.Fatal(err)
	}
	if roles, _ := teamRoles.Of(ctx, "u1", "a"); len(roles) != 0 {
		t.Fatalf("after RevokeAll: %v", roles)
	}
	// Check: the signed-in user of a context, for typed handlers and jobs.
	withUser := context.WithValue(ctx, userKey{}, &User{ID: "u4"})
	if err := teamRoles.Check(withUser, "a", "deploy.run"); err != nil {
		t.Fatal(err)
	}
	if err := teamRoles.Check(withUser, "a", "team.manage"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forbidden: %v", err)
	}
	var he *router.HTTPError
	if err := teamRoles.Check(ctx, "a", "deploy.read"); !errors.As(err, &he) || he.Status != 401 {
		t.Fatalf("no user: %v", err)
	}
	_ = a
}

func TestGrants(t *testing.T) {
	for _, c := range []struct {
		p, want string
		ok      bool
	}{
		{"*", "x.y", true}, {"deploy.read", "deploy.read", true}, {"deploy.*", "deploy.run", true},
		{"deploy.*", "deployment.run", false}, {"deploy.read", "deploy.run", false}, {"deploy.*", "deploy", false},
	} {
		if grants(c.p, c.want) != c.ok {
			t.Errorf("grants(%q, %q) != %v", c.p, c.want, c.ok)
		}
	}
}
