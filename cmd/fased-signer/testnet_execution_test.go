package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestDecodeTypedSignerStdin(t *testing.T) {
	t.Parallel()
	var req signer.Request
	if err := decodeTypedSignerStdin("hyperliquid-policy", []byte(`{"version":1,"network":"testnet","allowedAssets":["BTC"],"maxLeverage":5,"maxNotionalMicroUsd":5000000,"maxOrderPriceBpsDeviation":500,"expiresAt":1999999999}`), &req); err != nil {
		t.Fatalf("hyperliquid-policy decode failed: %v", err)
	}
	if req.HyperliquidPolicy == nil || req.HyperliquidPolicy.Network != "testnet" {
		t.Fatalf("unexpected decoded policy: %+v", req.HyperliquidPolicy)
	}
	// Unknown fields must fail closed.
	var badReq signer.Request
	if err := decodeTypedSignerStdin("agent-vault-set", []byte(`{"version":1,"vaultId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","approved":true}`), &badReq); err == nil {
		t.Fatal("expected unknown field error")
	}
	var revReq signer.Request
	if err := decodeTypedSignerStdin("agent-vault-revoke", []byte(`{"vaultId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), &revReq); err != nil || revReq.AgentVaultID != strings.Repeat("a", 32) {
		t.Fatalf("agent-vault-revoke decode failed: %+v err=%v", revReq, err)
	}
	if !isTypedOutputSignerAction("hyperliquid-status") || !isTypedOutputSignerAction("agent-vault-list") {
		t.Fatal("expected hyperliquid-status and agent-vault-list typed outputs")
	}
	var out, errOut bytes.Buffer
	if err := run([]string{"serve", "--disposable-fixture", "--config", "/nonexistent", "-execution-profile", "mainnet"}, nil, &out, &errOut); err == nil {
		t.Fatal("expected invalid execution profile error")
	}
}
