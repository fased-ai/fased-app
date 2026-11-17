//go:build !wallet_native || !wallet_payload

package main

import "github.com/fased-ai/fased-app/internal/bootstrap"

func walletDesktopPayload() *bootstrap.WalletPayload { return nil }
