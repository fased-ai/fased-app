//go:build !linux

package main

import (
	"errors"
	"io"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func custodyWindowTarget(owner int) (custodyinstall.Manifest, bool, error) {
	m, err := custodyinstall.Load(owner)
	return m, false, err
}

func initializePackageWallet(string, string, string, bool, bool, io.Writer) error {
	return errors.New("protected package wallet requires Linux")
}

func pendingPackageWalletTarget(int, string, string) (custodyinstall.Manifest, bool, error) {
	return custodyinstall.Manifest{}, false, nil
}
