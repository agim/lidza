//go:build evals || agenteval

package evals

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Shared by the platform evals (tag evals) and the agent-driven evals
// (tag agenteval): the CLI built from this checkout and one scaffolded
// app with its dependencies installed.
var (
	root  string // this checkout
	lidza string // the CLI built from it
	app   string // the scaffolded app
)

func TestMain(m *testing.M) {
	var err error
	root, err = filepath.Abs("..")
	if err != nil {
		panic(err)
	}
	tmp, err := os.MkdirTemp("", "lidza-evals-")
	if err != nil {
		panic(err)
	}
	lidza = filepath.Join(tmp, "lidza")
	if out, err := command(root, "go", "build", "-o", lidza, "./cmd/lidza"); err != nil {
		fmt.Fprintf(os.Stderr, "build lidza: %v\n%s", err, out)
		os.Exit(1)
	}
	// The MCP server's command tools and the agent run this CLI.
	os.Setenv("PATH", filepath.Dir(lidza)+string(os.PathListSeparator)+os.Getenv("PATH"))
	app = filepath.Join(tmp, "evalapp")
	if out, err := command(tmp, lidza, "new", "evalapp", "--lidza-dir", root); err != nil {
		fmt.Fprintf(os.Stderr, "lidza new: %v\n%s", err, out)
		os.Exit(1)
	}
	if out, err := command(app, "npm", "install", "--no-fund", "--no-audit"); err != nil {
		fmt.Fprintf(os.Stderr, "npm install: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func command(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LIDZA_LOG_LEVEL=error")
	return cmd.CombinedOutput()
}
