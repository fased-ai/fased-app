//go:build wallet_native && wallet_payload

package main

import (
	_ "embed"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
)

//go:embed wallet-desktop.bin
var walletDesktopData []byte
var compiledDesktopSHA256 string

func walletDesktopPayload() *bootstrap.WalletPayload {
	return &bootstrap.WalletPayload{Data: walletDesktopData, SHA256: compiledDesktopSHA256, Version: core.Version, Source: compiledWalletSource}
}
