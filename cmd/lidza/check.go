package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agim/lidza/pkg/diag"
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
	report := diag.Run(ctx, layers)

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
