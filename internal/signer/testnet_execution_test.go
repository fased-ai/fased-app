package signer

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func privateSignerTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func buildValidDevnetTestMessage(t *testing.T, v *Vault, memo string) string {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(priv)
	var feePayer [32]byte
	copy(feePayer[:], priv[ed25519.SeedSize:])
	var bh [32]byte
	bh[0] = 0x55
	bh[31] = 0xAA
	msg, err := onchain.BuildDevnetComputeMemoMessage(feePayer, bh, 50_000, 1_000, memo)
	if err != nil {
		t.Fatalf("BuildDevnetComputeMemoMessage failed: %v", err)
	}
	return base64.StdEncoding.EncodeToString(msg)
}

func buildArbitraryProgramTestMessage(t *testing.T, v *Vault) string {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(priv)
	var feePayer [32]byte
	copy(feePayer[:], priv[ed25519.SeedSize:])
	var unknownProgram [32]byte
	unknownProgram[0] = 0xDE
	unknownProgram[1] = 0xAD
	// Legacy message header [1, 0, 1], 2 keys (feePayer + unknownProgram), blockhash, 1 instr (progIdx=1, 0 accts, 0 data)
	var out []byte
	out = append(out, 1, 0, 1, 2)
	out = append(out, feePayer[:]...)
	out = append(out, unknownProgram[:]...)
	for i := 0; i < 32; i++ {
		out = append(out, byte(i+1))
	}
	out = append(out, 1, 1, 0, 0)
	return base64.StdEncoding.EncodeToString(out)
}

func TestTestnetExecutionDefaultOffAndMainnetRefusal(t *testing.T) {
	dir := privateSignerTempDir(t)
	signerID := strings.Repeat("a", 32)
	pass := []byte("correct-horse-battery-staple-99")
	v, err := Open(dir, signerID)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(pass, 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	// Default Status omits TestnetExecution when false.
	raw, err := json.Marshal(v.Status())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "testnetExecution") {
		t.Fatalf("expected default Status JSON to omit testnetExecution, got %s", raw)
	}

	// Default Vault refuses SignSolanaDevnetIPC until ConfigureTestnetExecution(true).
	_, err = v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
		Network:           "devnet",
		GenesisHash:       onchain.SolanaDevnetGenesisHash,
		OperationID:       "sol-dev-off",
		Asset:             "SOL",
		EstimatedMicroUSD: 1_000,
		MessageBase64:     buildValidDevnetTestMessage(t, v, "fased-off"),
		ExpiresAt:         time.Now().Add(time.Minute).Unix(),
	}, "11111111111111111111111111111111")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when default off, got %v", err)
	}

	// Mainnet ordinary vault refuses ConfigureTestnetExecution(true).
	mainnetDir := privateSignerTempDir(t)
	vm, err := OpenOrdinaryCustody(mainnetDir, signerID, "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	if err := vm.ConfigureTestnetExecution(true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable on mainnet-beta vault, got %v", err)
	}
}

func TestSignSolanaDevnetIPCAndAgentVaultEnforcement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		useV3Dev bool
	}{
		{name: "version1_controlled"},
		{name: "version3_devnet", useV3Dev: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateSignerTempDir(t)
			signerID := strings.Repeat("b", 32)
			pass := []byte("devnet-stage4-passphrase-2026")
			var v *Vault
			var err error
			if tc.useV3Dev {
				v, err = OpenOrdinaryCustody(dir, signerID, "devnet")
			} else {
				v, err = Open(dir, signerID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Create(pass); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}
			if err := v.Unlock(pass, 10*time.Minute); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}
			if err := v.ConfigureTestnetExecution(true); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}
			if !v.Status().TestnetExecution {
				_ = v.Close()
				t.Fatal("expected TestnetExecution=true in Status")
			}

			now := time.Now().Unix()
			vaultID := strings.Repeat("c", 32)
			if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
				Version:            1,
				VaultID:            vaultID,
				AgentID:            "desk-agent-1",
				AllowedVenues:      []string{"solana", "hyperliquid"},
				AllowedAssets:      []string{"SOL", "ETH"},
				MaxOrderMicroUSD:   5_000_000,
				DailySpendMicroUSD: 8_000_000,
				DailyLossMicroUSD:  2_000_000,
				MaxLeverage:        5,
				ExpiresAt:          now + 3600,
			}); err != nil {
				_ = v.Close()
				t.Fatalf("SetAgentVaultPolicy failed: %v", err)
			}

			var bh [32]byte
			for i := range bh {
				bh[i] = byte(i + 11)
			}
			bhB58 := onchain.Base58(bh[:])
			msgB64 := buildValidDevnetTestMessage(t, v, "fased-rehearsal-1")

			// Wrong network or mainnet rejected.
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "mainnet-beta",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-mainnet-reject",
				Asset:             "SOL",
				EstimatedMicroUSD: 1_000_000,
				Blockhash:         bhB58,
				Memo:              "fased-rehearsal-1",
				ExpiresAt:         now + 300,
			}, vaultID); !errors.Is(err, ErrUnavailable) {
				_ = v.Close()
				t.Fatalf("expected ErrUnavailable for mainnet-beta intent, got %v", err)
			}

			// Raw caller-supplied MessageBase64 (including arbitrary program) is unconditionally rejected.
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "devnet",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-bad-program",
				Asset:             "SOL",
				EstimatedMicroUSD: 1_000_000,
				MessageBase64:     buildArbitraryProgramTestMessage(t, v),
				ExpiresAt:         now + 300,
			}, vaultID); err == nil || !strings.Contains(err.Error(), "raw messageBase64 signing is disabled") {
				_ = v.Close()
				t.Fatalf("expected raw messageBase64 rejection, got %v", err)
			}
			_ = msgB64

			// Disallowed asset rejected.
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "devnet",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-bad-asset",
				Asset:             "BONK",
				EstimatedMicroUSD: 1_000_000,
				Blockhash:         bhB58,
				Memo:              "fased-rehearsal-1",
				ExpiresAt:         now + 300,
			}, vaultID); err == nil || !strings.Contains(err.Error(), "forbids asset") {
				_ = v.Close()
				t.Fatalf("expected forbids asset error, got %v", err)
			}

			// Over per-order limit rejected.
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "devnet",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-over-order",
				Asset:             "SOL",
				EstimatedMicroUSD: 6_000_000,
				Blockhash:         bhB58,
				Memo:              "fased-rehearsal-1",
				ExpiresAt:         now + 300,
			}, vaultID); err == nil || !strings.Contains(err.Error(), "per-order spend limit") {
				_ = v.Close()
				t.Fatalf("expected per-order spend limit error, got %v", err)
			}

			// Valid small-cap Devnet sign succeeds and journals spend.
			signed, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:                  "devnet",
				GenesisHash:              onchain.SolanaDevnetGenesisHash,
				OperationID:              "sol-dev-1",
				Asset:                    "SOL",
				EstimatedMicroUSD:        4_500_000,
				Blockhash:                bhB58,
				ComputeUnitLimit:         50_000,
				PriorityFeeMicroLamports: 1_000,
				Memo:                     "fased-rehearsal-1",
				ExpiresAt:                now + 300,
			}, vaultID)
			if err != nil {
				_ = v.Close()
				t.Fatalf("SignSolanaDevnetIPC failed: %v", err)
			}
			rawTx, err := base64.StdEncoding.DecodeString(signed.SignedTxBase64)
			if err != nil || len(rawTx) < 66 || rawTx[0] != 1 {
				_ = v.Close()
				t.Fatalf("unexpected signed tx bytes: len=%d err=%v", len(rawTx), err)
			}
			priv := ed25519.NewKeyFromSeed(v.seed)
			if !ed25519.Verify(ed25519.PublicKey(priv[ed25519.SeedSize:]), rawTx[65:], rawTx[1:65]) {
				wipe(priv)
				_ = v.Close()
				t.Fatal("ed25519.Verify failed on signed Devnet transaction")
			}
			wipe(priv)

			// Duplicate operation ID rejected.
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "devnet",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-dev-1",
				Asset:             "SOL",
				EstimatedMicroUSD: 1_000_000,
				Blockhash:         bhB58,
				Memo:              "fased-rehearsal-1",
				ExpiresAt:         now + 300,
			}, vaultID); err == nil || !strings.Contains(err.Error(), "duplicate agent vault operation") {
				_ = v.Close()
				t.Fatalf("expected duplicate operationId error, got %v", err)
			}

			// Daily spend cumulative check across close+reopen.
			_ = v.Close()
			if tc.useV3Dev {
				v, err = OpenOrdinaryCustody(dir, signerID, "devnet")
			} else {
				v, err = Open(dir, signerID)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close()
			if err := v.Unlock(pass, 10*time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := v.ConfigureTestnetExecution(true); err != nil {
				t.Fatal(err)
			}
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "devnet",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-dev-2",
				Asset:             "SOL",
				EstimatedMicroUSD: 4_000_000, // 4.5M + 4.0M = 8.5M > 8.0M daily cap
				Blockhash:         bhB58,
				Memo:              "fased-rehearsal-2",
				ExpiresAt:         now + 300,
			}, vaultID); err == nil || !strings.Contains(err.Error(), "daily spend limit exceeded") {
				t.Fatalf("expected daily spend limit error across reopen, got %v", err)
			}

			// Venue mismatch rejected cleanly.
			hlOnlyID := strings.Repeat("d", 32)
			if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
				Version:            1,
				VaultID:            hlOnlyID,
				AgentID:            "hl-only-agent",
				AllowedVenues:      []string{"hyperliquid"},
				AllowedAssets:      []string{"SOL"},
				MaxOrderMicroUSD:   5_000_000,
				DailySpendMicroUSD: 8_000_000,
				DailyLossMicroUSD:  2_000_000,
				MaxLeverage:        5,
				ExpiresAt:          now + 3600,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := v.SignSolanaDevnetIPC(SolanaDevnetSignIntent{
				Network:           "devnet",
				GenesisHash:       onchain.SolanaDevnetGenesisHash,
				OperationID:       "sol-wrong-venue",
				Asset:             "SOL",
				EstimatedMicroUSD: 1_000_000,
				Blockhash:         bhB58,
				Memo:              "fased-rehearsal-2",
				ExpiresAt:         now + 300,
			}, hlOnlyID); err == nil || !strings.Contains(err.Error(), "agent vault forbids solana venue") {
				t.Fatalf("expected forbids solana venue error, got %v", err)
			}
		})
	}
}
