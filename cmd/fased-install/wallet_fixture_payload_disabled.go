//go:build !custody_acceptance_fixture || !wallet_payload

package main

import "github.com/fased-ai/fased-app/internal/bootstrap"

// Normal builds never include an alternate embedded signer.
func fixtureSuccessorWalletPayload() *bootstrap.WalletPayload { return nil }
