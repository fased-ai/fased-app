//go:build wallet_payload

package main

import (
	_ "embed"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
)

//go:embed wallet-signer.bin
var walletSignerData []byte
var compiledWalletSource string
var compiledWalletSHA256 string

func walletPayload() *bootstrap.WalletPayload {
	return &bootstrap.WalletPayload{Data: walletSignerData, SHA256: compiledWalletSHA256, Version: core.Version, Source: compiledWalletSource}
}
