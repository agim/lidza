package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
)

// appToolPrefix marks tools that come from the app itself.
const appToolPrefix = "app_"

// addAppTools builds the app, starts it in tool-serving mode (LIDZA_MCP=
// stdio, so its packs and database are live) and mirrors every tool it
// declares as app_<name>. Failures are reported on stderr and leave the
// framework tools working.
func addAppTools(s *server.MCPServer, dir string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "tools.go")); err != nil {
		return
	}
	bin := filepath.Join(dir, devserver.BuildDir, "mcp-app")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "lidza mcp: app tools unavailable, build failed:\n%s", out)
		return
	}
	env := append(os.Environ(), "LIDZA_MCP=stdio", devserver.EnvMode+"=dev")
	c, err := client.NewStdioMCPClient(bin, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lidza mcp: app tools unavailable: %v\n", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		fmt.Fprintf(os.Stderr, "lidza mcp: app tools unavailable: %v\n", err)
		c.Close()
		return
	}
	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "lidza mcp: app tools unavailable: %v\n", err)
		c.Close()
		return
	}
	for _, t := range list.Tools {
		tool := t
		schema, _ := json.Marshal(tool.InputSchema)
		s.AddTool(mcp.NewToolWithRawSchema(appToolPrefix+tool.Name, "App tool: "+tool.Description, schema),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				call := mcp.CallToolRequest{}
				call.Params.Name = tool.Name
				call.Params.Arguments = req.GetArguments()
				return c.CallTool(ctx, call)
			})
	}
	fmt.Fprintf(os.Stderr, "lidza mcp: %d app tool(s) from %s\n", len(list.Tools), bin)
}
