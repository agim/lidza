package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/lidzatest"

	"notes/app"
	"notes/schema"
)

// The account's display choices: read empty, patched key by key, a null
// removes one, the pack's limits and the account check apply.
func TestPreferences(t *testing.T) {
	srv := lidzatest.Start(t, app.New(nil))
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/preferences", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
	creds := schema.Credentials{Email: fmt.Sprintf("p%d@example.com", time.Now().UnixNano()), Password: "correct horse battery"}
	var session schema.Session
	if res := srv.JSON(t, http.MethodPost, "/api/v1/auth/register", creds, &session); res.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", res.StatusCode, body(res))
	}

	var state auth.PreferenceState
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/preferences", nil, &state); res.StatusCode != http.StatusOK || len(state.Values) != 0 || state.Subject != session.UserID {
		t.Fatalf("fresh account: %d %+v", res.StatusCode, state)
	}
	patch := func(values map[string]any, account string) (*http.Response, auth.PreferenceState) {
		var out auth.PreferenceState
		raw := map[string]json.RawMessage{}
		for k, v := range values {
			b, _ := json.Marshal(v)
			raw[k] = b
		}
		res := srv.JSON(t, http.MethodPatch, "/api/v1/auth/preferences", auth.PreferencePatch{Account: account, Values: raw}, &out)
		return res, out
	}
	if res, out := patch(map[string]any{"notes.order": "oldest", "notes.width": 3}, ""); res.StatusCode != http.StatusOK || string(out.Values["notes.order"]) != `"oldest"` || string(out.Values["notes.width"]) != "3" {
		t.Fatalf("patch: %d %+v", res.StatusCode, out)
	}
	if res, out := patch(map[string]any{"notes.width": nil}, session.UserID); res.StatusCode != http.StatusOK || string(out.Values["notes.order"]) != `"oldest"` || out.Values["notes.width"] != nil {
		t.Fatalf("null removes a key, keeps the others: %d %+v", res.StatusCode, out)
	}
	if res, _ := patch(map[string]any{"notes.order": "newest"}, "someone-else"); res.StatusCode != http.StatusConflict {
		t.Fatalf("another account: %d %s", res.StatusCode, body(res))
	}
	if res, _ := patch(map[string]any{"Not A Key": true}, ""); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad key: %d %s", res.StatusCode, body(res))
	}
	if res := srv.JSON(t, http.MethodGet, "/api/v1/auth/preferences", nil, &state); res.StatusCode != http.StatusOK || string(state.Values["notes.order"]) != `"oldest"` || len(state.Values) != 1 {
		t.Fatalf("stored: %d %+v", res.StatusCode, state)
	}
}
