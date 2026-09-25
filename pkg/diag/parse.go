package diag

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// goPosRe matches "file.go:line:col: message", optionally prefixed by
// "vet: " as go vet prints type errors.
var goPosRe = regexp.MustCompile(`^(?:vet: )?(\S+?\.go):(\d+):(\d+): (.+)$`)

// parseGoText reads the plain-text errors the go tool prints on stderr:
// compile and type errors, one per line, under "# package" headers.
func parseGoText(root, tool string, out []byte) []Diagnostic {
	var diags []Diagnostic
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "go: downloading") || line == "too many errors" {
			continue
		}
		if m := goPosRe.FindStringSubmatch(line); m != nil {
			diags = append(diags, Diagnostic{
				Layer: "go", Tool: tool, Severity: "error",
				File: rel(root, m[1]), Line: atoi(m[2]), Column: atoi(m[3]), Message: m[4],
			})
			continue
		}
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
			// Continuation of the previous message (e.g. "have ... want ...").
			if n := len(diags); n > 0 {
				diags[n-1].Message += "\n" + strings.TrimSpace(line)
			}
			continue
		}
		diags = append(diags, Diagnostic{Layer: "go", Tool: tool, Severity: "error", Message: line})
	}
	return diags
}

// parseGoVetJSON reads `go vet -json` stdout: one JSON object per package,
// {"pkg": {"analyzer": [{"posn": "file:line:col", "message": "..."}]}}.
func parseGoVetJSON(root string, out []byte) []Diagnostic {
	var diags []Diagnostic
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkgs map[string]map[string][]struct {
			Posn    string `json:"posn"`
			Message string `json:"message"`
		}
		if err := dec.Decode(&pkgs); err != nil {
			break
		}
		for _, analyzers := range pkgs {
			for analyzer, findings := range analyzers {
				for _, f := range findings {
					d := Diagnostic{Layer: "go", Tool: "go vet", Severity: "warning", Code: analyzer, Message: f.Message}
					d.File, d.Line, d.Column = splitPos(root, f.Posn)
					diags = append(diags, d)
				}
			}
		}
	}
	return diags
}

// parseStaticcheck reads `staticcheck -f json`: one object per line. Go
// compile errors come back with code "compile" and the location inside the
// message, so those are re-parsed as text.
func parseStaticcheck(root string, out []byte) []Diagnostic {
	var diags []Diagnostic
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var f struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Location struct {
				File   string `json:"file"`
				Line   int    `json:"line"`
				Column int    `json:"column"`
			} `json:"location"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			continue
		}
		if f.Code == "compile" {
			diags = append(diags, parseGoText(root, "staticcheck", []byte(f.Message))...)
			continue
		}
		if f.Severity == "ignored" {
			continue
		}
		diags = append(diags, Diagnostic{
			Layer: "go", Tool: "staticcheck", Severity: f.Severity, Code: f.Code,
			File: rel(root, f.Location.File), Line: f.Location.Line, Column: f.Location.Column,
			Message: f.Message,
		})
	}
	return diags
}

// parseCargo reads `cargo check --message-format=json`. Paths in the
// messages are relative to the crate directory, cargoDir, which is itself
// relative to the project root.
func parseCargo(cargoDir string, out []byte) []Diagnostic {
	var diags []Diagnostic
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m struct {
			Reason  string `json:"reason"`
			Message struct {
				Level string `json:"level"`
				Code  *struct {
					Code string `json:"code"`
				} `json:"code"`
				Message string `json:"message"`
				Spans   []struct {
					File    string `json:"file_name"`
					Line    int    `json:"line_start"`
					Column  int    `json:"column_start"`
					Primary bool   `json:"is_primary"`
					Label   string `json:"label"`
				} `json:"spans"`
			} `json:"message"`
		}
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.Reason != "compiler-message" {
			continue
		}
		msg := m.Message
		if msg.Level != "error" && msg.Level != "warning" {
			continue
		}
		d := Diagnostic{Layer: "rust", Tool: "cargo check", Severity: msg.Level, Message: msg.Message}
		if msg.Code != nil {
			d.Code = msg.Code.Code
		}
		primary := -1
		for i, s := range msg.Spans {
			if s.Primary {
				primary = i
				break
			}
		}
		if primary < 0 {
			// Summaries such as "aborting due to 1 previous error" have no
			// span and carry nothing the per-finding messages do not.
			continue
		}
		s := msg.Spans[primary]
		d.File = filepath.ToSlash(filepath.Join(cargoDir, s.File))
		d.Line, d.Column = s.Line, s.Column
		if s.Label != "" {
			d.Message += ": " + s.Label
		}
		diags = append(diags, d)
	}
	return diags
}

// tscRe matches "src/x.ts(line,col): error TS1234: message".
var tscRe = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning) (TS\d+): (.*)$`)

// parseTSC reads `tsc --noEmit --pretty false`. Continuation lines are
// indented and belong to the previous finding.
func parseTSC(out []byte) []Diagnostic {
	var diags []Diagnostic
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m := tscRe.FindStringSubmatch(line); m != nil {
			diags = append(diags, Diagnostic{
				Layer: "frontend", Tool: "tsc", Severity: m[4],
				File: filepath.ToSlash(m[1]), Line: atoi(m[2]), Column: atoi(m[3]),
				Code: m[5], Message: m[6],
			})
			continue
		}
		if n := len(diags); n > 0 && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			diags[n-1].Message += "\n" + strings.TrimSpace(line)
		}
	}
	return diags
}

// parseESLint reads `eslint --format json`.
func parseESLint(root string, out []byte) []Diagnostic {
	var files []struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			RuleID   string `json:"ruleId"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &files); err != nil {
		return nil
	}
	var diags []Diagnostic
	for _, f := range files {
		for _, m := range f.Messages {
			sev := "warning"
			if m.Severity == 2 {
				sev = "error"
			}
			diags = append(diags, Diagnostic{
				Layer: "frontend", Tool: "eslint", Severity: sev, Code: m.RuleID,
				File: rel(root, f.FilePath), Line: m.Line, Column: m.Column, Message: m.Message,
			})
		}
	}
	return diags
}

// rel makes p relative to root with forward slashes. Paths the go tool
// prints as "./x.go" or absolute both end up as "x.go".
func rel(root, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
			p = r
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// splitPos splits "file:line:col" (column optional).
func splitPos(root, pos string) (string, int, int) {
	parts := strings.Split(pos, ":")
	switch len(parts) {
	case 3:
		return rel(root, parts[0]), atoi(parts[1]), atoi(parts[2])
	case 2:
		return rel(root, parts[0]), atoi(parts[1]), 0
	}
	return rel(root, pos), 0, 0
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// svelteRe matches svelte-check's machine output:
// 1790304146947 ERROR "src/App.svelte" 2:9 "message".
var svelteRe = regexp.MustCompile(`^\d+ (ERROR|WARNING) "([^"]+)" (\d+):(\d+) "(.*)"$`)

// parseSvelteCheck reads `svelte-check --output machine`.
func parseSvelteCheck(out []byte) []Diagnostic {
	var diags []Diagnostic
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := svelteRe.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r"))
		if m == nil {
			continue
		}
		d := Diagnostic{
			Layer: "frontend", Tool: "svelte-check", Severity: strings.ToLower(m[1]),
			File: filepath.ToSlash(m[2]), Line: atoi(m[3]), Column: atoi(m[4]), Message: m[5],
		}
		// The compiler reports accessibility problems as warnings; here
		// they are errors, as jsx-a11y's are in the react template.
		if strings.Contains(strings.ToLower(d.Message), "a11y") {
			d.Severity, d.Code = "error", "a11y"
		}
		diags = append(diags, d)
	}
	return diags
}
