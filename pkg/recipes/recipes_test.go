package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const guide = `# Guide

## Rules

1. Something.

## Recipes

Intro that belongs to no recipe.

### Add an API route

Expose an operation with "typed" input and output.
Second line of the paragraph.

1. Declare the shapes.
2. Register the handler.

### Write a test

Boot the app with lidzatest.

## Operations

### Not a recipe
`

func TestParse(t *testing.T) {
	rs := Parse(guide)
	if len(rs) != 2 {
		t.Fatalf("got %d recipes: %+v", len(rs), rs)
	}
	r := rs[0]
	if r.Name != "add-api-route" || r.Title != "Add an API route" {
		t.Errorf("name/title: %+v", r)
	}
	if r.Description != `Expose an operation with "typed" input and output. Second line of the paragraph.` {
		t.Errorf("description: %q", r.Description)
	}
	if !strings.HasPrefix(r.Body, "Expose") || !strings.HasSuffix(r.Body, "2. Register the handler.") {
		t.Errorf("body: %q", r.Body)
	}
	if rs[1].Name != "write-test" {
		t.Errorf("second: %+v", rs[1])
	}
	if !strings.HasPrefix(Skill(r), "---\nname: add-api-route\ndescription: \"Expose an operation with \\\"typed\\\" input and output. Second line of the paragraph.\"\n---\n"+marker) {
		t.Errorf("skill:\n%s", Skill(r))
	}
}

func TestWriteSkills(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, GuideFile), []byte(guide), 0o644)
	// A hand-written skill and a stale generated one.
	os.MkdirAll(filepath.Join(dir, SkillsDir, "mine"), 0o755)
	os.WriteFile(filepath.Join(dir, SkillsDir, "mine", "SKILL.md"), []byte("---\nname: mine\n---\nkeep"), 0o644)
	os.MkdirAll(filepath.Join(dir, SkillsDir, "old"), 0o755)
	os.WriteFile(filepath.Join(dir, SkillsDir, "old", "SKILL.md"), []byte("---\nname: old\n---\n"+marker), 0o644)

	rs, err := Sync(dir)
	if err != nil || len(rs) != 2 {
		t.Fatal(err, rs)
	}
	for _, name := range []string{"add-api-route", "write-test", "mine"} {
		if _, err := os.Stat(filepath.Join(dir, SkillsDir, name, "SKILL.md")); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, SkillsDir, "old")); !os.IsNotExist(err) {
		t.Error("stale generated skill kept")
	}
	agents, err := os.ReadFile(filepath.Join(dir, AgentsSkillsDir, "add-api-route", "SKILL.md"))
	claude, _ := os.ReadFile(filepath.Join(dir, SkillsDir, "add-api-route", "SKILL.md"))
	if err != nil || string(agents) != string(claude) {
		t.Errorf("codex skill: %v", err)
	}
	gem, err := os.ReadFile(filepath.Join(dir, GeminiCommandsDir, "add-api-route.toml"))
	if err != nil || !strings.HasPrefix(string(gem), geminiMarker+"\ndescription = \"Expose an operation with \\\"typed\\\" input and output. Second line of the paragraph.\"\nprompt = \"\"\"\n# Add an API route\n") || !strings.Contains(string(gem), "Task: {{args}}\n\"\"\"\n") {
		t.Errorf("gemini command: %v\n%s", err, gem)
	}
	// A second sync with one recipe gone removes its files everywhere.
	os.WriteFile(filepath.Join(dir, GuideFile), []byte("## Recipes\n\n### Write a test\n\nBoot.\n"), 0o644)
	if _, err := Sync(dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(SkillsDir, "add-api-route"), filepath.Join(AgentsSkillsDir, "add-api-route"), filepath.Join(GeminiCommandsDir, "add-api-route.toml")} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s not removed", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, SkillsDir, "mine", "SKILL.md")); err != nil {
		t.Error("hand-written skill removed")
	}
	if rs, err := Sync(t.TempDir()); err != nil || rs != nil {
		t.Errorf("no guide: %v %v", rs, err)
	}
}

func TestScopesAddAndReplace(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, GuideFile), []byte("# Guide\n\n## Recipes\n\nIntro.\n\n### Add an API route\n\nOld body.\n\n## Packs\n\nText.\n"), 0o644)

	// Add creates the App recipes section after Recipes.
	r, err := Add(dir, "Paginate a list", "Lists take limit and offset.", []string{"Use PageParams.", "`lidza check`."})
	if err != nil || r.Name != "paginate-list" || r.Scope != ScopeApp {
		t.Fatalf("%+v %v", r, err)
	}
	rs, _ := Load(dir)
	if len(rs) != 2 || rs[0].Scope != ScopeFramework || rs[1].Name != "paginate-list" || !strings.Contains(rs[1].Body, "1. Use PageParams.") {
		t.Fatalf("recipes: %+v", rs)
	}
	guide, _ := os.ReadFile(filepath.Join(dir, GuideFile))
	if i, j, k := strings.Index(string(guide), Heading), strings.Index(string(guide), AppHeading), strings.Index(string(guide), "## Packs"); !(i < j && j < k) {
		t.Fatalf("section order:\n%s", guide)
	}
	if _, err := Add(dir, "Paginate a list", "", nil); err == nil {
		t.Fatal("duplicate accepted")
	}
	// A second app recipe lands in the same section; skeleton steps when none given.
	if _, err := Add(dir, "Verify a webhook", "", nil); err != nil {
		t.Fatal(err)
	}
	rs, _ = Load(dir)
	if len(rs) != 3 || rs[2].Name != "verify-webhook" || !strings.Contains(rs[2].Body, "1. The first step") {
		t.Fatalf("second app recipe: %+v", rs)
	}

	// ReplaceFramework touches only the framework section.
	changed, err := ReplaceFramework(dir, "Fresh intro.\n\n### Add an API route\n\nNew body.\n\n### Write a test\n\nBoot.")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	rs, _ = Load(dir)
	var names []string
	for _, r := range rs {
		names = append(names, r.Scope+":"+r.Name)
	}
	if strings.Join(names, ",") != "framework:add-api-route,framework:write-test,app:paginate-list,app:verify-webhook" || rs[0].Body != "New body." {
		t.Fatalf("after replace: %v %q", names, rs[0].Body)
	}
	if again, _ := ReplaceFramework(dir, "Fresh intro.\n\n### Add an API route\n\nNew body.\n\n### Write a test\n\nBoot."); again {
		t.Fatal("replace reported a change on identical content")
	}
	guide, _ = os.ReadFile(filepath.Join(dir, GuideFile))
	if !strings.Contains(string(guide), "## Packs\n\nText.") {
		t.Fatalf("later section damaged:\n%s", guide)
	}
}
