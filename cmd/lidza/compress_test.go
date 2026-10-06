package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

// TestCompressDist: the build stores a Brotli and a gzip copy of each
// compressible file of 1 KB or more, both decoding to the file; small
// files, images and the build internals are left alone.
func TestCompressDist(t *testing.T) {
	dir := t.TempDir()
	js := strings.Repeat("export const greeting = 'hello';\n", 200)
	html := "<!doctype html>" + strings.Repeat("<p>shell</p>", 300)
	for name, data := range map[string]string{
		"dist/index.html":               html,
		"dist/.server/index.html":       html,
		"dist/.well-known/security.txt": strings.Repeat("Contact: mailto:a@example.com\n", 50),
		"dist/assets/app.js":            js,
		"dist/assets/tiny.js":           "x()",
		"dist/photo.png":                strings.Repeat("\x89PNG", 600),
	} {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(data), 0o644)
	}
	files, saved, err := compressDist(dir, "dist")
	if err != nil || files != 3 || saved <= 0 {
		t.Fatalf("%d files, %d saved, %v", files, saved, err)
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, "dist", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b
	}
	br, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(read("assets/app.js.br"))))
	zr, err := gzip.NewReader(bytes.NewReader(read("assets/app.js.gz")))
	if err != nil {
		t.Fatal(err)
	}
	gz, _ := io.ReadAll(zr)
	if string(br) != js || string(gz) != js {
		t.Errorf("copies do not decode to the file")
	}
	read("index.html.br")
	read(".well-known/security.txt.gz")
	for _, name := range []string{"assets/tiny.js.gz", "photo.png.gz", ".server/index.html.gz"} {
		if _, err := os.Stat(filepath.Join(dir, "dist", name)); err == nil {
			t.Errorf("%s stored", name)
		}
	}
}
