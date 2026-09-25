// Package diag runs the checkers for every layer of a Līdza project (Go,
// Rust, frontend) and merges their output into one list of diagnostics with
// a layer, file, line and message each. `lidza check --json` prints it.
package diag

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Diagnostic is one finding from one tool.
type Diagnostic struct {
	// Layer is "go", "rust" or "frontend".
	Layer string `json:"layer"`
	// Tool is the program that reported it: "go vet", "staticcheck",
	// "cargo check", "tsc".
	Tool string `json:"tool"`
	// Severity is "error" or "warning".
	Severity string `json:"severity"`
	// File is relative to the project root with forward slashes. Empty when
	// the tool gave no location.
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	// Code is the tool's identifier for the finding (SA4006, E0308, TS2322).
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// ToolRun records whether and how a tool ran.
type ToolRun struct {
	Tool  string `json:"tool"`
	Layer string `json:"layer"`
	// Skipped is true when the tool or its inputs are missing; Reason says why.
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Failed is true when the tool could not run to completion (as opposed
	// to running and reporting findings); Reason has the error.
	Failed     bool  `json:"failed,omitempty"`
	DurationMS int64 `json:"duration_ms"`
}

// Report is the output of Run.
type Report struct {
	// Status is "ok" when no diagnostic has severity "error".
	Status      string       `json:"status"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Tools       []ToolRun    `json:"tools"`
}

// Errors counts the diagnostics with severity "error".
func (r Report) Errors() int {
	n := 0
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			n++
		}
	}
	return n
}

// Layers describes what a project contains, found by Detect.
type Layers struct {
	// Dir is the project root; every path is reported relative to it.
	Dir string
	// Go is true when Dir has a go.mod.
	Go bool
	// CargoDir is the directory with the Cargo.toml, relative to Dir
	// ("." or "core"); empty when there is none.
	CargoDir string
	// TSConfig is true when Dir has a tsconfig.json.
	TSConfig bool
	// NodeModules is true when Dir has node_modules.
	NodeModules bool
}

// Detect looks at dir and reports which layers exist.
func Detect(dir string) Layers {
	l := Layers{Dir: dir}
	l.Go = fileExists(filepath.Join(dir, "go.mod"))
	for _, c := range []string{".", "core"} {
		if fileExists(filepath.Join(dir, c, "Cargo.toml")) {
			l.CargoDir = c
			break
		}
	}
	l.TSConfig = fileExists(filepath.Join(dir, "tsconfig.json"))
	l.NodeModules = fileExists(filepath.Join(dir, "node_modules"))
	return l
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Run executes every applicable checker, in parallel, and merges the results.
func Run(ctx context.Context, l Layers) Report {
	type job struct {
		tool, layer string
		run         func(context.Context) ([]Diagnostic, ToolRun)
	}
	jobs := []job{
		{"go vet", "go", func(ctx context.Context) ([]Diagnostic, ToolRun) { return runGoVet(ctx, l) }},
		{"staticcheck", "go", func(ctx context.Context) ([]Diagnostic, ToolRun) { return runStaticcheck(ctx, l) }},
		{"cargo check", "rust", func(ctx context.Context) ([]Diagnostic, ToolRun) { return runCargo(ctx, l) }},
		{"tsc", "frontend", func(ctx context.Context) ([]Diagnostic, ToolRun) { return runTSC(ctx, l) }},
		{"svelte-check", "frontend", func(ctx context.Context) ([]Diagnostic, ToolRun) { return runSvelteCheck(ctx, l) }},
		{"eslint", "frontend", func(ctx context.Context) ([]Diagnostic, ToolRun) { return runESLint(ctx, l) }},
		{"lidza rules", "go", func(ctx context.Context) ([]Diagnostic, ToolRun) {
			if !l.Go {
				return nil, skip("no go.mod")
			}
			return Rules(ctx, l.Dir), ToolRun{}
		}},
		{"lidza rules", "frontend", func(ctx context.Context) ([]Diagnostic, ToolRun) {
			if !l.TSConfig {
				return nil, skip("no tsconfig.json")
			}
			return WebRules(l.Dir), ToolRun{}
		}},
	}
	diags := make([][]Diagnostic, len(jobs))
	runs := make([]ToolRun, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			diags[i], runs[i] = j.run(ctx)
			runs[i].Tool, runs[i].Layer = j.tool, j.layer
			runs[i].DurationMS = time.Since(start).Milliseconds()
		}()
	}
	wg.Wait()

	var all []Diagnostic
	for _, d := range diags {
		all = append(all, d...)
	}
	all = dedupe(all)
	all = dropSuperseded(all)
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	if all == nil {
		all = []Diagnostic{}
	}
	r := Report{Status: "ok", Diagnostics: all, Tools: runs}
	if r.Errors() > 0 {
		r.Status = "error"
	}
	return r
}

// dropSuperseded removes the go tool's "no required module provides
// package X; to add it: go get X" for every X that L004 reports as
// nonexistent: the advice is wrong there, and the rule's message says what
// to do instead.
func dropSuperseded(in []Diagnostic) []Diagnostic {
	var missing []string
	for _, d := range in {
		if d.Code == "L004" && d.Layer == "go" {
			if f := strings.Fields(d.Message); len(f) > 1 && f[0] == "package" {
				missing = append(missing, "provides package "+f[1])
			}
		}
	}
	if len(missing) == 0 {
		return in
	}
	out := in[:0]
	for _, d := range in {
		drop := false
		for _, m := range missing {
			drop = drop || (d.Code == "" && strings.Contains(d.Message, m))
		}
		if !drop {
			out = append(out, d)
		}
	}
	return out
}

// dedupe drops findings that two tools reported identically, which happens
// for Go compile errors (go vet and staticcheck both surface them).
func dedupe(in []Diagnostic) []Diagnostic {
	type key struct {
		file    string
		line    int
		message string
	}
	seen := map[key]bool{}
	var out []Diagnostic
	for _, d := range in {
		k := key{d.File, d.Line, d.Message}
		if d.File != "" && seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	return out
}

// command runs a tool in dir and returns stdout, stderr and the exit error.
// A non-zero exit is normal for checkers with findings, so it is returned
// rather than treated as a failure.
func command(ctx context.Context, dir string, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

func have(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}

func skip(reason string) ToolRun { return ToolRun{Skipped: true, Reason: reason} }

func runGoVet(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !l.Go {
		return nil, skip("no go.mod")
	}
	if !have("go") {
		return nil, skip("go not installed")
	}
	stdout, stderr, err := command(ctx, l.Dir, "go", "vet", "-json", "./...")
	diags := append(parseGoText(l.Dir, "go vet", stderr), parseGoVetJSON(l.Dir, stdout)...)
	if err != nil && len(diags) == 0 {
		return nil, ToolRun{Failed: true, Reason: firstLine(stderr, err)}
	}
	return diags, ToolRun{}
}

func runStaticcheck(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !l.Go {
		return nil, skip("no go.mod")
	}
	if !have("staticcheck") {
		return nil, skip("staticcheck not installed")
	}
	stdout, stderr, err := command(ctx, l.Dir, "staticcheck", "-f", "json", "./...")
	diags := parseStaticcheck(l.Dir, stdout)
	if err != nil && len(diags) == 0 && len(stdout) == 0 {
		return nil, ToolRun{Failed: true, Reason: firstLine(stderr, err)}
	}
	return diags, ToolRun{}
}

func runCargo(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if l.CargoDir == "" {
		return nil, skip("no Cargo.toml")
	}
	if !have("cargo") {
		return nil, skip("cargo not installed")
	}
	dir := filepath.Join(l.Dir, l.CargoDir)
	stdout, stderr, err := command(ctx, dir, "cargo", "check", "--quiet", "--message-format=json")
	diags := parseCargo(l.CargoDir, stdout)
	if err != nil && len(diags) == 0 {
		return nil, ToolRun{Failed: true, Reason: firstLine(stderr, err)}
	}
	return diags, ToolRun{}
}

func runTSC(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !l.TSConfig {
		return nil, skip("no tsconfig.json")
	}
	if !l.NodeModules {
		return nil, skip("node_modules missing; run npm install")
	}
	tsc := filepath.Join(l.Dir, "node_modules", ".bin", "tsc")
	if !fileExists(tsc) {
		return nil, skip("typescript is not a dependency")
	}
	stdout, stderr, err := command(ctx, l.Dir, tsc, "--noEmit", "--pretty", "false")
	diags := parseTSC(stdout)
	if err != nil && len(diags) == 0 {
		return nil, ToolRun{Failed: true, Reason: firstLine(append(stdout, stderr...), err)}
	}
	return diags, ToolRun{}
}

// runSvelteCheck covers .svelte files, which tsc does not read.
func runSvelteCheck(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !l.TSConfig {
		return nil, skip("no tsconfig.json")
	}
	bin := filepath.Join(l.Dir, "node_modules", ".bin", "svelte-check")
	if !fileExists(bin) {
		return nil, skip("not a svelte project")
	}
	stdout, stderr, err := command(ctx, l.Dir, bin, "--output", "machine", "--tsconfig", "./tsconfig.json")
	diags := parseSvelteCheck(stdout)
	if err != nil && len(diags) == 0 && !bytes.Contains(stdout, []byte("COMPLETED")) {
		return nil, ToolRun{Failed: true, Reason: firstLine(append(stdout, stderr...), err)}
	}
	return diags, ToolRun{}
}

// runESLint covers the frontend lint rules, accessibility among them.
func runESLint(ctx context.Context, l Layers) ([]Diagnostic, ToolRun) {
	if !l.NodeModules {
		return nil, skip("node_modules missing")
	}
	bin := filepath.Join(l.Dir, "node_modules", ".bin", "eslint")
	if !fileExists(bin) || !fileExists(filepath.Join(l.Dir, "eslint.config.js")) {
		return nil, skip("no eslint configuration")
	}
	stdout, stderr, err := command(ctx, l.Dir, bin, "--format", "json", "src")
	diags := parseESLint(l.Dir, stdout)
	if err != nil && len(diags) == 0 && len(bytes.TrimSpace(stdout)) == 0 {
		return nil, ToolRun{Failed: true, Reason: firstLine(stderr, err)}
	}
	return diags, ToolRun{}
}

func firstLine(out []byte, err error) string {
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	if s := string(bytes.TrimSpace(out)); s != "" {
		return s
	}
	return err.Error()
}
