package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/packs/mail"
	"github.com/agim/lidza/pkg/lidzatest"

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

	// MAIL_PROVIDER=outbox in .env.test: nothing is sent, the outbox has
	// every message, newest first.
	// The table persists across runs, so assertions look at the newest row.
	outbox := func() []mail.Stored {
		rows, err := mail.From(srv.Context()).Outbox(context.Background(), 5)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	newest := func() string {
		if rows := outbox(); len(rows) > 0 {
			return rows[0].ID
		}
		return ""
	}
	// linkIn reads the token out of the emailed link; mail.Link made it
	// absolute when APP_URL is set, so match on the path.
	linkIn := func(row mail.Stored, prefix string) string {
		for _, f := range strings.Fields(*row.Text) {
			if i := strings.Index(f, prefix); i >= 0 {
				return f[i+len(prefix):]
			}
		}
		t.Fatalf("no %s link in %q", prefix, *row.Text)
		return ""
	}
	var session schema.Session
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", creds, &session); res.StatusCode != http.StatusCreated || session.Email != creds.Email || session.AccessToken == "" || session.Verified {
		t.Fatalf("register: %d %+v", res.StatusCode, session)
	}
	// WaitFor finds the message by address and subject, waiting for one a
	// job sends; this one was written in the request, so it is there.
	verification, err := mail.From(srv.Context()).WaitFor(context.Background(), creds.Email, "Verify", 5*time.Second)
	if err != nil || verification.Status != mail.StatusSent || verification.Template == nil || *verification.Template != "verify" {
		t.Fatalf("verification mail: %+v %v", verification, err)
	}
	verifyToken := linkIn(verification, "/verify?token=")
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

	// Tag suggestions come from the llm pack; .env.test makes it the fake,
	// scripted here, so the test checks the prompt and the validation.
	fake := llm.From(srv.Context()).Fake()
	fake.ReplyJSON(schema.NoteTags{Tags: []string{"lists", "words"}})
	var tags schema.NoteTags
	if res := srv.JSON(t, http.MethodPost, "/api/v1/notes/"+note.ID+"/tags", nil, &tags); res.StatusCode != http.StatusOK || len(tags.Tags) != 2 || tags.Tags[0] != "lists" {
		t.Fatalf("tags: %d %+v %s", res.StatusCode, tags, body(res))
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Schema == nil || !strings.Contains(calls[0].Messages[0].Content, note.Title) {
		t.Fatalf("prompt sent to the model: %+v", calls)
	}
	fake.ReplyJSON(schema.NoteTags{})
	if res := srv.JSON(t, http.MethodPost, "/api/v1/notes/"+note.ID+"/tags", nil, nil); res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("an empty tag list must fail validation: %d %s", res.StatusCode, body(res))
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
	last := newest()
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/forgot", schema.ForgotPassword{Email: "nobody@example.com"}, nil); res.StatusCode != http.StatusNoContent || newest() != last {
		t.Fatalf("forgot for unknown email must send nothing: %d", res.StatusCode)
	}
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/forgot", schema.ForgotPassword{Email: other.Email}, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("forgot: %d", res.StatusCode)
	}
	reset := outbox()[0]
	if reset.ID == last || reset.To != other.Email || reset.Template == nil || *reset.Template != "reset" {
		t.Fatalf("reset mail: %+v", reset)
	}
	resetToken := linkIn(reset, "/reset?token=")
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
