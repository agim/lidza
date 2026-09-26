package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// group registers tools, prompts and resources on the server and
// remembers their names, so a refresh can remove them and register the
// current set: the pack tools follow lidza.json, the prompts follow the
// guide, the app tools follow tools.go.
type group struct {
	s         *server.MCPServer
	tools     []string
	prompts   []string
	resources []string
	closers   []func()
}

func newGroup(s *server.MCPServer) *group { return &group{s: s} }

func (g *group) AddTool(t mcp.Tool, h server.ToolHandlerFunc) {
	g.tools = append(g.tools, t.Name)
	g.s.AddTool(t, h)
}

func (g *group) AddPrompt(p mcp.Prompt, h server.PromptHandlerFunc) {
	g.prompts = append(g.prompts, p.Name)
	g.s.AddPrompt(p, h)
}

func (g *group) AddResource(r mcp.Resource, h server.ResourceHandlerFunc) {
	g.resources = append(g.resources, r.URI)
	g.s.AddResource(r, h)
}

// onClose runs when the group is cleared (a client to close).
func (g *group) onClose(f func()) { g.closers = append(g.closers, f) }

// clear removes everything the group registered; the server tells
// connected clients the lists changed.
func (g *group) clear() {
	if len(g.tools) > 0 {
		g.s.DeleteTools(g.tools...)
	}
	if len(g.prompts) > 0 {
		g.s.DeletePrompts(g.prompts...)
	}
	if len(g.resources) > 0 {
		g.s.DeleteResources(g.resources...)
	}
	for _, f := range g.closers {
		f()
	}
	g.tools, g.prompts, g.resources, g.closers = nil, nil, nil, nil
}
