//go:build wallet_payload

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/fased-ai/fased-app/internal/core"
	"testing"
)

func TestCompiledWalletPayloadRemainsBoundAndInert(t *testing.T) {
	p := walletPayload()
	if p == nil || p.Version != core.Version || len(p.Source) != 40 || len(p.Data) == 0 {
		t.Fatal("missing compile-time wallet payload binding")
	}
	hash := sha256.Sum256(p.Data)
	if p.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("compiled signer does not match its binding")
	}
	// This test inspects inert embedded bytes only; it never executes a signer.
}
