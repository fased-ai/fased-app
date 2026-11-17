//go:build !linux

package ownerclient

import (
	"errors"

	"github.com/fased-ai/fased-app/internal/signer"
)

func localPackageOwnerTarget(int, string) (string, error) { return "", nil }

func LocalOwnerPackage(signer.OwnerIntent) (Package, error) {
	return Package{}, errors.New("Local protected Wallets requires Linux")
}
