package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/pack"
)

//go:embed assets/audit-layout.mjs
var auditLayoutScript []byte

const auditUsage = `usage:
  lidza audit layout [--viewport 1440x900,390x844] [--theme light,dark]
                     [--routes /posts/1,/settings] [--max-scroll -1]
                     [--base-url URL] [--storage-state FILE | --login FILE | --no-sign-in]
                     [--stability 6s [--trigger JS] [--allow SELECTORS]] [--json]
Builds and starts the app on the test database (as lidza test --e2e), or
uses the one at --base-url, visits every page the build prerenders plus
--routes, signed in as a throwaway user when the auth pack runs (or with
--storage-state, a Playwright storage state, or --login, a module whose
default export signs a page in), at each viewport and theme, and reports
what scrolls: sideways (always a fault; the elements past the edge are
named), down (a fault past --max-scroll pixels; -1, the default, only
reports it), and the containers inside the page that scroll on their own
(listed, never a fault). --stability scrolls those containers, runs
--trigger (a JavaScript expression, such as a refresh call) or only
waits that long, and faults a container that lost its position or the
focus inside it; --allow names the ones that move on purpose (a log that
follows its tail; data-audit-follow on the element does the same).
Exits non-zero on a fault.
`

// auditResult is one page at one viewport and theme.
type auditResult struct {
	Route    string `json:"route"`
	Viewport string `json:"viewport"`
	Theme    string `json:"theme"`
	// Status is the HTTP status of the page; none for a same-document
	// navigation (a hash route), which has no response.
	Status int `json:"status,omitempty"`
	// Navigation is "document", "same-document" or "failed".
	Navigation string          `json:"navigation"`
	Sideways   int             `json:"sideways"`
	Vertical   int             `json:"vertical"`
	Offenders  []string        `json:"offenders,omitempty"`
	Scrollers  []auditScroller `json:"scrollers,omitempty"`
	Stability  *auditStability `json:"stability,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// auditScroller is a container inside the page that scrolls on its own.
type auditScroller struct {
	Selector string `json:"selector"`
	Client   [2]int `json:"client"`
	Scroll   [2]int `json:"scroll"`
	Sideways int    `json:"sideways"`
	Vertical int    `json:"vertical"`
	Allowed  bool   `json:"allowed,omitempty"`
}

// auditStability is what moved during the stability wait.
type auditStability struct {
	Resets []struct {
		Selector string `json:"selector"`
		From     [2]int `json:"from"`
		To       [2]int `json:"to"`
	} `json:"resets,omitempty"`
	Replaced    []string `json:"replaced,omitempty"`
	FocusLost   bool     `json:"focusLost,omitempty"`
	LayoutShift float64  `json:"layoutShift"`
	Error       string   `json:"error,omitempty"`
}

func runAudit(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "layout" {
		fmt.Fprint(os.Stderr, auditUsage)
		return errors.New("audit: what to audit? layout")
	}
	fs := flags("audit layout")
	dir := fs.String("dir", ".", "project directory")
	viewports := fs.String("viewport", "1440x900,390x844", "viewports, WIDTHxHEIGHT, comma-separated")
	themes := fs.String("theme", "light", "color schemes, light and dark, comma-separated")
	extra := fs.String("routes", "", "routes to visit besides the prerendered pages (ones with parameters), comma-separated")
	maxScroll := fs.Int("max-scroll", -1, "pixels a page may scroll down before it is a fault; -1 only reports it")
	noSignIn := fs.Bool("no-sign-in", false, "visit signed out even when the auth pack runs")
	baseURL := fs.String("base-url", "", "audit the app running here instead of building and starting one")
	storageState := fs.String("storage-state", "", "a Playwright storage state file to visit signed in with")
	login := fs.String("login", "", "a JavaScript module whose default export, async (page, baseURL), signs the page in")
	stabilityWait := fs.Duration("stability", 0, "scroll the nested scrollers, wait this long (after --trigger), and fault the ones that lost their position")
	trigger := fs.String("trigger", "", "with --stability: a JavaScript expression run in the page before the wait (a refresh)")
	allow := fs.String("allow", "", "with --stability: CSS selectors of scrollers that move on purpose, comma-separated")
	asJSON := fs.Bool("json", false, "print one JSON report")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	var cfg *config.Config
	if fileExists(filepath.Join(abs, config.FileName)) {
		if abs, cfg, err = loadProject(*dir); err != nil {
			return err
		}
	}
	if *baseURL == "" && (cfg == nil || cfg.Frontend.Dist == "") {
		return errors.New("audit layout builds and starts a lidza.json app with a built frontend (react, svelte, astro); for any other app, start it and pass --base-url")
	}
	if !fileExists(filepath.Join(abs, "node_modules", "@playwright", "test", "package.json")) {
		return errors.New("audit layout runs the app's Playwright: no node_modules/@playwright/test here (npm install -D @playwright/test)")
	}
	if *storageState != "" && *login != "" {
		return errors.New("audit layout: --storage-state or --login, not both")
	}
	for _, f := range []*string{storageState, login} {
		if *f == "" {
			continue
		}
		if !filepath.IsAbs(*f) {
			*f = filepath.Join(abs, *f)
		}
		if !fileExists(*f) {
			return fmt.Errorf("audit layout: %s does not exist", *f)
		}
	}
	if (*trigger != "" || *allow != "") && *stabilityWait == 0 {
		return errors.New("audit layout: --trigger and --allow go with --stability")
	}
	if *stabilityWait < 0 || *stabilityWait > 2*time.Minute {
		return errors.New("audit layout: --stability is a wait of up to 2m, e.g. 6s")
	}
	vps, err := parseViewports(*viewports)
	if err != nil {
		return err
	}
	var schemes []string
	for _, t := range splitList(*themes) {
		if t != "light" && t != "dark" {
			return fmt.Errorf("audit layout: theme %q: light or dark", t)
		}
		schemes = append(schemes, t)
	}
	// With --json, stdout is the report alone: the build, the app and
	// the browser write to stderr.
	stdout := os.Stdout
	if *asJSON {
		os.Stdout = os.Stderr
		defer func() { os.Stdout = stdout }()
	}
	if exe, err := browserPath(ctx, abs); err != nil || !fileExists(exe) {
		return errors.New("the browser for e2e tests is missing: npx playwright install --with-deps chromium (or lidza test --e2e --install)")
	}
	base := strings.TrimRight(*baseURL, "/")
	if base == "" {
		os.Setenv(devserver.EnvMode, "test")
		if err := devserver.EnsureNodeModules(ctx, abs, os.Stdout); err != nil {
			return err
		}
		b, stop, err := startTestApp(ctx, abs, cfg)
		if err != nil {
			return err
		}
		defer stop()
		base = b
	}
	var routes []string
	if cfg != nil && cfg.Frontend.Dist != "" {
		routes = prerenderedRoutes(filepath.Join(abs, cfg.Frontend.Dist))
	}
	for _, r := range splitList(*extra) {
		if !strings.HasPrefix(r, "/") {
			r = "/" + r
		}
		if !slices.Contains(routes, r) {
			routes = append(routes, r)
		}
	}
	if len(routes) == 0 {
		routes = []string{"/"}
	}
	signIn := *storageState == "" && *login == "" && !*noSignIn && *baseURL == "" && slices.Contains(cfg.Packs, pack.OfficialPrefix+"auth")
	if err := os.MkdirAll(filepath.Join(abs, devserver.BuildDir), 0o755); err != nil {
		return err
	}
	script := filepath.Join(abs, devserver.BuildDir, "audit-layout.mjs")
	if err := os.WriteFile(script, auditLayoutScript, 0o644); err != nil {
		return err
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	fmt.Printf("[audit] %d page(s) × %d viewport(s) × %d theme(s) on %s\n", len(routes), len(vps), len(schemes), base)
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Dir = abs
	cmd.Env = append(testEnv(abs), "BASE_URL="+base, "AUDIT_ROUTES="+enc(routes), "AUDIT_VIEWPORTS="+enc(vps), "AUDIT_THEMES="+enc(schemes),
		"AUDIT_SIGN_IN="+map[bool]string{true: "1", false: "0"}[signIn],
		"AUDIT_STORAGE_STATE="+*storageState, "AUDIT_LOGIN="+*login,
		"AUDIT_STABILITY_MS="+strconv.FormatInt(stabilityWait.Milliseconds(), 10), "AUDIT_TRIGGER="+*trigger, "AUDIT_ALLOW="+enc(splitList(*allow)))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("audit layout: the browser run failed: %v\n%s", err, out.String())
	}
	var report struct {
		SignedIn bool          `json:"signedIn"`
		Results  []auditResult `json:"results"`
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &report); err != nil {
		return fmt.Errorf("audit layout: unreadable report: %v\n%s", err, out.String())
	}
	wanted := signIn || *storageState != "" || *login != ""
	faults := auditFaults(report.Results, *maxScroll)
	if *asJSON {
		e := json.NewEncoder(stdout)
		e.SetIndent("", "  ")
		e.Encode(map[string]any{"status": map[bool]string{true: "ok", false: "fault"}[faults == 0], "signedIn": report.SignedIn, "results": report.Results})
	} else {
		printAudit(report.Results, *maxScroll, wanted, report.SignedIn)
	}
	if faults > 0 {
		return fmt.Errorf("audit layout: %d fault(s)", faults)
	}
	return nil
}

// parseViewports reads "1440x900,390x844".
func parseViewports(s string) ([][2]int, error) {
	var out [][2]int
	for _, v := range splitList(s) {
		w, h, ok := strings.Cut(strings.ToLower(v), "x")
		wi, err1 := strconv.Atoi(w)
		hi, err2 := strconv.Atoi(h)
		if !ok || err1 != nil || err2 != nil || wi < 200 || hi < 200 {
			return nil, fmt.Errorf("audit layout: viewport %q: WIDTHxHEIGHT, e.g. 390x844", v)
		}
		out = append(out, [2]int{wi, hi})
	}
	if len(out) == 0 {
		return nil, errors.New("audit layout: no viewport")
	}
	return out, nil
}

// prerenderedRoutes lists the pages the build wrote: dist/index.html is
// "/", dist/about/index.html "/about" (the per-locale copies left out).
func prerenderedRoutes(dist string) []string {
	var routes []string
	filepath.WalkDir(dist, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != dist {
			return filepath.SkipDir
		}
		if d.Name() != "index.html" {
			return nil
		}
		rel, _ := filepath.Rel(dist, filepath.Dir(p))
		route := "/" + filepath.ToSlash(rel)
		if rel == "." {
			route = "/"
		}
		routes = append(routes, route)
		return nil
	})
	sort.Strings(routes)
	return routes
}

// auditFaults counts the pages that scroll sideways, fail to load,
// scroll down past maxScroll (when it is 0 or more), or, under the
// stability check, lost a scroller's position or the focus.
func auditFaults(results []auditResult, maxScroll int) int {
	n := 0
	for _, r := range results {
		if pageFault(r, maxScroll) {
			n++
		}
	}
	return n
}

func pageFault(r auditResult, maxScroll int) bool {
	if r.Error != "" || r.Sideways > 0 || (maxScroll >= 0 && r.Vertical > maxScroll) {
		return true
	}
	return r.Stability != nil && (len(r.Stability.Resets) > 0 || r.Stability.FocusLost || r.Stability.Error != "")
}

func printAudit(results []auditResult, maxScroll int, wanted, signedIn bool) {
	if wanted && !signedIn {
		fmt.Println("[audit] signing in failed: the pages were visited signed out")
	}
	for _, r := range results {
		mark := "ok   "
		if pageFault(r, maxScroll) {
			mark = "FAULT"
		}
		line := fmt.Sprintf("%s %-24s %-9s %-5s", mark, r.Route, r.Viewport, r.Theme)
		switch {
		case r.Error != "":
			line += " could not load: " + r.Error
		default:
			if r.Sideways > 0 {
				line += fmt.Sprintf(" scrolls sideways %dpx", r.Sideways)
			}
			if r.Vertical > 0 {
				line += fmt.Sprintf(" scrolls down %dpx", r.Vertical)
			}
			if r.Status >= 400 {
				line += fmt.Sprintf(" (HTTP %d)", r.Status)
			}
			if r.Navigation == "same-document" {
				line += " (same-document navigation)"
			}
		}
		fmt.Println(line)
		for _, o := range r.Offenders {
			fmt.Println("        past the edge: " + o)
		}
		for _, sc := range r.Scrollers {
			var dims []string
			if sc.Sideways > 0 {
				dims = append(dims, fmt.Sprintf("sideways %dpx", sc.Sideways))
			}
			if sc.Vertical > 0 {
				dims = append(dims, fmt.Sprintf("down %dpx", sc.Vertical))
			}
			fmt.Printf("        scroller: %s scrolls %s (%dx%d in %dx%d)\n", sc.Selector, strings.Join(dims, ", "), sc.Scroll[0], sc.Scroll[1], sc.Client[0], sc.Client[1])
		}
		if st := r.Stability; st != nil {
			if st.Error != "" {
				fmt.Println("        stability: " + st.Error)
			}
			for _, rs := range st.Resets {
				fmt.Printf("        lost its position: %s (%d,%d to %d,%d)\n", rs.Selector, rs.From[0], rs.From[1], rs.To[0], rs.To[1])
			}
			for _, rp := range st.Replaced {
				fmt.Println("        replaced: " + rp)
			}
			if st.FocusLost {
				fmt.Println("        lost the focus inside the first scroller")
			}
			if st.LayoutShift > 0 {
				fmt.Printf("        layout shift %.3f during the wait\n", st.LayoutShift)
			}
		}
	}
}
