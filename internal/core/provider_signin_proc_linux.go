//go:build linux

package core

import (
	"os/exec"
	"syscall"
)

// configureSubprocessGroup places the child in its own process group and binds
// parent death to SIGKILL on Linux so orphaned CLI workers never outlive fased-app.
func configureSubprocessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
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
