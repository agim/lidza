//go:build agenteval

// Agent-driven evals: a real coding agent gets a task on a fresh app and
// the result is scored by `lidza verify` and a probe per task. Run with
// the agent command in LIDZA_EVAL_AGENT, executed in the app directory
// with the task on stdin (or in place of {prompt}):
//
//	LIDZA_EVAL_AGENT='claude -p --permission-mode acceptEdits' go test -tags agenteval ./evals -v -timeout 1h
//	LIDZA_EVAL_AGENT='codex exec --full-auto' go test -tags agenteval ./evals -v -timeout 1h
//	LIDZA_EVAL_AGENT='gemini --yolo' go test -tags agenteval ./evals -v -timeout 1h
//
// Each task runs on a fresh copy of the scaffolded app; the agent's output
// is kept under the test's temporary directory and printed on failure.
// The permission mode is the operator's choice: the runner never adds a
// flag that skips permission checks.
package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/inspect"
)

type task struct {
	name   string
	prompt string
	// probe checks the result beyond lidza verify; it returns what is wrong.
	probe func(dir string) string
}

var tasks = []task{
	{
		name: "add-route",
		prompt: "Add GET /api/v1/time to this app: it replies with the server's current time as a type ServerTime " +
			"{ now datetime } declared in schema.lidza, using lidza.Now(ctx). Cover it in routes_test.go. " +
			"Run lidza check and lidza test until both pass. Do not touch the frontend.",
		probe: func(dir string) string {
			cx, err := inspect.Project(dir, nil)
			if err != nil {
				return err.Error()
			}
			for _, op := range cx.Operations {
				if op.Method == "GET" && op.Path == "/api/v1/time" && op.Output == "ServerTime" {
					return ""
				}
			}
			return "no typed operation GET /api/v1/time with output ServerTime in lidza context"
		},
	},
	{
		name: "add-page",
		prompt: "Add a page at /time to this React app that shows the server time from a new GET /api/v1/time route " +
			"(type ServerTime { now datetime } in schema.lidza). Call the API only through @lidza/client. " +
			"Link the page from the navigation. Run lidza check until it passes.",
		probe: func(dir string) string {
			router, _ := os.ReadFile(filepath.Join(dir, "src", "router.tsx"))
			if !strings.Contains(string(router), "'/time'") {
				return "src/router.tsx has no '/time' route"
			}
			return ""
		},
	},
	{
		name: "add-recipe",
		prompt: "This app paginates every list endpoint with limit and offset query parameters, capped at 200, read with the PageParams helper. " +
			"Record that as a recipe named \"Paginate a list\" so the next developer follows it, the way the guide says recipes are added. " +
			"Then run lidza check.",
		probe: func(dir string) string {
			if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "paginate-list", "SKILL.md")); err != nil {
				return "no skill .claude/skills/paginate-list/SKILL.md (was the recipe added under App recipes and lidza gen run?)"
			}
			guide, _ := os.ReadFile(filepath.Join(dir, "docs", "lidza-guide.md"))
			if !strings.Contains(string(guide), "### Paginate a list") {
				return "docs/lidza-guide.md has no '### Paginate a list'"
			}
			return ""
		},
	},
	{
		name: "add-tool",
		prompt: "Add an MCP tool named word_count to tools.go that takes { text string } and returns the number of words. " +
			"Run lidza check until it passes.",
		probe: func(dir string) string {
			tools, _ := os.ReadFile(filepath.Join(dir, "tools.go"))
			if !strings.Contains(string(tools), `"word_count"`) || !strings.Contains(string(tools), "lidza.ToolFunc") {
				return "tools.go has no lidza.ToolFunc named word_count"
			}
			return ""
		},
	},
}

func TestAgent(t *testing.T) {
	agent := os.Getenv("LIDZA_EVAL_AGENT")
	if agent == "" {
		t.Skip("set LIDZA_EVAL_AGENT to the agent command, e.g. 'claude -p --permission-mode acceptEdits'")
	}
	for _, tk := range tasks {
		t.Run(tk.name, func(t *testing.T) {
			dir := copyApp(t)
			start := time.Now()
			out, err := runAgent(dir, agent, tk.prompt)
			os.WriteFile(filepath.Join(dir, "agent-output.txt"), out, 0o644)
			if err != nil {
				t.Fatalf("agent failed after %s: %v\n%s", time.Since(start).Round(time.Second), err, tail(out))
			}
			// Score as the pre-commit hook would see the agent's work: with
			// everything it wrote staged, so the "generated files staged"
			// step measures whether it left the generated files current,
			// not whether it ran git add.
			command(dir, "git", "add", "-A")
			verify, _ := command(dir, lidza, "verify", "--json")
			var rep struct {
				Status      string `json:"status"`
				Steps       []struct{ Name, Status, Reason string }
				Diagnostics []struct{ Severity, Code, File, Message string }
			}
			if jerr := json.Unmarshal(verify, &rep); jerr != nil {
				t.Fatalf("verify --json: %v\n%s", jerr, verify)
			}
			problem := tk.probe(dir)
			t.Logf("%s: agent %s, verify %s, %d diagnostic(s), probe: %s", tk.name, time.Since(start).Round(time.Second), rep.Status, len(rep.Diagnostics), or(problem, "ok"))
			if rep.Status != "ok" {
				t.Errorf("verify: %+v", rep.Steps)
				for _, d := range rep.Diagnostics {
					t.Logf("  %s %s %s: %s", d.Severity, d.Code, d.File, d.Message)
				}
				status, _ := command(dir, "git", "status", "--porcelain")
				diff, _ := command(dir, "git", "diff", "--stat")
				t.Logf("git status after verify:\n%s%s", status, diff)
			}
			if problem != "" {
				t.Error(problem)
			}
			if t.Failed() {
				t.Logf("agent output (%s):\n%s", filepath.Join(dir, "agent-output.txt"), tail(out))
			}
		})
	}
}

// copyApp copies the scaffolded app (node_modules included, by symlink)
// into a fresh directory so tasks do not see each other's changes.
func copyApp(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	if out, err := command(filepath.Dir(app), "cp", "-r", "--no-dereference", app, dir); err != nil {
		t.Fatalf("copy: %v\n%s", err, out)
	}
	os.RemoveAll(filepath.Join(dir, "node_modules"))
	os.Symlink(filepath.Join(app, "node_modules"), filepath.Join(dir, "node_modules"))
	if out, err := command(dir, "git", "add", "-A"); err != nil {
		t.Logf("git add: %v %s", err, out)
	}
	if out, err := command(dir, "git", "-c", "user.name=eval", "-c", "user.email=eval@example.com", "commit", "-q", "--no-verify", "-m", "scaffold"); err != nil {
		t.Logf("git commit: %v %s", err, out)
	}
	return dir
}

// runAgent runs the agent command in dir with the prompt substituted for
// {prompt}, or on stdin when the command has no placeholder (every agent
// CLI reads its prompt there; appending it would let a variadic flag
// swallow it).
func runAgent(dir, agent, prompt string) ([]byte, error) {
	args := strings.Fields(agent)
	replaced := false
	for i, a := range args {
		if a == "{prompt}" {
			args[i], replaced = prompt, true
		}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	// The agent must run the CLI the scorer runs: the one built from this
	// checkout, ahead of any lidza on the PATH.
	cmd.Env = append(os.Environ(), "LIDZA_DIR="+root, "PATH="+filepath.Dir(lidza)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if !replaced {
		cmd.Stdin = strings.NewReader(prompt)
	}
	return cmd.CombinedOutput()
}

func tail(b []byte) string {
	s := string(b)
	if len(s) > 4000 {
		s = "…" + s[len(s)-4000:]
	}
	return s
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var _ = fmt.Sprint
