//go:build !windows

package devserver

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the copier goroutine and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestProcPrefixesAndStopsGroup(t *testing.T) {
	var out syncBuffer
	// sh spawns a grandchild (sleep) that must die with the group.
	cmd := exec.Command("sh", "-c", "echo hello; echo err >&2; sleep 30 & wait")
	p, err := startProc(cmd, &out, "[x] ")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "[x] err") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.exited() {
		t.Fatal("exited too early")
	}
	start := time.Now()
	p.stop(2 * time.Second)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("stop took %v: grandchild kept the pipe open, group kill failed", d)
	}
	if !p.exited() {
		t.Fatal("not exited after stop")
	}
	got := out.String()
	if !strings.Contains(got, "[x] hello\n") || !strings.Contains(got, "[x] err\n") {
		t.Fatalf("output %q", got)
	}
}

func TestProcExitsOnItsOwn(t *testing.T) {
	var out syncBuffer
	p, err := startProc(exec.Command("true"), &out, "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("never reaped")
	}
	p.stop(time.Second) // must be a no-op, not hang
}
