package pack

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// NeedsBuild reports whether the pack's WASM is missing or older than its
// crate sources.
func NeedsBuild(root string, m *Manifest) bool {
	info, err := os.Stat(filepath.Join(root, m.WasmFile()))
	if err != nil {
		return true
	}
	built := info.ModTime()
	stale := false
	crate := filepath.Join(root, m.CrateDir())
	_ = filepath.WalkDir(crate, func(p string, d os.DirEntry, err error) error {
		if err != nil || stale {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "target" {
				return filepath.SkipDir
			}
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(built) {
			stale = true
		}
		return nil
	})
	return stale
}

// Build compiles the crate to wasm32-wasip1 in release mode and copies the
// module to packs/<name>/<name>.wasm. Output goes to out with a prefix.
func Build(ctx context.Context, root string, m *Manifest, out io.Writer) error {
	if _, err := exec.LookPath("cargo"); err != nil {
		return fmt.Errorf("pack %s: cargo is not installed (run install.sh)", m.Name)
	}
	crate := filepath.Join(root, m.CrateDir())
	start := time.Now()
	cmd := exec.CommandContext(ctx, "cargo", "build", "--release", "--target", "wasm32-wasip1", "--quiet")
	cmd.Dir = crate
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pack %s: cargo build failed", m.Name)
	}
	built := filepath.Join(crate, "target", "wasm32-wasip1", "release", strings.ReplaceAll(m.Name, "-", "_")+".wasm")
	data, err := os.ReadFile(built)
	if err != nil {
		return fmt.Errorf("pack %s: built module not found at %s (is the crate named %q?)", m.Name, built, m.Name)
	}
	if err := os.WriteFile(filepath.Join(root, m.WasmFile()), data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "[lidza] pack %s: built %s (%.1f KB, %s)\n", m.Name, m.WasmFile(), float64(len(data))/1024, time.Since(start).Round(100*time.Millisecond))
	return nil
}

// BuildStale builds every enabled pack whose module is missing or stale.
func BuildStale(ctx context.Context, root string, names []string, out io.Writer) error {
	for _, name := range names {
		m, err := Load(root, name)
		if err != nil {
			return err
		}
		if NeedsBuild(root, m) {
			if err := Build(ctx, root, m, out); err != nil {
				return err
			}
		}
	}
	return nil
}
