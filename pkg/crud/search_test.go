package crud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

const articleSrc = `model Article @public {
  id        uuid   @id @default(uuid())
  title     string @search
  body      text?  @search
  slug      string
  createdAt time   @default(now())
  @@search("english")
}
`

// searchCheck runs in the generated app against Postgres: the search
// stems ("running" finds "run"), a title match outranks a body match,
// it uses the index's expression, and the highlights mark the words as
// parts, never as markup.
const searchCheck = `package searchcheck

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza/pkg/list"

	queries "app/db/queries/gen"
)

func TestSearch(t *testing.T) {
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
	ns := fmt.Sprintf("searchcheck_%d", time.Now().UnixNano())
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
	for _, r := range [][2]string{
		{"Gardening notes", "We were running the databases all night"},
		{"Running a database", "A short guide"},
		{"<b>Bold</b> cooking", "Nothing about it"},
	} {
		if _, err := pool.Exec(ctx, "INSERT INTO article (title, body, slug) VALUES ($1, $2, 'x')", r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
	q := queries.New(pool)
	str := func(s string) *string { return &s }
	rows, err := q.ListArticles(ctx, queries.ListArticlesParams{Q: str("run database"), Sort: "relevance", Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	var ids []string
	for _, r := range rows {
		titles = append(titles, r.Title)
		ids = append(ids, r.ID)
	}
	if strings.Join(titles, "|") != "Running a database|Gardening notes" {
		t.Fatalf("ranked: %v", titles)
	}
	total, err := q.CountArticles(ctx, queries.CountArticlesParams{Q: str("run database")})
	if err != nil || total != 2 {
		t.Fatalf("count %d %v", total, err)
	}
	hs, err := q.HighlightArticles(ctx, queries.HighlightArticlesParams{Q: str("run database"), IDs: ids})
	if err != nil || len(hs) != 2 {
		t.Fatalf("highlights %v %v", hs, err)
	}
	for _, h := range hs {
		hits := 0
		for _, p := range list.Split(h.Headline) {
			if p.Hit {
				hits++
				if lower := strings.ToLower(p.Text); !strings.HasPrefix(lower, "run") && !strings.HasPrefix(lower, "database") {
					t.Errorf("marked %q", p.Text)
				}
			}
		}
		if hits < 2 {
			t.Errorf("%s: %d hits in %q", h.ID, hits, h.Headline)
		}
	}
	// Markup in a row never becomes markup in the highlight: the parser
	// drops tags, and the hit is marked with control characters only.
	rows, _ = q.ListArticles(ctx, queries.ListArticlesParams{Q: str("cooking"), Sort: "relevance", Lim: 10})
	hs, _ = q.HighlightArticles(ctx, queries.HighlightArticlesParams{Q: str("cooking"), IDs: []string{rows[0].ID}})
	if h := hs[0].Headline; strings.Contains(h, "<") || !strings.Contains(h, "Bold") || !strings.Contains(h, list.HighlightStart+"cooking"+list.HighlightStop) {
		t.Errorf("headline %q", h)
	}
	// The planner takes the GIN index for the search (sequential scans off).
	conn, _ := pool.Acquire(ctx)
	defer conn.Release()
	conn.Exec(ctx, "SET enable_seqscan = off")
	var plan strings.Builder
	prows, err := conn.Query(ctx, "EXPLAIN SELECT id FROM article WHERE "+os.Getenv("SEARCH_COND"), "run database")
	if err != nil {
		t.Fatal(err)
	}
	for prows.Next() {
		var line string
		prows.Scan(&line)
		plan.WriteString(line + "\n")
	}
	if !strings.Contains(plan.String(), "article_search_idx") {
		t.Errorf("index not used:\n%s", plan.String())
	}
}
`

// TestGenerateSearch: a model with @search fields lists by full-text
// search: ranked by relevance when a search has no chosen sort,
// highlighted in parts, on the GIN index schema.lidza makes; the List
// type carries the highlights.
func TestGenerateSearch(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(articleSrc), 0o644)
	if _, _, err := pack.Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Article", Module: "app"}); err != nil {
		t.Fatal(err)
	}
	src := read(t, root, schema.FileName)
	for _, want := range []string{"highlights SearchHighlight[]?", "type SearchHighlight {", "type HighlightPart {"} {
		if !strings.Contains(src, want) {
			t.Errorf("schema lacks %q:\n%s", want, src)
		}
	}
	h := read(t, root, "handlers/article.go")
	for _, want := range []string{`"relevance"`, `p.Sort, p.Desc = "relevance", true`, "q.HighlightArticles(ctx", "list.Split(h.Headline)"} {
		if !strings.Contains(h, want) {
			t.Errorf("handlers lack %q", want)
		}
	}
	s, _ := schema.Parse(src)
	vector := s.Model("Article").SearchVector()
	t.Setenv("SEARCH_COND", vector+" @@ websearch_to_tsquery('english'::regconfig, $1)")
	os.MkdirAll(filepath.Join(root, "searchcheck"), 0o755)
	os.WriteFile(filepath.Join(root, "searchcheck", "search_test.go"), []byte(searchCheck), 0o644)
	compileApp(t, root, "article.go", "./searchcheck/")
}
