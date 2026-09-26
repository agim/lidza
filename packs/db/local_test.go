package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalURL(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	old := SocketDirs
	t.Cleanup(func() { SocketDirs = old })
	t.Setenv("PGHOST", "")
	t.Setenv("PGPORT", "")

	// No socket anywhere: TCP.
	SocketDirs = []string{a, b}
	if got := LocalURL("app_dev"); got != "postgres://127.0.0.1:5432/app_dev" {
		t.Fatalf("tcp: %s", got)
	}
	// The second directory has it (a Mac's /tmp).
	os.WriteFile(filepath.Join(b, ".s.PGSQL.5432"), nil, 0o600)
	if got := LocalURL("app_dev"); got != "postgres:///app_dev?host="+b {
		t.Fatalf("socket: %s", got)
	}
	// PGPORT picks another socket.
	t.Setenv("PGPORT", "5433")
	os.WriteFile(filepath.Join(a, ".s.PGSQL.5433"), nil, 0o600)
	if got := LocalURL("app_dev"); got != "postgres:///app_dev?host="+a+"&port=5433" {
		t.Fatalf("port: %s", got)
	}
	t.Setenv("PGPORT", "")

	// An address written for a directory without the socket is repaired;
	// one that works, or a TCP address, is left alone.
	missing := filepath.Join(a, "nope")
	if got, ok := RepairSocket("postgres:///app_dev?host=" + missing); !ok || got != "postgres:///app_dev?host="+b {
		t.Fatalf("repair: %s %v", got, ok)
	}
	if _, ok := RepairSocket("postgres:///app_dev?host=" + b); ok {
		t.Fatal("a working socket was changed")
	}
	if _, ok := RepairSocket("postgres://u:p@db.example.com/app"); ok {
		t.Fatal("a TCP address was changed")
	}
}
