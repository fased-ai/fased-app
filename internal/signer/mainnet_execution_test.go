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

func TestVaultV3MainnetCappedPolicyReplay(t *testing.T) {
	dir := privateSignerTempDir(t)
	signerID := strings.Repeat("c", 32)
	pass := []byte("vault-v3-mainnet-capped-pass")

	v, err := OpenControlledOwnerCustody(dir, signerID, "mainnet-beta")
	if err != nil {
		t.Fatalf("OpenControlledOwnerCustody failed: %v", err)
	}
	if err := v.Create(pass); err != nil {
		_ = v.Close()
		t.Fatalf("Create failed: %v", err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		_ = v.Close()
		t.Fatalf("Unlock failed: %v", err)
	}
	if st := v.Status(); st.Account == nil || st.Account.Version != 3 || st.Account.Network != "mainnet-beta" {
		_ = v.Close()
		t.Fatalf("expected v3 mainnet-beta vault, got %+v", st.Account)
	}
	if err := v.ConfigureMainnetCappedExecution(MainnetCappedPolicy{
		Confirmed:        true,
		MaxOrderMicroUSD: 25_000_000,
		MaxDailyMicroUSD: 100_000_000,
	}); err != nil {
		_ = v.Close()
		t.Fatalf("ConfigureMainnetCappedExecution failed on v3 mainnet-beta vault: %v", err)
	}

	vaultID := hex.EncodeToString([]byte("v3mainnetvault01"))
	if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "operator",
		AllowedVenues:      []string{"solana"},
		AllowedAssets:      []string{"SOL"},
		MaxOrderMicroUSD:   25_000_000,
		DailySpendMicroUSD: 100_000_000,
		DailyLossMicroUSD:  10_000_000,
		MaxLeverage:        1,
		ExpiresAt:          time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		_ = v.Close()
		t.Fatalf("SetAgentVaultPolicy failed on v3 mainnet-beta vault: %v", err)
	}

	memoB64 := buildValidDevnetTestMessage(t, v, "v3-mainnet-capped-proof")
	exp := time.Now().Add(2 * time.Minute).Unix()
	signed, err := v.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-v3-main-1",
		Asset:             "SOL",
		EstimatedMicroUSD: 20_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID)
	if err != nil {
		_ = v.Close()
		t.Fatalf("SignSolanaMainnetCappedIPC failed on v3 mainnet-beta vault: %v", err)
	}
	if signed.Signature == "" {
		_ = v.Close()
		t.Fatal("expected non-empty signature")
	}
	if err := v.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen the Version-3 mainnet-beta vault and verify policy & spend journal replay cleanly.
	reopened, err := OpenControlledOwnerCustody(dir, signerID, "mainnet-beta")
	if err != nil {
		t.Fatalf("reopen v3 mainnet-beta vault failed: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatalf("reopened Unlock failed: %v", err)
	}
	if err := reopened.ConfigureMainnetCappedExecution(MainnetCappedPolicy{
		Confirmed:        true,
		MaxOrderMicroUSD: 25_000_000,
		MaxDailyMicroUSD: 100_000_000,
	}); err != nil {
		t.Fatalf("reopened ConfigureMainnetCappedExecution failed: %v", err)
	}
	vaults, err := reopened.AgentVaults()
	if err != nil {
		t.Fatalf("AgentVaults failed: %v", err)
	}
	if len(vaults) != 1 || vaults[0].Policy.VaultID != vaultID || vaults[0].DailySpentMicroUSD != 20_000_000 {
		t.Fatalf("expected replayed agent vault policy %s with 20_000_000 daily spend, got %+v", vaultID, vaults)
	}
	// Duplicate operationId must still be blocked across journal replay.
	if _, err := reopened.SignSolanaMainnetCappedIPC(SolanaMainnetCappedSignIntent{
		Network:           "mainnet-beta",
		GenesisHash:       onchain.SolanaMainnetGenesisHash,
		OperationID:       "op-v3-main-1",
		Asset:             "SOL",
		EstimatedMicroUSD: 10_000_000,
		MessageBase64:     memoB64,
		ExpiresAt:         exp,
	}, vaultID); err == nil {
		t.Fatal("expected duplicate operationId to be rejected after v3 journal replay")
	}
}
