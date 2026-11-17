//go:build custody_acceptance_fixture && wallet_payload

package main

import (
	_ "embed"

	"github.com/fased-ai/fased-app/internal/bootstrap"
)

// This payload exists only in an explicitly tagged disposable acceptance
// helper. The verified development release must match its compiled identity.
//
//go:embed wallet-signer-fixture-successor.bin
var fixtureSuccessorSigner []byte

var fixtureSuccessorVersion string
var fixtureSuccessorSource string
var fixtureSuccessorSHA256 string

func fixtureSuccessorWalletPayload() *bootstrap.WalletPayload {
	return &bootstrap.WalletPayload{Data: fixtureSuccessorSigner, SHA256: fixtureSuccessorSHA256, Version: fixtureSuccessorVersion, Source: fixtureSuccessorSource}
}
