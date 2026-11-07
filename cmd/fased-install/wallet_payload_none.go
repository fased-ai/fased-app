//go:build !wallet_payload

package main

import "github.com/fased-ai/fased-app/internal/bootstrap"

func walletPayload() *bootstrap.WalletPayload { return nil }
