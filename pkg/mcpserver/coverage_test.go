package mcpserver

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/pack"
)

// commands reads the command names lidza's main switch handles.
func commands(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../../cmd/lidza/main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range cc.List {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && s != "" && s[0] != '-' {
					out = append(out, s)
				}
			}
		}
		return true
	})
	return out
}

// TestCoverage: every lidza command has its tools or a reason to stay in
// a terminal, every official Go pack has an inspection tool or a reason,
// and every tool the tables name is served when the packs are enabled.
func TestCoverage(t *testing.T) {
	cmds := commands(t)
	if len(cmds) < 20 {
		t.Fatalf("read %d commands from main.go: the switch moved?", len(cmds))
	}
	for _, c := range cmds {
		_, tools := CommandTools[c]
		_, terminal := TerminalOnly[c]
		if !tools && !terminal {
			t.Errorf("lidza %s has no MCP tool and no TerminalOnly reason (pkg/mcpserver/coverage.go)", c)
		}
	}
	var packs []string
	for _, o := range pack.Officials {
		if o.Rust {
			continue // their capabilities are tools of their own (pack_<name>_<capability>)
		}
		_, tools := PackTools[o.Name]
		_, none := PackNoTool[o.Name]
		if !tools && !none {
			t.Errorf("pack %s has no inspection tool and no PackNoTool reason (pkg/mcpserver/coverage.go)", o.Name)
		}
		packs = append(packs, pack.OfficialPrefix+o.Name)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n"), 0o644)
	cfg := config.Default("demo", "react")
	cfg.Packs = packs
	c, err := client.NewInProcessClient(New(dir, &cfg).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	served := map[string]bool{}
	for _, tl := range list.Tools {
		served[tl.Name] = true
	}
	for _, m := range []map[string][]string{CommandTools, PackTools} {
		for owner, tools := range m {
			for _, name := range tools {
				if !served[name] {
					t.Errorf("%s: tool %s is not served", owner, name)
				}
			}
		}
	}
}
