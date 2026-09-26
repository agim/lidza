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
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/recipes"
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

// Server is the MCP server of one project. Its pack tools follow
// lidza.json, its prompts the guide, its app tools tools.go: Refresh reads
// them again and connected clients are told the lists changed, so an
// agent sees a pack it just added or a recipe it just recorded without a
// restart. Only a newer CLI binary needs a reconnect.
type Server struct {
	*server.MCPServer
	dir     string
	cfg     *config.Config
	mu      sync.Mutex
	packs   *group
	app     *group
	recipes *group
	stamps  map[string]time.Time
}

// New builds the server for the project in dir. cfg may be nil for a plain
// Go module.
func New(dir string, cfg *config.Config) *Server {
	s := server.NewMCPServer("lidza", version.String(),
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
		server.WithPromptCapabilities(true),
		server.WithInstructions(instructions),
	)
	srv := &Server{MCPServer: s, dir: dir, cfg: cfg, packs: newGroup(s), app: newGroup(s), recipes: newGroup(s), stamps: map[string]time.Time{}}

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

	addCommandTools(s, dir, cfg, srv.Refresh)
	addPackTools(srv.packs, dir, cfg)
	addAppTools(srv.app, dir, cfg)
	addAPI(s, dir)
	addSnippets(s)
	addRecipes(srv.recipes, dir)
	addRecipeTool(s, dir, cfg, srv.Refresh)
	addDecisionTool(s, dir, cfg)
	addCredentialTools(s, dir, cfg)
	srv.stamps = srv.stamp()

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
	return srv
}

// watched are the files whose change moves a list: lidza.json (packs and
// their tools), the guide (prompts), tools.go (app tools).
func (srv *Server) watched() []string {
	return []string{
		filepath.Join(srv.dir, config.FileName),
		filepath.Join(srv.dir, filepath.FromSlash(recipes.GuideFile)),
		filepath.Join(srv.dir, "tools.go"),
	}
}

func (srv *Server) stamp() map[string]time.Time {
	out := map[string]time.Time{}
	for _, p := range srv.watched() {
		if info, err := os.Stat(p); err == nil {
			out[p] = info.ModTime()
		}
	}
	return out
}

// Refresh reads lidza.json, the guide and tools.go again and re-registers
// what changed: the pack tools, the prompts, the app tools. Command tools
// that change the project call it; Watch calls it when a file changes.
func (srv *Server) Refresh() {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	now := srv.stamp()
	changed := func(name string) bool {
		p := filepath.Join(srv.dir, name)
		return !now[p].Equal(srv.stamps[p])
	}
	cfgChanged, guideChanged, toolsChanged := changed(config.FileName), changed(filepath.FromSlash(recipes.GuideFile)), changed("tools.go")
	srv.stamps = now
	if cfgChanged {
		if cfg, err := config.Load(srv.dir); err == nil {
			srv.cfg = cfg
		}
		srv.packs.clear()
		addPackTools(srv.packs, srv.dir, srv.cfg)
	}
	if guideChanged {
		srv.recipes.clear()
		addRecipes(srv.recipes, srv.dir)
	}
	if toolsChanged || cfgChanged {
		srv.app.clear()
		addAppTools(srv.app, srv.dir, srv.cfg)
	}
}

// Watch calls Refresh every two seconds while a watched file changes,
// until ctx ends.
func (srv *Server) Watch(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := srv.stamp()
			srv.mu.Lock()
			same := len(now) == len(srv.stamps)
			for p, m := range now {
				if !m.Equal(srv.stamps[p]) {
					same = false
				}
			}
			srv.mu.Unlock()
			if !same {
				srv.Refresh()
			}
		}
	}
}

// Serve runs the server on stdin/stdout until the client disconnects,
// refreshing its lists as the project changes.
func Serve(dir string, cfg *config.Config) error {
	srv := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Watch(ctx)
	return server.ServeStdio(srv.MCPServer)
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
