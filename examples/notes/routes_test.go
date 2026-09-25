package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/lidzatest"

	"notes/handlers"
	"notes/schema"
)

// lidzatest.Start boots the app with its packs against .env.test
// (LIDZA_MODE=test); its client keeps cookies, so a login carries over
// to the calls after it. `lidza test` creates and migrates the database
// first, then runs this.
func TestNotes(t *testing.T) {
	srv := lidzatest.Start(t, app())
	creds := schema.Credentials{Email: fmt.Sprintf("u%d@example.com", time.Now().UnixNano()), Password: "correct horse battery"}

	if res := srv.JSON(t, http.MethodGet, "/api/v1/notes", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
	var state schema.SessionState
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/session", nil, &state); res.StatusCode != http.StatusOK || state.User != nil {
		t.Fatalf("visitor session: %d %+v", res.StatusCode, state)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", schema.Credentials{Email: "x", Password: "short"}, nil); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid credentials: %d %s", res.StatusCode, body(res))
	}
	// The password policy: long enough for the schema, refused by the pack.
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", schema.Credentials{Email: creds.Email, Password: "password123"}, nil); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body(res), "too common") {
		t.Fatalf("weak password: %d %s", res.StatusCode, body(res))
	}

	// Emails are captured instead of sent.
	var mails []string
	handlers.Mail = func(ctx context.Context, to, subject, body string) error {
		mails = append(mails, body)
		return nil
	}
	var session schema.Session
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", creds, &session); res.StatusCode != http.StatusCreated || session.Email != creds.Email || session.AccessToken == "" || session.Verified {
		t.Fatalf("register: %d %+v", res.StatusCode, session)
	}
	if len(mails) != 1 || !strings.Contains(mails[0], "/verify?token=") {
		t.Fatalf("verification mail: %v", mails)
	}
	verifyToken := strings.TrimPrefix(strings.Fields(mails[0])[1], "/verify?token=")
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/verify", schema.VerifyEmail{Token: "nope"}, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad verify token: %d", res.StatusCode)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/verify", schema.VerifyEmail{Token: verifyToken}, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("verify: %d %s", res.StatusCode, body(res))
	}
	var afterVerify schema.Session
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/login", creds, &afterVerify); res.StatusCode != http.StatusOK || !afterVerify.Verified {
		t.Fatalf("login after verify: %d %+v", res.StatusCode, afterVerify)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", creds, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate email: %d", res.StatusCode)
	}

	var note schema.Note
	if res := srv.JSON(t, http.MethodPost, "/api/v1/notes", schema.CreateNote{Title: "first"}, &note); res.StatusCode != http.StatusCreated || note.OwnerID != session.UserID {
		t.Fatalf("create: %d %+v", res.StatusCode, note)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/notes", schema.CreateNote{Title: ""}, nil); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty title: %d", res.StatusCode)
	}
	var list schema.NoteList
	if res := srv.JSON(t, http.MethodGet, "/api/v1/notes", nil, &list); res.StatusCode != http.StatusOK || list.Total != 1 || list.Items[0].ID != note.ID {
		t.Fatalf("list: %d %+v", res.StatusCode, list)
	}

	// The stats pack (Rust) over a note: "first" from the title plus the body.
	noteBody := "the cat and the dog and the bird"
	var withBody schema.Note
	if res := srv.JSON(t, http.MethodPatch, "/api/v1/notes/"+note.ID, schema.UpdateNote{Body: &noteBody}, &withBody); res.StatusCode != http.StatusOK || withBody.Body == nil {
		t.Fatalf("patch: %d %+v", res.StatusCode, withBody)
	}
	var st schema.TextStats
	if res := srv.JSON(t, http.MethodGet, "/api/v1/notes/"+note.ID+"/stats", nil, &st); res.StatusCode != http.StatusOK || st.Words != 9 || st.Unique != 6 || len(st.TopWords) != 5 || st.TopWords[0].Word != "the" || st.TopWords[0].Count != 3 {
		t.Fatalf("stats: %d %+v", res.StatusCode, st)
	}

	// A bearer token works without the cookies.
	var me schema.Session
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/me", nil, &me, lidzatest.Bearer(session.AccessToken)); res.StatusCode != http.StatusOK || me.UserID != session.UserID {
		t.Fatalf("me: %d %+v", res.StatusCode, me)
	}

	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/session", nil, &state); res.StatusCode != http.StatusOK || state.User == nil || state.User.UserID != session.UserID {
		t.Fatalf("user session: %d %+v", res.StatusCode, state)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/logout", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/me", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", res.StatusCode)
	}

	// Another user sees none of the first one's notes.
	other := schema.Credentials{Email: "o" + creds.Email, Password: creds.Password}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", other, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("second user: %d", res.StatusCode)
	}
	if res := srv.JSON(t, http.MethodGet, "/api/v1/notes", nil, &list); res.StatusCode != http.StatusOK || list.Total != 0 {
		t.Fatalf("other's list: %d %+v", res.StatusCode, list)
	}
	if res := srv.JSON(t, http.MethodGet, "/api/v1/notes/"+note.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("other's get: %d", res.StatusCode)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/login", schema.Credentials{Email: other.Email, Password: "wrong password"}, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}

	// Password reset: a link by mail, a new password, every session ended.
	mails = nil
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/forgot", schema.ForgotPassword{Email: "nobody@example.com"}, nil); res.StatusCode != http.StatusNoContent || len(mails) != 0 {
		t.Fatalf("forgot for unknown email: %d %v", res.StatusCode, mails)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/forgot", schema.ForgotPassword{Email: other.Email}, nil); res.StatusCode != http.StatusNoContent || len(mails) != 1 {
		t.Fatalf("forgot: %d %v", res.StatusCode, mails)
	}
	resetToken := strings.TrimPrefix(strings.Fields(mails[0])[1], "/reset?token=")
	newPassword := "battery staple horse"
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/reset", schema.ResetPassword{Token: resetToken, Password: newPassword}, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: %d %s", res.StatusCode, body(res))
	}
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/me", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session should be revoked after reset: %d", res.StatusCode)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/login", schema.Credentials{Email: other.Email, Password: newPassword}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("login with the new password: %d", res.StatusCode)
	}
}

// TestThrottle: the credential routes are rate limited per client.
func TestThrottle(t *testing.T) {
	srv := lidzatest.Start(t, app())
	var last int
	for i := 0; i < 12; i++ {
		last = srv.JSON(t, http.MethodPost, "/api/v1/auth/login", schema.Credentials{Email: "x@example.com", Password: "wrong password"}, nil).StatusCode
		if last == http.StatusTooManyRequests {
			break
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("no 429 after repeated attempts: %d", last)
	}
}

func body(res *http.Response) string {
	b := make([]byte, 512)
	n, _ := res.Body.Read(b)
	return string(b[:n])
}
