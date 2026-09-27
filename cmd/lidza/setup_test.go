package main

import (
	"bufio"

	"github.com/agim/lidza/pkg/brief"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
