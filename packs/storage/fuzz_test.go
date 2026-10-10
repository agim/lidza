package storage

import (
	"path"
	"strings"
	"testing"
)

// FuzzCheckKey: keys come from uploads and URLs; an accepted key never
// leaves its prefix or directory.
func FuzzCheckKey(f *testing.F) {
	for _, s := range []string{"avatars/1.png", "../x", "a/../../b", "/etc/passwd", `a\b`, "a//b", "a/./b", "a/", ".", ".."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if checkKey(key) != nil {
			return
		}
		joined := path.Join("/root/prefix", key)
		if !strings.HasPrefix(joined, "/root/prefix/") {
			t.Fatalf("key %q escapes to %q", key, joined)
		}
		for _, seg := range strings.Split(key, "/") {
			if seg == "" || seg == "." || seg == ".." {
				t.Fatalf("key %q has segment %q", key, seg)
			}
		}
	})
}
