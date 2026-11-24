//go:build linux

package main

import (
	"context"
	"errors"
	"os/exec"
)

func launchOwnerEnrollment(ctx context.Context, path string, args []string) error {
	term, err := resolveTrustedLinuxTerminal(nil)
	if err != nil {
		return errors.New("a trusted system terminal is required for initial OS approval")
	}
	// Wait for the delegated controller, not merely a terminal launcher. No
	// shell string, private input or password enters this process.
	cmdArgs := append(append([]string(nil), term.execArgs...), path)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, term.path, cmdArgs...)
	if err = cmd.Run(); err != nil {
		return errors.New("wallet setup did not complete in the system window; reconnect to resume")
	}
	// Some terminals return zero even after a failed delegated command. The
	// caller must inspect the existing enrollment's readiness after this exit.
	return nil
}
