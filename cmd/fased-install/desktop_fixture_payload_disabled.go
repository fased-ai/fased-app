//go:build !custody_acceptance_fixture || !wallet_payload || !wallet_native || !fased_realhost_fixture

package main

import "github.com/fased-ai/fased-app/internal/bootstrap"

func fixtureSuccessorDesktopPayload() *bootstrap.WalletPayload { return nil }
