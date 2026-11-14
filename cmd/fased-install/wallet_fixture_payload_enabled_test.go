//go:build custody_acceptance_fixture && wallet_payload

package main

import (
	"io"
	"testing"

	"github.com/fased-ai/fased-app/internal/bootstrap"
)

func TestCompiledFixtureSuccessorRemainsBound(t *testing.T) {
	p := fixtureSuccessorWalletPayload()
	if p == nil || p.Version != fixtureSuccessorVersion || p.Source != fixtureSuccessorSource || p.SHA256 != fixtureSuccessorSHA256 || p.Version == walletPayload().Version {
		t.Fatal("distinct fixture signer identity required")
	}
	if err := bootstrap.WriteWalletPayload(io.Discard, p); err != nil {
		t.Fatal("fixture signer bytes differ from their compiled identity", err)
	}
	id := bootstrap.MaintenanceIdentity{Version: p.Version, Source: p.Source}
	if selectEmbeddedSigner(id, false, walletPayload(), p) != nil || selectEmbeddedSigner(id, true, walletPayload(), p) != p {
		t.Fatal("fixture successor crossed its development boundary")
	}
}
