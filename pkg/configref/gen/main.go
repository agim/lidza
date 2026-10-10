// Command gen writes docs/configuration.md from the framework's source
// (go generate ./pkg/configref).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agim/lidza/pkg/configref"
)

func main() {
	root := filepath.Join("..", "..")
	settings, err := configref.Collect(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(root, configref.File), []byte(configref.Render(settings)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d settings\n", configref.File, len(settings))
}
