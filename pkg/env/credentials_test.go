package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agim/lidza/pkg/credentials"
)

func TestLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIL_API_KEY=from-env-file\nMAIL_FROM=app@example.com\n"), 0o644)
	for _, name := range []string{credentials.EnvMasterKey, "MAIL_API_KEY", "LLM_API_KEY"} {
		t.Setenv(name, "") // restored after the test
		os.Unsetenv(name)  // an empty variable counts as set
	}
	type cfg struct {
		Key  string `env:"MAIL_API_KEY"`
		From string `env:"MAIL_FROM"`
		LLM  string `env:"LLM_API_KEY"`
	}
	var c cfg
	if err := Load(dir, &c); err != nil || c.Key != "from-env-file" {
		t.Fatalf("without credentials: %+v %v", c, err)
	}
	if _, err := credentials.Generate(dir); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Set(dir, map[string]string{"MAIL_API_KEY": "sealed", "LLM_API_KEY": "sk-sealed"}); err != nil {
		t.Fatal(err)
	}
	c = cfg{}
	if err := Load(dir, &c); err != nil || c.Key != "sealed" || c.LLM != "sk-sealed" || c.From != "app@example.com" {
		t.Fatalf("credentials over .env: %+v %v", c, err)
	}
	t.Setenv("MAIL_API_KEY", "from-process")
	c = cfg{}
	if err := Load(dir, &c); err != nil || c.Key != "from-process" {
		t.Fatalf("process env over credentials: %+v %v", c, err)
	}
}
