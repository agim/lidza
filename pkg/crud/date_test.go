package crud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

const eventSrc = `model Event {
  id      uuid   @id @default(uuid())
  title   string @max(200)
  day     date
  endsOn  date?
  zone    string @timezone
  startAt time
}
`

// TestGenerateDate: a resource over date fields keeps civil.Date from
// schema/ to the queries sqlc generates (the managed override), so the
// handlers compile and a day is never a midnight timestamp.
func TestGenerateDate(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(eventSrc), 0o644)
	if _, _, err := pack.Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Event", Module: "app", Auth: true}); err != nil {
		t.Fatal(err)
	}
	compileApp(t, root, "event.go")
	cfg, _ := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if !strings.Contains(string(cfg), `go_type: "github.com/agim/lidza/pkg/civil.Date"`) {
		t.Errorf("sqlc.yaml:\n%s", cfg)
	}
	models, _ := os.ReadFile(filepath.Join(root, "db/queries/gen/models.go"))
	if !strings.Contains(string(models), "civil.Date") || !strings.Contains(string(models), "*civil.Date") {
		t.Errorf("models.go:\n%s", models)
	}
	go_, _ := os.ReadFile(filepath.Join(root, "schema/schema.go"))
	for _, want := range []string{"Day     civil.Date", "validate.Timezone(v.Zone)"} {
		if !strings.Contains(strings.Join(strings.Fields(string(go_)), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("schema.go lacks %q:\n%s", want, go_)
		}
	}
}
