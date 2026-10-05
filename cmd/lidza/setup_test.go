package main

import (
	"bufio"
	"context"
	"fmt"

	"github.com/agim/lidza/pkg/brief"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
)

// An .env written with Linux's socket directory is repaired on a machine
// whose Postgres listens elsewhere (a Mac's /tmp); the rest of the file
// and its permissions stay.
func TestRepairEnvSocket(t *testing.T) {
	sock := t.TempDir()
	os.WriteFile(filepath.Join(sock, ".s.PGSQL.5432"), nil, 0o600)
	old := db.SocketDirs
	db.SocketDirs = []string{filepath.Join(sock, "missing"), sock}
	t.Cleanup(func() { db.SocketDirs = old })
	t.Setenv("PGHOST", "")
	t.Setenv("PGPORT", "")

	dir := t.TempDir()
	env := filepath.Join(dir, ".env.test")
	os.WriteFile(env, []byte("# test\nDATABASE_URL=postgres:///app_test?host=/var/run/postgresql-nowhere\nMAIL_PROVIDER=outbox\n"), 0o644)
	from, to, err := db.RepairEnvFile(env)
	if err != nil || from != "postgres:///app_test?host=/var/run/postgresql-nowhere" || to != "postgres:///app_test?host="+sock {
		t.Fatalf("repair: %q %q %v", from, to, err)
	}
	data, _ := os.ReadFile(env)
	if !strings.Contains(string(data), "DATABASE_URL=postgres:///app_test?host="+sock+"\n") || !strings.Contains(string(data), "MAIL_PROVIDER=outbox") {
		t.Fatalf("file: %s", data)
	}
	if st, _ := os.Stat(env); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode: %v", st.Mode())
	}
	// Once right, nothing changes.
	if _, to, _ := db.RepairEnvFile(env); to != "" {
		t.Fatalf("repaired twice: %s", to)
	}
	if _, to, _ := db.RepairEnvFile(filepath.Join(dir, "absent")); to != "" {
		t.Fatal("a missing file")
	}
}

func TestTail(t *testing.T) {
	if got := tail("a\nb\nc\nd\n", 2); got != "c\nd" {
		t.Fatalf("tail: %q", got)
	}
}

// The doctor's tested Node is the one install.sh installs.
func TestNodeTested(t *testing.T) {
	data, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "NODE_VERSION="); ok {
			if nodeMajor(v) != nodeTested {
				t.Fatalf("install.sh installs Node %s, the doctor tests against %d", v, nodeTested)
			}
			return
		}
	}
	t.Fatal("no NODE_VERSION in install.sh")
}

// The terminal interview: numbers pick suggestions, several for a "many"
// question, words are the developer's own, Enter skips, q stops.
func TestInterview(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	if _, err := brief.Ensure(dir, "demo"); err != nil {
		t.Fatal(err)
	}
	// purpose (text), users (many), journeys (text, skipped), out_of_scope
	// (many: a number with added words), then q.
	in := bufio.NewReader(strings.NewReader("A place to keep recipes\n1,2\n\n3: for now\nq\n"))
	var out strings.Builder
	if err := interview(dir, "demo", false, in, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := brief.Load(dir)
	if b.Answers["purpose"] != "A place to keep recipes" || b.Answers["users"] != "Individuals, for themselves, Small teams working together" || b.Answers["journeys"] != "" || b.Answers["out_of_scope"] != "Offline use: for now" {
		t.Fatalf("answers: %+v\n%s", b.Answers, out.String())
	}
	if !strings.Contains(out.String(), "3 answer(s) saved") {
		t.Fatalf("summary: %s", out.String())
	}
	// s skips a question for good; S skips the rest.
	in = bufio.NewReader(strings.NewReader("s\nS\n"))
	out.Reset()
	if err := interview(dir, "demo", false, in, &out); err != nil {
		t.Fatal(err)
	}
	b, _ = brief.Load(dir)
	if !b.Skipped["journeys"] || len(b.Open()) != 0 || !strings.Contains(out.String(), "skipped the remaining") {
		t.Fatalf("skip: %+v\n%s", b.Skipped, out.String())
	}
	// Several picks combine for a free question with suggestions too.
	done, _ := brief.Find("done")
	if got := resolveChoice(done, "1,3"); got != "The README says how to use it, Screenshots in the report" {
		t.Fatalf("text with picks: %q", got)
	}
	// A pasted question header or explanation is removed; the numbers
	// after it are still choices.
	users, _ := brief.Find("users")
	if got := resolveChoice(users, brief.Clean(users, users.Why+"1,2,4")); got != "Individuals, for themselves, Small teams working together, Visitors who only read" {
		t.Fatalf("pasted why: %q", got)
	}
	purpose, _ := brief.Find("purpose")
	if got := brief.Clean(purpose, "[Product 1/31] "+purpose.Ask); got != "" {
		t.Fatalf("pasted question: %q", got)
	}
	q, _ := brief.Find("palette")
	if got := resolveChoice(q, "1,2"); got != "1,2" {
		t.Fatalf("a one question took two choices: %q", got)
	}
	if got := resolveChoice(q, "9"); got != "9" {
		t.Fatalf("out of range: %q", got)
	}
}

// lidza admin add and remove edit ADMIN_USERS without duplicates, in
// any case, keeping the order.
func TestAdminList(t *testing.T) {
	list := editList([]string{"a@x.io"}, []string{"B@x.io", "A@X.io", " "}, true)
	if strings.Join(list, ",") != "a@x.io,B@x.io" {
		t.Fatalf("add: %v", list)
	}
	if list = editList(list, []string{"a@X.io"}, false); strings.Join(list, ",") != "B@x.io" {
		t.Fatalf("remove: %v", list)
	}
}

// lidza test --e2e loads e2e/seed.sql into the test database, several
// statements at once, and a rerun leaves the same rows.
func TestE2ESeed(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer pool.Close()
	dir := t.TempDir()
	t.Setenv("DATABASE_URL", url)
	if err := seedTestDB(ctx, dir); err != nil {
		t.Fatalf("no seed file: %v", err)
	}
	os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
	os.WriteFile(filepath.Join(dir, E2ESeed), []byte("CREATE TABLE IF NOT EXISTS e2e_seed_probe (id int PRIMARY KEY);\nINSERT INTO e2e_seed_probe VALUES (1), (2) ON CONFLICT DO NOTHING;\n"), 0o644)
	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS e2e_seed_probe") })
	for i := 0; i < 2; i++ {
		if err := seedTestDB(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM e2e_seed_probe").Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows: %d %v", n, err)
	}
}

// An existing .env gains the keys of packs enabled since it was written,
// and keeps every line it had; a key another layer sets is not added.
func TestAddMissingEnv(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lidza.json"), []byte(`{"name":"demo","frontend":{"template":"htmx"},"packs":["lidza/db","lidza/cache","lidza/auth","lidza/mail"]}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("LIDZA_ADDR=127.0.0.1:3000\nMAIL_FROM=\"Me <me@example.com>\"\nDB_MAX_CONNS=5\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.dev"), []byte("AUTH_SECRET=from-dev-file-xxxxxxxxxxxxxxxxxxxxxxxx\nAUTH_ACCESS_TTL=5m\nAUTH_REFRESH_TTL=24h\nMAIL_PROVIDER=outbox\nAPP_URL=http://127.0.0.1:3000\n"), 0o600)
	t.Setenv("LIDZA_MODE", "dev")
	added, err := addMissingEnv(dir, map[string]string{"DATABASE_URL": "postgres:///demo_dev", "AUTH_SECRET": "s", "MAIL_FROM": "x", "CACHE_URL": "memory"})
	if err != nil || strings.Join(added, ",") != "DATABASE_URL,CACHE_URL" {
		t.Fatalf("added %v, %v", added, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if s := string(data); !strings.HasPrefix(s, "LIDZA_ADDR=127.0.0.1:3000\nMAIL_FROM=\"Me <me@example.com>\"\n") || !strings.HasSuffix(s, "DATABASE_URL=postgres:///demo_dev\nCACHE_URL=memory\n") {
		t.Fatalf(".env:\n%s", s)
	}
	if added, _ := addMissingEnv(dir, map[string]string{"DATABASE_URL": "other"}); added != nil {
		t.Fatalf("added again: %v", added)
	}
}

// lidza dev's repair applies pending migrations and goes on, but refuses
// to start the app when one dropping data is held: the code after it
// would run on a schema it does not match.
func TestDevRepairRefusesHeldMigration(t *testing.T) {
	base := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if base == "" {
		base = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: base, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer pool.Close()
	name := fmt.Sprintf("lidza_devrepair_%d", time.Now().UnixNano())
	url := "postgres:///" + name + "?host=/var/run/postgresql"
	t.Cleanup(func() { pool.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lidza.json"), []byte(`{"name":"demo","frontend":{"template":"htmx"},"packs":["lidza/db"]}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL="+url+"\n"), 0o600)
	migrations := filepath.Join(dir, "db", "migrations")
	os.MkdirAll(migrations, 0o755)
	os.WriteFile(filepath.Join(migrations, "0001_create_note.up.sql"), []byte("CREATE TABLE note (id int PRIMARY KEY, body text);\n"), 0o644)
	t.Setenv("LIDZA_DATABASE_URL", url)
	t.Setenv("DATABASE_URL", url)
	_, cfg, err := loadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := devRepair(ctx, cfg, &out); err != nil {
		t.Fatalf("clean migrations refused: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "1 migration(s) applied") {
		t.Fatalf("not applied:\n%s", out.String())
	}
	os.WriteFile(filepath.Join(migrations, "0002_alter_note.up.sql"), []byte("ALTER TABLE note DROP COLUMN body; "+db.DataLoss+"\n"), 0o644)
	os.WriteFile(filepath.Join(migrations, "0003_create_tag.up.sql"), []byte("CREATE TABLE tag (id int PRIMARY KEY);\n"), 0o644)
	out.Reset()
	err = devRepair(ctx, cfg, &out)
	if err == nil || !strings.Contains(err.Error(), "the app is not started") || !strings.Contains(err.Error(), "lidza install --migrate") {
		t.Fatalf("held migration not refused: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "0002_alter_note") {
		t.Fatalf("held migration not named:\n%s", out.String())
	}
}
