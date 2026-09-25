package apidoc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	root, _ := filepath.Abs("../..")
	dir, err := ModuleDir(context.Background(), root)
	if err != nil || dir != root {
		t.Fatal(dir, err)
	}
	pkgs, err := Packages(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"", "pkg/router", "packs/db", "pkg/apidoc"} {
		found := false
		for _, p := range pkgs {
			found = found || p == want
		}
		if !found {
			t.Errorf("%q missing from %v", want, pkgs)
		}
	}
	for _, p := range pkgs {
		if strings.HasPrefix(p, "cmd") || strings.HasPrefix(p, "templates") {
			t.Errorf("%q listed", p)
		}
	}
	text, err := Render(dir, []string{"pkg/router"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## github.com/agim/lidza/pkg/router (package router)", "func Route[In, Out any](", "func (r *Request[In]) Param(name string) string", "func NotFound("} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "func writeError") {
		t.Error("unexported function rendered")
	}
	only, _ := Render(dir, []string{"pkg/router"}, "notfound")
	if !strings.Contains(only, "func NotFound(") || strings.Contains(only, "func Route[") {
		t.Errorf("filter:\n%s", only)
	}
	if !Exists(dir, "github.com/agim/lidza/pkg/router") || Exists(dir, "github.com/agim/lidza/pkg/orm") || Exists(dir, "github.com/x/y") {
		t.Error("Exists")
	}
	if s := Suggest(dir, "github.com/agim/lidza/pkg/routers"); s != "github.com/agim/lidza/pkg/router" {
		t.Errorf("suggest: %q", s)
	}
}
