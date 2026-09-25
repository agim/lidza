package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"

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

	var session schema.Session
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", creds, &session); res.StatusCode != http.StatusCreated || session.Email != creds.Email || session.AccessToken == "" {
		t.Fatalf("register: %d %+v", res.StatusCode, session)
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
	body := "the cat and the dog and the bird"
	var withBody schema.Note
	if res := srv.JSON(t, http.MethodPatch, "/api/v1/notes/"+note.ID, schema.UpdateNote{Body: &body}, &withBody); res.StatusCode != http.StatusOK || withBody.Body == nil {
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
}

func body(res *http.Response) string {
	b := make([]byte, 512)
	n, _ := res.Body.Read(b)
	return string(b[:n])
}
