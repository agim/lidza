package devserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PIDFile, under the project, records the running `lidza dev`: its pid
// and the CLI version, so `lidza update` can say when one still runs the
// old CLI.
var PIDFile = filepath.Join(BuildDir, "dev.pid")

// WritePID records this process as the project's dev server.
func WritePID(dir, version string) error {
	p := filepath.Join(dir, PIDFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(fmt.Sprintf("%d %s\n", os.Getpid(), version)), 0o644)
}

// RemovePID removes the record when it is this process's.
func RemovePID(dir string) {
	if pid, _, ok := RunningDev(dir); ok && pid == os.Getpid() {
		os.Remove(filepath.Join(dir, PIDFile))
	}
}

// RunningDev reports the project's running dev server: its pid and CLI
// version. ok is false when none is recorded or its process is gone.
func RunningDev(dir string) (pid int, version string, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, PIDFile))
	if err != nil {
		return 0, "", false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, "", false
	}
	pid, err = strconv.Atoi(fields[0])
	if err != nil || !alive(pid) {
		return 0, "", false
	}
	if len(fields) > 1 {
		version = fields[1]
	}
	return pid, version, true
}
