package db

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SocketDirs are where a local Postgres keeps its Unix socket, in the
// order LocalURL tries them: Debian and Ubuntu, Fedora and Arch, then
// /tmp (Homebrew and Postgres.app on macOS, source builds).
var SocketDirs = []string{"/var/run/postgresql", "/run/postgresql", "/tmp"}

// LocalURL is the address of a database on this machine's Postgres: its
// Unix socket where one of SocketDirs (or PGHOST, when it is a
// directory) has it, else TCP on 127.0.0.1. The port is PGPORT, else
// 5432. No password is needed on a socket with the default peer or
// trust authentication.
func LocalURL(name string) string {
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	if dir := socketDir(port); dir != "" {
		u := "postgres:///" + name + "?host=" + dir
		if port != "5432" {
			u += "&port=" + port
		}
		return u
	}
	return "postgres://127.0.0.1:" + port + "/" + name
}

func socketDir(port string) string {
	dirs := SocketDirs
	if h := os.Getenv("PGHOST"); strings.HasPrefix(h, "/") {
		dirs = append([]string{h}, dirs...)
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, ".s.PGSQL."+port)); err == nil {
			return d
		}
	}
	return ""
}

// RepairSocket returns raw with its socket directory replaced when raw
// names a Unix socket (host=/dir) that does not exist and this machine's
// Postgres has one elsewhere (an address written for Linux, used on a
// Mac); ok is false when raw needs no change.
func RepairSocket(raw string) (fixed string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	q := u.Query()
	host := q.Get("host")
	if !strings.HasPrefix(host, "/") {
		return raw, false
	}
	port := q.Get("port")
	if port == "" {
		port = "5432"
	}
	if _, err := os.Stat(filepath.Join(host, ".s.PGSQL."+port)); err == nil {
		return raw, false
	}
	dir := socketDir(port)
	if dir == "" || dir == host {
		return raw, false
	}
	// The directory is swapped in place, so the address stays as readable
	// as it was written.
	return strings.Replace(raw, "host="+host, "host="+dir, 1), true
}
