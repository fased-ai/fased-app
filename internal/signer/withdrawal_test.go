package signer

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestVaultSolanaWithdrawalSOLAndSPLAndOwnerOnlyGuard(t *testing.T) {
	t.Parallel()
	dir := privateSignerTempDir(t)
	signerID := strings.Repeat("d", 32)
	pass := []byte("withdraw-vault-passphrase-1234")
	v, err := OpenControlledOwnerCustody(dir, signerID, "mainnet-beta")
	if err != nil {
		t.Fatalf("OpenControlledOwnerCustody failed: %v", err)
	}
	defer func() { _ = v.Close() }()
	if err := v.Create(pass); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	destPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	destAddr := base58PublicKey(destPub)
	var bh [32]byte
	for i := range bh {
		bh[i] = byte(i + 21)
	}
	bhB58 := base58PublicKey(bh[:])
	exp := time.Now().Add(5 * time.Minute).Unix()

	// App role must never be permitted to invoke owner-only withdrawal action.
	if AppActionAllowed("solana-withdraw-sign") {
		t.Fatal("solana-withdraw-sign must remain owner-only, never in AppActionAllowed")
	}

	// Locked vault fails closed with ErrLocked.
	if _, err := v.SignSolanaWithdrawal(SolanaWithdrawIntent{
		Network:         "mainnet-beta",
		GenesisHash:     onchain.SolanaMainnetGenesisHash,
		Destination:     destAddr,
		Amount:          1_000_000,
		RecentBlockhash: bhB58,
		ExpiresAt:       exp,
	}); err != ErrLocked {
		t.Fatalf("expected ErrLocked when vault locked, got %v", err)
	}

	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	// Devnet request rejected on mainnet-beta vault.
	if _, err := v.SignSolanaWithdrawal(SolanaWithdrawIntent{
		Network:         "devnet",
		GenesisHash:     onchain.SolanaDevnetGenesisHash,
		Destination:     destAddr,
		Amount:          1_000_000,
		RecentBlockhash: bhB58,
		ExpiresAt:       exp,
	}); err == nil {
		t.Fatal("expected devnet withdrawal on mainnet-beta vault to fail")
	}

	// Valid mainnet-beta SOL withdrawal succeeds.
	solSigned, err := v.SignSolanaWithdrawal(SolanaWithdrawIntent{
		Network:                  "mainnet-beta",
		GenesisHash:              onchain.SolanaMainnetGenesisHash,
		Destination:              destAddr,
		Amount:                   50_000_000,
		RecentBlockhash:          bhB58,
		ComputeUnits:             75_000,
		PriorityFeeMicroLamports: 1_000,
		ExpiresAt:                exp,
	})
	if err != nil {
		t.Fatalf("SignSolanaWithdrawal(SOL) failed: %v", err)
	}
	if solSigned.GenesisHash != onchain.SolanaMainnetGenesisHash || solSigned.Signature == "" || solSigned.SignedTxBase64 == "" {
		t.Fatalf("unexpected SOL signed tx: %+v", solSigned)
	}
	pkt, err := base64.StdEncoding.DecodeString(solSigned.SignedTxBase64)
	if err != nil || len(pkt) <= 65 {
		t.Fatalf("invalid signed SOL tx packet: len=%d err=%v", len(pkt), err)
	}

	// Valid mainnet-beta SPL withdrawal (USDC) succeeds.
	splSigned, err := v.SignSolanaWithdrawal(SolanaWithdrawIntent{
		Network:         "mainnet-beta",
		GenesisHash:     onchain.SolanaMainnetGenesisHash,
		Destination:     destAddr,
		Mint:            onchain.USDC,
		Amount:          10_000_000,
		Decimals:        6,
		CreateDestATA:   true,
		RecentBlockhash: bhB58,
		ExpiresAt:       exp,
	})
	if err != nil {
		t.Fatalf("SignSolanaWithdrawal(SPL) failed: %v", err)
	}
	if splSigned.Signature == "" || splSigned.MessageSHA256 == "" {
		t.Fatalf("unexpected SPL signed tx: %+v", splSigned)
	}

	// Owner execute accepts solana-withdraw-sign even on an ordinary mainnet-beta vault with Stage 4 OFF,
	// while app channel (owner=false) is denied.
	withdrawReq := Request{
		Action: "solana-withdraw-sign",
		SolanaWithdraw: &SolanaWithdrawIntent{
			Network:         "mainnet-beta",
			GenesisHash:     onchain.SolanaMainnetGenesisHash,
			Destination:     destAddr,
			Amount:          2_000_000,
			RecentBlockhash: bhB58,
			ExpiresAt:       exp,
		},
	}
	if appResp := execute(v, withdrawReq, false); appResp.Error != "owner administration denied" {
		t.Fatalf("expected app channel withdrawal denial, got %+v", appResp)
	}
	dispResp := execute(v, withdrawReq, true)
	if dispResp.Error != "" || dispResp.SolanaDevnetSigned == nil || dispResp.SolanaDevnetSigned.Signature == "" {
		t.Fatalf("execute owner withdrawal failed: %+v", dispResp)
	}
}
