package devserver

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// proc is a child process whose output is copied to a writer line by line
// with a prefix, and which is stopped together with its process group.
type proc struct {
	cmd  *exec.Cmd
	done chan struct{} // closed once the process has exited and been reaped
}

// startProc starts cmd in its own process group with stdout and stderr
// copied to out, each line prefixed.
func startProc(cmd *exec.Cmd, out io.Writer, prefix string) (*proc, error) {
	setProcessGroup(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, err
	}
	p := &proc{cmd: cmd, done: make(chan struct{})}
	copied := make(chan struct{})
	go func() {
		copyLines(pr, out, prefix)
		close(copied)
	}()
	go func() {
		// Wait returns once the process exited and every descendant that
		// inherited the pipe has closed it; then the copier drains.
		_ = cmd.Wait()
		pw.Close()
		<-copied
		close(p.done)
	}()
	return p, nil
}

// stop terminates the process group, forcibly after grace, and waits for the
// exit to be reaped and the output drained.
func (p *proc) stop(grace time.Duration) {
	if p == nil {
		return
	}
	terminate(p.cmd, false)
	select {
	case <-p.done:
		return
	case <-time.After(grace):
	}
	terminate(p.cmd, true)
	<-p.done
}

// exited reports whether the process has already ended on its own.
func (p *proc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func copyLines(r io.Reader, out io.Writer, prefix string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Fprintf(out, "%s%s\n", prefix, sc.Text())
	}
}

// prefixWriter returns a writer that prefixes each line written through it.
// Suitable for short-lived commands whose output ends when they exit.
func prefixWriter(out io.Writer, prefix string) io.WriteCloser {
	pr, pw := io.Pipe()
	go copyLines(pr, out, prefix)
	return pw
}
