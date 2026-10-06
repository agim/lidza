//go:build evals

package evals

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditPages are the fixtures of TestAuditLayout: a wide table in its
// own scroller, a poll that replaces a scroller, one that only changes
// its text, and a log that follows its tail on purpose.
var auditPages = map[string]string{
	"/wide": `<div id="wrap" style="overflow:auto;width:100%"><table style="width:1200px"><tr><td>wide row <a href="#">Details</a></td></tr></table></div>`,
	"/replaced": `<div id="list"></div><script>
const draw = () => { document.getElementById('list').outerHTML = '<div id="list" style="overflow:auto;width:100%"><table style="width:1200px"><tr><td>row <a href=#>Details</a></td></tr></table></div>' }
draw(); setInterval(draw, 500)</script>`,
	"/updated": `<div id="list" style="overflow:auto;width:100%"><table style="width:1200px"><tr><td id="cell">row</td><td><a href="#">Details</a></td></tr></table></div><script>
let n = 0; setInterval(() => { document.getElementById('cell').textContent = 'row ' + (++n) }, 500)</script>`,
	"/follow": `<pre id="log" data-audit-follow style="overflow:auto;height:100px"></pre><script>
const log = document.getElementById('log'); for (let i = 0; i < 50; i++) log.textContent += 'line ' + i + '\n'
setInterval(() => { log.textContent += 'more\n'; log.scrollTop = log.scrollHeight }, 300)</script>`,
}

// TestAuditLayout runs lidza audit layout against a server it did not
// start (--base-url): a scroller inside the page is listed while the
// document does not scroll sideways, the stability check faults a
// scroller a poll replaces and passes one it only updates or one that
// follows on purpose, and a hash route is a same-document navigation,
// not a failed load.
func TestAuditLayout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/"})
		case "/private":
			if c, err := r.Cookie("session"); err != nil || c.Value != "fixture" {
				http.Error(w, "sign in", http.StatusUnauthorized)
				return
			}
		}
		body, ok := auditPages[r.URL.Path]
		if !ok {
			body = `<p>home</p>`
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><meta name="viewport" content="width=device-width"><body style="margin:0">` + body))
	}))
	defer srv.Close()
	type report struct {
		Status  string `json:"status"`
		Results []struct {
			Route      string `json:"route"`
			Status     int    `json:"status"`
			Navigation string `json:"navigation"`
			Sideways   int    `json:"sideways"`
			Scrollers  []struct {
				Selector string `json:"selector"`
				Sideways int    `json:"sideways"`
			} `json:"scrollers"`
			Stability *struct {
				Resets   []any    `json:"resets"`
				Replaced []string `json:"replaced"`
			} `json:"stability"`
		} `json:"results"`
	}
	audit := func(args ...string) report {
		t.Helper()
		out, _ := command(app, lidza, append([]string{"audit", "layout", "--json", "--base-url", srv.URL, "--viewport", "390x844"}, args...)...)
		var r report
		i := strings.Index(string(out), "{\n")
		if i < 0 || json.NewDecoder(bytes.NewReader(out[i:])).Decode(&r) != nil {
			t.Fatalf("audit %v: no report\n%s", args, out)
		}
		return r
	}

	r := audit("--routes", "/wide,/,/#/settings")
	if r.Status != "ok" || len(r.Results) != 3 {
		t.Fatalf("plain audit: %+v", r)
	}
	wide := r.Results[0]
	if wide.Sideways != 0 || len(wide.Scrollers) != 1 || wide.Scrollers[0].Sideways < 700 || !strings.HasPrefix(wide.Scrollers[0].Selector, "div#wrap") {
		t.Errorf("a wide table in its scroller: %+v", wide)
	}
	if h := r.Results[2]; h.Navigation != "same-document" || h.Status != 0 {
		t.Errorf("hash route: %+v", h)
	}
	if h := r.Results[1]; h.Navigation != "document" || h.Status != 200 {
		t.Errorf("home: %+v", h)
	}

	if r := audit("--routes", "/replaced", "--stability", "2s"); r.Status != "fault" || r.Results[0].Stability == nil || len(r.Results[0].Stability.Resets) == 0 || len(r.Results[0].Stability.Replaced) == 0 {
		t.Errorf("a poll that replaces the scroller passed: %+v", r)
	}
	if r := audit("--routes", "/updated,/follow", "--stability", "2s"); r.Status != "ok" {
		t.Errorf("an updated scroller or a log following on purpose failed: %+v", r)
	}
	if r := audit("--routes", "/follow", "--stability", "2s", "--allow", "#none"); r.Status != "ok" {
		t.Errorf("data-audit-follow ignored: %+v", r)
	}

	// An app's own sign-in, without registration.
	if r := audit("--routes", "/private"); r.Results[0].Status != 401 {
		t.Errorf("signed out: %+v", r)
	}
	login := filepath.Join(app, ".lidza", "audit-login.mjs")
	if err := os.WriteFile(login, []byte("export default async (page, base) => { await page.goto(base + '/login') }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := audit("--routes", "/private", "--login", login); r.Results[0].Status != 200 {
		t.Errorf("--login: %+v", r)
	}
}
