package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The scanners run when the lock files changed or the last scan is a day
// old, and are skipped with a reason otherwise; --security and the CI
// mode always run them.
func TestSecurityDue(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte("a v1 h1:x\n"), 0o644)
	l := Layers{Dir: dir, Go: true}
	if due, _ := securityDue(l, SecurityWhenChanged); !due {
		t.Fatal("first run should scan")
	}
	recordScan(l, []ToolRun{{Tool: "govulncheck"}, {Tool: "npm audit", Skipped: true}})
	due, reason := securityDue(l, SecurityWhenChanged)
	if due || !strings.Contains(reason, "unchanged since the last scan") || !strings.Contains(reason, "--security") {
		t.Fatalf("after a scan: %v %q", due, reason)
	}
	if due, _ := securityDue(l, SecurityAlways); !due {
		t.Fatal("always")
	}
	if due, reason := securityDue(l, SecurityNever); due || reason == "" {
		t.Fatal("never")
	}
	// A changed lock file is due again.
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte("a v2 h1:y\n"), 0o644)
	if due, _ := securityDue(l, SecurityWhenChanged); !due {
		t.Fatal("a changed go.sum should scan")
	}
	// A failed scanner leaves no stamp.
	os.Remove(filepath.Join(dir, filepath.FromSlash(scanStampFile)))
	recordScan(l, []ToolRun{{Tool: "govulncheck", Failed: true, Reason: "offline"}})
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(scanStampFile))); err == nil {
		t.Fatal("a failed scan was recorded")
	}
	// A day later it is due.
	recordScan(l, nil)
	p := filepath.Join(dir, filepath.FromSlash(scanStampFile))
	data, _ := json.Marshal(scanStamp{Locks: lockHash(dir), At: time.Now().Add(-25 * time.Hour)})
	os.WriteFile(p, data, 0o644)
	if due, _ := securityDue(l, SecurityWhenChanged); !due {
		t.Fatal("a day-old scan should run again")
	}
}
