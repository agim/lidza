package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvMasterKey, "")
	if _, err := Read(dir); err != ErrNoKey {
		t.Fatalf("no key: %v", err)
	}
	created, err := Generate(dir)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	if again, err := Generate(dir); err != nil || again {
		t.Fatal("generate twice")
	}
	key, _ := os.ReadFile(filepath.Join(dir, "config", "master.key"))
	if len(strings.TrimSpace(string(key))) != 64 {
		t.Fatalf("key: %q", key)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(ignore), "config/master.key") {
		t.Fatalf(".gitignore: %q", ignore)
	}
	if vals, err := Read(dir); err != nil || len(vals) != 0 {
		t.Fatalf("empty: %v %v", vals, err)
	}
	if err := Set(dir, map[string]string{"MAIL_API_KEY": "key-123", "MAIL_SMTP_URL": "smtp://u:p@h:587", "LLM_API_KEY": "sk: with colon #hash", "EMPTY": ""}); err != nil {
		t.Fatal(err)
	}
	if err := Set(dir, map[string]string{"bad-name": "x"}); err == nil {
		t.Fatal("bad name accepted")
	}
	vals, err := Read(dir)
	if err != nil || vals["MAIL_API_KEY"] != "key-123" || vals["MAIL_SMTP_URL"] != "smtp://u:p@h:587" || vals["LLM_API_KEY"] != "sk: with colon #hash" || vals["EMPTY"] != "" {
		t.Fatalf("read: %v %v", vals, err)
	}
	sealed, _ := os.ReadFile(filepath.Join(dir, "config", "credentials.yml.enc"))
	if strings.Contains(string(sealed), "key-123") || strings.Contains(string(sealed), "MAIL_API_KEY") {
		t.Fatal("file is not sealed")
	}
	if err := Unset(dir, "EMPTY"); err != nil {
		t.Fatal(err)
	}
	if names := Names(dir); strings.Join(names, ",") != "LLM_API_KEY,MAIL_API_KEY,MAIL_SMTP_URL" {
		t.Fatalf("names: %v", names)
	}
	// The key from the environment, as in production; a wrong key fails.
	t.Setenv(EnvMasterKey, strings.TrimSpace(string(key)))
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "config"), 0o755)
	os.WriteFile(filepath.Join(other, "config", "credentials.yml.enc"), sealed, 0o644)
	if vals, err := Read(other); err != nil || vals["MAIL_API_KEY"] != "key-123" {
		t.Fatalf("env key: %v %v", vals, err)
	}
	t.Setenv(EnvMasterKey, strings.Repeat("ab", 32))
	if _, err := Read(other); err == nil || !strings.Contains(err.Error(), "wrong master key") {
		t.Fatalf("wrong key: %v", err)
	}
	t.Setenv(EnvMasterKey, "short")
	if _, err := Key(other); err == nil {
		t.Fatal("bad key accepted")
	}

	// Runtime overrides win over the file.
	t.Setenv(EnvMasterKey, strings.TrimSpace(string(key)))
	SetOverrides(map[string]string{"MAIL_API_KEY": "from-admin"})
	t.Cleanup(func() { SetOverrides(nil) })
	if v := Values(dir); v["MAIL_API_KEY"] != "from-admin" || v["LLM_API_KEY"] == "" {
		t.Fatalf("values: %v", v)
	}
	SetOverride("MAIL_API_KEY", "")
	if v := Values(dir); v["MAIL_API_KEY"] != "key-123" {
		t.Fatalf("override removed: %v", v)
	}
}

func TestParseFormat(t *testing.T) {
	text := Format(map[string]string{"B": "plain", "A": "needs \"quotes\": yes", "C": ""})
	vals, err := Parse(text)
	if err != nil || vals["A"] != "needs \"quotes\": yes" || vals["B"] != "plain" || vals["C"] != "" {
		t.Fatalf("%q -> %v %v", text, vals, err)
	}
	if !strings.HasPrefix(text, "#") || strings.Index(text, "A:") > strings.Index(text, "B:") {
		t.Fatalf("format: %q", text)
	}
	for _, bad := range []string{"no colon", "lower: x", "A: \"unterminated"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// One sealed file for every mode: a mode reads the plain values and its
// own section, which wins; production needs only the master key.
func TestModeSections(t *testing.T) {
	text := `# comment
SAAS_EMAIL: team@example.com
STRIPE_SECRET_KEY: sk_plain

dev:
  STRIPE_SECRET_KEY: sk_test_1
  MULTI: "line one\nline two"
production:
  STRIPE_SECRET_KEY: sk_live_1
AFTER: x
`
	raw, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"SAAS_EMAIL": "team@example.com", "STRIPE_SECRET_KEY": "sk_plain", "dev.STRIPE_SECRET_KEY": "sk_test_1", "dev.MULTI": "line one\nline two", "production.STRIPE_SECRET_KEY": "sk_live_1", "AFTER": "x"}
	if len(raw) != len(want) {
		t.Fatalf("parsed %v", raw)
	}
	for k, v := range want {
		if raw[k] != v {
			t.Errorf("%s = %q, want %q", k, raw[k], v)
		}
	}
	// Format writes the sections back; Parse reads the same.
	again, err := Parse(Format(raw))
	if err != nil || len(again) != len(raw) || again["dev.MULTI"] != "line one\nline two" {
		t.Fatalf("round trip: %v %v\n%s", again, err, Format(raw))
	}
	if !strings.Contains(Format(raw), "\ndev:\n  MULTI: ") {
		t.Errorf("format:\n%s", Format(raw))
	}
	for mode, key := range map[string]string{"dev": "sk_test_1", "production": "sk_live_1", "staging": "sk_plain"} {
		got := Resolve(raw, mode)
		if got["STRIPE_SECRET_KEY"] != key || got["SAAS_EMAIL"] != "team@example.com" {
			t.Errorf("%s: %v", mode, got)
		}
		for k := range got {
			if strings.Contains(k, ".") {
				t.Errorf("%s: section name %s leaked", mode, k)
			}
		}
	}
	if _, ok := Resolve(raw, "production")["MULTI"]; ok {
		t.Error("dev's value in production")
	}

	// Set and Values through the sealed file, by mode.
	dir := t.TempDir()
	t.Setenv(EnvMasterKey, "")
	if _, err := Generate(dir); err != nil {
		t.Fatal(err)
	}
	if err := Set(dir, map[string]string{"STRIPE_SECRET_KEY": "sk_plain", "dev.STRIPE_SECRET_KEY": "sk_test_2", "production.STRIPE_SECRET_KEY": "sk_live_2"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIDZA_MODE", "dev")
	if v := Values(dir)["STRIPE_SECRET_KEY"]; v != "sk_test_2" {
		t.Errorf("dev reads %q", v)
	}
	t.Setenv("LIDZA_MODE", "")
	if v := Values(dir)["STRIPE_SECRET_KEY"]; v != "sk_live_2" {
		t.Errorf("production reads %q", v)
	}

	for _, bad := range []string{"development.STRIPE_KEY", "Dev.STRIPE_KEY", ".STRIPE_KEY", "dev.lower", "dev.prod.X"} {
		if err := Set(dir, map[string]string{bad: "x"}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := Parse("development:\n  X: 1\n"); err == nil || !strings.Contains(err.Error(), `"dev"`) {
		t.Errorf("development section: %v", err)
	}
	if _, err := Parse("  X: 1\n"); err == nil {
		t.Error("indented outside a section accepted")
	}
}

// A checkout with the sealed file but not its key (a clone, CI) gets no
// new key, which could not open the file; LIDZA_MASTER_KEY is a key.
func TestGenerateInClone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvMasterKey, "")
	if _, err := Generate(dir); err != nil {
		t.Fatal(err)
	}
	if err := Set(dir, map[string]string{"PAY_KEY": "x"}); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(dir, MasterKeyFile))
	os.Remove(filepath.Join(dir, MasterKeyFile))
	if created, err := Generate(dir); created || err != ErrKeyNotHere {
		t.Fatalf("clone: created %v, %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(dir, MasterKeyFile)); err == nil {
		t.Fatal("a key was written that cannot open the file")
	}
	t.Setenv(EnvMasterKey, strings.TrimSpace(string(key)))
	if created, err := Generate(dir); created || err != nil {
		t.Fatalf("with %s: created %v, %v", EnvMasterKey, created, err)
	}
	if _, err := os.Stat(filepath.Join(dir, MasterKeyFile)); err == nil {
		t.Fatal("a key file was written beside " + EnvMasterKey)
	}
	if v := Values(dir)["PAY_KEY"]; v != "x" {
		t.Fatalf("read with the variable: %q", v)
	}
}

// Write replaces the file in one rename: no temporary file is left, the
// mode is 0644 (sealed, committed, read by a container's user), and
// concurrent readers never see a partial file.
func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvMasterKey, strings.Repeat("ab", 32))
	if err := Write(dir, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if err := Set(dir, map[string]string{"A": strings.Repeat("x", i)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for reading := true; reading; {
		select {
		case <-done:
			reading = false
		default:
			if _, err := Read(dir); err != nil {
				t.Fatalf("read during writes: %v", err)
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "config"))
	if len(entries) != 1 {
		t.Fatalf("config holds %d files, want the credentials only", len(entries))
	}
	if info, _ := os.Stat(filepath.Join(dir, File)); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}
