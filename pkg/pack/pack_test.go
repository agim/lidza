package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/schema"
)

func TestScaffoldValidateGenerate(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte("type Greeting {\n  name string\n}\n"), 0o644)
	m, err := Scaffold(root, "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"packs/demo/pack.lidza.json", "packs/demo/rust/Cargo.toml", "packs/demo/rust/src/lib.rs", "packs/demo/rust/src/abi.rs", "packs/demo/.gitignore"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := Scaffold(root, "demo"); err == nil {
		t.Error("existing pack accepted")
	}
	if _, err := Scaffold(root, "Bad-Name"); err == nil {
		t.Error("bad name accepted")
	}

	s, err := schema.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Model("ReverseInput") == nil || s.Model("ReverseOutput") == nil || s.Model("Greeting") == nil {
		t.Fatalf("schema types: %+v", s.Types)
	}
	if problems := m.Validate(s, []string{"lidza_alloc", "lidza_free", "reverse"}); len(problems) != 0 {
		t.Fatalf("valid manifest reported %+v", problems)
	}
	problems := m.Validate(&schema.Schema{}, []string{"reverse"})
	joined := ""
	for _, p := range problems {
		joined += p.Message + "\n"
	}
	for _, want := range []string{"input type ReverseInput is not declared", "output type ReverseOutput is not declared", "does not export lidza_alloc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing problem %q in:\n%s", want, joined)
		}
	}
	m2 := *m
	m2.Capabilities = append(m2.Capabilities, Capability{Name: "lidza_x", Input: "Greeting", Output: "Greeting"}, Capability{Name: "reverse", Input: "Greeting", Output: "Greeting"})
	if p := m2.Validate(s, nil); len(p) != 2 {
		t.Errorf("reserved and duplicate names: %+v", p)
	}

	loaded, err := Load(root, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Rust.Instances != 4 || loaded.Rust.TimeoutMS != 5000 || loaded.Path != "packs/demo/pack.lidza.json" {
		t.Fatalf("defaults: %+v", loaded.Rust)
	}

	changed, err := Generate(root, "app", []string{"demo"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("changed: %v", changed)
	}
	packGo, _ := os.ReadFile(filepath.Join(root, "packs/demo/pack.go"))
	for _, want := range []string{
		"package demo", "//go:embed demo.wasm", "type Demo struct", "func From(ctx context.Context) *Demo",
		"engine.NewPool(ctx, m, 4, 5000*time.Millisecond)",
		"func (p *Demo) TelemetryStats() map[string]float64 { return p.pool.Stats() }",
		"func (p *Demo) Reverse(ctx context.Context, in schema.ReverseInput) (schema.ReverseOutput, error)",
		`"app/schema"`,
	} {
		if !strings.Contains(string(packGo), want) {
			t.Errorf("pack.go missing %q", want)
		}
	}
	packsGo, _ := os.ReadFile(filepath.Join(root, PacksFile))
	if !strings.Contains(string(packsGo), `demo "app/packs/demo"`) || !strings.Contains(string(packsGo), "demo.Pack(),") {
		t.Errorf("packs.go:\n%s", packsGo)
	}
	rs, _ := os.ReadFile(filepath.Join(root, "packs/demo/rust/src/schema.rs"))
	if !strings.Contains(string(rs), "pub struct ReverseInput") {
		t.Errorf("schema.rs:\n%s", rs)
	}
	if again, _ := Generate(root, "app", []string{"demo"}, s); len(again) != 0 {
		t.Errorf("second generate changed %v", again)
	}
	if !NeedsBuild(root, m) {
		t.Error("missing wasm should need a build")
	}
	if got := GeneratePacksGo("app", nil); !strings.Contains(got, "return nil") {
		t.Errorf("empty packs.go:\n%s", got)
	}
	if exported("image_resize") != "ImageResize" || exported("db") != "DB" {
		t.Error("exported")
	}
}

func TestABIMatchesCore(t *testing.T) {
	tmpl, _ := files.ReadFile("files/abi.rs.tmpl")
	core, err := os.ReadFile("../../core/src/abi.rs")
	if err != nil {
		t.Skip("core not present")
	}
	if string(tmpl) != string(core) {
		t.Fatal("pkg/pack/files/abi.rs.tmpl differs from core/src/abi.rs; keep them identical")
	}
}

func TestOfficialGo(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".env.example"), []byte("LIDZA_ADDR=127.0.0.1:3000\n"), 0o644)
	entry, o, err := Add(root, "db")
	if err != nil || entry != "lidza/db" || o.Rust {
		t.Fatalf("%q %+v %v", entry, o, err)
	}
	for _, f := range []string{"sqlc.yaml", "db/queries/queries.sql"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	envx, _ := os.ReadFile(filepath.Join(root, ".env.example"))
	if !strings.Contains(string(envx), "DATABASE_URL=") {
		t.Errorf(".env.example: %s", envx)
	}
	if _, _, err := Add(root, "db"); err != nil {
		t.Fatalf("second add: %v", err)
	}
	envx, _ = os.ReadFile(filepath.Join(root, ".env.example"))
	if strings.Count(string(envx), "DATABASE_URL=") != 1 {
		t.Errorf("env lines duplicated: %s", envx)
	}
	if _, _, err := Add(root, "nope"); err == nil || !strings.Contains(err.Error(), "available") {
		t.Errorf("unknown pack: %v", err)
	}
	if g := GeneratePacksGo("app", []string{"lidza/i18n"}); !strings.Contains(g, "//go:embed all:locales") || !strings.Contains(g, `i18n.Pack(lidza.Sub(locales, "locales"))`) {
		t.Errorf("i18n packs.go:\n%s", g)
	}
	got := GeneratePacksGo("app", []string{"lidza/db", "lidza/realtime", "local"})
	for _, want := range []string{`db "github.com/agim/lidza/packs/db"`, `realtime "github.com/agim/lidza/packs/realtime"`, `local "app/packs/local"`, "db.Pack(),", "realtime.Pack(),", "local.Pack(),"} {
		if !strings.Contains(got, want) {
			t.Errorf("packs.go missing %q:\n%s", want, got)
		}
	}
}

func TestSyncFragments(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "schema.lidza"), []byte("type Greeting {\n  message string\n}\n\n// Types of the auth pack.\nmodel AuthSession @table(\"auth_session\") {\n  id string @id\n  subject string\n  refreshHash string @unique\n  expiresAt time\n  createdAt time @default(now())\n}\n"), 0o644)
	// AuthSession is there but behind the pack's current declaration (no
	// prevRefreshHash): it is updated in place; AuthToken is added.
	added, err := SyncFragments(dir, []string{"lidza/db", "lidza/auth"})
	if err != nil || strings.Join(added, ",") != "AuthSession,AuthToken,AuthAccount,AuthUser,AuthIdentity" {
		t.Fatalf("synced %v, %v", added, err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "schema.lidza"))
	if !strings.Contains(string(src), "model AuthToken @table(\"auth_token\")") || strings.Count(string(src), "model AuthSession") != 1 ||
		!strings.Contains(string(src), "prevRefreshHash") || !strings.HasPrefix(string(src), "type Greeting {") {
		t.Fatalf("schema:\n%s", src)
	}
	if again, err := SyncFragments(dir, []string{"lidza/auth"}); err != nil || again != nil {
		t.Fatalf("second sync: %v %v", again, err)
	}
	if _, err := schema.Parse(string(src)); err != nil {
		t.Fatal(err)
	}
}

// TestAddWithExistingDeclaration: an add after a half-done one, or after
// lidza gen synced the pack's model, keeps the declaration and adds only
// what is missing.
func TestAddWithExistingDeclaration(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lidza.json"), []byte(`{"name":"x","frontend":{"template":"react","dev":"npm run dev","url":"http://127.0.0.1:5173","dist":"dist"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".env.example"), []byte("LIDZA_ADDR=127.0.0.1:3000\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "schema.lidza"), []byte("model AuthSession @table(\"auth_session\") {\n  id string @id\n  subject string\n  refreshHash string @unique\n  expiresAt time\n}\n"), 0o644)
	if _, _, err := Add(dir, "auth"); err != nil {
		t.Fatalf("add with an existing model: %v", err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "schema.lidza"))
	if strings.Count(string(src), "model AuthSession") != 1 || !strings.Contains(string(src), "model AuthToken") {
		t.Fatalf("schema:\n%s", src)
	}
	if _, err := schema.Parse(string(src)); err != nil {
		t.Fatal(err)
	}
}
