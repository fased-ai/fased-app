//go:build !linux

package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func RecoverApplication(context.Context, Manifest, string, ApplicationUpdateCallbacks, *custodyinstall.System) (Manifest, error) {
	return Manifest{}, unsupported
}

func ResumeApplication(context.Context, Manifest, string, ApplicationUpdateCallbacks, *custodyinstall.System) (Manifest, error) {
	return Manifest{}, unsupported
}

func ApplicationUpdateNeedsSource(int, string) (bool, error) { return false, unsupported }

func RejectUnstagedApplication(context.Context, Manifest, string, uint64, func() error, *custodyinstall.System) error {
	return unsupported
}
