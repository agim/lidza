package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/router"
)

// callWith is call with request headers.
func callWith(t *testing.T, client *http.Client, method, u string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	if len(raw) > 0 {
		json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out
}

// browser is another client with its own cookies.
func browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func rows(t *testing.T, a *Auth, query string, args ...any) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// appTable is a table of the app's own, dropped after the test.
func appTable(t *testing.T, a *Auth, ddl string) {
	t.Helper()
	ctx := context.Background()
	name := strings.Fields(ddl)[2]
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS `+name)
	if _, err := a.pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.pool.Exec(context.Background(), `DROP TABLE IF EXISTS `+name) })
}

// TestSignUpHook: OnSignUp runs once per new account, in the account's
// transaction, for a registration and for a provider's first sign-in;
// not for a later sign-in, an identity that joins an account, or a
// refused registration. Its error rolls the account back.
func TestSignUpHook(t *testing.T) {
	a := testAuth(t)
	appTable(t, a, `CREATE TABLE app_profile (subject text PRIMARY KEY, method text NOT NULL, lang text NOT NULL)`)
	issuer := newFakeIssuer(t, "client-1")
	var mu sync.Mutex
	var calls []SignUp
	opt := Options{
		Providers: []Provider{OIDC("fake", "Fake", issuer.srv.URL, "client-1", "secret")},
		OnSignUp: func(ctx context.Context, tx pgx.Tx, s SignUp) error {
			if _, err := tx.Exec(ctx, `INSERT INTO app_profile (subject, method, lang) VALUES ($1, $2, $3)`, s.Profile.Subject, s.Method, s.Lang); err != nil {
				return err
			}
			if s.Profile.Email == "blocked@example.com" {
				return router.Errorf(http.StatusForbidden, "sign-ups are closed")
			}
			mu.Lock()
			calls = append(calls, s)
			mu.Unlock()
			return nil
		},
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(calls)
	}
	srv, client := signinServer(t, a, opt)
	api := srv.URL + Prefix

	// A registration, in the request's language.
	code, out := callWith(t, client, "POST", api+"/register", map[string]string{"email": "lea@example.com", "password": "correct horse battery"}, map[string]string{"Accept-Language": "de-CH, de;q=0.9"})
	if code != 201 {
		t.Fatalf("register: %d %v", code, out)
	}
	lea := out["subject"].(string)
	if count() != 1 || calls[0].Method != MethodPassword || calls[0].Profile.Subject != lea || calls[0].Lang != "de-CH" || calls[0].Identity != nil {
		t.Fatalf("sign-up: %+v", calls)
	}
	if rows(t, a, `SELECT count(*) FROM app_profile WHERE subject = $1 AND lang = 'de-CH'`, lea) != 1 {
		t.Fatal("the app's row was not written")
	}
	// Signing in again, a taken address: no sign-up.
	call(t, client, "POST", api+"/logout", nil)
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "lea@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if code, _ := call(t, browser(), "POST", api+"/register", map[string]string{"email": "lea@example.com", "password": "another long password"}); code != 409 || count() != 1 {
		t.Fatalf("taken address: %d, %d sign-ups", code, count())
	}
	// The hook refuses: its status reaches the client, nothing is kept.
	code, out = call(t, browser(), "POST", api+"/register", map[string]string{"email": "blocked@example.com", "password": "correct horse battery"})
	if code != 403 || out["error"] != "sign-ups are closed" || count() != 1 {
		t.Fatalf("refused sign-up: %d %v", code, out)
	}
	if rows(t, a, `SELECT count(*) FROM auth_user WHERE email = 'blocked@example.com'`) != 0 || rows(t, a, `SELECT count(*) FROM app_profile`) != 1 {
		t.Fatal("a refused sign-up left rows")
	}

	// A provider's first sign-in is a sign-up, the next one is not.
	ana := browser()
	if _, code := follow(t, ana, api+"/fake/start"); code != 302 {
		t.Fatalf("callback: %d", code)
	}
	if count() != 2 || calls[1].Method != "fake" || calls[1].Identity == nil || calls[1].Identity.Subject != "u-1" || calls[1].Profile.Email != "ana@example.com" {
		t.Fatalf("provider sign-up: %+v", calls)
	}
	call(t, ana, "POST", api+"/logout", nil)
	if _, code := follow(t, ana, api+"/fake/start"); code != 302 || count() != 2 {
		t.Fatalf("second sign-in: %d, %d sign-ups", code, count())
	}
	// An identity that joins the local account of its address: no sign-up.
	issuer.user = map[string]any{"sub": "u-2", "email": "lea@example.com", "email_verified": true}
	if _, code := follow(t, browser(), api+"/fake/start"); code != 302 || count() != 2 {
		t.Fatalf("joined account: %d, %d sign-ups", code, count())
	}
	// The hook refuses a provider sign-up: the failure page, no account,
	// no identity.
	issuer.user = map[string]any{"sub": "u-3", "email": "blocked@example.com", "email_verified": true}
	if to, code := follow(t, browser(), api+"/fake/start"); code != 302 || to != "/login?error=signup" {
		t.Fatalf("refused provider sign-up: %d %q", code, to)
	}
	if rows(t, a, `SELECT count(*) FROM auth_identity WHERE id = 'fake:u-3'`) != 0 || rows(t, a, `SELECT count(*) FROM auth_user WHERE email = 'blocked@example.com'`) != 0 {
		t.Fatal("a refused provider sign-up left rows")
	}
	if n := rows(t, a, `SELECT count(*) FROM app_profile`); n != 2 || count() != 2 {
		t.Fatalf("app rows %d, sign-ups %d", n, count())
	}
}

// TestDeleteAccount: the delete route confirms with the password (or a
// fresh sign-in without one), is refused to a cross-site form, and
// removes every row of the pack and, through OnDeleteUser in the same
// transaction, the app's.
func TestDeleteAccount(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	appTable(t, a, `CREATE TABLE app_note (subject text NOT NULL, body text NOT NULL)`)
	issuer := newFakeIssuer(t, "client-1")
	var mu sync.Mutex
	refuse := false
	opt := Options{
		Providers: []Provider{OIDC("fake", "Fake", issuer.srv.URL, "client-1", "secret")},
		OnDeleteUser: func(ctx context.Context, tx pgx.Tx, subject string) error {
			if _, err := tx.Exec(ctx, `DELETE FROM app_note WHERE subject = $1`, subject); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if refuse {
				return errors.New("the app refuses")
			}
			return nil
		},
	}
	srv, client := signinServer(t, a, opt)
	api := srv.URL + Prefix

	// The app's first account, with a note, tokens and two sessions.
	code, out := call(t, client, "POST", api+"/register", map[string]string{"email": "ana@example.com", "password": "correct horse battery"})
	if code != 201 {
		t.Fatalf("register: %d %v", code, out)
	}
	ana := out["subject"].(string)
	a.pool.Exec(ctx, `INSERT INTO app_note (subject, body) VALUES ($1, 'hello')`, ana)
	a.IssueToken(ctx, PurposeVerifyEmail, ana, 0)
	a.IssueToken(ctx, PurposeResetPassword, "ana@example.com", 0)
	if code, _ := call(t, browser(), "POST", api+"/login", map[string]string{"email": "ana@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("second session: %d", code)
	}
	exists := func() bool { return rows(t, a, `SELECT count(*) FROM auth_user WHERE subject = $1`, ana) == 1 }

	// A cross-site form cannot ride the cookie.
	req, _ := http.NewRequest("POST", api+"/delete", strings.NewReader("password=correct+horse+battery"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 || !exists() {
		t.Fatalf("form post: %d", res.StatusCode)
	}
	// No password, a wrong one, a visitor.
	if code, _ := call(t, client, "POST", api+"/delete", map[string]string{}); code != 403 || !exists() {
		t.Fatalf("no password: %d", code)
	}
	if code, _ := call(t, client, "POST", api+"/delete", map[string]string{"password": "wrong password here"}); code != 403 || !exists() {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := call(t, browser(), "POST", api+"/delete", map[string]string{"password": "correct horse battery"}); code != 401 {
		t.Fatalf("visitor: %d", code)
	}
	// The app's hook fails: everything is rolled back, its own delete too.
	mu.Lock()
	refuse = true
	mu.Unlock()
	if code, _ := call(t, client, "POST", api+"/delete", map[string]string{"password": "correct horse battery"}); code != 500 || !exists() ||
		rows(t, a, `SELECT count(*) FROM app_note WHERE subject = $1`, ana) != 1 {
		t.Fatalf("refused deletion: %d", code)
	}
	mu.Lock()
	refuse = false
	mu.Unlock()
	if code, _ := call(t, client, "POST", api+"/delete", map[string]string{"password": "correct horse battery"}); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := call(t, client, "GET", api+"/me", nil); code != 401 {
		t.Fatalf("session after delete: %d", code)
	}
	for _, q := range []string{
		`SELECT count(*) FROM auth_user WHERE subject = $1`,
		`SELECT count(*) FROM auth_account WHERE subject = $1`,
		`SELECT count(*) FROM auth_identity WHERE subject = $1`,
		`SELECT count(*) FROM auth_token WHERE subject = $1 OR subject = 'ana@example.com'`,
		`SELECT count(*) FROM auth_session WHERE subject = $1 AND revoked_at IS NULL`,
		`SELECT count(*) FROM app_note WHERE subject = $1`,
	} {
		if n := rows(t, a, q, ana); n != 0 {
			t.Errorf("%d rows left: %s", n, q)
		}
	}
	// The first account's first session stays, revoked, so the first
	// account (an admin) does not pass to the next user.
	if n := rows(t, a, `SELECT count(*) FROM auth_session WHERE subject = $1`, ana); n != 1 {
		t.Fatalf("first account's sessions: %d", n)
	}
	fresh, _ := New(Config{Secret: testSecret}, a.pool)
	if first, _ := fresh.FirstSubject(ctx); first != ana {
		t.Fatalf("first subject moved to %q", first)
	}
	if code, _ := call(t, client, "POST", api+"/login", map[string]string{"email": "ana@example.com", "password": "correct horse battery"}); code != 401 {
		t.Fatalf("login after delete: %d", code)
	}

	// Without a password: a sign-in within ReauthWindow confirms.
	bo := browser()
	if _, code := follow(t, bo, api+"/fake/start"); code != 302 {
		t.Fatalf("callback: %d", code)
	}
	_, out = call(t, bo, "GET", api+"/me", nil)
	boID := out["subject"].(string)
	a.pool.Exec(ctx, `UPDATE auth_session SET created_at = now() - interval '1 hour' WHERE subject = $1`, boID)
	if code, out := call(t, bo, "POST", api+"/delete", map[string]string{}); code != 403 || out["error"] != "sign in again to delete the account" {
		t.Fatalf("stale sign-in: %d %v", code, out)
	}
	if _, code := follow(t, bo, api+"/fake/start"); code != 302 {
		t.Fatalf("sign-in again: %d", code)
	}
	if code, _ := call(t, bo, "POST", api+"/delete", map[string]string{}); code != 204 {
		t.Fatalf("delete after a fresh sign-in: %d", code)
	}
	for _, table := range []string{"auth_user", "auth_account", "auth_identity", "auth_session"} {
		if n := rows(t, a, `SELECT count(*) FROM `+table+` WHERE subject = $1`, boID); n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
	// The same provider account signs up anew.
	if _, code := follow(t, bo, api+"/fake/start"); code != 302 {
		t.Fatalf("sign up again: %d", code)
	}
	if _, out := call(t, bo, "GET", api+"/me", nil); out["subject"] == boID || out["subject"] == nil {
		t.Fatalf("new account: %v", out)
	}

	// An unknown subject is a 404.
	var httpErr *router.HTTPError
	if err := a.DeleteUser(ctx, "nobody"); !errors.As(err, &httpErr) || httpErr.Status != 404 {
		t.Fatalf("unknown subject: %v", err)
	}
	// Without OnDeleteUser the route is left out.
	srv2, client2 := signinServer(t, a, Options{})
	call(t, client2, "POST", srv2.URL+Prefix+"/register", map[string]string{"email": "cy@example.com", "password": "correct horse battery"})
	if code, _ := call(t, client2, "POST", srv2.URL+Prefix+"/delete", map[string]string{"password": "correct horse battery"}); code == 204 {
		t.Fatal("delete route mounted without OnDeleteUser")
	}
}

// TestLocalizedEmails: the verification link goes out through
// auth_verify.<lang> for the request's language, with the template's
// subject, else auth_verify, else the plain text.
func TestLocalizedEmails(t *testing.T) {
	a := testAuth(t)
	ctx := context.Background()
	a.pool.Exec(ctx, `DROP TABLE IF EXISTS mail_message`)
	if _, err := a.pool.Exec(ctx, mail.OutboxTable); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "auth_verify.txt.tmpl"), []byte("Verify {{.Email}}: {{.Link}}"), 0o644)
	os.WriteFile(filepath.Join(dir, "auth_verify.de.txt.tmpl"), []byte("{{define \"subject\"}}E-Mail für {{.App}} bestätigen{{end}}Bestätige {{.Email}}: {{.Link}}"), 0o644)
	m, err := mail.New(mail.Config{Provider: "outbox", From: "app@example.com", TemplatesDir: dir, AppURL: "https://app.example.com"}, a.pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := signinServerWith(t, a, Options{Title: "Notes"}, m)
	api := srv.URL + Prefix
	sent := func(to string) mail.Stored {
		t.Helper()
		msgs, err := m.Outbox(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range msgs {
			if s.To == to {
				return s
			}
		}
		t.Fatalf("no message to %s", to)
		return mail.Stored{}
	}
	register := func(email, lang string) {
		t.Helper()
		headers := map[string]string{}
		if lang != "" {
			headers["Accept-Language"] = lang
		}
		if code, out := callWith(t, browser(), "POST", api+"/register", map[string]string{"email": email, "password": "correct horse battery"}, headers); code != 201 {
			t.Fatalf("register %s: %d %v", email, code, out)
		}
	}
	register("de@example.com", "de-AT, de;q=0.9, en;q=0.5")
	if s := sent("de@example.com"); s.Subject != "E-Mail für Notes bestätigen" || *s.Template != "auth_verify.de" || !strings.HasPrefix(*s.Text, "Bestätige de@example.com: https://app.example.com/verify?token=") {
		t.Fatalf("german: %+v %s", s, *s.Text)
	}
	register("fr@example.com", "fr")
	if s := sent("fr@example.com"); s.Subject != "Verify your email" || *s.Template != "auth_verify" || !strings.HasPrefix(*s.Text, "Verify fr@example.com: ") {
		t.Fatalf("fallback: %+v", s)
	}
	register("none@example.com", "")
	if s := sent("none@example.com"); *s.Template != "auth_verify" {
		t.Fatalf("no language: %+v", s)
	}
	// No auth_reset template at all: the plain text.
	if code, _ := callWith(t, browser(), "POST", api+"/forgot", map[string]string{"email": "de@example.com"}, map[string]string{"Accept-Language": "de"}); code != 204 {
		t.Fatalf("forgot: %d", code)
	}
	msgs, _ := m.Outbox(ctx, 1)
	if len(msgs) != 1 || msgs[0].Subject != "Reset your password" || msgs[0].Template != nil || !strings.Contains(*msgs[0].Text, "set a new password for Notes") {
		t.Fatalf("reset: %+v", msgs)
	}
}
