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

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/pack"
)

//go:embed assets/audit-layout.mjs
var auditLayoutScript []byte

const auditUsage = `usage:
  lidza audit layout [--viewport 1440x900,390x844] [--theme light,dark]
                     [--routes /posts/1,/settings] [--max-scroll -1]
                     [--no-sign-in] [--json]
Builds and starts the app on the test database (as lidza test --e2e),
visits every page the build prerenders plus --routes, signed in as a
throwaway user when the auth pack runs, at each viewport and theme, and
reports what scrolls: sideways (always a fault; the elements past the
edge are named) and down (a fault past --max-scroll pixels; -1, the
default, only reports it). Exits non-zero on a fault.
`

// auditResult is one page at one viewport and theme.
type auditResult struct {
	Route     string   `json:"route"`
	Viewport  string   `json:"viewport"`
	Theme     string   `json:"theme"`
	Status    int      `json:"status"`
	Sideways  int      `json:"sideways"`
	Vertical  int      `json:"vertical"`
	Offenders []string `json:"offenders,omitempty"`
	Error     string   `json:"error,omitempty"`
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
	asJSON := fs.Bool("json", false, "print one JSON report")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil || cfg.Frontend.Dist == "" {
		return errors.New("audit layout needs a lidza.json app with a built frontend (react, svelte, astro)")
	}
	if _, err := os.Stat(filepath.Join(abs, "playwright.config.ts")); err != nil {
		return errors.New("audit layout runs the app's Playwright: no playwright.config.ts here")
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
	os.Setenv(devserver.EnvMode, "test")
	if err := devserver.EnsureNodeModules(ctx, abs, os.Stdout); err != nil {
		return err
	}
	if exe, err := browserPath(ctx, abs); err != nil || !fileExists(exe) {
		return errors.New("the browser for e2e tests is missing: npx playwright install --with-deps chromium (or lidza test --e2e --install)")
	}
	base, stop, err := startTestApp(ctx, abs, cfg)
	if err != nil {
		return err
	}
	defer stop()
	routes := prerenderedRoutes(filepath.Join(abs, cfg.Frontend.Dist))
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
	signIn := !*noSignIn && slices.Contains(cfg.Packs, pack.OfficialPrefix+"auth")
	script := filepath.Join(abs, devserver.BuildDir, "audit-layout.mjs")
	if err := os.WriteFile(script, auditLayoutScript, 0o644); err != nil {
		return err
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	fmt.Printf("[audit] %d page(s) × %d viewport(s) × %d theme(s) on %s\n", len(routes), len(vps), len(schemes), base)
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Dir = abs
	cmd.Env = append(testEnv(abs), "BASE_URL="+base, "AUDIT_ROUTES="+enc(routes), "AUDIT_VIEWPORTS="+enc(vps), "AUDIT_THEMES="+enc(schemes), "AUDIT_SIGN_IN="+map[bool]string{true: "1", false: "0"}[signIn])
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
	faults := auditFaults(report.Results, *maxScroll)
	if *asJSON {
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		e.Encode(map[string]any{"status": map[bool]string{true: "ok", false: "fault"}[faults == 0], "signedIn": report.SignedIn, "results": report.Results})
	} else {
		printAudit(report.Results, *maxScroll, signIn, report.SignedIn)
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

// auditFaults counts the pages that scroll sideways, fail to load, or
// scroll down past maxScroll (when it is 0 or more).
func auditFaults(results []auditResult, maxScroll int) int {
	n := 0
	for _, r := range results {
		if r.Error != "" || r.Sideways > 0 || (maxScroll >= 0 && r.Vertical > maxScroll) {
			n++
		}
	}
	return n
}

func printAudit(results []auditResult, maxScroll int, wanted, signedIn bool) {
	if wanted && !signedIn {
		fmt.Println("[audit] signing up a throwaway user failed: the pages were visited signed out")
	}
	for _, r := range results {
		mark := "ok   "
		switch {
		case r.Error != "":
			mark = "FAULT"
		case r.Sideways > 0 || (maxScroll >= 0 && r.Vertical > maxScroll):
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
		}
		fmt.Println(line)
		for _, o := range r.Offenders {
			fmt.Println("        past the edge: " + o)
		}
	}
}
