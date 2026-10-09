package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/router"
)

// claimAuth is testAuth with the owner claim on, its files in a
// temporary directory and an empty claim table.
func claimAuth(t *testing.T, platformToken string) (*Auth, string) {
	t.Helper()
	a := testAuth(t)
	ctx := context.Background()
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS auth_owner_claim`)
	if _, err := a.pool.Exec(ctx, OwnerClaimTable); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "owner-claim")
	a.cfg.OwnerClaim, a.cfg.OwnerClaimDir, a.cfg.OwnerClaimToken = true, dir, platformToken
	return a, dir
}

func readStatus(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestOwnerClaim: with the claim on, signing in first makes nobody the
// owner; the token file is private and ignored by git; a wrong token
// changes nothing; of many claims at once exactly one wins; the token
// file goes and the status says claimed; ownership survives a restart,
// the owner's sessions ending and its deletion; a claimed app has no
// token to rotate.
func TestOwnerClaim(t *testing.T) {
	ctx := context.Background()
	a, dir := claimAuth(t, "")
	if err := a.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "token")
	info, err := os.Stat(tokenFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", info, err)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Errorf("claim directory mode %v", d.Mode().Perm())
	}
	if gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.Contains(string(gi), "*") {
		t.Errorf("claim directory not ignored by git: %q", gi)
	}
	if st := readStatus(t, dir); !strings.Contains(st, `"unclaimed"`) {
		t.Errorf("status %s", st)
	}
	raw, _ := os.ReadFile(tokenFile)
	token := strings.TrimSpace(string(raw))
	if len(token) < 40 {
		t.Fatalf("token %d characters", len(token))
	}
	var stored string
	a.pool.QueryRow(ctx, `SELECT token_hash FROM auth_owner_claim`).Scan(&stored)
	if strings.Contains(stored, token) {
		t.Fatal("the token is stored, not its hash")
	}

	// Signing in first is not owning.
	if _, err := a.Login(ctx, "early-bird", nil); err != nil {
		t.Fatal(err)
	}
	if first, _ := a.FirstSubject(ctx); first != "" {
		t.Fatalf("first sign-in became the owner: %q", first)
	}
	if err := a.ClaimOwner(ctx, "early-bird", token+"x"); !errors.Is(err, ErrOwnerToken) {
		t.Fatalf("wrong token: %v", err)
	}
	if err := a.ClaimOwner(ctx, "early-bird", ""); !errors.Is(err, ErrOwnerToken) {
		t.Fatalf("empty token: %v", err)
	}

	// Many at once: one owner.
	var wg sync.WaitGroup
	results := make([]error, 12)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = a.ClaimOwner(ctx, "claimant-"+string(rune('a'+i)), token)
		}(i)
	}
	wg.Wait()
	winner := ""
	for i, err := range results {
		switch {
		case err == nil && winner == "":
			winner = "claimant-" + string(rune('a'+i))
		case err == nil:
			t.Fatalf("two owners: %s and claimant-%c", winner, 'a'+i)
		case !errors.Is(err, ErrOwnerClaimed):
			t.Fatalf("claim %d: %v", i, err)
		}
	}
	if winner == "" {
		t.Fatal("nobody claimed")
	}
	if first, _ := a.FirstSubject(ctx); first != winner {
		t.Fatalf("FirstSubject %q, owner %q", first, winner)
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("token file left after the claim: %v", err)
	}
	if st := readStatus(t, dir); !strings.Contains(st, `"claimed"`) || !strings.Contains(st, "claimedAt") {
		t.Errorf("status %s", st)
	}
	if err := a.ClaimOwner(ctx, "late", token); !errors.Is(err, ErrOwnerClaimed) {
		t.Errorf("used token: %v", err)
	}
	if err := a.RotateOwnerToken(ctx); !errors.Is(err, ErrOwnerClaimed) {
		t.Errorf("rotating a claimed app: %v", err)
	}

	// A restart, the owner's sessions ended, the owner deleted: still owned.
	a.pool.Exec(ctx, `UPDATE auth_session SET revoked_at = now()`)
	a.pool.Exec(ctx, `DELETE FROM auth_session`)
	restarted, err := New(a.cfg, a.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	if first, _ := restarted.FirstSubject(ctx); first != winner {
		t.Fatalf("after a restart FirstSubject %q, want %q", first, winner)
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("a restart wrote a token for a claimed app: %v", err)
	}
	if st, _ := restarted.OwnerClaimStatus(ctx); st.State != "claimed" {
		t.Errorf("status %+v", st)
	}
}

// TestOwnerClaimRotate: rotating an unclaimed token makes the old one
// useless at once; a restart keeps the token it finds.
func TestOwnerClaimRotate(t *testing.T) {
	ctx := context.Background()
	a, dir := claimAuth(t, "")
	if err := a.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(dir, "token"))
	restarted, _ := New(a.cfg, a.pool)
	if err := restarted.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(dir, "token")); string(again) != string(old) {
		t.Fatal("a restart replaced the token")
	}
	if err := a.RotateOwnerToken(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, _ := os.ReadFile(filepath.Join(dir, "token"))
	if string(fresh) == string(old) {
		t.Fatal("rotate kept the token")
	}
	if err := a.ClaimOwner(ctx, "u1", strings.TrimSpace(string(old))); !errors.Is(err, ErrOwnerToken) {
		t.Fatalf("old token after rotate: %v", err)
	}
	if err := a.ClaimOwner(ctx, "u1", strings.TrimSpace(string(fresh))); err != nil {
		t.Fatalf("new token: %v", err)
	}
}

// TestOwnerClaimPlatformToken: a token the deployment supplies is used as
// it is, never written to a file; one too short refuses to start.
func TestOwnerClaimPlatformToken(t *testing.T) {
	ctx := context.Background()
	a, dir := claimAuth(t, "short")
	if err := a.startOwnerClaim(ctx); err == nil {
		t.Fatal("a short platform token started")
	}
	platform := strings.Repeat("p", 48)
	a.cfg.OwnerClaimToken = platform
	if err := a.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "token")); !os.IsNotExist(err) {
		t.Fatalf("platform token written to a file: %v", err)
	}
	if a.OwnerClaimFile() != "" {
		t.Errorf("OwnerClaimFile %q with a platform token", a.OwnerClaimFile())
	}
	if err := a.RotateOwnerToken(ctx); err == nil {
		t.Error("rotated a platform token")
	}
	if err := a.ClaimOwner(ctx, "op", platform); err != nil {
		t.Fatal(err)
	}
}

// TestOwnerClaimExistingApp: turning the claim on in an app that already
// has a first account keeps that account the owner; nothing reopens.
func TestOwnerClaimExistingApp(t *testing.T) {
	ctx := context.Background()
	a, dir := claimAuth(t, "")
	if _, err := a.Login(ctx, "founder", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	a.Login(ctx, "second", nil)
	if err := a.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	if first, _ := a.FirstSubject(ctx); first != "founder" {
		t.Fatalf("FirstSubject %q", first)
	}
	if _, err := os.Stat(filepath.Join(dir, "token")); !os.IsNotExist(err) {
		t.Errorf("token written for an app that has its owner: %v", err)
	}
	if st := readStatus(t, dir); !strings.Contains(st, `"claimed"`) {
		t.Errorf("status %s", st)
	}
}

// TestOwnerClaimRoutes: the routes answer only a signed-in account, 403
// for a wrong token and 409 once claimed; they never return the token;
// with the claim off they are 404.
func TestOwnerClaimRoutes(t *testing.T) {
	ctx := context.Background()
	a, dir := claimAuth(t, "")
	a.cfg.LoginRPS, a.cfg.LoginBurst = 1000, 1000
	if err := a.startOwnerClaim(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "token"))
	token := strings.TrimSpace(string(raw))
	s := lidza.NewServices()
	lidza.Provide(s, a)
	r := router.New()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(lidza.WithServices(req.Context(), s)))
		})
	})
	Mount(r, Options{})
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	if code, _ := call(t, client, "POST", srv.URL+"/api/v1/auth/owner/claim", map[string]string{"token": token}); code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", code)
	}
	if code, _ := call(t, client, "POST", srv.URL+"/api/v1/auth/register", map[string]string{"email": "op@example.com", "password": "correct horse battery", "name": "Op"}); code >= 300 {
		t.Fatalf("register: %d", code)
	}
	if code, body := call(t, client, "GET", srv.URL+"/api/v1/auth/owner", nil); code != 200 || body["state"] != "unclaimed" {
		t.Fatalf("status: %d %v", code, body)
	}
	if code, body := call(t, client, "POST", srv.URL+"/api/v1/auth/owner/claim", map[string]string{"token": "nope"}); code != http.StatusForbidden || strings.Contains(strings.ToLower(toString(body)), token) {
		t.Fatalf("wrong token: %d %v", code, body)
	}
	if code, body := call(t, client, "POST", srv.URL+"/api/v1/auth/owner/claim", map[string]string{"token": token}); code != 200 || body["state"] != "claimed" {
		t.Fatalf("claim: %d %v", code, body)
	}
	if code, _ := call(t, client, "POST", srv.URL+"/api/v1/auth/owner/claim", map[string]string{"token": token}); code != http.StatusConflict {
		t.Fatalf("second claim: %d", code)
	}
	a.cfg.OwnerClaim = false
	if code, _ := call(t, client, "GET", srv.URL+"/api/v1/auth/owner", nil); code != http.StatusNotFound {
		t.Fatalf("claim off: %d", code)
	}
}

func toString(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		if s, ok := v.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}
