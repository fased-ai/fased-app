package signer

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestMainnetCappedPolicyValidation(t *testing.T) {
	if err := (MainnetCappedPolicy{Confirmed: false, MaxOrderMicroUSD: 1000, MaxDailyMicroUSD: 2000}).Validate(); err == nil {
		t.Fatal("expected unconfirmed policy to fail")
	}
	if err := (MainnetCappedPolicy{Confirmed: true, MaxOrderMicroUSD: MainnetCappedHardMaxOrderMicroUSD + 1, MaxDailyMicroUSD: MainnetCappedHardMaxDailyMicroUSD}).Validate(); err == nil {
		t.Fatal("expected >$250 max order to fail")
	}
	if err := (MainnetCappedPolicy{Confirmed: true, MaxOrderMicroUSD: 50_000_000, MaxDailyMicroUSD: MainnetCappedHardMaxDailyMicroUSD + 1}).Validate(); err == nil {
		t.Fatal("expected >$1000 max daily spend to fail")
	}
	if err := (MainnetCappedPolicy{Confirmed: true, MaxOrderMicroUSD: 50_000_000, MaxDailyMicroUSD: 25_000_000}).Validate(); err == nil {
		t.Fatal("expected maxDaily < maxOrder to fail")
	}
	if err := (MainnetCappedPolicy{Confirmed: true, MaxOrderMicroUSD: 50_000_000, MaxDailyMicroUSD: 200_000_000}).Validate(); err != nil {
		t.Fatalf("expected valid capped policy, got %v", err)
	}
}

func TestVaultMainnetCappedSolanaSigningAndLimits(t *testing.T) {
	dir := privateSignerTempDir(t)
	signerID := strings.Repeat("b", 32)
	pass := []byte("vault-pass-mainnet-capped-1234")
	v, err := Open(dir, signerID)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer v.Close()

	if err := v.Create(pass); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
	if err := v.ConfigureMainnetCappedExecution(MainnetCappedPolicy{
		Confirmed:        true,
		MaxOrderMicroUSD: 50_000_000,  // $50/order
		MaxDailyMicroUSD: 100_000_000, // $100/day
	}); err != nil {
		t.Fatalf("ConfigureMainnetCappedExecution failed: %v", err)
	}
	if st := v.Status(); !st.MainnetCapped || st.MainnetMaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected mainnet-capped status: %+v", st)
	}

	vaultID := hex.EncodeToString([]byte("0123456789abcdef"))
	if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "operator",
		AllowedVenues:      []string{"solana"},
		AllowedAssets:      []string{"SOL", "USDC"},
		MaxOrderMicroUSD:   50_000_000,
		DailySpendMicroUSD: 100_000_000,
		DailyLossMicroUSD:  20_000_000,
		MaxLeverage:        1,
		ExpiresAt:          time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SetAgentVaultPolicy failed: %v", err)
	}

	memoB64 := buildValidDevnetTestMessage(t, v, "capped-mainnet-proof")
	exp := time.Now().Add(2 * time.Minute).Unix()

	// Genesis mismatch (Devnet hash under mainnet-capped) must fail.
	if _, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaDevnetGenesisHash,
		OperationID:       "op-bad-gen",
		Asset:             "SOL",
		EstimatedMicroUSD: 10_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID); err == nil {
		t.Fatal("expected devnet genesis hash mismatch to fail")
	}

	// WEN asset rejected even if vault only lists non-WEN.
	if _, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-wen-held",
		Asset:             "SAT",
		EstimatedMicroUSD: 10_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID); err == nil {
		t.Fatal("expected SAT/WEN asset to remain held")
	}

	// Valid $45 mainnet-beta sign passes.
	signed, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-main-1",
		Asset:             "SOL",
		EstimatedMicroUSD: 45_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID)
	if err != nil {
		t.Fatalf("SignSolanaMainnetCappedIPC valid failed: %v", err)
	}
	if signed.GenesisHash != onchain.SolanaMainnetGenesisHash || signed.Signature == "" {
		t.Fatalf("unexpected signed tx receipt: %+v", signed)
	}

	// Over $50 per-order cap rejected.
	if _, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-over-order",
		Asset:             "SOL",
		EstimatedMicroUSD: 55_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID); err == nil {
		t.Fatal("expected over-order cap spend to fail")
	}

	// Second $45 order passes ($90/$100 daily used).
	if _, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-main-2",
		Asset:             "SOL",
		EstimatedMicroUSD: 45_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID); err != nil {
		t.Fatalf("second $45 order failed: %v", err)
	}

	// Third $20 order exceeds $100 daily cap -> rejected.
	if _, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-over-daily",
		Asset:             "SOL",
		EstimatedMicroUSD: 20_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID); err == nil {
		t.Fatal("expected >$100 daily spend to fail")
	}

	// Close clears mainnetCappedEnabled.
	if err := v.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if st := v.Status(); st.MainnetCapped {
		t.Fatalf("expected Close to clear MainnetCapped, got %+v", st)
	}
}
