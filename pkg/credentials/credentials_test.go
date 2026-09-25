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
