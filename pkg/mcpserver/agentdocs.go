package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/apidoc"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/recipes"
	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/snippets"
)

// addRecipes serves the guide's recipes as prompts: one per level-3
// heading under "## Recipes" in docs/lidza-guide.md, with an optional task
// argument appended. The guide is re-read on every request; the list of
// prompts is taken when the server starts.
func addRecipes(s *server.MCPServer, dir string) {
	rs, err := recipes.Load(dir)
	if err != nil {
		return
	}
	for _, r := range rs {
		name := r.Name
		s.AddPrompt(mcp.NewPrompt(name,
			mcp.WithPromptDescription(r.Description),
			mcp.WithArgument("task", mcp.ArgumentDescription("What to add or change, in one line (optional).")),
		), func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			current, err := recipes.Load(dir)
			if err != nil {
				return nil, err
			}
			for _, c := range current {
				if c.Name != name {
					continue
				}
				text := "# " + c.Title + "\n\n" + c.Body
				if task := strings.TrimSpace(req.Params.Arguments["task"]); task != "" {
					text += "\n\nTask: " + task
				}
				return mcp.NewGetPromptResult(c.Title, []mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(text))}), nil
			}
			return nil, errNoRecipe(name)
		})
	}
}

// addRecipeTool lets an agent record a convention of the app as a recipe
// under "App recipes" in the guide; the prompts, skills and commands are
// regenerated at once (the prompt list of this server updates on restart).
func addRecipeTool(s *server.MCPServer, dir string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.AddTool(mcp.NewTool("lidza_recipe_add",
		mcp.WithDescription("Record one of this app's conventions as a recipe (a pattern used twice: how lists paginate, how ownership is checked, ...). It is appended under \"App recipes\" in docs/lidza-guide.md and becomes a prompt, a skill for Claude Code and Codex, and a Gemini command; the agent files list it. Give numbered steps that name files, functions and commands, and point at a file in this app that already does it."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Imperative title, e.g. \"Paginate a list\".")),
		mcp.WithString("description", mcp.Description("When the recipe applies, one or two sentences.")),
		mcp.WithArray("steps", mcp.Description("The steps in order; each names the file, the function or type, the command, and ends with the check."), mcp.Items(map[string]any{"type": "string"})),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var steps []string
		for _, v := range req.GetStringSlice("steps", nil) {
			if strings.TrimSpace(v) != "" {
				steps = append(steps, v)
			}
		}
		r, err := recipes.Add(dir, req.GetString("title", ""), req.GetString("description", ""), steps)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("recipe", err), nil
		}
		if _, err := scaffold.Refresh(dir, cfg); err != nil {
			return mcp.NewToolResultErrorFromErr("recipe", err), nil
		}
		return jsonResult(map[string]any{"name": r.Name, "title": r.Title, "scope": r.Scope, "skill": recipes.SkillsDir + "/" + r.Name + "/SKILL.md", "note": "the prompt appears after lidza mcp restarts; the skill and command are in place"})
	})
}

// addDecisionTool records an entry in the app's decision log.
func addDecisionTool(s *server.MCPServer, dir string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.AddTool(mcp.NewTool("lidza_decision_add",
		mcp.WithDescription("Record why this app is built a way in docs/decisions.md: a pack added, Rust chosen for a module (say which of contained input, a crate Go lacks, or heap pressure), a dependency taken, a schema tradeoff, an integration. Call it in the same change, before lidza_verify. Read the file first (lidza://decisions or Read) when working in those areas."),
		mcp.WithString("title", mcp.Required(), mcp.Description("What was decided, e.g. \"Word statistics in a Rust pack\".")),
		mcp.WithString("why", mcp.Required(), mcp.Description("The reason, one or two sentences, with the alternative that was not taken.")),
		mcp.WithString("touches", mcp.Description("The files, packs or tables it concerns.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		e, err := decisions.Add(dir, req.GetString("title", ""), req.GetString("why", ""), req.GetString("touches", ""))
		if err != nil {
			return mcp.NewToolResultErrorFromErr("decision", err), nil
		}
		return jsonResult(map[string]any{"file": decisions.File, "date": e.Date, "title": e.Title})
	})
	s.AddResource(mcp.NewResource("lidza://decisions", "decisions", mcp.WithResourceDescription("This app's decision log, docs/decisions.md: why it is built the way it is."), mcp.WithMIMEType("text/markdown")),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(decisions.File)))
			if err != nil {
				return nil, err
			}
			return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "text/markdown", Text: string(data)}}, nil
		})
}

type errNoRecipe string

func (e errNoRecipe) Error() string {
	return "recipe " + string(e) + " is no longer in " + recipes.GuideFile
}

// addAPI serves the framework's public Go API, rendered from the sources
// the app resolves: the resource lidza://api (every package app code
// imports), lidza://api/{package} for one, and the tool lidza_api with a
// package and a name filter.
func addAPI(s *server.MCPServer, dir string) {
	render := func(ctx context.Context, pkg, filter string) (string, error) {
		src, rels, err := apidoc.Resolve(ctx, dir, pkg)
		if err != nil {
			return "", err
		}
		return src.Render(rels, filter)
	}
	s.AddTool(mcp.NewTool("lidza_api",
		mcp.WithDescription("Public Go API with doc comments, rendered from the sources: the framework as this app depends on it, or this app's own packages. Read it before calling a function; guessing a name is how imports fail. Without arguments: every framework package app code imports (lidza, pkg/router, pkg/middleware, pkg/lidzatest, pkg/report, pkg/resilience, pkg/env, packs/*). \"app\": every package of this app. \"list\": the names of both."),
		mcp.WithString("package", mcp.Description("A framework package as an import path or relative path (\"pkg/router\", \"packs/auth\", \"\" for the root package lidza); one of this app's as \"./handlers\" or its import path; \"app\" for all of this app's; \"list\" for the names.")),
		mcp.WithString("filter", mcp.Description("Keep only declarations whose name contains this text (case-insensitive), e.g. \"cookie\".")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req.GetString("package", "") == "list" {
			text, err := apidoc.Listing(ctx, dir)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("api", err), nil
			}
			return mcp.NewToolResultText(text), nil
		}
		text, err := render(ctx, req.GetString("package", ""), req.GetString("filter", ""))
		if err != nil {
			return mcp.NewToolResultErrorFromErr("api", err), nil
		}
		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultText("(no declaration matches)"), nil
		}
		return mcp.NewToolResultText(text), nil
	})
	s.AddResource(mcp.NewResource("lidza://api", "api",
		mcp.WithResourceDescription("The framework's public Go API: every package app code imports, rendered from the version this app uses."),
		mcp.WithMIMEType("text/markdown")),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			text, err := render(ctx, "", "")
			if err != nil {
				return nil, err
			}
			return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "text/markdown", Text: text}}, nil
		})
	s.AddResourceTemplate(mcp.NewResourceTemplate("lidza://api/{+package}", "api package",
		mcp.WithTemplateDescription("One package's public API: lidza://api/pkg/router or lidza://api/packs/auth for the framework, lidza://api/app for this app's packages."),
		mcp.WithTemplateMIMEType("text/markdown")),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			pkg := strings.TrimPrefix(req.Params.URI, "lidza://api/")
			text, err := render(ctx, pkg, "")
			if err != nil {
				return nil, err
			}
			return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "text/markdown", Text: text}}, nil
		})
}

// addSnippets serves the reference app's files: the tool lidza_snippet
// (by name; without one, the catalog) and the resource lidza://snippets.
func addSnippets(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("lidza_snippet",
		mcp.WithDescription("A file of the Līdza reference app (examples/notes), verified by its tests: how an owned resource, auth routes, a page on the generated client, a handler test, a browser test or an MCP tool are written. Copy from it instead of guessing. Names: "+strings.Join(snippets.Names(), ", ")+". Without a name, the catalog."),
		mcp.WithString("name", mcp.Description("Which snippet: "+strings.Join(snippets.Names(), ", ")+"."), mcp.Enum(snippets.Names()...)),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("name", "")
		if name == "" {
			return mcp.NewToolResultText(snippets.Catalog()), nil
		}
		sn, content, ok := snippets.Get(name)
		if !ok {
			return mcp.NewToolResultError("no snippet " + name + "; names: " + strings.Join(snippets.Names(), ", ")), nil
		}
		return mcp.NewToolResultText(snippets.Header(sn) + "\n\n" + content), nil
	})
	s.AddResource(mcp.NewResource("lidza://snippets", "snippets",
		mcp.WithResourceDescription("Catalog of the reference app's files served by lidza_snippet."),
		mcp.WithMIMEType("text/plain")),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "text/plain", Text: snippets.Catalog()}}, nil
		})
}
