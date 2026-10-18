package diag

import (
	"os"
	"strings"
	"testing"
)

func TestParseGovulncheck(t *testing.T) {
	out, err := os.ReadFile("testdata/govulncheck.json")
	if err != nil {
		t.Fatal(err)
	}
	diags := parseGovulncheck("/app", out)
	// Module- and package-level findings are not reported: only the one
	// call the code makes.
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Tool != "govulncheck" || d.Severity != "warning" || d.Code != "GO-2021-0113" {
		t.Errorf("got %+v", d)
	}
	if d.File != "internal/lang/lang.go" || d.Line != 6 {
		t.Errorf("located at %s:%d, want the app's call site internal/lang/lang.go:6", d.File, d.Line)
	}
	for _, want := range []string{"golang.org/x/text/language.Parse", "go get golang.org/x/text@v0.3.7"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q lacks %q", d.Message, want)
		}
	}
}

func TestParseCargoDeny(t *testing.T) {
	out, err := os.ReadFile("testdata/cargo-deny.json")
	if err != nil {
		t.Fatal(err)
	}
	diags, ok := parseCargoDeny("packs/stats/rust", out)
	if !ok || len(diags) != 1 {
		t.Fatalf("ok %v, got %+v", ok, diags)
	}
	d := diags[0]
	if d.Layer != "rust" || d.Code != "RUSTSEC-2021-0003" || d.File != "packs/stats/rust/Cargo.lock" {
		t.Errorf("got %+v", d)
	}
	for _, want := range []string{"smallvec 1.6.0", "cargo update -p smallvec"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q lacks %q", d.Message, want)
		}
	}
	// No summary line: cargo deny did not get to check.
	if _, ok := parseCargoDeny(".", []byte(`{"type":"log","fields":{"level":"ERROR","message":"failed to fetch advisory database"}}`)); ok {
		t.Error("a run without a summary parsed as checked")
	}
}

func TestParseNpmAudit(t *testing.T) {
	out, err := os.ReadFile("testdata/npm-audit.json")
	if err != nil {
		t.Fatal(err)
	}
	diags, ok := parseNpmAudit(out)
	// The moderate advisory stays below the bar.
	if !ok || len(diags) != 1 {
		t.Fatalf("ok %v, got %+v", ok, diags)
	}
	d := diags[0]
	if d.Layer != "frontend" || d.File != "package.json" {
		t.Errorf("got %+v", d)
	}
	for _, want := range []string{"high: lodash", "Command Injection in lodash", "fixed by lodash@4.18.1: npm audit fix"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q lacks %q", d.Message, want)
		}
	}
	if strings.Contains(d.Message, "ReDoS") {
		t.Errorf("message %q lists a moderate advisory", d.Message)
	}
	// Offline, npm prints an error object instead of a report.
	if _, ok := parseNpmAudit([]byte(`{"message":"request failed","error":{"summary":"","detail":""}}`)); ok {
		t.Error("an npm error parsed as an audit")
	}
	// A clean audit is a run with nothing to report.
	if diags, ok := parseNpmAudit([]byte(`{"auditReportVersion":2,"vulnerabilities":{}}`)); !ok || len(diags) != 0 {
		t.Errorf("clean audit: ok %v, %+v", ok, diags)
	}
}
