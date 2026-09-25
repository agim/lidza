// Package pack implements Līdza packs: a directory under packs/ with a
// manifest, a Rust crate compiled to WASM, and a generated Go wrapper
// that exposes each capability as a typed method backed by the engine.
package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/agim/lidza/pkg/schema"
)

// Dir is where an app keeps its packs, relative to the project root.
const Dir = "packs"

// ManifestFile is the manifest name inside a pack directory.
const ManifestFile = "pack.lidza.json"

// Manifest is pack.lidza.json.
type Manifest struct {
	// Name is the pack's identifier: the directory, the Go package, the
	// crate and the WASM file name.
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description"`
	Rust         Rust         `json:"rust"`
	Capabilities []Capability `json:"capabilities"`
	// Path is the manifest's location, relative to the project root.
	Path string `json:"-"`
}

// Rust locates and bounds the crate.
type Rust struct {
	// Dir is the crate directory inside the pack, default "rust".
	Dir string `json:"dir,omitempty"`
	// MemoryMB caps each WASM instance's memory, default 64.
	MemoryMB int `json:"memory_mb,omitempty"`
	// Instances is the pool size, default 4.
	Instances int `json:"instances,omitempty"`
	// TimeoutMS bounds each call, default 5000.
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// Uninterruptible drops the per-loop deadline checks: loops run
	// several times faster, and a call that overruns the timeout keeps
	// its instance busy until it returns (the caller still gets the
	// error at the deadline, and the instance is replaced afterwards).
	// Set it for capabilities whose loops are bounded by their input.
	Uninterruptible bool `json:"uninterruptible,omitempty"`
}

// Capability is one exported function: JSON in, JSON out, typed by
// schema.lidza types on both sides.
type Capability struct {
	// Name is the WASM export and, exported, the Go method.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Input and Output are type names from schema.lidza.
	Input  string `json:"input"`
	Output string `json:"output"`
	// Rules are instructions for the agent using the capability.
	Rules string `json:"rules,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Load reads packs/<name>/pack.lidza.json.
func Load(root, name string) (*Manifest, error) {
	rel := filepath.Join(Dir, name, ManifestFile)
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	m.Path = filepath.ToSlash(rel)
	m.defaults()
	if m.Name != name {
		return nil, fmt.Errorf("%s: name %q does not match the directory", rel, m.Name)
	}
	return &m, nil
}

func (m *Manifest) defaults() {
	if m.Rust.Dir == "" {
		m.Rust.Dir = "rust"
	}
	if m.Rust.MemoryMB <= 0 {
		m.Rust.MemoryMB = 64
	}
	if m.Rust.Instances <= 0 {
		m.Rust.Instances = 4
	}
	if m.Rust.TimeoutMS <= 0 {
		m.Rust.TimeoutMS = 5000
	}
}

// Save writes the manifest.
func (m *Manifest) Save(root string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(root, Dir, m.Name, ManifestFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// CrateDir is the crate directory, relative to the project root.
func (m *Manifest) CrateDir() string { return filepath.Join(Dir, m.Name, m.Rust.Dir) }

// WasmFile is the built module, relative to the project root, embedded by
// the generated Go wrapper.
func (m *Manifest) WasmFile() string { return filepath.Join(Dir, m.Name, m.Name+".wasm") }

// GoFile is the generated wrapper, relative to the project root.
func (m *Manifest) GoFile() string { return filepath.Join(Dir, m.Name, "pack.go") }

// Problem is one validation finding, located in the manifest.
type Problem struct {
	Message string
}

// Validate checks the manifest against itself, the app schema (nil to
// skip type checks) and the module's exports (nil to skip).
func (m *Manifest) Validate(s *schema.Schema, exports []string) []Problem {
	var out []Problem
	add := func(format string, a ...any) { out = append(out, Problem{fmt.Sprintf(format, a...)}) }
	if !nameRe.MatchString(m.Name) {
		add("name %q: use lowercase letters, digits and underscores", m.Name)
	}
	if len(m.Capabilities) == 0 {
		add("no capabilities declared")
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !nameRe.MatchString(c.Name) {
			add("capability %q: use lowercase letters, digits and underscores", c.Name)
			continue
		}
		if seen[c.Name] {
			add("capability %q declared twice", c.Name)
		}
		seen[c.Name] = true
		if strings.HasPrefix(c.Name, "lidza_") {
			add("capability %q: the lidza_ prefix is reserved", c.Name)
		}
		for _, t := range []struct{ kind, name string }{{"input", c.Input}, {"output", c.Output}} {
			if t.name == "" {
				add("capability %q: %s type is required", c.Name, t.kind)
			} else if s != nil && s.Model(t.name) == nil {
				add("capability %q: %s type %s is not declared in %s", c.Name, t.kind, t.name, schema.FileName)
			}
		}
		if exports != nil && !slices.Contains(exports, c.Name) {
			add("capability %q is not exported by %s: add lidza_export!(%s, ...) to the crate", c.Name, m.WasmFile(), c.Name)
		}
	}
	if exports != nil {
		for _, need := range []string{"lidza_alloc", "lidza_free"} {
			if !slices.Contains(exports, need) {
				add("%s does not export %s: the crate must include abi.rs", m.WasmFile(), need)
			}
		}
	}
	return out
}

// List returns the manifests of the local packs lidza.json enables, in
// order; official Go packs have none.
func List(root string, names []string) ([]*Manifest, error) {
	var out []*Manifest
	for _, name := range names {
		if IsOfficialGo(name) {
			continue
		}
		m, err := Load(root, name)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
