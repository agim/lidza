package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const auditPerformanceUsage = `usage:
  lidza audit performance [--samples 3] [--budget lcp=2500,js=300kb,...]
                          [--routes /a,/b] [--base-url URL]
                          [--storage-state FILE | --login FILE | --no-sign-in] [--json]
Builds and starts the app (as lidza test --e2e), or uses the one at
--base-url, and loads every page the build prerenders plus --routes cold
on a throttled phone (Lighthouse's mobile settings), --samples times. It
reports the median FCP, LCP, CLS and TBT, the bytes downloaded by kind,
the JavaScript that does not run on load, and what costs a visitor time:
uncompressed text, assets cached briefly, images larger than drawn or
without a size, a lazy LCP image, a missing title, lang, viewport or
description, and an llms.txt served as HTML. A page over a budget is a
fault; the rest are advice. Sizes and layout shift are held to budgets
by default (cls=0.1, js=300kb, css=100kb, images=1000kb, total=1600kb);
timings, which vary between machines, only when set (--budget
fcp=1800,lcp=2500,tbt=200, in ms; more --samples steadies them). Exits
non-zero on a fault.
`

// perfBudgetDefaults are the limits a page is held to unless --budget
// sets others: a shift score and bytes downloaded. Timings (fcp, lcp,
// tbt, in ms) vary between machines and gate only when set.
var perfBudgetDefaults = map[string]float64{
	"cls": 0.1, "js": 300 * 1024, "css": 100 * 1024, "images": 1000 * 1024, "total": 1600 * 1024,
}

// perfBudgetNames are the budgets --budget may set.
var perfBudgetNames = []string{"fcp", "lcp", "cls", "tbt", "js", "css", "images", "total"}

// parseBudgets reads "lcp=3000,js=250kb" over the defaults.
func parseBudgets(s string) (map[string]float64, error) {
	b := map[string]float64{}
	for k, v := range perfBudgetDefaults {
		b[k] = v
	}
	for _, kv := range splitList(s) {
		k, v, ok := strings.Cut(kv, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		if !ok || !slices.Contains(perfBudgetNames, k) {
			return nil, fmt.Errorf("audit performance: budget %q: one of fcp, lcp, cls, tbt, js, css, images, total, as name=value", kv)
		}
		v = strings.ToLower(strings.TrimSpace(v))
		mult := 1.0
		switch {
		case strings.HasSuffix(v, "kb"):
			v, mult = strings.TrimSuffix(v, "kb"), 1024
		case strings.HasSuffix(v, "mb"):
			v, mult = strings.TrimSuffix(v, "mb"), 1024*1024
		case strings.HasSuffix(v, "ms"):
			v = strings.TrimSuffix(v, "ms")
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return nil, fmt.Errorf("audit performance: budget %q: a number (ms for times, kb or mb for sizes)", kv)
		}
		b[k] = f * mult
	}
	return b, nil
}

// perfResult is one page.
type perfResult struct {
	Route      string           `json:"route"`
	Status     int              `json:"status"`
	Samples    int              `json:"samples"`
	FCP        float64          `json:"fcp"`
	LCP        float64          `json:"lcp"`
	CLS        float64          `json:"cls"`
	TBT        float64          `json:"tbt"`
	LCPElement string           `json:"lcpElement,omitempty"`
	Bytes      map[string]int64 `json:"bytes"`
	UnusedJS   int64            `json:"unusedJS"`
	Findings   []perfFinding    `json:"findings,omitempty"`
	Over       []string         `json:"over,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type perfFinding struct {
	Level   string `json:"level"`
	Check   string `json:"check"`
	Message string `json:"message"`
}

// over lists the budgets a page exceeds, as "lcp 3100 ms > 2500".
func (r *perfResult) over(b map[string]float64) []string {
	var out []string
	check := func(name string, got float64, unit string) {
		if limit, set := b[name]; set && got > limit {
			switch unit {
			case "KB":
				out = append(out, fmt.Sprintf("%s %.0f KB > %.0f", name, got/1024, b[name]/1024))
			case "":
				out = append(out, fmt.Sprintf("%s %.3f > %.3f", name, got, b[name]))
			default:
				out = append(out, fmt.Sprintf("%s %.0f %s > %.0f", name, got, unit, b[name]))
			}
		}
	}
	check("fcp", r.FCP, "ms")
	check("lcp", r.LCP, "ms")
	check("cls", r.CLS, "")
	check("tbt", r.TBT, "ms")
	check("js", float64(r.Bytes["script"]), "KB")
	check("css", float64(r.Bytes["stylesheet"]), "KB")
	check("images", float64(r.Bytes["image"]), "KB")
	check("total", float64(r.Bytes["total"]), "KB")
	return out
}

func runAuditPerformance(ctx context.Context, args []string) error {
	fs := flags("audit performance")
	o := auditFlags(fs, "performance")
	samples := fs.Int("samples", 3, "cold loads per page; the medians are reported")
	budget := fs.String("budget", "", "budgets over the defaults, e.g. lcp=3000,js=250kb")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *samples < 1 || *samples > 15 {
		return errors.New("audit performance: --samples from 1 to 15")
	}
	budgets, err := parseBudgets(*budget)
	if err != nil {
		return err
	}
	r, err := o.start(ctx)
	if err != nil {
		return err
	}
	defer r.close()
	fmt.Printf("[audit] %d page(s) × %d cold load(s) on a throttled phone, %s\n", len(r.routes), *samples, r.base)
	out, err := r.browse(ctx, "audit-performance.mjs", auditPerformanceScript, "AUDIT_SAMPLES="+strconv.Itoa(*samples))
	if err != nil {
		return err
	}
	var report struct {
		SignedIn bool         `json:"signedIn"`
		Device   string       `json:"device"`
		LLMs     perfLLMs     `json:"llms"`
		Results  []perfResult `json:"results"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		return fmt.Errorf("audit performance: unreadable report: %v\n%s", err, out)
	}
	faults := 0
	for i := range report.Results {
		res := &report.Results[i]
		if res.Error == "" {
			res.Over = res.over(budgets)
		}
		if res.Error != "" || len(res.Over) > 0 {
			faults++
		}
	}
	if report.LLMs.Problem != "" {
		faults++
	}
	if *o.asJSON {
		r.report(map[string]any{"status": map[bool]string{true: "ok", false: "fault"}[faults == 0], "signedIn": report.SignedIn,
			"device": report.Device, "budgets": budgets, "llms": report.LLMs, "results": report.Results})
	} else {
		printPerformance(report.Results, report.LLMs, report.Device, r.wantsSignIn(), report.SignedIn)
	}
	if faults > 0 {
		return fmt.Errorf("audit performance: %d fault(s)", faults)
	}
	return nil
}

type perfLLMs struct {
	Status  int    `json:"status"`
	Type    string `json:"type,omitempty"`
	Problem string `json:"problem,omitempty"`
}

func printPerformance(results []perfResult, llms perfLLMs, device string, wanted, signedIn bool) {
	if wanted && !signedIn {
		fmt.Println("[audit] signing in failed: the pages were visited signed out")
	}
	fmt.Printf("[audit] %s; medians\n", device)
	for _, r := range results {
		mark := "ok   "
		if r.Error != "" || len(r.Over) > 0 {
			mark = "FAULT"
		}
		if r.Error != "" {
			fmt.Printf("%s %-24s could not load: %s\n", mark, r.Route, r.Error)
			continue
		}
		fmt.Printf("%s %-24s FCP %4.0f ms  LCP %4.0f ms  CLS %.3f  TBT %3.0f ms  JS %d KB (%d KB unused)  CSS %d KB  images %d KB  total %d KB\n",
			mark, r.Route, r.FCP, r.LCP, r.CLS, r.TBT, r.Bytes["script"]/1024, r.UnusedJS/1024, r.Bytes["stylesheet"]/1024, r.Bytes["image"]/1024, r.Bytes["total"]/1024)
		for _, o := range r.Over {
			fmt.Println("        over budget: " + o)
		}
		sort.SliceStable(r.Findings, func(i, j int) bool { return r.Findings[i].Check < r.Findings[j].Check })
		for _, f := range r.Findings {
			fmt.Printf("        %s: %s\n", f.Check, f.Message)
		}
	}
	switch {
	case llms.Problem != "":
		fmt.Println("FAULT /llms.txt " + llms.Problem)
	case llms.Status == 200:
		fmt.Println("ok    /llms.txt served as " + llms.Type)
	default:
		fmt.Printf("      /llms.txt: none (%d); lidza gen llms writes one\n", llms.Status)
	}
}
