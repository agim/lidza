package diag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppSettingsInEnv(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "deploy"), 0o755)
	os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL=postgres:///x\nDB_MAX_CONNS=10\n# MAIL_API_KEY=documented\nMAIL_API_KEY=key-1\nexport LLM_PROVIDER=anthropic\n"), 0o600)
	os.WriteFile(filepath.Join(root, ".env.test"), []byte("MAIL_PROVIDER=outbox\nAUTH_SECRET=fixture\n"), 0o644)
	os.WriteFile(filepath.Join(root, "deploy", "production.env"), []byte("LIDZA_LOG=json\n# lidza:ignore L023\nAPP_URL=https://x.example\nSTRIPE_SECRET_KEY=sk\n"), 0o600)
	got := appSettingsInEnv(root)
	var where []string
	for _, d := range got {
		where = append(where, d.File+":"+d.Message[:len("MAIL_API_KEY")])
	}
	if len(got) != 3 || got[0].File != ".env" || got[0].Line != 4 || got[1].Line != 5 || got[2].File != "deploy/production.env" || got[2].Line != 4 {
		t.Fatalf("findings: %v %+v", where, got)
	}
}
