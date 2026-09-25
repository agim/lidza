// Command sync copies the reference app's files listed in the snippet
// index into pkg/snippets/_files, for embedding. Run through
// `go generate ./pkg/snippets`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agim/lidza/pkg/snippets"
)

func main() {
	// go generate runs in the package directory.
	src := filepath.Join("..", "..", "examples", "notes")
	for _, s := range snippets.Index {
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(s.File)))
		if err != nil {
			fmt.Fprintln(os.Stderr, "sync:", err)
			os.Exit(1)
		}
		dst := filepath.Join("_files", filepath.FromSlash(s.File))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "sync:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "sync:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("synced %d snippet(s) from %s\n", len(snippets.Index), src)
}
