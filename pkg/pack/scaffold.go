package pack

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/agim/lidza/pkg/schema"
)

//go:embed files
var files embed.FS

// Scaffold creates packs/<name> with a manifest, a crate holding the ABI
// and one example capability (reverse), and the schema types the example
// needs. The caller adds the name to lidza.json and runs the generators.
func Scaffold(root, name string) (*Manifest, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("pack name %q: use lowercase letters, digits and underscores", name)
	}
	dir := filepath.Join(root, Dir, name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%s exists", filepath.Join(Dir, name))
	}
	m := &Manifest{
		Name:        name,
		Version:     "0.1.0",
		Description: "Describe what the " + name + " pack computes.",
		Capabilities: []Capability{{
			Name:        "reverse",
			Description: "Reverses a string; the example capability, replace it.",
			Input:       "ReverseInput",
			Output:      "ReverseOutput",
			Rules:       "Text must not be empty.",
		}},
	}
	m.defaults()
	m.Path = filepath.ToSlash(filepath.Join(Dir, name, ManifestFile))
	if err := m.Save(root); err != nil {
		return nil, err
	}
	data := map[string]string{"Name": name}
	for _, f := range []struct{ src, dst string }{
		{"Cargo.toml.tmpl", filepath.Join(m.CrateDir(), "Cargo.toml")},
		{"lib.rs.tmpl", filepath.Join(m.CrateDir(), "src", "lib.rs")},
		{"abi.rs.tmpl", filepath.Join(m.CrateDir(), "src", "abi.rs")},
		{"gitignore.tmpl", filepath.Join(Dir, name, ".gitignore")},
	} {
		body, err := files.ReadFile("files/" + f.src)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if f.src == "abi.rs.tmpl" {
			// Verbatim: Rust source with no placeholders (and shift
			// operators that look like delimiters).
			buf.Write(body)
		} else {
			t, err := template.New(f.src).Delims("<<", ">>").Parse(string(body))
			if err != nil {
				return nil, err
			}
			if err := t.Execute(&buf, data); err != nil {
				return nil, err
			}
		}
		p := filepath.Join(root, f.dst)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			return nil, err
		}
	}
	if err := ensureExampleTypes(root); err != nil {
		return nil, err
	}
	return m, nil
}

// ensureExampleTypes appends the example capability's types to
// schema.lidza when they are missing.
func ensureExampleTypes(root string) error {
	p := filepath.Join(root, schema.FileName)
	existing, _ := os.ReadFile(p)
	s, err := schema.Parse(string(existing))
	if err != nil {
		return err
	}
	if s.Model("ReverseInput") != nil && s.Model("ReverseOutput") != nil {
		return nil
	}
	var add strings.Builder
	if len(bytes.TrimSpace(existing)) > 0 {
		add.WriteString("\n")
	}
	add.WriteString("// Types of the example pack capability \"reverse\".\n")
	if s.Model("ReverseInput") == nil {
		add.WriteString("type ReverseInput {\n  text string @min(1)\n}\n\n")
	}
	if s.Model("ReverseOutput") == nil {
		add.WriteString("type ReverseOutput {\n  reversed string\n  len      int\n}\n")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(add.String())
	return err
}
