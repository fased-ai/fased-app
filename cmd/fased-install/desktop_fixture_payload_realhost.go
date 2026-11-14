//go:build custody_acceptance_fixture && wallet_payload && wallet_native && fased_realhost_fixture

package main

import (
	_ "embed"

	"github.com/fased-ai/fased-app/internal/bootstrap"
)

// The alternate window exists only in the separately compiled host fixture.
//
//go:embed wallet-desktop-fixture-successor.bin
var fixtureSuccessorDesktop []byte

var fixtureSuccessorDesktopSHA256 string

func fixtureSuccessorDesktopPayload() *bootstrap.WalletPayload {
	return &bootstrap.WalletPayload{Data: fixtureSuccessorDesktop, SHA256: fixtureSuccessorDesktopSHA256, Version: fixtureSuccessorVersion, Source: fixtureSuccessorSource}
}
