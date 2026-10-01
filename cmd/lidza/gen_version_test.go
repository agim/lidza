package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/version"
)

func TestGenerationVersion(t *testing.T) {
	for _, tc := range []struct {
		name, cli, require, replace string
		blocked                     bool
	}{
		{"matching", "v0.1.43", "v0.1.43", "", false},
		{"older CLI", "v0.1.31", "v0.1.43", "", true},
		{"newer CLI", "v0.1.44", "v0.1.43", "", true},
		{"development CLI", "dev", "v0.1.43", "", false},
		{"local replacement", "v0.1.44", "v0.1.43", "replace github.com/agim/lidza => ../lidza\n", false},
		{"version replacement", "v0.1.44", "v0.1.43", "replace github.com/agim/lidza => github.com/agim/lidza v0.1.44\n", false},
		{"replacement mismatch", "v0.1.43", "v0.1.43", "replace github.com/agim/lidza => github.com/agim/lidza v0.1.44\n", true},
		{"unrelated replacement", "v0.1.44", "v0.1.43", "replace example.com/pkg => ../pkg\n", true},
		{"different replaced version", "v0.1.44", "v0.1.43", "replace github.com/agim/lidza v0.1.42 => ../lidza\n", true},
		{"matching pseudo version", "v0.1.44-0.20260930123456-0123456789ab", "v0.1.44-0.20260930123456-0123456789ab", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			data := "module example.com/app\n\nrequire github.com/agim/lidza " + tc.require + "\n\n" + tc.replace
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			err := generationVersion(dir, tc.cli)
			if (err != nil) != tc.blocked {
				t.Fatalf("blocked=%v: %v", tc.blocked, err)
			}
			if tc.blocked && (!strings.Contains(err.Error(), "go install") || !strings.Contains(err.Error(), "before rewriting")) {
				t.Fatalf("missing recovery instructions: %v", err)
			}
		})
	}
}

func TestGenerationMismatchLeavesPackSchemaUntouched(t *testing.T) {
	dir := t.TempDir()
	before := []byte("model AuthSession {\n  id string @id\n  remember bool @default(true)\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "schema.lidza"), before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\nrequire github.com/agim/lidza v0.1.43\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := version.Version
	version.Version = "v0.1.31"
	t.Cleanup(func() { version.Version = previous })
	if err := generateAll(dir, &config.Config{Packs: []string{"lidza/auth"}}, io.Discard); err == nil {
		t.Fatal("a mismatched CLI generated the app")
	}
	after, err := os.ReadFile(filepath.Join(dir, "schema.lidza"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("schema changed before version rejection: %q, %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "db")); !os.IsNotExist(err) {
		t.Fatalf("generation created database files: %v", err)
	}
}
