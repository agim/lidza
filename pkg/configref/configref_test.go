package configref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferenceCurrent: docs/configuration.md is what the source says;
// a setting added, renamed or redocumented needs go generate.
func TestReferenceCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	settings, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(filepath.Join(root, File))
	if err != nil || string(have) != Render(settings) {
		t.Fatalf("%s is stale: go generate ./pkg/configref", File)
	}
	for _, s := range settings {
		if strings.TrimSpace(s.Doc) == "" {
			t.Errorf("%s (%s) has no doc comment", s.Name, s.Area)
		}
	}
	for _, want := range []string{"DATABASE_URL", "AUTH_SECRET", "LIDZA_TRUSTED_PROXIES", "DB_MIGRATE_LOCK_TIMEOUT", "REALTIME_MAX_TOPICS", "OTEL_EXPORTER_OTLP_ENDPOINT", "STRIPE_WEBHOOK_SECRET"} {
		if !strings.Contains(string(have), "`"+want+"`") {
			t.Errorf("%s missing", want)
		}
	}
}
