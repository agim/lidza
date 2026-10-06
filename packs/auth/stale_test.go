package auth

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// A browser whose session the server no longer knows (a reset database)
// is signed out, not stuck: the session route answers {user: null} and
// clears the cookies; a required route answers 401 and clears them too;
// a bearer token keeps its 401.
func TestStaleCookieSession(t *testing.T) {
	a := testAuth(t)
	srv, _ := signinServer(t, a, Options{Providers: []Provider{}})
	api := srv.URL + Prefix
	b, _ := user(t, api, "stale@example.com")
	if code, out := call(t, b, "GET", api+"/session", nil); code != 200 || out["user"] == nil {
		t.Fatalf("signed in: %d %v", code, out)
	}
	// The server forgets every session.
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM auth_session`); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	if code, out := call(t, b, "GET", api+"/session", nil); code != 200 || out["user"] != nil {
		t.Fatalf("stale session: %d %v", code, out)
	}
	for _, c := range b.Jar.Cookies(u) {
		if c.Name == AccessCookie || c.Name == RefreshCookie {
			t.Fatalf("stale cookie %s kept", c.Name)
		}
	}
	// Cleared: the next call is a plain visitor.
	if code, out := call(t, b, "GET", api+"/session", nil); code != 200 || out["user"] != nil {
		t.Fatalf("after clearing: %d %v", code, out)
	}

	// A required route: 401, cookies cleared.
	b2, _ := user(t, api, "stale2@example.com")
	a.pool.Exec(context.Background(), `DELETE FROM auth_session`)
	if code, _ := call(t, b2, "GET", api+"/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("required route: %d", code)
	}
	for _, c := range b2.Jar.Cookies(u) {
		if c.Name == AccessCookie || c.Name == RefreshCookie {
			t.Fatalf("stale cookie %s kept on 401", c.Name)
		}
	}

	// A bad bearer token on the session route stays a 401.
	req, _ := http.NewRequest("GET", api+"/session", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bearer: %d", res.StatusCode)
	}
}
