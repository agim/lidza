package mcpserver

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/agim/lidza/pkg/apidoc"
	"github.com/agim/lidza/pkg/recipes"
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
		moduleDir, err := apidoc.ModuleDir(ctx, dir)
		if err != nil {
			return "", err
		}
		var rels []string
		if pkg != "" {
			rel, ok := apidoc.Rel(pkg)
			if !ok {
				rel = strings.TrimPrefix(pkg, "/")
			}
			if rel == "lidza" || rel == "." {
				rel = ""
			}
			if !apidoc.Exists(moduleDir, apidoc.ImportPath(rel)) {
				return "", errNoPackage(apidoc.ImportPath(rel))
			}
			rels = []string{rel}
		}
		return apidoc.Render(moduleDir, rels, filter)
	}
	s.AddTool(mcp.NewTool("lidza_api",
		mcp.WithDescription("The framework's public Go API as it is in this app's dependency: exported functions, types, methods and their doc comments. Read this before calling a lidza function; guessing a name is how imports fail. Without arguments: every package app code imports (lidza, pkg/router, pkg/middleware, pkg/lidzatest, pkg/report, pkg/resilience, pkg/env, packs/*)."),
		mcp.WithString("package", mcp.Description("One package, as an import path or relative to the module: \"pkg/router\", \"packs/auth\", \"\" for the root package lidza.")),
		mcp.WithString("filter", mcp.Description("Keep only declarations whose name contains this text (case-insensitive), e.g. \"cookie\".")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		mcp.WithTemplateDescription("One framework package's public API, e.g. lidza://api/pkg/router or lidza://api/packs/auth."),
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

type errNoPackage string

func (e errNoPackage) Error() string {
	return "no package " + string(e) + " in this version of Līdza; lidza_api without arguments lists the packages"
}
