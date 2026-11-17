//go:build !linux

package signer

import (
	"context"
	"errors"

	"github.com/fased-ai/fased-app/internal/update"
)

func VerifyInstallationGuardBoundary(Config, string, string) error {
	return errors.New("Linux signer restart guard required")
}

func InstallationPreparationFenceBytes(int, string) ([]byte, error) {
	return nil, errors.New("Linux signer restart guard required")
}

func VerifyInstallationPreparationBoundary(Config) error {
	return errors.New("Linux signer restart guard required")
}

func VerifyInstallationPreparationStart(Config) error {
	return errors.New("Linux signer restart guard required")
}

func VerifyInstallationStartGuard(context.Context, Config, string, string, string) error {
	return errors.New("Linux signer start guard required")
}

func VerifyInstallationGuardInventory(Config, *update.Installation, string, string, string) error {
	return errors.New("Linux signer installation guard required")
}
