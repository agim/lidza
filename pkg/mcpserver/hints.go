package mcpserver

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// hint is what a tool tells the client about itself: whether it changes
// anything, whether a change can lose data, whether running it twice is
// the same as once, whether it reaches outside the project. Clients
// decide from it what to run without asking and what to run in
// parallel; a tool without one counts as destructive.
type hint struct{ readOnly, destructive, idempotent, openWorld bool }

var (
	reads    = hint{readOnly: true, idempotent: true}
	adds     = hint{}
	replaces = hint{idempotent: true}
)

// hints are the built-in tools' hints, except the commands, which set
// their own (commands.go). Every lidza_ tool has one: TestToolHints.
var hints = map[string]hint{
	"lidza_routes":           reads,
	"lidza_logs":             reads,
	"lidza_config":           reads,
	"lidza_api":              reads,
	"lidza_snippet":          reads,
	"lidza_recipes":          reads,
	"lidza_packs":            reads,
	"lidza_credentials_list": reads,
	"lidza_brief":            reads,
	"lidza_errors":           reads,
	"lidza_llm_usage":        reads,
	"lidza_storage":          reads,
	"lidza_mail":             reads,
	"lidza_context":          replaces, // rewrites .lidza/context.json
	"lidza_recipe_add":       adds,
	"lidza_decision_add":     adds,
	"lidza_note_add":         adds,
	"lidza_brief_answer":     adds,
	"lidza_brief_skip":       adds,
	// Replaces a sealed value: the old one is gone.
	"lidza_credentials_set": {destructive: true, idempotent: true},
	// Sends a prompt to the configured model provider.
	"lidza_llm": {openWorld: true},
}

// annotate is the tool filter that puts the hints on the listed tools.
func annotate(_ context.Context, tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		if h, ok := hints[t.Name]; ok {
			t.Annotations.ReadOnlyHint = mcp.ToBoolPtr(h.readOnly)
			t.Annotations.DestructiveHint = mcp.ToBoolPtr(h.destructive)
			t.Annotations.IdempotentHint = mcp.ToBoolPtr(h.idempotent)
			t.Annotations.OpenWorldHint = mcp.ToBoolPtr(h.openWorld)
		}
		out[i] = t
	}
	return out
}

// builtin reports whether a tool is the framework's (not an app's or a
// pack's capability).
func builtin(name string) bool { return strings.HasPrefix(name, "lidza_") }
