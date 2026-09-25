//go:build !windows

package devserver

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so that stopping it
// also stops what it spawned (npm starts node, node starts vite).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate signals the whole process group: SIGTERM, or SIGKILL when force.
func terminate(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
