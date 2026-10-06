package crud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

const itemSrc = `enum Status { open done }

model Item @public {
  id        uuid   @id @default(uuid())
  title     string
  note      text?
  status    Status
  pinned    bool   @default(false)
  createdAt time   @default(now())
}
`

// listCheck runs in the generated app against Postgres: the list query
// searches, filters, bounds the time, sorts both ways and pages, and the
// count agrees.
const listCheck = `package listcheck

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	queries "app/db/queries/gen"
)

func TestList(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil || admin.Ping(ctx) != nil {
		t.Skip("no test database")
	}
	defer admin.Close()
	ns := fmt.Sprintf("listcheck_%d", time.Now().UnixNano())
	admin.Exec(ctx, "CREATE SCHEMA "+ns)
	defer admin.Exec(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = ns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ddl, _ := os.ReadFile("../db/schema.sql")
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, r := range []struct {
		title, status string
		pinned        bool
	}{{"Apple pie", "open", true}, {"Banana bread", "done", false}, {"apple tart 100%", "done", true}, {"Cherry", "open", false}} {
		if _, err := pool.Exec(ctx, "INSERT INTO item (title, status, pinned, created_at) VALUES ($1, $2, $3, $4)", r.title, r.status, r.pinned, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	q := queries.New(pool)
	str := func(s string) *string { return &s }
	list := func(p queries.ListItemsParams) string {
		t.Helper()
		if p.Lim == 0 {
			p.Lim = 50
		}
		rows, err := q.ListItems(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return strings.Join(out, "|")
	}
	// The default: newest first.
	if got := list(queries.ListItemsParams{Sort: "createdAt", Desc: true}); got != "Cherry|apple tart 100%|Banana bread|Apple pie" {
		t.Fatalf("default: %s", got)
	}
	// Search: any case, a percent sign is just a character.
	if got := list(queries.ListItemsParams{Q: str("APPLE"), Sort: "title"}); got != "Apple pie|apple tart 100%" {
		t.Fatalf("search: %s", got)
	}
	if got := list(queries.ListItemsParams{Q: str("100%"), Sort: "title"}); got != "apple tart 100%" {
		t.Fatalf("search with %%: %s", got)
	}
	// Filters on an enum and a bool, as text.
	if got := list(queries.ListItemsParams{Status: str("done"), Pinned: str("true"), Sort: "title"}); got != "apple tart 100%" {
		t.Fatalf("filters: %s", got)
	}
	// The range: since inclusive, until exclusive.
	since, until := day.AddDate(0, 0, 1), day.AddDate(0, 0, 3)
	if got := list(queries.ListItemsParams{Since: &since, Until: &until, Sort: "createdAt"}); got != "Banana bread|apple tart 100%" {
		t.Fatalf("range: %s", got)
	}
	// Sort by title both ways; a page of two after the first.
	// (The database's collation orders "apple tart" with "Apple pie".)
	if got := list(queries.ListItemsParams{Sort: "title", Desc: true, Lim: 2, Off: 1}); got != "Banana bread|apple tart 100%" {
		t.Fatalf("sorted page: %s", got)
	}
	total, err := q.CountItems(ctx, queries.CountItemsParams{Q: str("apple")})
	if err != nil || total != 2 {
		t.Fatalf("count: %d %v", total, err)
	}
}
`

// TestGenerateList: the list route's queries do what list.Read promises,
// against Postgres.
func TestGenerateList(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(itemSrc), 0o644)
	if _, _, err := pack.Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Item", Module: "app"}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "listcheck"), 0o755)
	os.WriteFile(filepath.Join(root, "listcheck", "list_test.go"), []byte(listCheck), 0o644)
	compileApp(t, root, "item.go", "./listcheck/")
}
