package lidza

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/jsonschema"
	"github.com/agim/lidza/pkg/validate"
	"github.com/agim/lidza/pkg/version"
)

// Tool is an MCP tool the app defines: a name, a description, a JSON
// input and a handler that runs with the app's services (packs, clock,
// logger) in its context. `lidza mcp` exposes app tools next to the
// framework's; with LIDZA_MCP_TOKEN set, the running binary serves them
// at /mcp (Streamable HTTP) to agents that present the token.
type Tool struct {
	Name        string
	Description string
	// Input is a value of the input type; its JSON Schema is derived from
	// the Go type. nil means no input.
	Input any
	// Handler receives the raw JSON input and returns a JSON-encodable
	// result or an error the caller sees.
	Handler func(ctx context.Context, input json.RawMessage) (any, error)
}

// ToolFunc builds a Tool from a typed function. In is decoded from the
// input and validated when it implements validate.Validator.
func ToolFunc[In, Out any](name, description string, fn func(ctx context.Context, in In) (Out, error)) Tool {
	var zero In
	return Tool{
		Name:        name,
		Description: description,
		Input:       zero,
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in In
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, fmt.Errorf("invalid input: %w", err)
				}
			}
			if v, ok := any(in).(validate.Validator); ok {
				if err := v.Validate(); err != nil {
					return nil, err
				}
			}
			return fn(ctx, in)
		},
	}
}

// Environment for app tools.
const (
	// EnvMCP set to "stdio" makes the binary serve its tools over stdio
	// instead of HTTP; `lidza mcp` uses it.
	EnvMCP = "LIDZA_MCP"
	// EnvMCPToken enables /mcp on the running binary for clients that
	// send it as a bearer token.
	EnvMCPToken = "LIDZA_MCP_TOKEN"
	// MCPPath is the Streamable HTTP endpoint.
	MCPPath = "/mcp"
)

// mcpServer builds the MCP server for the app's tools. Handlers run with
// the services in their context.
func mcpServer(app App, services *Services) *server.MCPServer {
	s := server.NewMCPServer(name(app), version.String(), server.WithToolCapabilities(false),
		server.WithInstructions("Tools of the "+name(app)+" application. Each runs inside the app with its packs and database."))
	for _, t := range app.Tools {
		tool := t
		schema, _ := json.Marshal(jsonschema.Of(tool.Input))
		if tool.Input == nil {
			schema = []byte(`{"type":"object","properties":{}}`)
		}
		s.AddTool(mcp.NewToolWithRawSchema(tool.Name, tool.Description, schema),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				raw, err := json.Marshal(req.GetArguments())
				if err != nil {
					return mcp.NewToolResultErrorFromErr("arguments", err), nil
				}
				ctx = WithServices(ctx, services)
				out, err := tool.Handler(ctx, raw)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				data, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return nil, err
				}
				return mcp.NewToolResultText(string(data)), nil
			})
	}
	return s
}

// serveToolsStdio boots the app and serves its tools over stdio until the
// client disconnects.
func serveToolsStdio(ctx context.Context, app App) error {
	booted, err := Boot(ctx, app)
	if err != nil {
		return err
	}
	defer booted.Close(context.Background())
	fmt.Fprintf(os.Stderr, "%s: serving %d tool(s) over stdio\n", name(app), len(app.Tools))
	return server.ServeStdio(mcpServer(app, booted.Services))
}

// mcpHTTP returns the /mcp handler guarded by the token, or nil when the
// app has no tools or no token is configured.
func mcpHTTP(app App, services *Services) http.Handler {
	token := os.Getenv(EnvMCPToken)
	if len(app.Tools) == 0 || token == "" {
		return nil
	}
	inner := server.NewStreamableHTTPServer(mcpServer(app, services), server.WithEndpointPath(MCPPath), server.WithStateLess(true))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"mcp: bearer token required"}` + "\n"))
			return
		}
		inner.ServeHTTP(w, r)
	})
}
