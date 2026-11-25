//go:build !linux

package vpsnetwork

import (
	"context"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// Begin starts or resumes a timed recoverable Remote network lockdown attempt.
func Begin(context.Context, Manifest, *custodyinstall.System, Approval) (Status, error) {
	return Status{}, errUnsupportedOS
}

// Confirm promotes a pending lockdown attempt from a second private SSH session.
func Confirm(context.Context, Manifest, *custodyinstall.System, string) (Status, error) {
	return Status{}, errUnsupportedOS
}

// Rollback reverts owned SSH/firewall state and removes owned temporary systemd units.
func Rollback(context.Context, Manifest, *custodyinstall.System) (Status, error) {
	return Status{}, errUnsupportedOS
}

// Inspect verifies and reports the current Remote network lockdown state.
func Inspect(context.Context, Manifest, *custodyinstall.System) (Status, error) {
	return Status{}, errUnsupportedOS
}
