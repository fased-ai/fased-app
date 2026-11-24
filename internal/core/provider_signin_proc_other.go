//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

const createNewProcessGroupFlag = 0x00000200

func configureSubprocessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroupFlag
	cmd.Cancel = func() error {
		killSubprocessGroup(cmd)
		return nil
	}
}

func killSubprocessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
