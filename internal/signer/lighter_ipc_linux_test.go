//go:build linux

package signer

import (
	"context"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLighterLinuxIPCRoundTrip(t *testing.T) {
	dir := fixtureDir(t)
	sockets := fixtureDir(t)
	signerID := strings.Repeat("7", 32)
	uid := os.Geteuid()
	pass := []byte("linux-lighter-ipc-passphrase-2026")
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
	vaultID := strings.Repeat("8", 32)
	if _, err := hlSocketCall(t, cfg, true, Request{
		Action: "agent-vault-set",
		AgentVault: &AgentVaultPolicy{
			Version:            1,
			VaultID:            vaultID,
			AgentID:            "ipc-lighter-agent",
			AllowedVenues:      []string{"lighter"},
			AllowedAssets:      []string{"ETH"},
			MaxOrderMicroUSD:   5_000_000,
			DailySpendMicroUSD: 10_000_000,
			DailyLossMicroUSD:  2_000_000,
			MaxLeverage:        5,
			ExpiresAt:          now + 3600,
		},
	}); err != nil {
		t.Fatalf("owner agent-vault-set failed: %v", err)
	}

	stResp, err := hlSocketCall(t, cfg, false, Request{Action: "lighter-status"})
	if err != nil || stResp.LighterStatus == nil || stResp.LighterStatus.PublicKeyHex == "" {
		t.Fatalf("app lighter-status failed: resp=%+v err=%v", stResp, err)
	}

	// Typed request hash guard rejects smuggling extra fields.
	if _, err := hlSocketCall(t, cfg, false, Request{
		Action:       "lighter-sign-order",
		AgentVaultID: vaultID,
		IntentID:     "smuggled",
		LighterOrder: &LighterOrderIntent{
			Network:           "testnet",
			ChainID:           LighterTestnetChainID,
			OperationID:       "ipc-l-bad",
			Asset:             "ETH",
			EstimatedMicroUSD: 1_000_000,
			AccountIndex:      10,
			MarketIndex:       0,
			ClientOrderIndex:  1,
			BaseAmount:        100,
			Price:             200000,
			OrderType:         0,
			TimeInForce:       1,
			OrderExpiry:       (now + 3600) * 1000,
			Nonce:             1,
			ExpiredAt:         (now + 300) * 1000,
			IntentExpiresAt:   now + 300,
		},
	}); err == nil {
		t.Fatal("expected typed request hash guard rejection")
	}

	signResp, err := hlSocketCall(t, cfg, false, Request{
		Action:       "lighter-sign-order",
		AgentVaultID: vaultID,
		LighterOrder: &LighterOrderIntent{
			Network:           "testnet",
			ChainID:           LighterTestnetChainID,
			OperationID:       "ipc-l-1",
			Asset:             "ETH",
			EstimatedMicroUSD: 2_500_000,
			Leverage:          3,
			AccountIndex:      10,
			ApiKeyIndex:       0,
			MarketIndex:       0,
			ClientOrderIndex:  505,
			BaseAmount:        150,
			Price:             180000,
			SizeDecimals:      5,
			PriceDecimals:     2,
			OrderType:         0,
			TimeInForce:       1,
			OrderExpiry:       (now + 3600) * 1000,
			Nonce:             2,
			ExpiredAt:         (now + 300) * 1000,
			IntentExpiresAt:   now + 300,
		},
	})
	if err != nil || signResp.LighterSigned == nil {
		t.Fatalf("app lighter-sign-order failed: resp=%+v err=%v", signResp, err)
	}
	pkB, _ := hex.DecodeString(strings.TrimPrefix(stResp.LighterStatus.PublicKeyHex, "0x"))
	txB, _ := hex.DecodeString(strings.TrimPrefix(signResp.LighterSigned.TxHashHex, "0x"))
	sigB, _ := hex.DecodeString(strings.TrimPrefix(signResp.LighterSigned.SignatureHex, "0x"))
	if err := LighterVerifySignature(pkB, txB, sigB); err != nil {
		t.Fatalf("LighterVerifySignature failed: %v", err)
	}
}
