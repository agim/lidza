package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/diag"
	"github.com/agim/lidza/pkg/engine"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

// errCheckFailed is returned by `lidza check` when there are errors; the
// findings themselves were already printed.
var errCheckFailed = errors.New("check failed")

func runCheck(ctx context.Context, args []string) error {
	fs := flags("check")
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON report instead of text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	layers := diag.Detect(abs)
	if !layers.Go && layers.CargoDir == "" && !layers.TSConfig {
		return fmt.Errorf("nothing to check in %s: no go.mod, Cargo.toml or tsconfig.json", abs)
	}
	// Generated code must be current before the checkers see it: the
	// schema package for go vet, the client for tsc. A generator that
	// fails (a schema.lidza that does not parse, a pack that does not
	// build) is a diagnostic like any other, not a note on stderr.
	var generateErr error
	if _, cfg, err := loadProject(abs); err == nil {
		generateErr = generateAll(abs, cfg, io.Discard)
	}
	report := diag.Run(ctx, layers)
	if generateErr != nil {
		report.Diagnostics = append([]diag.Diagnostic{generateDiagnostic(generateErr)}, report.Diagnostics...)
		report.Status = "error"
	}
	if _, cfg, err := loadProject(abs); err == nil && cfg != nil {
		report.Diagnostics = append(report.Diagnostics, packDiagnostics(ctx, abs, cfg)...)
		if report.Errors() > 0 {
			report.Status = "error"
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printReport(report)
	}
	if report.Status != "ok" {
		return errCheckFailed
	}
	return nil
}

// generateDiagnostic turns a generator error into a diagnostic. Schema
// errors carry "schema.lidza: line N: message"; the file and line are
// lifted out so an agent lands on the spot.
func generateDiagnostic(err error) diag.Diagnostic {
	d := diag.Diagnostic{Layer: "schema", Tool: "lidza gen", Severity: "error", Message: err.Error()}
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, schema.FileName+": "); ok {
		d.File = schema.FileName
		if lineText, after, ok := strings.Cut(rest, ": "); ok {
			if n, err := strconv.Atoi(strings.TrimPrefix(lineText, "line ")); err == nil {
				d.Line = n
				d.Message = after
			}
		}
	}
	return d
}

func printReport(r diag.Report) {
	for _, d := range r.Diagnostics {
		loc := d.File
		if d.Line > 0 {
			loc = fmt.Sprintf("%s:%d:%d", d.File, d.Line, d.Column)
		}
		code := ""
		if d.Code != "" {
			code = " [" + d.Code + "]"
		}
		fmt.Printf("%s: %s %s%s: %s\n", loc, d.Layer, d.Severity, code, d.Message)
	}
	for _, t := range r.Tools {
		switch {
		case t.Failed:
			fmt.Printf("%s: failed to run: %s\n", t.Tool, t.Reason)
		case t.Skipped:
			fmt.Printf("%s: skipped (%s)\n", t.Tool, t.Reason)
		}
	}
	fmt.Printf("%s: %d error(s), %d diagnostic(s)\n", r.Status, r.Errors(), len(r.Diagnostics))
}

// packDiagnostics validates every enabled pack's manifest against the
// schema and the built module's exports.
func packDiagnostics(ctx context.Context, dir string, cfg *config.Config) []diag.Diagnostic {
	var out []diag.Diagnostic
	s, _ := schema.Load(dir)
	for _, name := range cfg.Packs {
		if pack.IsOfficialGo(name) {
			if _, ok := pack.FindOfficial(name); !ok {
				out = append(out, diag.Diagnostic{Layer: "pack", Tool: "lidza", Severity: "error", File: "lidza.json", Message: "unknown official pack " + name})
			}
			continue
		}
		m, err := pack.Load(dir, name)
		if err != nil {
			out = append(out, diag.Diagnostic{Layer: "pack", Tool: "lidza", Severity: "error", File: filepath.ToSlash(filepath.Join(pack.Dir, name, pack.ManifestFile)), Message: err.Error()})
			continue
		}
		var exports []string
		if wasm, err := os.ReadFile(filepath.Join(dir, m.WasmFile())); err == nil {
			if mod, err := engine.Compile(ctx, wasm, engine.Options{}); err == nil {
				exports = mod.Exports()
				mod.Close(ctx)
			} else {
				out = append(out, diag.Diagnostic{Layer: "pack", Tool: "lidza", Severity: "error", File: filepath.ToSlash(m.WasmFile()), Message: err.Error()})
			}
		}
		for _, p := range m.Validate(s, exports) {
			out = append(out, diag.Diagnostic{Layer: "pack", Tool: "lidza", Severity: "error", File: m.Path, Message: p.Message})
		}
	}
	return out
}
