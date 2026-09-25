// Package mcpserver is `lidza mcp`: a Model Context Protocol server over
// stdio that gives an agent the project's route map, context, diagnostics,
// configuration and the runtime log of `lidza dev`, so it asks instead of
// grepping.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/version"
)

// LogFile is where `lidza dev` writes its combined output, relative to the
// project root; lidza_logs reads it.
const LogFile = devserver.BuildDir + "/dev.log"

const instructions = `Līdza project. Go owns /api (routes.go); the frontend never defines API routes.
Every lidza command is a tool here: prefer lidza_check, lidza_gen,
lidza_gen_resource, lidza_pack_add, lidza_db_migrate, lidza_test, lidza_verify
and lidza_build over running the CLI in a shell; each returns one JSON result.
Use lidza_routes before adding a route, lidza_api before calling a framework
function, lidza_check after every change (fix until its status is "ok"),
lidza_logs when a request misbehaves under lidza dev, lidza_snippet for
verified code of the reference app, and lidza_recipe_add to record a
convention of this app. The prompts are the project's task recipes
(add-api-route, add-resource, ...); follow one step by step.`

// New builds the server for the project in dir. cfg may be nil for a plain
// Go module.
func New(dir string, cfg *config.Config) *server.MCPServer {
	s := server.NewMCPServer("lidza", version.String(),
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
		server.WithInstructions(instructions),
	)

	s.AddTool(mcp.NewTool("lidza_routes",
		mcp.WithDescription("List the API routes: method, path, handler name, file and line, signature. Built-in routes are marked."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, err := inspect.Project(dir, cfg)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("inspect", err), nil
		}
		return jsonResult(c.Routes)
	})

	s.AddTool(mcp.NewTool("lidza_context",
		mcp.WithDescription("The whole project as JSON: app, module, template, routes with handler signatures, Rust crate exports. Same content as .lidza/context.json."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, err := inspect.Project(dir, cfg)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("inspect", err), nil
		}
		return jsonResult(c)
	})

	s.AddTool(mcp.NewTool("lidza_logs",
		mcp.WithDescription("The last lines of the lidza dev output: [web] frontend dev server, [go] build errors, [app] the running app, [lidza] the coordinator."),
		mcp.WithNumber("lines", mcp.Description("How many lines from the end (default 200)."), mcp.DefaultNumber(200)),
		mcp.WithString("filter", mcp.Description("Only lines containing this text, e.g. \"[app]\" or \"error\".")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := tail(filepath.Join(dir, LogFile), req.GetInt("lines", 200), req.GetString("filter", ""))
		if err != nil {
			return mcp.NewToolResultError("no log: is `lidza dev` running in this project?"), nil
		}
		return mcp.NewToolResultText(text), nil
	})

	s.AddTool(mcp.NewTool("lidza_config",
		mcp.WithDescription("The project's lidza.json."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if cfg == nil {
			return mcp.NewToolResultError("no lidza.json: this is a plain Go module"), nil
		}
		return jsonResult(cfg)
	})

	addCommandTools(s, dir, cfg)
	addPackTools(s, dir, cfg)
	addAppTools(s, dir, cfg)
	addAPI(s, dir)
	addSnippets(s)
	addRecipes(s, dir)
	addRecipeTool(s, dir, cfg)
	addDecisionTool(s, dir, cfg)

	for _, r := range []struct{ name, uri, desc string }{
		{"llms.txt", "lidza://llms.txt", "Short description of the app for language models: routes, commands, files."},
		{"llms-full.txt", "lidza://llms-full.txt", "The project guide, every handler signature, Rust exports and configuration."},
	} {
		full := strings.HasPrefix(r.name, "llms-full")
		s.AddResource(mcp.NewResource(r.uri, r.name, mcp.WithResourceDescription(r.desc), mcp.WithMIMEType("text/plain")),
			func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
				short, long, err := inspect.LLMSFor(dir, cfg)
				if err != nil {
					return nil, err
				}
				text := short
				if full {
					text = long
				}
				return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "text/plain", Text: text}}, nil
			})
	}
	return s
}

// Serve runs the server on stdin/stdout until the client disconnects.
func Serve(dir string, cfg *config.Config) error {
	return server.ServeStdio(New(dir, cfg))
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(data)), nil
}

// tail returns the last n lines of the file, optionally only those
// containing filter.
func tail(path string, n int, filter string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if filter != "" {
		kept := lines[:0]
		for _, l := range lines {
			if strings.Contains(l, filter) {
				kept = append(kept, l)
			}
		}
		lines = kept
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return fmt.Sprintf("(empty: %s)", path), nil
	}
	return strings.Join(lines, "\n"), nil
}
