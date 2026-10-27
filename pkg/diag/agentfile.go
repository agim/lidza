package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/brief"
)

// agentFileBudget is the most an agent instruction file may hold before
// L024 warns: about 4,000 tokens, read at the start of every session,
// before any task.
const agentFileBudget = 16 * 1024

// agentFileSize is L024: an agent instruction file (AGENTS.md, or a
// CLAUDE.md or GEMINI.md that is not the one-line import) larger than
// agentFileBudget, or one still carrying team notes under its Team notes
// heading. Every session reads these files whole, so what grows there
// costs every task; lasting facts belong in docs/notes.md (lidza note
// add; lidza gen moves old notes there) and area documentation in docs/,
// found on demand.
func agentFileSize(root string) []Diagnostic {
	var out []Diagnostic
	for _, name := range brief.AgentFiles(root) {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		s := string(data)
		if notes := notesInSection(s); notes > 0 {
			out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Code: "L024", Severity: "warning", File: name, Line: 1, Column: 1,
				Message: fmt.Sprintf("%s holds %d team notes under its Team notes heading, read whole at the start of every session: run lidza gen to move them to %s", name, notes, brief.NotesFile)})
		}
		if len(data) > agentFileBudget {
			out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Code: "L024", Severity: "warning", File: name, Line: 1, Column: 1,
				Message: fmt.Sprintf("%s is %d KB (about %d tokens), read whole at the start of every session: keep it under %d KB; lasting facts go in %s (lidza note add), why things are the way they are in docs/decisions.md (lidza decision add), and an area's documentation in docs/<area>.md, named from here in one line", name, len(data)/1024, len(data)/4, agentFileBudget/1024, brief.NotesFile)})
		}
	}
	return out
}

// notesInSection counts the dated note lines under the Team notes
// heading.
func notesInSection(s string) int {
	k := strings.Index(s, "## Team notes")
	if k < 0 {
		return 0
	}
	body := s[k:]
	if next := strings.Index(body[len("## Team notes"):], "\n## "); next >= 0 {
		body = body[:len("## Team notes")+next]
	}
	n := 0
	for _, l := range strings.Split(body, "\n") {
		if len(l) > 14 && strings.HasPrefix(l, "- ") && l[6] == '-' && l[9] == '-' && l[12] == ':' {
			n++
		}
	}
	return n
}
