//go:build windows

package devserver

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func terminate(cmd *exec.Cmd, force bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
