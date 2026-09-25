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
	"github.com/agim/lidza/pkg/engine"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

// packRunner keeps one small pool per pack module, rebuilt when the WASM
// file changes, so an agent can call a capability straight from MCP.
type packRunner struct {
	mu    sync.Mutex
	dir   string
	pools map[string]*runningPack
}

type runningPack struct {
	modTime time.Time
	module  *engine.Module
	pool    *engine.Pool
}

func (r *packRunner) pool(ctx context.Context, m *pack.Manifest) (*engine.Pool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(r.dir, m.WasmFile())
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("pack %s is not built: run `lidza pack build %s`", m.Name, m.Name)
	}
	if rp, ok := r.pools[m.Name]; ok {
		if rp.modTime.Equal(info.ModTime()) {
			return rp.pool, nil
		}
		rp.pool.Close(ctx)
		rp.module.Close(ctx)
		delete(r.pools, m.Name)
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mod, err := engine.Compile(ctx, wasm, engine.Options{MemoryMB: m.Rust.MemoryMB})
	if err != nil {
		return nil, err
	}
	pool, err := engine.NewPool(ctx, mod, 1, time.Duration(m.Rust.TimeoutMS)*time.Millisecond)
	if err != nil {
		mod.Close(ctx)
		return nil, err
	}
	r.pools[m.Name] = &runningPack{modTime: info.ModTime(), module: mod, pool: pool}
	return pool, nil
}

// addPackTools registers lidza_packs and one tool per capability of every
// enabled pack, with the capability's input type as the tool schema.
func addPackTools(s *server.MCPServer, dir string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	runner := &packRunner{dir: dir, pools: map[string]*runningPack{}}

	s.AddTool(mcp.NewTool("lidza_packs",
		mcp.WithDescription("The enabled packs: capabilities, their input and output types, rules, and whether the module is built."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		type capInfo struct {
			pack.Capability
			Tool string `json:"tool"`
		}
		type info struct {
			Name         string    `json:"name"`
			Version      string    `json:"version"`
			Description  string    `json:"description"`
			Built        bool      `json:"built"`
			Capabilities []capInfo `json:"capabilities"`
		}
		out := []info{}
		for _, name := range cfg.Packs {
			m, err := pack.Load(dir, name)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("pack "+name, err), nil
			}
			i := info{Name: m.Name, Version: m.Version, Description: m.Description, Built: !pack.NeedsBuild(dir, m)}
			for _, c := range m.Capabilities {
				i.Capabilities = append(i.Capabilities, capInfo{c, "pack_" + m.Name + "_" + c.Name})
			}
			out = append(out, i)
		}
		return jsonResult(out)
	})

	sch, _ := schema.Load(dir)
	var defs map[string]any
	if sch != nil {
		defs = schema.JSONSchema(sch)
	}
	for _, name := range cfg.Packs {
		m, err := pack.Load(dir, name)
		if err != nil {
			continue
		}
		for _, c := range m.Capabilities {
			inputSchema := map[string]any{"type": "object"}
			if def, ok := defs[c.Input].(map[string]any); ok {
				inputSchema = inlineRefs(def, defs)
			}
			raw, _ := json.Marshal(inputSchema)
			desc := fmt.Sprintf("Pack %s: %s Input: %s, output: %s.", m.Name, c.Description, c.Input, c.Output)
			if c.Rules != "" {
				desc += " Rules: " + c.Rules
			}
			capability, manifest := c, m
			s.AddTool(mcp.NewToolWithRawSchema("pack_"+m.Name+"_"+c.Name, desc, raw),
				func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					pool, err := runner.pool(ctx, manifest)
					if err != nil {
						return mcp.NewToolResultError(err.Error()), nil
					}
					input, err := json.Marshal(req.GetArguments())
					if err != nil {
						return mcp.NewToolResultErrorFromErr("arguments", err), nil
					}
					out, err := pool.Call(ctx, capability.Name, input)
					if err != nil {
						return mcp.NewToolResultError(err.Error()), nil
					}
					return mcp.NewToolResultText(string(out)), nil
				})
		}
	}
}

// inlineRefs replaces $ref to other schema definitions with the definition
// itself, since a tool schema has no components section.
func inlineRefs(def map[string]any, defs map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range def {
		switch val := v.(type) {
		case map[string]any:
			if ref, ok := val["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if target, ok := defs[name].(map[string]any); ok {
					out[k] = inlineRefs(target, defs)
					continue
				}
			}
			out[k] = inlineRefs(val, defs)
		case []any:
			list := make([]any, len(val))
			for i, item := range val {
				if m, ok := item.(map[string]any); ok {
					list[i] = inlineRefs(m, defs)
				} else {
					list[i] = item
				}
			}
			out[k] = list
		default:
			out[k] = v
		}
	}
	return out
}
