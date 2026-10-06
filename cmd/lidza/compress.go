package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/andybalholm/brotli"

	"github.com/agim/lidza/pkg/devserver"
)

// compressDist writes a Brotli (name.br) and a gzip (name.gz) copy of
// every compressible file of the build in dir/dist, so the static server
// sends the smaller one a browser accepts without compressing per
// request. A copy that saves less than a tenth is not kept; the build's
// internals (dot directories but .well-known) are left alone. It returns
// the files compressed and the bytes a Brotli client saves.
func compressDist(dir, dist string) (files int, saved int64, err error) {
	if dist == "" {
		return 0, 0, nil
	}
	root := filepath.Join(dir, dist)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && strings.HasPrefix(name, ".") && name != ".well-known" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(path.Ext(name))
		if !devserver.Compressible(ext) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, e := range []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
			os.Remove(p + e.ext)
		}
		if len(data) < devserver.MinCompress {
			return nil
		}
		kept := false
		for _, e := range []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
			var buf bytes.Buffer
			var w io.WriteCloser
			if e.name == "br" {
				w = brotli.NewWriterLevel(&buf, brotli.BestCompression)
			} else {
				w, _ = gzip.NewWriterLevel(&buf, gzip.BestCompression)
			}
			w.Write(data)
			if err := w.Close(); err != nil {
				return err
			}
			if buf.Len() > len(data)*9/10 {
				continue
			}
			if err := os.WriteFile(p+e.ext, buf.Bytes(), 0o644); err != nil {
				return err
			}
			if e.name == "br" {
				saved += int64(len(data) - buf.Len())
			}
			kept = true
		}
		if kept {
			files++
		}
		return nil
	})
	return files, saved, err
}
