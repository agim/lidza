package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/recipes"
)

// The command tools run the CLI for the agent: the same binary that
// serves this MCP server, as a subprocess in the project directory, so an
// agent needs no shell for a Līdza project and every result comes back
// as one JSON object. Tools that change the project run one at a time.
var projectMu sync.Mutex

// commandResult is what every command tool returns.
type commandResult struct {
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Exit    int    `json:"exit"`
	// Report is the parsed JSON output of commands that have --json.
	Report json.RawMessage `json:"report,omitempty"`
	// Output is the combined text output (tail) of the others.
	Output string `json:"output,omitempty"`
	// Duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// Note says when the CLI on disk is newer than this server.
	Note string `json:"note,omitempty"`
}

const outputCap = 64 << 10

// runCLI runs `lidza args...` in dir with a timeout and returns the
// result; jsonOut says the command prints one JSON document on stdout.
func runCLI(ctx context.Context, dir string, timeout time.Duration, jsonOut bool, args ...string) (*commandResult, error) {
	exe, err := cliPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LIDZA_LOG_LEVEL=warn")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	res := &commandResult{Command: "lidza " + strings.Join(args, " "), OK: runErr == nil, DurationMS: time.Since(start).Milliseconds()}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		res.Exit = exit.ExitCode()
	} else if runErr != nil {
		res.Exit = -1
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.Output = fmt.Sprintf("timed out after %s\n", timeout)
	}
	if jsonOut && json.Valid(stdout.Bytes()) {
		res.Report = json.RawMessage(stdout.Bytes())
		res.Output += tailOf(stderr.String())
	} else {
		res.Output += tailOf(stdout.String() + stderr.String())
	}
	return res, nil
}

// cliPath is the lidza binary to run: this process when it is the CLI,
// else (a test binary hosting the server) the lidza on the PATH.
func cliPath() (string, error) {
	exe, err := os.Executable()
	if err == nil && strings.HasPrefix(filepath.Base(exe), "lidza") && !strings.Contains(filepath.Base(exe), ".test") {
		return exe, nil
	}
	if p, err := exec.LookPath("lidza"); err == nil {
		return p, nil
	}
	return "", errors.New("the lidza CLI is not on the PATH")
}

func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > outputCap {
		s = "…" + s[len(s)-outputCap:]
	}
	return s
}

// addCommandTools registers the CLI commands as tools.
func addCommandTools(s *server.MCPServer, dir string, cfg *config.Config, after func()) {
	type tool struct {
		name, desc string
		opts       []mcp.ToolOption
		timeout    time.Duration
		jsonOut    bool
		changes    bool
		args       func(req mcp.CallToolRequest) ([]string, error)
	}
	fixed := func(args ...string) func(mcp.CallToolRequest) ([]string, error) {
		return func(mcp.CallToolRequest) ([]string, error) { return args, nil }
	}
	tools := []tool{
		{"lidza_check", "Regenerate, then run go vet, staticcheck, cargo check, tsc or svelte-check, eslint and the Līdza rules; one report with status and diagnostics (layer, file, line, code, message). Status is \"ok\" when there are no errors; fix until it is.", nil, 10 * time.Minute, true, true, fixed("check", "--json")},
		{"lidza_gen", "Generate from schema.lidza (Go structs, SQL and a migration when models changed, Rust structs), the pack wrappers, the handlers' OpenAPI and client, the recipes' skills. lidza dev and lidza_check do it too; call it after editing schema.lidza to see the migration.", nil, 10 * time.Minute, false, true, fixed("gen")},
		{"lidza_gen_resource", "Generate a resource for a model in schema.lidza: queries, Create/Update/List types, handlers with the five routes under /api/v1/<plural>, the registration line. Needs the db pack.", []mcp.ToolOption{
			mcp.WithString("model", mcp.Required(), mcp.Description("The model name in schema.lidza, e.g. Post.")),
			mcp.WithBoolean("force", mcp.Description("Overwrite an existing handlers file.")),
		}, 10 * time.Minute, false, true, func(req mcp.CallToolRequest) ([]string, error) {
			model := req.GetString("model", "")
			if model == "" {
				return nil, errors.New("model is required")
			}
			args := []string{"gen", "resource", model}
			if req.GetBool("force", false) {
				args = append(args, "--force")
			}
			return args, nil
		}},
		{"lidza_pack_add", "Enable an official pack: db, auth, jobs, cache, i18n, realtime, analytics, mail, llm, storage (Go), geo, media (Rust). Writes its .env.example lines and schema types and records why in docs/decisions.md; then lidza_gen and lidza_db_migrate. A pack no code uses is flagged by lidza check (L011): add one for a feature you are building now.", []mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("The pack name.")),
			mcp.WithString("why", mcp.Required(), mcp.Description("Why the app needs it: the feature it serves and the alternative not taken, one or two sentences. Recorded as a decision.")),
		}, 10 * time.Minute, false, true, func(req mcp.CallToolRequest) ([]string, error) {
			name, why := req.GetString("name", ""), strings.TrimSpace(req.GetString("why", ""))
			if name == "" || why == "" {
				return nil, errors.New("name and why are required")
			}
			return []string{"pack", "add", name, "--why", why}, nil
		}},
		{"lidza_pack_scaffold", "Create a local Rust pack under packs/<name> with a crate, an example capability and a manifest, and build it.", []mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("The pack name, lowercase.")),
		}, 10 * time.Minute, false, true, func(req mcp.CallToolRequest) ([]string, error) {
			name := req.GetString("name", "")
			if name == "" {
				return nil, errors.New("name is required")
			}
			return []string{"pack", "scaffold", name}, nil
		}},
		{"lidza_pack_build", "Build the local Rust packs (one, or all that are stale) to WASM.", []mcp.ToolOption{
			mcp.WithString("name", mcp.Description("One pack; all when empty.")),
		}, 15 * time.Minute, false, true, func(req mcp.CallToolRequest) ([]string, error) {
			if name := req.GetString("name", ""); name != "" {
				return []string{"pack", "build", name}, nil
			}
			return []string{"pack", "build"}, nil
		}},
		{"lidza_db_migrate", "Apply the pending migrations in db/migrations to DATABASE_URL from .env (db pack).", nil, 5 * time.Minute, false, true, fixed("db", "migrate")},
		{"lidza_db_rollback", "Revert the last applied migration.", nil, 5 * time.Minute, false, true, fixed("db", "rollback")},
		{"lidza_db_status", "List the migrations and whether each is applied.", nil, time.Minute, false, false, fixed("db", "status")},
		{"lidza_test", "Run the Go tests (the test database is created and migrated, LIDZA_MODE=test), then the frontend check; or the browser suite with e2e.", []mcp.ToolOption{
			mcp.WithBoolean("e2e", mcp.Description("Build the app and run the Playwright suite instead of the Go tests.")),
			mcp.WithBoolean("install", mcp.Description("With e2e: install the browser when it is missing.")),
			mcp.WithString("run", mcp.Description("Only Go tests matching this regexp (go test -run).")),
			mcp.WithBoolean("verbose", mcp.Description("Every Go test by name as it runs (go test -v): proof that a test ran, not only that the package passed.")),
		}, 20 * time.Minute, false, true, func(req mcp.CallToolRequest) ([]string, error) {
			args := []string{"test"}
			if req.GetBool("e2e", false) {
				args = append(args, "--e2e")
				if req.GetBool("install", false) {
					args = append(args, "--install")
				}
				return args, nil
			}
			if req.GetBool("verbose", false) {
				args = append(args, "-v")
			}
			if run := req.GetString("run", ""); run != "" {
				args = append(args, "-run", run)
			}
			return args, nil
		}},
		{"lidza_verify", "What the pre-commit hook runs: regenerate and require the generated files to be staged, the check, the Go tests. One JSON report with a step list and the diagnostics.", []mcp.ToolOption{
			mcp.WithBoolean("no_test", mcp.Description("Skip the Go tests.")),
		}, 20 * time.Minute, true, true, func(req mcp.CallToolRequest) ([]string, error) {
			args := []string{"verify", "--json"}
			if req.GetBool("no_test", false) {
				args = append(args, "--no-test")
			}
			return args, nil
		}},
		{"lidza_build", "Build the frontend (prerendered) and compile the production binary bin/<name>.", nil, 20 * time.Minute, false, true, fixed("build")},
		{"lidza_ship", "Release build: lidza verify, the browser suite, lidza build, then deploy/production.env with the deployment settings (the TLS domains and contact, remembered in lidza.json).", []mcp.ToolOption{
			mcp.WithString("domains", mcp.Description("Domains the deployed binary serves over TLS (LIDZA_TLS_DOMAINS), comma-separated.")),
			mcp.WithString("email", mcp.Description("ACME account contact (LIDZA_TLS_EMAIL).")),
			mcp.WithBoolean("no_e2e", mcp.Description("Skip the browser suite.")),
		}, 40 * time.Minute, true, true, func(req mcp.CallToolRequest) ([]string, error) {
			args := []string{"ship"}
			if d := req.GetString("domains", ""); d != "" {
				args = append(args, "--domains", d)
			}
			if e := req.GetString("email", ""); e != "" {
				args = append(args, "--email", e)
			}
			if req.GetBool("no_e2e", false) {
				args = append(args, "--no-e2e")
			}
			return args, nil
		}},
		{"lidza_doctor", "Report the toolchain, the services, node_modules, the pack builds and the browser for e2e tests, each missing item with its fix.", nil, 2 * time.Minute, false, false, fixed("doctor")},
	}
	for _, t := range tools {
		t := t
		opts := append([]mcp.ToolOption{mcp.WithDescription(t.desc)}, t.opts...)
		s.AddTool(mcp.NewTool(t.name, opts...), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := t.args(req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if t.changes {
				projectMu.Lock()
				defer projectMu.Unlock()
			}
			res, err := runCLI(ctx, dir, t.timeout, t.jsonOut, args...)
			if err != nil {
				return mcp.NewToolResultErrorFromErr(t.name, err), nil
			}
			if t.changes {
				after()
			}
			if staleCLI() {
				res.Note = "the lidza CLI on disk is newer than this MCP server (the commands ran the new one): reconnect the server to get its tools (/mcp in Claude Code, or restart the agent)"
			}
			return jsonResult(res)
		})
	}

	s.AddTool(mcp.NewTool("lidza_recipes",
		mcp.WithDescription("The project's recipes with their scope: the framework's under \"Recipes\" and this app's own under \"App recipes\" in docs/lidza-guide.md. Each is also a prompt of this server; lidza_recipe_add records a new one."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rs, err := recipes.Load(dir)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("recipes", err), nil
		}
		type summary struct {
			Name, Title, Scope, Description string
		}
		out := make([]summary, 0, len(rs))
		for _, r := range rs {
			out = append(out, summary{r.Name, r.Title, r.Scope, r.Description})
		}
		return jsonResult(out)
	})
	_ = cfg
}

// cliStamp is when the CLI binary this server runs from was last
// changed, taken at start; staleCLI reports a newer one on disk.
var cliStamp = func() time.Time {
	if p, err := cliPath(); err == nil {
		if info, err := os.Stat(p); err == nil {
			return info.ModTime()
		}
	}
	return time.Time{}
}()

func staleCLI() bool {
	if cliStamp.IsZero() {
		return false
	}
	p, err := cliPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.ModTime().After(cliStamp)
}
