// Package decisions keeps an app's decision log, docs/decisions.md: why
// the app is built the way it is (a pack added, Rust chosen for a module,
// a dependency taken, a schema tradeoff), one dated entry each, written
// by people and agents alike and read before working in those areas. A
// recipe says how this app does something; a decision says why.
package decisions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// File is the log, relative to the app root.
const File = "docs/decisions.md"

// Entry is one decision.
type Entry struct {
	Date    string `json:"date"`
	Title   string `json:"title"`
	Why     string `json:"why"`
	Touches string `json:"touches,omitempty"`
}

var header = `# Decisions

What shaped %s and why, one entry each, newest last: a pack added, Rust
chosen for a module, a dependency taken, a schema tradeoff, an
integration. Read this before working in those areas. Record yours in the
same commit as the change: ` + "`lidza decision add \"Title\" --why \"...\"`" + `
(MCP: ` + "`lidza_decision_add`" + `). A recipe in docs/lidza-guide.md says how
this app does something; a decision here says why it is done that way.
`

// Ensure writes the log with its header when the app has none.
func Ensure(dir, app string) (bool, error) {
	p := filepath.Join(dir, filepath.FromSlash(File))
	if _, err := os.Stat(p); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(p, []byte(fmt.Sprintf(header, app)), 0o644)
}

// Add appends a dated entry. The title and the why are required; touches
// names the files, packs or tables it concerns.
func Add(dir, title, why, touches string) (Entry, error) {
	title, why, touches = strings.TrimSpace(title), strings.TrimSpace(why), strings.TrimSpace(touches)
	if title == "" {
		return Entry{}, errors.New("decision: a title is required")
	}
	if why == "" {
		return Entry{}, errors.New("decision: say why (--why)")
	}
	if _, err := Ensure(dir, filepath.Base(dir)); err != nil {
		return Entry{}, err
	}
	e := Entry{Date: time.Now().UTC().Format("2006-01-02"), Title: title, Why: why, Touches: touches}
	p := filepath.Join(dir, filepath.FromSlash(File))
	data, err := os.ReadFile(p)
	if err != nil {
		return Entry{}, err
	}
	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "\n" + format(e)
	return e, os.WriteFile(p, []byte(text), 0o644)
}

func format(e Entry) string {
	s := fmt.Sprintf("## %s: %s\n\nWhy: %s\n", e.Date, e.Title, e.Why)
	if e.Touches != "" {
		s += "\nTouches: " + e.Touches + "\n"
	}
	return s
}

var heading = regexp.MustCompile(`(?m)^## (\d{4}-\d{2}-\d{2}): (.+)$`)

// Load reads the entries, oldest first; no log is no entries.
func Load(dir string) ([]Entry, error) {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := string(data)
	locs := heading.FindAllStringSubmatchIndex(text, -1)
	var out []Entry
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		e := Entry{Date: text[loc[2]:loc[3]], Title: strings.TrimSpace(text[loc[4]:loc[5]])}
		for _, line := range strings.Split(text[loc[1]:end], "\n") {
			switch {
			case strings.HasPrefix(line, "Why: "):
				e.Why = strings.TrimSpace(strings.TrimPrefix(line, "Why: "))
			case strings.HasPrefix(line, "Touches: "):
				e.Touches = strings.TrimSpace(strings.TrimPrefix(line, "Touches: "))
			}
		}
		out = append(out, e)
	}
	return out, nil
}
