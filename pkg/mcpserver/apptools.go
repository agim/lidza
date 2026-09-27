package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
)

// appToolPrefix marks tools that come from the app itself.
const appToolPrefix = "app_"

// addAppTools builds the app, starts it in tool-serving mode (LIDZA_MCP=
// stdio, so its packs and database are live) and mirrors every tool it
// declares as app_<name>. The instance runs no job workers (the app under
// lidza dev does), and a call after a Go source changed rebuilds and
// restarts it first, so a tool never runs yesterday's code. Failures are
// reported on stderr and leave the framework tools working.
func addAppTools(s *group, dir string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "tools.go")); err != nil {
		return
	}
	a := &appInstance{dir: dir, bin: filepath.Join(dir, devserver.BuildDir, "mcp-app")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "lidza mcp: app tools unavailable: %v\n", err)
		return
	}
	list, err := a.c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "lidza mcp: app tools unavailable: %v\n", err)
		a.close()
		return
	}
	s.onClose(a.close)
	for _, t := range list.Tools {
		tool := t
		schema, _ := json.Marshal(tool.InputSchema)
		s.AddTool(mcp.NewToolWithRawSchema(appToolPrefix+tool.Name, "App tool: "+tool.Description, schema),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				call := mcp.CallToolRequest{}
				call.Params.Name = tool.Name
				call.Params.Arguments = req.GetArguments()
				return a.call(ctx, call)
			})
	}
	fmt.Fprintf(os.Stderr, "lidza mcp: %d app tool(s) from %s\n", len(list.Tools), a.bin)
}

// appInstance is the app binary serving its tools to lidza mcp.
type appInstance struct {
	dir, bin string
	mu       sync.Mutex
	c        *client.Client
	built    time.Time // when the sources were read for the running build
}

// start builds the binary and starts it; the caller holds mu or owns a.
func (a *appInstance) start(ctx context.Context) error {
	built := time.Now()
	build := exec.CommandContext(ctx, "go", "build", "-o", a.bin, ".")
	build.Dir = a.dir
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build failed:\n%s", out)
	}
	// JOBS_WORKERS=0: this instance serves tools only; background jobs
	// run in the app under lidza dev, on its current build.
	env := append(os.Environ(), "LIDZA_MCP=stdio", devserver.EnvMode+"=dev", "JOBS_WORKERS=0")
	c, err := client.NewStdioMCPClient(a.bin, env)
	if err != nil {
		return err
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		c.Close()
		return err
	}
	a.c, a.built = c, built
	return nil
}

// call runs a tool, rebuilding and restarting the instance first when a
// Go source is newer than its build.
func (a *appInstance) call(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.c == nil || devserver.NewestSource(a.dir).After(a.built) {
		a.closeLocked()
		if err := a.start(ctx); err != nil {
			return mcp.NewToolResultError("the app did not rebuild: " + err.Error()), nil
		}
	}
	return a.c.CallTool(ctx, req)
}

func (a *appInstance) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeLocked()
}

func (a *appInstance) closeLocked() {
	if a.c != nil {
		a.c.Close()
		a.c = nil
	}
}
