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

// TestAuditPerformance runs lidza audit performance against fixture
// pages at --base-url: a page with JavaScript it never runs, sent
// uncompressed, an image far larger than drawn and without a size, and
// no description is reported with each finding and faults on a small
// JS budget; an llms.txt served as HTML is a fault.
func TestAuditPerformance(t *testing.T) {
	unused := "function never" + strings.Repeat("x", 10) + "() { return " + strings.Repeat("'unused code that never runs' + ", 2000) + "'' }\nwindow.ran = true\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Write([]byte(unused))
		case "/big.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="3000" height="2000"><rect width="3000" height="2000" fill="#241821"/></svg>`))
		case "/llms.txt":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<!doctype html><div id=app></div>"))
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<!doctype html><html lang="en"><meta name="viewport" content="width=device-width"><title>Fixture</title><body><h1>Fixture</h1><img src="/big.svg" alt="" style="width:100px"><script src="/app.js"></script>`))
		}
	}))
	defer srv.Close()
	out, _ := command(app, lidza, "audit", "performance", "--json", "--base-url", srv.URL, "--samples", "1", "--budget", "js=10kb,tbt=100000,fcp=100000,lcp=100000")
	i := strings.Index(string(out), "{\n")
	var r struct {
		Status string `json:"status"`
		LLMs   struct {
			Problem string `json:"problem"`
		} `json:"llms"`
		Results []struct {
			Route    string `json:"route"`
			UnusedJS int    `json:"unusedJS"`
			Over     []string
			Findings []struct {
				Check   string `json:"check"`
				Message string `json:"message"`
			} `json:"findings"`
			Bytes map[string]int `json:"bytes"`
		} `json:"results"`
	}
	if i < 0 || json.NewDecoder(bytes.NewReader(out[i:])).Decode(&r) != nil || len(r.Results) != 1 {
		t.Fatalf("no report:\n%s", out)
	}
	res := r.Results[0]
	if r.Status != "fault" || len(res.Over) != 1 || !strings.HasPrefix(res.Over[0], "js ") {
		t.Errorf("js budget: status %s, over %v", r.Status, res.Over)
	}
	if res.UnusedJS < 20*1024 || res.Bytes["script"] < 50*1024 {
		t.Errorf("unused JS %d of %d", res.UnusedJS, res.Bytes["script"])
	}
	checks := map[string]bool{}
	for _, f := range res.Findings {
		checks[f.Check] = true
	}
	for _, want := range []string{"compression", "unused-js", "image-size", "image-dimensions", "seo"} {
		if !checks[want] {
			t.Errorf("no %s finding: %+v", want, res.Findings)
		}
	}
	if !strings.Contains(r.LLMs.Problem, "HTML") {
		t.Errorf("llms.txt served as HTML: %+v", r.LLMs)
	}
}
