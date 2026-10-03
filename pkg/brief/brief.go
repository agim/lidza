// Package brief is an app's brief: what it is for, who owns the data, how
// it looks, which services it uses, where it runs and how agents work on
// it, answered by the people who own the app. It lives in docs/brief.md,
// in git, so every developer and every agent reads the same thing; the
// kickoff interview (`lidza brief`, or the MCP tools lidza_brief and
// lidza_brief_answer) fills it with suggestions or free answers. An
// answer also lands where it acts: a decision in docs/decisions.md, the
// working agreements in the agent files, the palette in the design
// tokens, a seeded app recipe.
package brief

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/recipes"
)

// File is the brief, relative to the project root.
const File = "docs/brief.md"

// open is what an unanswered question shows; skipped, one the team chose
// not to answer (it is not asked again, and L015 does not count it).
const (
	open    = "_Open._"
	skipped = "_Skipped._"
)

// Brief is the file's content: the app's name, the answers by question
// id, and the free notes at the end.
type Brief struct {
	App     string
	Answers map[string]string
	// Skipped are the questions the team chose not to answer.
	Skipped map[string]bool
	Notes   string
}

var (
	titleRe    = regexp.MustCompile(`(?m)^# Brief: (.+)$`)
	questionRe = regexp.MustCompile(`(?m)^### .*<!-- brief:([a-z_]+) -->[ \t]*$`)
)

// Ensure writes an empty brief when the app has none; it reports whether
// it did.
func Ensure(dir, app string) (bool, error) {
	p := filepath.Join(dir, filepath.FromSlash(File))
	if _, err := os.Stat(p); err == nil {
		return false, nil
	}
	return true, Save(dir, Brief{App: app, Answers: map[string]string{}, Skipped: map[string]bool{}})
}

// Load reads the brief; an app without one gets an empty Brief and
// os.ErrNotExist.
func Load(dir string) (Brief, error) {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File)))
	if err != nil {
		return Brief{Answers: map[string]string{}, Skipped: map[string]bool{}}, err
	}
	s := string(data)
	b := Brief{Answers: map[string]string{}, Skipped: map[string]bool{}}
	if m := titleRe.FindStringSubmatch(s); m != nil {
		b.App = strings.TrimSpace(m[1])
	}
	locs := questionRe.FindAllStringSubmatchIndex(s, -1)
	for i, loc := range locs {
		id := s[loc[2]:loc[3]]
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := s[loc[1]:end]
		// The answer stops at the next heading (a section, or Notes).
		if k := regexp.MustCompile(`(?m)^## `).FindStringIndex(body); k != nil {
			body = body[:k[0]]
		}
		body = strings.TrimSpace(body)
		switch body {
		case "", open:
		case skipped:
			b.Skipped[id] = true
		default:
			b.Answers[id] = body
		}
	}
	if k := strings.Index(s, "\n## Notes\n"); k >= 0 {
		b.Notes = strings.TrimSpace(s[k+len("\n## Notes\n"):])
	}
	return b, nil
}

// Save writes the brief: every question in order, its answer or "Open".
func Save(dir string, b Brief) error {
	var out strings.Builder
	fmt.Fprintf(&out, "# Brief: %s\n\n", b.App)
	out.WriteString("What this app is for, who owns its data, how it looks, what it uses,\n" +
		"where it runs and how agents work on it, in the words of the people who\n" +
		"own it. Agents read it before building anything. The kickoff interview\n" +
		"fills it: `lidza brief` in a terminal, or the recipe \"Start with the\n" +
		"brief\" in an agent session (MCP `lidza_brief`, `lidza_brief_answer`).\n" +
		"Change an answer the same way, or edit it here.\n")
	for _, section := range Sections {
		fmt.Fprintf(&out, "\n## %s\n", section)
		for _, q := range Questions {
			if q.Section != section {
				continue
			}
			answer := b.Answers[q.ID]
			switch {
			case answer != "":
			case b.Skipped[q.ID]:
				answer = skipped
			default:
				answer = open
			}
			fmt.Fprintf(&out, "\n### %s <!-- brief:%s -->\n\n%s\n", q.Ask, q.ID, answer)
		}
	}
	out.WriteString("\n## Notes\n\n")
	if b.Notes != "" {
		out.WriteString(b.Notes + "\n")
	} else {
		out.WriteString("Anything else the team should know that no question above asks.\n")
	}
	p := filepath.Join(dir, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(out.String()), 0o644)
}

// Open lists the questions without an answer that were not skipped, in
// interview order: the ones an interview asks.
func (b Brief) Open() []Question {
	var out []Question
	for _, q := range Questions {
		if (b.Answers[q.ID] == "" || Garbled(q, b.Answers[q.ID])) && !b.Skipped[q.ID] {
			out = append(out, q)
		}
	}
	return out
}

// OpenRequired lists the required questions without an answer.
func (b Brief) OpenRequired() []Question {
	var out []Question
	for _, q := range b.Open() {
		if q.Required {
			out = append(out, q)
		}
	}
	return out
}

// Result is what an answer changed.
type Result struct {
	ID       string   `json:"id"`
	Answer   string   `json:"answer"`
	Files    []string `json:"files"`
	Decision string   `json:"decision,omitempty"`
	Recipe   string   `json:"recipe,omitempty"`
	// Manual lists what the agent applies by hand (a stylesheet without
	// tokens, an admin theme the app wrote itself).
	Manual []string `json:"manual,omitempty"`
	// Open counts the questions still without an answer.
	Open int `json:"open"`
}

// Answer records the answer to a question and applies it: a decision, the
// agent files' working agreements, the palette, a seeded recipe. An empty
// answer clears the question.
func Answer(dir, app, id, answer string) (Result, error) {
	q, ok := Find(id)
	if !ok {
		return Result{}, fmt.Errorf("brief: no question %q (lidza brief --list shows them)", id)
	}
	answer = Clean(q, answer)
	b, err := Load(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	if b.App == "" {
		b.App = app
	}
	if b.Skipped == nil {
		b.Skipped = map[string]bool{}
	}
	previous := b.Answers[id]
	delete(b.Skipped, id)
	if answer == "" {
		delete(b.Answers, id)
	} else {
		b.Answers[id] = answer
	}
	if err := Save(dir, b); err != nil {
		return Result{}, err
	}
	res := Result{ID: id, Answer: answer, Files: []string{File}, Open: len(b.Open())}
	if answer == "" || answer == previous {
		return res, nil
	}
	if q.Decision != "" {
		title := strings.TrimPrefix(q.Decision, "Brief: ")
		title = strings.ToUpper(title[:1]) + title[1:] + ": " + short(firstLine(answer), 70)
		if _, err := decisions.Add(dir, title, "Answered in the brief interview ("+File+"): "+oneLine(answer)+". "+q.Why, File); err != nil {
			return res, err
		}
		res.Decision, res.Files = title, append(res.Files, decisions.File)
	}
	if q.Agreement != "" {
		files, err := SyncAgreements(dir, b)
		if err != nil {
			return res, err
		}
		res.Files = append(res.Files, files...)
	}
	if q.ID == "palette" {
		if p, ok := paletteFor(answer); ok {
			changed, manual, err := applyPalette(dir, p)
			if err != nil {
				return res, err
			}
			res.Files, res.Manual = append(res.Files, changed...), manual
		} else {
			res.Manual = append(res.Manual, "the palette: write the colours named in the answer into src/index.css tokens and admin/theme.css")
		}
	}
	if name, err := seedRecipe(dir, q.ID, answer); err != nil {
		return res, err
	} else if name != "" {
		res.Recipe, res.Files = name, append(res.Files, recipes.GuideFile)
	}
	return res, nil
}

// Skip marks questions as skipped: not answered, not asked again, not
// counted by L015. No ids skips every open question (the whole brief). An
// answer given later replaces the skip. It returns the ids it skipped.
func Skip(dir, app string, ids ...string) ([]string, error) {
	b, err := Load(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if b.App == "" {
		b.App = app
	}
	if b.Skipped == nil {
		b.Skipped = map[string]bool{}
	}
	if len(ids) == 0 {
		for _, q := range b.Open() {
			ids = append(ids, q.ID)
		}
	}
	var done []string
	for _, id := range ids {
		if _, ok := Find(id); !ok {
			return nil, fmt.Errorf("brief: no question %q (lidza brief --list shows them)", id)
		}
		if b.Answers[id] != "" || b.Skipped[id] {
			continue
		}
		b.Skipped[id] = true
		done = append(done, id)
	}
	return done, Save(dir, b)
}

// headerRe matches the interview's question header ("[Product 1/31] ..."),
// which a copy and paste can carry into an answer.
var headerRe = regexp.MustCompile(`\[[A-Za-z ]+ \d+/\d+\]`)

// Clean removes what a copy and paste of the interview can carry into an
// answer: the question header, the question and its explanation. What
// is left is the answer; "" when nothing is.
func Clean(q Question, answer string) string {
	answer = headerRe.ReplaceAllString(answer, "")
	for _, s := range []string{q.Ask, q.Why} {
		if s != "" {
			answer = strings.ReplaceAll(answer, s, "")
		}
	}
	return strings.TrimSpace(answer)
}

// menuRe matches an answer that is only menu numbers ("1,3", "2").
var menuRe = regexp.MustCompile(`^[\d\s,;]+$`)

// Garbled reports whether a saved answer is not an answer: the pasted
// question or its explanation, an interview header, or menu numbers a
// terminal saved as text (briefs answered before those were resolved).
// Such a question counts as open, so the agent asks again instead of
// building on it.
func Garbled(q Question, answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return false
	}
	return Clean(q, answer) != answer || menuRe.MatchString(answer)
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

func oneLine(s string) string {
	return strings.TrimRight(strings.Join(strings.Fields(s), " "), ".")
}

func short(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Markers of the agent files' sections the brief and the notes own.
const (
	agreementsOpen  = "<!-- lidza:agreements -->"
	agreementsClose = "<!-- /lidza:agreements -->"
	notesHeading    = "## Team notes"
)

// AgentFile holds the instructions every coding agent reads: Codex and
// the tools that follow the AGENTS.md convention read it as it is, and
// Claude Code and Gemini CLI through the one-line stubs AgentStubs.
const AgentFile = "AGENTS.md"

// AgentStubs import AgentFile with "@AGENTS.md", which Claude Code and
// Gemini CLI expand; Codex has no imports, so the content stays in
// AGENTS.md.
var AgentStubs = []string{"CLAUDE.md", "GEMINI.md"}

// AgentStub is the content of each stub.
const AgentStub = "@" + AgentFile + "\n"

// IsAgentStub reports whether data is a stub importing AgentFile.
func IsAgentStub(data []byte) bool {
	return strings.TrimSpace(string(data)) == strings.TrimSpace(AgentStub)
}

// AgentFiles lists the agent files in dir that hold instructions:
// AGENTS.md, and a CLAUDE.md or GEMINI.md that is still a full copy (an
// app from before the stubs that ConvertAgentStubs left alone because
// it differs).
func AgentFiles(dir string) []string {
	out := []string{AgentFile}
	for _, name := range AgentStubs {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil && !IsAgentStub(data) {
			out = append(out, name)
		}
	}
	return out
}

// ConvertAgentStubs makes CLAUDE.md and GEMINI.md the stubs: a missing
// one is written, and one identical to AGENTS.md (the three copies apps
// had before) is replaced. One that differs keeps its content, and is
// returned in kept so the developer merges it into AGENTS.md first.
func ConvertAgentStubs(dir string) (changed, kept []string, err error) {
	main, err := os.ReadFile(filepath.Join(dir, AgentFile))
	if err != nil {
		return nil, nil, err
	}
	for _, name := range AgentStubs {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		switch {
		case err == nil && IsAgentStub(data):
			continue
		case err == nil && string(data) != string(main):
			kept = append(kept, name)
			continue
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return changed, kept, err
		}
		if err := os.WriteFile(p, []byte(AgentStub), 0o644); err != nil {
			return changed, kept, err
		}
		changed = append(changed, name)
	}
	return changed, kept, nil
}

// Sections the agent files end with: the working agreements the brief
// writes, and the team's notes.
const agentSections = "\n## Working agreements\n\n" + agreementsOpen + "\n_Set by the brief interview: `lidza brief`, or the recipe \"Start with the brief\"._\n" + agreementsClose + "\n\n" +
	notesHeading + "\n\nLasting facts about this app that the team and every agent should know, shared\nthrough git: `lidza note add \"...\"` (MCP `lidza_note_add`). Record them here,\nnever in an agent's local memory.\n"

// EnsureAgentSections gives agent files from before the brief their two
// closing sections; it returns the files it changed.
func EnsureAgentSections(dir string) ([]string, error) {
	var changed []string
	for _, name := range AgentFiles(dir) {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), agreementsOpen) {
			continue
		}
		if err := os.WriteFile(p, []byte(strings.TrimRight(string(data), "\n")+"\n"+agentSections), 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, name)
	}
	return changed, nil
}

// SyncAgreements writes the working-agreement answers into every agent
// file; it returns the files it changed.
func SyncAgreements(dir string, b Brief) ([]string, error) {
	var lines []string
	for _, q := range Questions {
		if q.Agreement != "" && b.Answers[q.ID] != "" {
			lines = append(lines, "- "+q.Agreement+": "+oneLine(b.Answers[q.ID])+".")
		}
	}
	block := "_Set by the brief interview: `lidza brief`, or the recipe \"Start with the brief\"._"
	if len(lines) > 0 {
		block = "From the brief (" + File + "); follow them in every session.\n\n" + strings.Join(lines, "\n")
	}
	if _, err := EnsureAgentSections(dir); err != nil {
		return nil, err
	}
	var changed []string
	for _, name := range AgentFiles(dir) {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(data)
		i, j := strings.Index(s, agreementsOpen), strings.Index(s, agreementsClose)
		if i < 0 || j < i {
			continue
		}
		next := s[:i+len(agreementsOpen)] + "\n" + block + "\n" + s[j:]
		if next == s {
			continue
		}
		if err := os.WriteFile(p, []byte(next), 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, name)
	}
	return changed, nil
}

// AddNote appends a dated note to the Team notes of every agent file; it
// returns the files it changed.
func AddNote(dir, text string) ([]string, error) {
	text = oneLine(strings.TrimSpace(text))
	if text == "" {
		return nil, errors.New("note: the text is empty")
	}
	if _, err := EnsureAgentSections(dir); err != nil {
		return nil, err
	}
	line := "- " + time.Now().Format("2006-01-02") + ": " + text + "."
	var changed []string
	for _, name := range AgentFiles(dir) {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(data)
		k := strings.Index(s, notesHeading)
		if k < 0 {
			s = strings.TrimRight(s, "\n") + "\n\n" + notesHeading + "\n"
			k = strings.Index(s, notesHeading)
		}
		// The notes run to the next section or the end of the file.
		end := len(s)
		if next := strings.Index(s[k+len(notesHeading):], "\n## "); next >= 0 {
			end = k + len(notesHeading) + next + 1
		}
		notes := strings.TrimRight(s[k:end], "\n")
		sep := "\n\n"
		if lines := strings.Split(notes, "\n"); strings.HasPrefix(lines[len(lines)-1], "- ") {
			sep = "\n" // right after the previous note
		}
		rest := s[end:]
		if rest != "" {
			rest = "\n" + rest
		}
		s = s[:k] + notes + sep + line + "\n" + rest
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, name)
	}
	if len(changed) == 0 {
		return nil, errors.New("note: no " + AgentFile + " in the project")
	}
	return changed, nil
}
