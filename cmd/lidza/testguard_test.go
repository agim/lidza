package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const guardGoTest = `package main

import "testing"

func TestPosts(t *testing.T) {
	got := 2
	if got != 2 {
		t.Errorf("got %d", got)
	}
	if got < 0 {
		t.Fatal("negative")
	}
}
`

const guardSpec = `import { test, expect } from "@playwright/test";

test("home", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading")).toBeVisible();
});
`

// guardRepo is a git repository with no author configured, as on a CI
// runner: the environment supplies one, the user's configuration is
// ignored.
func guardRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
		EnvAllowTestChanges:   "",
	} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	return dir, git
}

func writeGuardFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTestGuardFirstCommit(t *testing.T) {
	dir, git := guardRepo(t)
	writeGuardFile(t, dir, "posts_test.go", guardGoTest)
	git("add", "-A")
	reason, err := testGuard(context.Background(), dir, false)
	if !errors.Is(err, errSkipped) || reason != "first commit" {
		t.Fatalf("reason %q, err %v", reason, err)
	}
}

func TestTestGuard(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		// change edits the committed files and stages the result.
		change func(t *testing.T, dir string, git func(...string))
		// want are the findings' texts; none when empty.
		want []string
	}{
		{"unrelated change", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "main.go", "package main\n\nfunc main() { println() }\n")
			git("add", "-A")
		}, nil},
		{"go skip added", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, "\tgot := 2\n", "\tt.Skip(\"later\")\n\tgot := 2\n", 1))
			git("add", "-A")
		}, []string{`posts_test.go:6: skip added: t.Skip("later")`}},
		{"go skip with a reason", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, "\tgot := 2\n", "\t// lidza:allow-skip needs a mail server\n\tt.Skip(\"no mail server\")\n\tgot := 2\n", 1))
			git("add", "-A")
		}, nil},
		{"skip marker without a reason", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, "\tgot := 2\n", "\tt.SkipNow() // lidza:allow-skip\n\tgot := 2\n", 1))
			git("add", "-A")
		}, []string{"posts_test.go:6: skip added"}},
		{"assertions removed", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, "\t\tt.Errorf(\"got %d\", got)\n", "", 1))
			git("add", "-A")
		}, []string{"posts_test.go:8 (HEAD): assertion removed (1 removed, 0 added in this file)"}},
		{"assertion commented out", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, "\t\tt.Fatal(\"negative\")\n", "\t\t// t.Fatal(\"negative\")\n", 1))
			git("add", "-A")
		}, []string{"posts_test.go:11 (HEAD): assertion removed"}},
		{"assertion rewritten", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, `t.Errorf("got %d", got)`, `t.Errorf("got %d, want 2", got)`, 1))
			git("add", "-A")
		}, nil},
		{"test file deleted", func(t *testing.T, dir string, git func(...string)) {
			git("rm", "-q", "e2e/home.spec.ts")
		}, []string{"e2e/home.spec.ts: test file deleted"}},
		{"test file moved", func(t *testing.T, dir string, git func(...string)) {
			git("mv", "e2e/home.spec.ts", "e2e/start.spec.ts")
		}, nil},
		{"only added", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "e2e/home.spec.ts", strings.Replace(guardSpec, `test("home"`, `test.only("home"`, 1))
			git("add", "-A")
		}, []string{"e2e/home.spec.ts:3: only added"}},
		{"expect removed", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "e2e/home.spec.ts", strings.Replace(guardSpec, "  await expect(page.getByRole(\"heading\")).toBeVisible();\n", "", 1))
			git("add", "-A")
		}, []string{"e2e/home.spec.ts:5 (HEAD): assertion removed"}},
		{"fixme in a unit test", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "src/lib/format.test.ts", "import { test } from \"vitest\";\ntest.fixme(\"formats\", () => {});\n")
			git("add", "-A")
		}, []string{"src/lib/format.test.ts:2: skip added"}},
		{"only staged changes count", func(t *testing.T, dir string, git func(...string)) {
			writeGuardFile(t, dir, "posts_test.go", strings.Replace(guardGoTest, "\tgot := 2\n", "\tt.Skip()\n\tgot := 2\n", 1))
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, git := guardRepo(t)
			writeGuardFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
			writeGuardFile(t, dir, "posts_test.go", guardGoTest)
			writeGuardFile(t, dir, "e2e/home.spec.ts", guardSpec)
			git("add", "-A")
			git("commit", "-q", "--no-verify", "-m", "start")
			c.change(t, dir, git)

			reason, err := testGuard(ctx, dir, false)
			if len(c.want) == 0 {
				if err != nil {
					t.Fatalf("want no finding, got %q, %v", reason, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want %q, passed (%q)", c.want, reason)
			}
			msg := err.Error()
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("want %q in:\n%s", w, msg)
				}
			}
			for _, w := range []string{EnvAllowTestChanges + "=1 git commit", "--allow-test-changes", "lidza:allow-skip <reason>", "say so to the developer"} {
				if !strings.Contains(msg, w) {
					t.Errorf("the message should say %q:\n%s", w, msg)
				}
			}
			// Confirmed by the flag, or by the environment for the hook.
			if reason, err := testGuard(ctx, dir, true); err != nil || !strings.Contains(reason, "--allow-test-changes") {
				t.Errorf("with the flag: %q, %v", reason, err)
			}
			t.Setenv(EnvAllowTestChanges, "1")
			if reason, err := testGuard(ctx, dir, false); err != nil || !strings.Contains(reason, EnvAllowTestChanges) {
				t.Errorf("with %s=1: %q, %v", EnvAllowTestChanges, reason, err)
			}
		})
	}
}

// A skip that only moves or is re-indented is not a new one; one inside
// a comment is not a skip.
func TestParseTestDiffMovedSkip(t *testing.T) {
	diff := `diff --git a/x_test.go b/x_test.go
index 1..2 100644
--- a/x_test.go
+++ b/x_test.go
@@ -3 +3 @@
-	t.Skip("slow")
+		t.Skip("slow")
@@ -9,0 +10 @@
+	// t.Skip("never")
`
	if f := parseTestDiff([]byte(diff)); len(f) != 0 {
		t.Fatalf("findings %v", f)
	}
}

// Files outside the tests, and dependencies' tests, are not checked.
func TestIsTestFile(t *testing.T) {
	for path, want := range map[string]bool{
		"routes_test.go":                        true,
		"e2e/home.spec.ts":                      true,
		"src/pages/Home.test.tsx":               true,
		"src/lib/util.spec.js":                  true,
		"routes.go":                             false,
		"src/pages/Home.tsx":                    false,
		"node_modules/pkg/index.test.js":        false,
		"web/node_modules/pkg/index.spec.ts":    false,
		"docs/testing.md":                       false,
		"packs/stats/rust/src/lib_test.rs":      false,
		"internal/posts/posts_test.go":          true,
		"playwright.config.ts":                  false,
		"src/components/Button.stories.test.ts": true,
	} {
		if got := isTestFile(path); got != want {
			t.Errorf("isTestFile(%q) = %v, want %v", path, got, want)
		}
	}
}
