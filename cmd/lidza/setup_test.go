package main

import (
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
	from, to, err := repairEnvSocket(env)
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
	if _, to, _ := repairEnvSocket(env); to != "" {
		t.Fatalf("repaired twice: %s", to)
	}
	if _, to, _ := repairEnvSocket(filepath.Join(dir, "absent")); to != "" {
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
