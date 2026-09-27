//go:build windows

package devserver

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func terminate(cmd *exec.Cmd, force bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// alive is unknown on Windows: a recorded dev server counts as running.
func alive(pid int) bool { return pid > 0 }
