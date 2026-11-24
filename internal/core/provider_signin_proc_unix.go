//go:build !windows

package core

import (
	"os/exec"
	"syscall"
)

// configureSubprocessGroup places the child in its own process group so
// cancellation or timeout tears down grandchildren (e.g. Node/npx workers) too.
func configureSubprocessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		killSubprocessGroup(cmd)
		return nil
	}
}

func killSubprocessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}
