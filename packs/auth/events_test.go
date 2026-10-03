package auth

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestEvents: every account event the routes handle reaches OnEvent, with
// the account, the address, the method, the reason and the request.
func TestEvents(t *testing.T) {
	a := testAuth(t)
	var mu sync.Mutex
	var got []Event
	srv, client := signinServer(t, a, Options{OnEvent: func(_ context.Context, e Event) {
		if e.Request == nil || e.Request.RemoteAddr == "" {
			t.Errorf("%s without the request", e.Kind)
		}
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	}, OnDeleteUser: func(context.Context, pgx.Tx, string) error { return nil }})
	api := srv.URL + Prefix
	last := func(kind string) Event {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		for i := len(got) - 1; i >= 0; i-- {
			if got[i].Kind == kind {
				return got[i]
			}
		}
		t.Fatalf("no %s in %+v", kind, got)
		return Event{}
	}
	kinds := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(got))
		for i, e := range got {
			out[i] = e.Kind
		}
		return out
	}

	_, out := call(t, client, "POST", api+"/register", map[string]string{"email": "ev@example.com", "password": "correct horse battery"})
	subject := out["subject"].(string)
	if e := last(EventSignedUp); e.Subject != subject || e.Method != MethodPassword || e.Email != "ev@example.com" {
		t.Fatalf("signed up: %+v", e)
	}
	if k := kinds(); !slices.Equal(k, []string{EventSignedUp, EventSignedIn}) {
		t.Fatalf("register: %v", k)
	}
	call(t, client, "POST", api+"/logout", nil)
	if e := last(EventSignedOut); e.Subject != subject || e.Email != "ev@example.com" {
		t.Fatalf("signed out: %+v", e)
	}

	// Failed sign-ins: a wrong password names the account; an unknown
	// address does not.
	call(t, client, "POST", api+"/login", map[string]string{"email": "EV@example.com", "password": "wrong password!!"})
	if e := last(EventSignInFailed); e.Subject != subject || e.Reason != "bad_credentials" || e.Method != MethodPassword || e.Email != "ev@example.com" {
		t.Fatalf("wrong password: %+v", e)
	}
	call(t, client, "POST", api+"/login", map[string]string{"email": "nobody@example.com", "password": "wrong password!!"})
	if e := last(EventSignInFailed); e.Subject != "" || e.Email != "nobody@example.com" {
		t.Fatalf("unknown address: %+v", e)
	}
	call(t, client, "POST", api+"/login", map[string]string{"email": "ev@example.com", "password": "correct horse battery"})
	if e := last(EventSignedIn); e.Subject != subject || e.Method != MethodPassword {
		t.Fatalf("signed in: %+v", e)
	}

	// Password change: a wrong current one, then the change.
	call(t, client, "POST", api+"/password", map[string]string{"current": "not the password", "password": "new horse battery staple"})
	if e := last(EventPasswordCheckFailed); e.Subject != subject {
		t.Fatalf("check failed: %+v", e)
	}
	if code, _ := call(t, client, "POST", api+"/password", map[string]string{"current": "correct horse battery", "password": "new horse battery staple"}); code != 204 {
		t.Fatalf("change: %d", code)
	}
	last(EventPasswordChanged)

	// Reset: requested (an unknown address reports nothing), then used.
	n := len(kinds())
	call(t, client, "POST", api+"/forgot", map[string]string{"email": "nobody@example.com"})
	if len(kinds()) != n {
		t.Fatalf("unknown address reported: %v", kinds())
	}
	call(t, client, "POST", api+"/forgot", map[string]string{"email": "ev@example.com"})
	last(EventResetRequested)
	tok, err := a.IssueToken(context.Background(), PurposeResetPassword, subject, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, client, "POST", api+"/reset", map[string]string{"token": tok, "password": "third horse battery staple"}); code != 204 {
		t.Fatalf("reset: %d", code)
	}
	last(EventPasswordReset)
	vt, _ := a.IssueToken(context.Background(), PurposeVerifyEmail, subject, time.Hour)
	call(t, client, "POST", api+"/verify", map[string]string{"token": vt})
	last(EventEmailVerified)

	// Deletion.
	call(t, client, "POST", api+"/login", map[string]string{"email": "ev@example.com", "password": "third horse battery staple"})
	if code, _ := call(t, client, "POST", api+"/delete", map[string]string{"password": "third horse battery staple"}); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if e := last(EventAccountDeleted); e.Subject != subject {
		t.Fatalf("deleted: %+v", e)
	}
}
