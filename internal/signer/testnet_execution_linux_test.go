//go:build linux

package signer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestServeTestnetExecutionIPC(t *testing.T) {
	dir := fixtureDir(t)
	sockets := fixtureDir(t)
	signerID := strings.Repeat("e", 32)
	uid := os.Geteuid()
	pass := []byte("linux-ipc-testnet-passphrase-2026")
	cfg := Config{
		Version:   1,
		Mode:      FixtureNetwork,
		SignerID:  signerID,
		OwnerUID:  uid,
		AppUID:    uid,
		SignerUID: uid,
		StateDir:  dir,
		SocketDir: sockets,
	}
	v, err := openVaultNetwork(dir, signerID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Create(pass); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.ConfigureTestnetExecution(true); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := serveListeners(ctx, cfg, v)
	if err != nil {
		cancel()
		_ = v.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})

	if _, err := hlSocketCall(t, cfg, true, Request{Action: "unlock", Password: pass, UnlockSeconds: 300}); err != nil {
		t.Fatalf("owner unlock failed: %v", err)
	}
	now := time.Now().Unix()
	vaultID := strings.Repeat("f", 32)
	if _, err := hlSocketCall(t, cfg, true, Request{
		Action: "agent-vault-set",
		AgentVault: &AgentVaultPolicy{
			Version:            1,
			VaultID:            vaultID,
			AgentID:            "ipc-agent",
			AllowedVenues:      []string{"solana"},
			AllowedAssets:      []string{"SOL"},
			MaxOrderMicroUSD:   2_000_000,
			DailySpendMicroUSD: 4_000_000,
			DailyLossMicroUSD:  1_000_000,
			MaxLeverage:        2,
			ExpiresAt:          now + 3600,
		},
	}); err != nil {
		t.Fatalf("owner agent-vault-set failed: %v", err)
	}

	stResp, err := hlSocketCall(t, cfg, false, Request{Action: "status"})
	if err != nil {
		t.Fatalf("app status failed: %v", err)
	}
	if !stResp.Status.TestnetExecution || stResp.Status.Account == nil {
		t.Fatalf("unexpected status response: %+v", stResp.Status)
	}
	msgB64 := buildValidDevnetTestMessage(t, v, "fased-ipc-devnet")

	// Typed hash guard rejects stray extra fields.
	if _, err := hlSocketCall(t, cfg, false, Request{
		Action: "solana-devnet-sign",
		SolanaDevnetSign: &SolanaDevnetSignIntent{
			Network:           "devnet",
			GenesisHash:       onchain.SolanaDevnetGenesisHash,
			OperationID:       "ipc-bad-field",
			Asset:             "SOL",
			EstimatedMicroUSD: 500_000,
			MessageBase64:     msgB64,
			ExpiresAt:         now + 300,
		},
		AgentVaultID: vaultID,
		IntentID:     "smuggled",
	}); err == nil {
		t.Fatal("expected typed request hash guard error")
	}

	signResp, err := hlSocketCall(t, cfg, false, Request{
		Action: "solana-devnet-sign",
		SolanaDevnetSign: &SolanaDevnetSignIntent{
			Network:           "devnet",
			GenesisHash:       onchain.SolanaDevnetGenesisHash,
			OperationID:       "ipc-sol-1",
			Asset:             "SOL",
			EstimatedMicroUSD: 500_000,
			MessageBase64:     msgB64,
			ExpiresAt:         now + 300,
		},
		AgentVaultID: vaultID,
	})
	if err != nil {
		t.Fatalf("app solana-devnet-sign failed: %v", err)
	}
	if signResp.SolanaDevnetSigned == nil || signResp.SolanaDevnetSigned.GenesisHash != onchain.SolanaDevnetGenesisHash {
		t.Fatalf("unexpected signed response: %+v", signResp)
	}
	rawTx, _ := base64.StdEncoding.DecodeString(signResp.SolanaDevnetSigned.SignedTxBase64)
	msgBytes, _ := base64.StdEncoding.DecodeString(msgB64)
	priv := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(priv)
	if !ed25519.Verify(ed25519.PublicKey(priv[ed25519.SeedSize:]), msgBytes, rawTx[1:65]) {
		t.Fatal("IPC signed Devnet transaction failed Ed25519 verification")
	}

	// ServeTestnetExecution refuses Version 2 and mainnet-beta before verifying uid/paths.
	if _, err := ServeTestnetExecution(ctx, Config{Version: 2, Network: "devnet"}); err == nil {
		t.Fatal("expected Version 2 ServeTestnetExecution rejection")
	}
	if _, err := ServeTestnetExecution(ctx, Config{Version: 3, Mode: OrdinaryCustodyMode, Profile: OwnerCustodyProfile, Provenance: OwnerKeyProvenance, Network: "mainnet-beta"}); err == nil {
		t.Fatal("expected mainnet-beta ServeTestnetExecution rejection")
	}
}
