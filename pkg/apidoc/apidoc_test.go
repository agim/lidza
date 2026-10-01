package apidoc

import (
	"context"
	"os"
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
	public := strings.Join(AppPackages(dir), ",")
	if !strings.Contains(public, "pkg/webhook") {
		t.Errorf("pkg/webhook is not public: %s", public)
	}
	if hook, _ := Render(dir, nil, "Stripe"); !strings.Contains(hook, "func Stripe(setting string, h Handler, opts ...Option) *Endpoint") {
		t.Errorf("webhook API missing:\n%s", hook)
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

	// The reference app as an app source.
	src, ok := App(filepath.Join(root, "examples", "notes"))
	if !ok || src.Module != "notes" {
		t.Fatalf("app source: %+v %v", src, ok)
	}
	pkgs, _ = Packages(src.Dir)
	if strings.Join(pkgs, ",") != ",db/queries/gen,handlers,packs/stats,schema" {
		t.Errorf("app packages: %v", pkgs)
	}
	for _, p := range []string{"notes/handlers", "./handlers", "handlers/"} {
		if rel, ok := src.Rel(p); p != "handlers/" && (!ok || rel != "handlers") {
			t.Errorf("Rel(%q) = %q %v", p, rel, ok)
		}
	}
	if !src.Exists("./handlers") || src.Exists("./nope") || src.Exists("github.com/agim/lidza/pkg/router") {
		t.Error("app Exists")
	}
	appText, err := src.Render([]string{"handlers"}, "")
	if err != nil || !strings.Contains(appText, "## notes/handlers (package handlers)") || !strings.Contains(appText, "func NoteRoutes(r *router.Router)") {
		t.Errorf("app render: %v\n%s", err, appText)
	}
}

func TestApplicationInternalPackages(t *testing.T) {
	root := t.TempDir()
	fw, app := filepath.Join(root, "framework"), filepath.Join(root, "app")
	write := func(dir, name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(fw, "go.mod", "module "+Module+"\n")
	write(fw, "lidza.go", "package lidza\nfunc Run() {}\n")
	write(fw, "internal/private/private.go", "package private\nfunc Hidden() {}\n")
	write(app, "go.mod", "module example.com/shop\nreplace "+Module+" => "+filepath.ToSlash(fw)+"\n")
	write(app, "internal/orders/orders.go", "package orders\n// Submit validates an order.\nfunc Submit() {}\nfunc private() {}\n")
	write(app, "internal/providers/vendor/client.go", "package vendor\ntype Client struct{}\n")
	write(app, "internal/orders/testdata/ignored.go", "package ignored\n")
	write(app, "internal/other/go.mod", "module example.com/other\n")
	write(app, "internal/other/other.go", "package other\n")
	ctx := context.Background()
	source, rels, err := Resolve(ctx, app, "app")
	if err != nil || strings.Join(rels, ",") != "internal/orders,internal/providers/vendor" {
		t.Fatalf("application packages: %v %v", rels, err)
	}
	text, err := source.Render(rels, "Submit")
	if err != nil || !strings.Contains(text, "func Submit()") || strings.Contains(text, "func private()") {
		t.Fatalf("internal API: %v\n%s", err, text)
	}
	for _, name := range []string{"./internal/orders", "internal/orders", "example.com/shop/internal/orders"} {
		source, rels, err := Resolve(ctx, app, name)
		if err != nil || source.Module != "example.com/shop" || strings.Join(rels, ",") != "internal/orders" {
			t.Errorf("Resolve(%q): %+v %v %v", name, source, rels, err)
		}
	}
	listing, err := Listing(ctx, app)
	if err != nil || !strings.Contains(listing, "example.com/shop/internal/orders  (this app)") || strings.Contains(listing, Module+"/internal/private") {
		t.Fatalf("listing: %v\n%s", err, listing)
	}
	framework, err := Framework(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	frameworkPackages, err := framework.Packages()
	if err != nil || strings.Contains(strings.Join(frameworkPackages, ","), "internal") {
		t.Errorf("framework packages: %v %v", frameworkPackages, err)
	}
}
