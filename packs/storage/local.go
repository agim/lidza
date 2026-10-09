package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// local keeps objects as files under dir and their content type in a
// sidecar under dir/.meta. Presigned URLs are the public address when one
// is set; otherwise the app serves them (Handler). Every file operation
// goes through an os.Root on dir, so no key and no symlink placed inside
// it reaches a file outside.
type local struct {
	dir, public string
	root        *os.Root
}

func newLocal(dir, public string) (*local, error) {
	if err := os.MkdirAll(filepath.Join(dir, ".meta"), 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &local{dir: dir, public: strings.TrimRight(public, "/"), root: root}, nil
}

func (l *local) Name() string { return "local" }

// paths returns the object's file and its metadata file, relative to the
// root, refusing a key
// that would land outside the directory (Storage checks keys first; this
// holds for any caller).
func (l *local) paths(key string) (file, meta string, err error) {
	rel := filepath.Clean(filepath.FromSlash(key))
	if !filepath.IsLocal(rel) || rel == ".meta" || strings.HasPrefix(rel, ".meta"+string(filepath.Separator)) {
		return "", "", fmt.Errorf("storage: invalid key %q", key)
	}
	return rel, filepath.Join(".meta", rel+".json"), nil
}

type localMeta struct {
	ContentType  string `json:"contentType"`
	CacheControl string `json:"cacheControl,omitempty"`
}

func (l *local) Put(ctx context.Context, key string, data []byte, opt PutOptions) (Object, error) {
	file, meta, err := l.paths(key)
	if err != nil {
		return Object{}, err
	}
	for _, p := range []string{file, meta} {
		if err := l.root.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Object{}, err
		}
	}
	if err := l.root.WriteFile(file, data, 0o644); err != nil {
		return Object{}, err
	}
	m, _ := json.Marshal(localMeta(opt))
	if err := l.root.WriteFile(meta, m, 0o644); err != nil {
		return Object{}, err
	}
	return l.Stat(ctx, key)
}

func (l *local) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	obj, err := l.Stat(ctx, key)
	if err != nil {
		return nil, Object{}, err
	}
	file, _, err := l.paths(key)
	if err != nil {
		return nil, Object{}, err
	}
	f, err := l.root.Open(file)
	if err != nil {
		return nil, Object{}, err
	}
	return f, obj, nil
}

func (l *local) Stat(ctx context.Context, key string) (Object, error) {
	file, meta, err := l.paths(key)
	if err != nil {
		return Object{}, err
	}
	info, err := l.root.Stat(file)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, err
	}
	var m localMeta
	if data, err := l.root.ReadFile(meta); err == nil {
		json.Unmarshal(data, &m)
	}
	if m.ContentType == "" {
		m.ContentType = mimeByExtension(filepath.Ext(key))
	}
	return Object{Key: key, Size: info.Size(), ContentType: m.ContentType, ModifiedAt: info.ModTime().UTC()}, nil
}

func (l *local) Delete(ctx context.Context, key string) error {
	file, meta, err := l.paths(key)
	if err != nil {
		return err
	}
	if err := l.root.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	l.root.Remove(meta)
	return nil
}

func (l *local) List(ctx context.Context, prefix string, limit int) ([]Object, error) {
	var out []Object
	err := fs.WalkDir(l.root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == ".meta" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(rel, prefix) {
			return nil
		}
		obj, err := l.Stat(ctx, rel)
		if err == nil {
			out = append(out, obj)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (l *local) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if u := l.URL(key); u != "" {
		return u, nil
	}
	return "", errors.New("storage: the local provider has no presigned URLs; set STORAGE_PUBLIC_URL to where storage.Handler serves the files, or stream the object from a handler")
}

func (l *local) PresignPut(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error) {
	return "", errors.New("storage: the local provider has no presigned uploads; accept the upload in a handler and call Put")
}

func (l *local) URL(key string) string {
	if l.public == "" {
		return ""
	}
	return l.public + "/" + key
}

func mimeByExtension(ext string) string {
	if ext == "" {
		return ""
	}
	return mime.TypeByExtension(strings.ToLower(ext))
}
