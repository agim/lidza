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
	if rs, err := Sync(t.TempDir()); err != nil || rs != nil {
		t.Errorf("no guide: %v %v", rs, err)
	}
}
