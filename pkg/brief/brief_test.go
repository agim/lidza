package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/recipes"
)

func app(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, recipes.GuideFile), []byte("# Guide\n\n## Recipes\n\n### Add an API route\n\nExpose an operation.\n\n1. Step.\n\n## App recipes\n\nThis app's own.\n\n## Packs\n\nText.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "index.css"), []byte("@import \"tailwindcss\";\n@theme {\n  --color-brand: #1d4ed8;\n  --color-brand-strong: #1e40af;\n  --color-ink: #1a1a1a;\n  --font-sans: system-ui;\n}\n"), 0o644)
	for _, f := range AgentFiles {
		os.WriteFile(filepath.Join(dir, f), []byte("# app\n\n- A line.\n"), 0o644)
	}
	if _, err := decisions.Ensure(dir, "app"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRoundTrip(t *testing.T) {
	dir := app(t)
	if created, err := Ensure(dir, "galeria"); err != nil || !created {
		t.Fatalf("ensure: %v %v", created, err)
	}
	b, err := Load(dir)
	if err != nil || b.App != "galeria" || len(b.Answers) != 0 || len(b.Open()) != len(Questions) || len(b.OpenRequired()) == 0 {
		t.Fatalf("empty brief: %+v %v", b, err)
	}
	b.Answers["purpose"] = "Discover art.\n\nAnd save it to collections."
	b.Answers["journeys"] = "- Search\n- Save\n- Share"
	b.Notes = "Keep it quiet."
	if err := Save(dir, b); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(dir)
	if got.Answers["purpose"] != b.Answers["purpose"] || got.Answers["journeys"] != b.Answers["journeys"] || got.Notes != "Keep it quiet." || len(got.Open()) != len(Questions)-2 {
		t.Fatalf("round trip: %+v", got)
	}
	// A hand edit to an answer is read back as the answer.
	p := filepath.Join(dir, File)
	data, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(data), "Discover art.", "Discover and collect art.", 1)), 0o644)
	if got, _ := Load(dir); !strings.HasPrefix(got.Answers["purpose"], "Discover and collect art.") {
		t.Fatalf("hand edit: %q", got.Answers["purpose"])
	}
	if created, _ := Ensure(dir, "galeria"); created {
		t.Fatal("ensure overwrote the brief")
	}
}

func TestAnswerApplies(t *testing.T) {
	dir := app(t)
	Ensure(dir, "galeria")

	// A decision, once per change.
	res, err := Answer(dir, "galeria", "signin", "Email and password, Google")
	if err != nil || res.Decision != "Sign-in: Email and password, Google" {
		t.Fatalf("signin: %+v %v", res, err)
	}
	if res, _ := Answer(dir, "galeria", "signin", "Email and password, Google"); res.Decision != "" {
		t.Fatal("the same answer recorded twice")
	}
	es, _ := decisions.Load(dir)
	if len(es) != 1 || !strings.Contains(es[0].Why, "Email and password, Google") {
		t.Fatalf("decisions: %+v", es)
	}

	// The palette: tokens and the admin theme.
	res, err = Answer(dir, "galeria", "palette", "Forest and linen")
	if err != nil || len(res.Manual) != 0 {
		t.Fatalf("palette: %+v %v", res, err)
	}
	css, _ := os.ReadFile(filepath.Join(dir, "src", "index.css"))
	theme, _ := os.ReadFile(filepath.Join(dir, "admin", "theme.css"))
	if !strings.Contains(string(css), "--color-brand: oklch(45% 0.09 160);") || strings.Contains(string(css), "#1d4ed8") || !strings.Contains(string(css), "--font-sans: system-ui;") {
		t.Fatalf("tokens: %s", css)
	}
	if !strings.Contains(string(theme), "--admin-accent: oklch(45% 0.09 160);") || !strings.Contains(string(theme), "[data-bs-theme=dark]") {
		t.Fatalf("admin theme: %s", theme)
	}
	// The style recipe was seeded.
	if res.Recipe != recipes.Slug(RecipeStyle) {
		t.Fatalf("style recipe: %+v", res)
	}
	// An admin theme the app wrote itself is left alone.
	os.WriteFile(filepath.Join(dir, "admin", "theme.css"), []byte(":root{--admin-accent:red}"), 0o644)
	res, _ = Answer(dir, "galeria", "palette", "Ink and saffron")
	if theme, _ := os.ReadFile(filepath.Join(dir, "admin", "theme.css")); string(theme) != ":root{--admin-accent:red}" || len(res.Manual) != 1 {
		t.Fatalf("own theme: %s %+v", theme, res)
	}
	// An own palette is for the agent to apply.
	if res, _ := Answer(dir, "galeria", "palette", OwnBrand+": #0b3d2e and #f2e8cf"); len(res.Manual) == 0 {
		t.Fatal("own brand colours need the agent")
	}

	// Ownership seeds the scope recipe for its model, once.
	res, _ = Answer(dir, "galeria", "ownership", "Shared with invited members")
	rs, _ := recipes.Load(dir)
	var scope recipes.Recipe
	for _, r := range rs {
		if r.Name == recipes.Slug(RecipeScope) {
			scope = r
		}
	}
	if res.Recipe != scope.Name || !strings.Contains(scope.Body, "requireMember") {
		t.Fatalf("scope recipe: %+v %+v", res, scope)
	}
	if res, _ := Answer(dir, "galeria", "ownership", "Each user owns their rows"); res.Recipe != "" {
		t.Fatal("the recipe was seeded twice")
	}
	// Content: only a dataset or an import seeds the data recipe.
	if res, _ := Answer(dir, "galeria", "content", "People create it in the app"); res.Recipe != "" {
		t.Fatal("no data recipe for user content")
	}
	if res, _ := Answer(dir, "galeria", "content", "A public dataset: The Met Open Access, CC0"); res.Recipe != recipes.Slug(RecipeData) {
		t.Fatalf("data recipe: %+v", res)
	}

	// Working agreements land in every agent file.
	Answer(dir, "galeria", "push", "After every verified commit")
	Answer(dir, "galeria", "tests", "A Go test for every handler")
	for _, f := range AgentFiles {
		data, _ := os.ReadFile(filepath.Join(dir, f))
		s := string(data)
		if !strings.Contains(s, "- Pushing: After every verified commit.") || !strings.Contains(s, "- Tests: A Go test for every handler.") || !strings.Contains(s, "- A line.") {
			t.Fatalf("%s: %s", f, s)
		}
	}
	// Clearing an answer reopens it.
	Answer(dir, "galeria", "push", "")
	if b, _ := Load(dir); b.Answers["push"] != "" {
		t.Fatal("not cleared")
	}
	if _, err := Answer(dir, "galeria", "nope", "x"); err == nil {
		t.Fatal("unknown question accepted")
	}
}

func TestNotes(t *testing.T) {
	dir := app(t)
	if _, err := AddNote(dir, "Prices are in euros, always with VAT"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddNote(dir, "The museum asks for a User-Agent with a contact."); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(data)
	i := strings.Index(s, "## Team notes")
	if i < 0 || !strings.Contains(s[i:], ": Prices are in euros, always with VAT.\n- ") || !strings.HasSuffix(s, "with a contact.\n") || strings.Count(s, "## Working agreements") != 1 {
		t.Fatalf("notes: %s", s)
	}
	if _, err := AddNote(dir, "  "); err == nil {
		t.Fatal("empty note accepted")
	}
}

func TestQuestions(t *testing.T) {
	seen := map[string]bool{}
	for _, q := range Questions {
		if seen[q.ID] || q.Ask == "" || q.Why == "" {
			t.Errorf("question %q: duplicate or incomplete", q.ID)
		}
		seen[q.ID] = true
		found := false
		for _, s := range Sections {
			found = found || s == q.Section
		}
		if !found {
			t.Errorf("question %q: unknown section %q", q.ID, q.Section)
		}
		if q.Kind != Text && len(q.Suggestions) < 2 {
			t.Errorf("question %q: a choice needs suggestions", q.ID)
		}
	}
	for _, p := range Palettes {
		for _, k := range []string{"accent", "accent-fg", "bg", "surface", "surface-2", "line", "fg", "muted", "sidebar"} {
			if p.Light[k] == "" || (k != "sidebar" && p.Dark[k] == "") {
				t.Errorf("palette %s: %s missing", p.Name, k)
			}
		}
		for _, k := range []string{"brand", "brand-strong", "ink", "muted", "surface", "line"} {
			if p.Tokens[k] == "" {
				t.Errorf("palette %s: token %s missing", p.Name, k)
			}
		}
	}
}

// A skipped question is neither open nor required any more; the whole
// brief can be skipped; an answer given later replaces the skip.
func TestSkip(t *testing.T) {
	dir := app(t)
	Ensure(dir, "galeria")
	if done, err := Skip(dir, "galeria", "domain", "palette"); err != nil || len(done) != 2 {
		t.Fatalf("skip: %v %v", done, err)
	}
	b, _ := Load(dir)
	if !b.Skipped["palette"] || len(b.Open()) != len(Questions)-2 {
		t.Fatalf("skipped: %+v", b.Skipped)
	}
	for _, q := range b.OpenRequired() {
		if q.ID == "palette" {
			t.Fatal("a skipped required question still counts")
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, File))
	if strings.Count(string(data), skipped) != 2 {
		t.Fatalf("file: %s", data)
	}
	Answer(dir, "galeria", "purpose", "Art")
	done, _ := Skip(dir, "galeria")
	b, _ = Load(dir)
	if len(done) != len(Questions)-3 || len(b.Open()) != 0 || len(b.OpenRequired()) != 0 || b.Answers["purpose"] != "Art" {
		t.Fatalf("skip all: %d %+v", len(done), b.Open())
	}
	Answer(dir, "galeria", "palette", "Forest and linen")
	if b, _ := Load(dir); b.Skipped["palette"] || b.Answers["palette"] != "Forest and linen" {
		t.Fatalf("answer after skip: %+v", b)
	}
	if _, err := Skip(dir, "galeria", "nope"); err == nil {
		t.Fatal("unknown id skipped")
	}
}
