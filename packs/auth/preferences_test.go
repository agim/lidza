package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func preferenceAuth(t *testing.T) *Auth {
	t.Helper()
	a := testAuth(t)
	if _, err := a.pool.Exec(context.Background(), `DROP TABLE IF EXISTS auth_preference;`+PreferenceTable); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPreferencesValidationAndAnonymousGoCalls(t *testing.T) {
	for _, input := range []string{`{}`, `{"values":null}`, `{"values":{"../unsafe":true}}`, `{"values":{"view":` + strings.Repeat(`"`, 1) + strings.Repeat("a", MaxPreferenceValueBytes) + `"}}`} {
		var patch PreferencePatch
		if err := json.Unmarshal([]byte(input), &patch); err != nil {
			t.Fatal(err)
		}
		if err := patch.Validate(); err == nil {
			t.Fatal("invalid patch accepted")
		}
	}
	a := &Auth{}
	if _, err := a.Preferences(context.Background()); err == nil {
		t.Fatal("anonymous read accepted")
	}
	if _, err := a.PatchPreferences(context.Background(), PreferencePatch{Values: PreferenceValues{}}); err == nil {
		t.Fatal("anonymous write accepted")
	}
}

func TestPreferencesPersistAcrossSessionsNodesAndIsolateAccounts(t *testing.T) {
	a := preferenceAuth(t)
	ctx := context.Background()
	srv, client := signinServer(t, a, Options{})
	alice, err := a.Login(ctx, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := a.Login(ctx, "bob", nil)
	if err != nil {
		t.Fatal(err)
	}
	header := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	api := srv.URL + Prefix + "/preferences"
	patch := map[string]any{"values": map[string]any{"products.view": "list", "products.status": "active"}}
	if code, _ := authCall(t, client, "PATCH", api, patch, header(alice.Access)); code != 200 {
		t.Fatal(code)
	}
	if code, _ := authCall(t, client, "PATCH", api, map[string]any{"account": "bob", "values": map[string]any{"products.view": "grid"}}, header(alice.Access)); code != 409 {
		t.Fatal("stale account expectation accepted", code)
	}
	if code, body := authCall(t, client, "GET", api, nil, header(bob.Access)); code != 200 || len(body["values"].(map[string]any)) != 0 {
		t.Fatal("cross-account read", code, body)
	}
	if code, _ := authCall(t, client, "PATCH", api+"?subject=alice", map[string]any{"subject": "alice", "values": map[string]any{"products.view": "grid"}}, header(bob.Access)); code != 200 {
		t.Fatal(code)
	}
	if code, body := authCall(t, client, "GET", api, nil, header(alice.Access)); code != 200 || body["subject"] != "alice" || body["values"].(map[string]any)["products.view"] != "list" {
		t.Fatal("cross-account write", body)
	}
	if code, _ := authCall(t, client, "GET", api, nil, nil); code != 401 {
		t.Fatal("anonymous request accepted", code)
	}
	if code, _ := authCall(t, client, "POST", srv.URL+Prefix+"/logout", map[string]any{}, header(alice.Access)); code != 204 {
		t.Fatal(code)
	}
	if code, _ := authCall(t, client, "GET", api, nil, header(alice.Access)); code != 401 {
		t.Fatal("revoked session accepted", code)
	}
	// A new session and independently constructed pack/server see the database
	// document, without any browser storage or process-local cache.
	newAlice, err := a.Login(ctx, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(a.cfg, a.pool)
	if err != nil {
		t.Fatal(err)
	}
	otherServer, otherClient := signinServer(t, other, Options{})
	if code, body := authCall(t, otherClient, "GET", otherServer.URL+Prefix+"/preferences", nil, header(newAlice.Access)); code != 200 || body["values"].(map[string]any)["products.view"] != "list" {
		t.Fatal("preferences lost across sessions/nodes", code, body)
	}
	req, _ := http.NewRequest("GET", api, nil)
	req.Header.Set("Authorization", "Bearer "+newAlice.Access)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("private preferences cacheable")
	}
	// Cookie sessions must keep the normal CSRF guard on state changes.
	jar, _ := cookiejar.New(nil)
	cookieClient := &http.Client{Jar: jar}
	parsed, _ := url.Parse(srv.URL)
	jar.SetCookies(parsed, []*http.Cookie{{Name: AccessCookie, Value: newAlice.Access}})
	if code, _ := authCall(t, cookieClient, "PATCH", api, patch, map[string]string{"Content-Type": "text/plain", "Sec-Fetch-Site": "cross-site", "Origin": "https://other.test"}); code != 403 {
		t.Fatal("CSRF request accepted", code)
	}
}

func TestPreferenceConcurrentPatchesLimitsDeleteAndCancellation(t *testing.T) {
	a := preferenceAuth(t)
	ctx := context.Background()
	srv, client := signinServer(t, a, Options{})
	tokens, err := a.Login(ctx, "parallel", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.Verify(tokens.Access)
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual HTTP middleware/context for concurrent partial updates.
	api := srv.URL + Prefix + "/preferences"
	header := map[string]string{"Authorization": "Bearer " + tokens.Access}
	var wg sync.WaitGroup
	failures := make(chan string, 20)
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			code, _ := authCall(t, client, "PATCH", api, map[string]any{"values": map[string]any{fmt.Sprintf("panel.key%d", n): n}}, header)
			if code != 200 {
				failures <- fmt.Sprint(code)
			}
		}(n)
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	code, body := authCall(t, client, "GET", api, nil, header)
	if code != 200 || len(body["values"].(map[string]any)) != 20 {
		t.Fatal("concurrent patches lost keys", code, body)
	}
	if code, _ = authCall(t, client, "PATCH", api, map[string]any{"values": map[string]any{"panel.key0": nil}}, header); code != 200 {
		t.Fatal(code)
	}
	large := map[string]any{}
	for n := 0; n < 9; n++ {
		large[fmt.Sprintf("large.key%d", n)] = strings.Repeat("v", 4000)
	}
	if code, _ = authCall(t, client, "PATCH", api, map[string]any{"values": large}, header); code != 422 {
		t.Fatal("oversized patch accepted", code)
	}
	_, body = authCall(t, client, "GET", api, nil, header)
	values := body["values"].(map[string]any)
	if len(values) != 19 || values["panel.key0"] != nil || values["large.key0"] != nil {
		t.Fatal("invalid patch changed document", values)
	}
	tooMany := map[string]any{}
	for n := 0; n < 110; n++ {
		tooMany[fmt.Sprintf("extra.key%d", n)] = true
	}
	if code, _ = authCall(t, client, "PATCH", api, map[string]any{"values": tooMany}, header); code != 422 {
		t.Fatal("merged key limit not enforced", code)
	}
	_, body = authCall(t, client, "GET", api, nil, header)
	if len(body["values"].(map[string]any)) != 19 {
		t.Fatal("failed merge was committed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	// CurrentUser is normally set by Require. Test deadline handling through
	// the same middleware rather than manufacturing an identity context.
	req, _ := http.NewRequestWithContext(cancelled, "GET", api, nil)
	req.Header.Set("Authorization", "Bearer "+tokens.Access)
	if res, e := client.Do(req); e == nil {
		res.Body.Close()
		t.Fatal("cancelled request succeeded")
	}
	if err = a.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.pool.QueryRow(ctx, `SELECT count(*) FROM auth_preference WHERE subject=$1`, u.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("account preferences not removed", count, err)
	}
	if code, _ = authCall(t, client, "GET", api, nil, header); code != 401 {
		t.Fatal("deleted account read accepted", code)
	}
}
