package diag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The vulnerability scanners read a database over the network: offline,
// they fail to run and the check goes on without them. Their findings
// are warnings, so a newly published advisory fails --strict, not every
// commit, and the deadline bounds a slow database fetch.
const securityTimeout = 3 * time.Minute

// runGovulncheck reports the known vulnerabilities the app's code
// reaches (govulncheck's symbol-level findings), at the app's call site.
func runGovulncheck(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !l.Go {
		return nil, skip("no go.mod")
	}
	if !have("govulncheck") {
		return nil, skip("govulncheck not installed (sh install.sh installs it)")
	}
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	stdout, stderr, err := command(ctx, l.Dir, "govulncheck", "-format", "json", "./...")
	if err != nil {
		// With -format json it exits 0 on findings: an error is a scan
		// that did not run (no database, a module that does not load).
		return nil, ToolRun{Failed: true, Reason: firstLine(append(stderr, stdout...), err)}
	}
	return parseGovulncheck(l.Dir, stdout), ToolRun{}
}

// parseGovulncheck reads `govulncheck -format json`: a stream of
// objects, the advisories ("osv") and the findings. A finding whose
// trace starts in a function is one the code calls; module- and
// package-level findings are not reported.
func parseGovulncheck(root string, out []byte) []Diagnostic {
	type frame struct {
		Module   string `json:"module"`
		Version  string `json:"version"`
		Package  string `json:"package"`
		Receiver string `json:"receiver"`
		Function string `json:"function"`
		Position *struct {
			Filename string `json:"filename"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
		} `json:"position"`
	}
	summaries := map[string]string{}
	type finding struct {
		OSV          string  `json:"osv"`
		FixedVersion string  `json:"fixed_version"`
		Trace        []frame `json:"trace"`
	}
	var findings []finding
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var msg struct {
			OSV *struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
			} `json:"osv"`
			Finding *finding `json:"finding"`
		}
		if err := dec.Decode(&msg); err != nil {
			break
		}
		if msg.OSV != nil {
			summaries[msg.OSV.ID] = msg.OSV.Summary
		}
		if msg.Finding != nil {
			findings = append(findings, *msg.Finding)
		}
	}
	var diags []Diagnostic
	for _, f := range findings {
		if len(f.Trace) == 0 || f.Trace[0].Function == "" {
			continue
		}
		vuln, entry := f.Trace[0], f.Trace[len(f.Trace)-1]
		symbol := vuln.Function
		if vuln.Receiver != "" {
			symbol = vuln.Receiver + "." + symbol
		}
		symbol = vuln.Package + "." + symbol
		msg := f.OSV
		if s := summaries[f.OSV]; s != "" {
			msg += " (" + s + ")"
		}
		msg += ": the code calls " + symbol
		switch {
		case vuln.Module == "stdlib" && f.FixedVersion != "":
			msg += "; fixed in Go " + strings.TrimPrefix(f.FixedVersion, "v") + ": upgrade Go"
		case f.FixedVersion != "":
			msg += fmt.Sprintf("; fixed in %s %s: go get %s@%s", vuln.Module, f.FixedVersion, vuln.Module, f.FixedVersion)
		default:
			msg += "; no fixed version yet"
		}
		d := Diagnostic{Layer: "go", Tool: "govulncheck", Severity: "warning", Code: f.OSV, Message: msg}
		if p := entry.Position; p != nil && len(f.Trace) > 1 {
			d.File, d.Line, d.Column = rel(root, p.Filename), p.Line, p.Column
		}
		diags = append(diags, d)
	}
	return diags
}

// runCargoDeny reports RustSec advisories (vulnerable, unsound or
// yanked crates) in the app's Rust crates. Licenses and bans are the
// app's own policy and not checked.
func runCargoDeny(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if l.CargoDir == "" && len(l.PackCrates) == 0 {
		return nil, skip("no Cargo.toml")
	}
	if !have("cargo-deny") {
		return nil, skip("cargo-deny not installed (sh install.sh installs it)")
	}
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	crates := l.PackCrates
	if l.CargoDir != "" {
		crates = append([]string{l.CargoDir}, crates...)
	}
	var diags []Diagnostic
	for _, c := range crates {
		manifest := filepath.Join(l.Dir, c, "Cargo.toml")
		_, stderr, err := command(ctx, l.Dir, "cargo", "deny", "--manifest-path", manifest, "--format", "json", "check", "advisories")
		found, ok := parseCargoDeny(c, stderr)
		if !ok {
			return diags, ToolRun{Failed: true, Reason: c + ": " + firstLine(lastLines(stderr), err)}
		}
		diags = append(diags, found...)
	}
	return diags, ToolRun{}
}

// parseCargoDeny reads `cargo deny --format json check`'s stderr: one
// object per line, the findings as "diagnostic" and the totals as
// "summary". ok is false without a summary (it failed before checking).
func parseCargoDeny(crateDir string, out []byte) (diags []Diagnostic, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m struct {
			Type   string `json:"type"`
			Fields struct {
				Severity string `json:"severity"`
				Code     string `json:"code"`
				Message  string `json:"message"`
				Advisory *struct {
					ID      string `json:"id"`
					Package string `json:"package"`
				} `json:"advisory"`
				Graphs []struct {
					Krate struct {
						Name    string `json:"name"`
						Version string `json:"version"`
					} `json:"Krate"`
				} `json:"graphs"`
				Notes []string `json:"notes"`
			} `json:"fields"`
		}
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch m.Type {
		case "summary":
			ok = true
			continue
		case "diagnostic":
		default:
			continue
		}
		f := m.Fields
		if f.Advisory == nil || (f.Severity != "error" && f.Severity != "warning") {
			continue
		}
		krate := f.Advisory.Package
		if len(f.Graphs) > 0 {
			krate = f.Graphs[0].Krate.Name + " " + f.Graphs[0].Krate.Version
		}
		msg := fmt.Sprintf("%s (%s): %s %s", f.Advisory.ID, f.Message, krate, f.Code)
		for _, n := range f.Notes {
			if s, found := strings.CutPrefix(n, "Solution: "); found {
				msg += "; " + s
			}
		}
		diags = append(diags, Diagnostic{Layer: "rust", Tool: "cargo deny", Severity: "warning", File: rel(".", filepath.Join(crateDir, "Cargo.lock")), Code: f.Advisory.ID, Message: msg})
	}
	return diags, ok
}

// lastLines keeps the end of a long stderr, where a failure is said.
func lastLines(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return b[i+1:]
	}
	return b
}

// runNpmAudit reports high and critical advisories in the packages the
// frontend ships (production dependencies; the dev toolchain is left
// out, it never reaches a browser).
func runNpmAudit(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !fileExists(filepath.Join(l.Dir, "package-lock.json")) {
		return nil, skip("no package-lock.json")
	}
	if !have("npm") {
		return nil, skip("npm not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	stdout, stderr, err := command(ctx, l.Dir, "npm", "audit", "--omit=dev", "--json")
	diags, ok := parseNpmAudit(stdout)
	if !ok {
		return nil, ToolRun{Failed: true, Reason: firstLine(append(stdout, stderr...), err)}
	}
	return diags, ToolRun{}
}

// parseNpmAudit reads `npm audit --json` (report version 2): one entry
// per vulnerable package, its advisories in "via" (objects; a string
// names the dependency it is vulnerable through). ok is false when the
// audit did not run (npm reports an "error").
func parseNpmAudit(out []byte) (diags []Diagnostic, ok bool) {
	var r struct {
		Error           json.RawMessage `json:"error"`
		Vulnerabilities map[string]struct {
			Name     string            `json:"name"`
			Severity string            `json:"severity"`
			Via      []json.RawMessage `json:"via"`
			Fix      json.RawMessage   `json:"fixAvailable"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(out, &r); err != nil || len(r.Error) > 0 || r.Vulnerabilities == nil {
		return nil, false
	}
	names := make([]string, 0, len(r.Vulnerabilities))
	for n := range r.Vulnerabilities {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := r.Vulnerabilities[n]
		if v.Severity != "high" && v.Severity != "critical" {
			continue
		}
		var titles, through []string
		for _, raw := range v.Via {
			var adv struct {
				Title    string `json:"title"`
				URL      string `json:"url"`
				Severity string `json:"severity"`
			}
			var dep string
			switch {
			case json.Unmarshal(raw, &dep) == nil:
				through = append(through, dep)
			case json.Unmarshal(raw, &adv) == nil && (adv.Severity == "high" || adv.Severity == "critical"):
				titles = append(titles, adv.Title+" ("+adv.URL+")")
			}
		}
		msg := v.Severity + ": " + v.Name
		if len(titles) > 0 {
			msg += ": " + strings.Join(titles, "; ")
		}
		if len(through) > 0 {
			msg += "; through " + strings.Join(through, ", ")
		}
		var fix struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Major   bool   `json:"isSemVerMajor"`
		}
		var fixBool bool
		switch {
		case json.Unmarshal(v.Fix, &fix) == nil && fix.Name != "":
			msg += fmt.Sprintf("; fixed by %s@%s", fix.Name, fix.Version)
			if fix.Major {
				msg += " (a major upgrade: npm audit fix --force)"
			} else {
				msg += ": npm audit fix"
			}
		case json.Unmarshal(v.Fix, &fixBool) == nil && fixBool:
			msg += "; npm audit fix"
		default:
			msg += "; no fix available yet"
		}
		diags = append(diags, Diagnostic{Layer: "frontend", Tool: "npm audit", Severity: "warning", File: "package.json", Message: msg})
	}
	return diags, true
}
