package main

import (
	"context"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
)

// tools lists the app's MCP tools: functions an agent can call through
// `lidza mcp` (as app_<name>) and, with LIDZA_MCP_TOKEN set, through the
// running binary at /mcp. They run inside the app with its packs.
func tools() []lidza.Tool {
	return []lidza.Tool{
		lidza.ToolFunc("count_notes", "Number of notes over all users.", func(ctx context.Context, _ struct{}) (int64, error) {
			var n int64
			err := db.From(ctx).QueryRow(ctx, "SELECT count(*) FROM note").Scan(&n)
			return n, err
		}),
	}
}
