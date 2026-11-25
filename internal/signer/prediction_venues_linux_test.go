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

func TestPredictionVenuesLinuxIPCAndUIDSeparation(t *testing.T) {
	dir := fixtureDir(t)
	sockets := fixtureDir(t)
	signerID := strings.Repeat("1", 32)
	uid := os.Geteuid()
	pass := []byte("linux-prediction-ipc-passphrase-2026")
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
	vaultID := strings.Repeat("2", 32)
	if _, err := hlSocketCall(t, cfg, true, Request{
		Action: "agent-vault-set",
		AgentVault: &AgentVaultPolicy{
			Version:            1,
			VaultID:            vaultID,
			AgentID:            "ipc-prediction-agent",
			AllowedVenues:      []string{"polymarket", "kalshi"},
			AllowedAssets:      []string{"USDC", "USD"},
			MaxOrderMicroUSD:   5_000_000,
			DailySpendMicroUSD: 10_000_000,
			DailyLossMicroUSD:  2_000_000,
			MaxLeverage:        1,
			ExpiresAt:          now + 3600,
		},
	}); err != nil {
		t.Fatalf("owner agent-vault-set failed: %v", err)
	}

	// App peer CANNOT generate or provision Kalshi keys (owner-only).
	for _, ownerOnly := range []Request{
		{Action: "kalshi-generate-key", KalshiKeyID: "app-forbidden"},
		{Action: "kalshi-provision-key", KalshiKey: &KalshiKeyProvision{Environment: "demo", KeyID: "x", PrivateKeyPEM: []byte("none")}},
	} {
		resp, err := hlSocketCall(t, cfg, false, ownerOnly)
		if err == nil || !strings.Contains(resp.Error, "owner administration denied") {
			t.Fatalf("app channel unexpectedly allowed %s: resp=%+v err=%v", ownerOnly.Action, resp, err)
		}
	}

	// Owner generates Kalshi Demo key.
	genResp, err := hlSocketCall(t, cfg, true, Request{Action: "kalshi-generate-key", KalshiKeyID: "ipc-kalshi-demo"})
	if err != nil || genResp.KalshiStatus == nil || !genResp.KalshiStatus.Configured {
		t.Fatalf("owner kalshi-generate-key failed: resp=%+v err=%v", genResp, err)
	}

	// App channel inspects both venue statuses.
	pmStResp, err := hlSocketCall(t, cfg, false, Request{Action: "polymarket-status"})
	if err != nil || pmStResp.PolymarketStatus == nil || pmStResp.PolymarketStatus.SignerAddress == "" {
		t.Fatalf("app polymarket-status failed: resp=%+v err=%v", pmStResp, err)
	}
	kStResp, err := hlSocketCall(t, cfg, false, Request{Action: "kalshi-status"})
	if err != nil || kStResp.KalshiStatus == nil || kStResp.KalshiStatus.KeyID != "ipc-kalshi-demo" {
		t.Fatalf("app kalshi-status failed: resp=%+v err=%v", kStResp, err)
	}

	// App signs Polymarket order via IPC + verify ecrecover.
	pmSignResp, err := hlSocketCall(t, cfg, false, Request{
		Action:       "polymarket-sign-order",
		AgentVaultID: vaultID,
		PolymarketOrder: &PolymarketOrderIntent{
			Network:         "amoy",
			ChainID:         PolymarketAmoyChainID,
			OperationID:     "ipc-pm-1",
			Asset:           "USDC",
			Salt:            "424242",
			TokenID:         "9990001",
			MakerAmount:     "2000000",
			TakerAmount:     "4000000",
			Side:            0,
			IntentExpiresAt: now + 300,
		},
	})
	if err != nil || pmSignResp.PolymarketSigned == nil {
		t.Fatalf("app polymarket-sign-order failed: resp=%+v err=%v", pmSignResp, err)
	}
	sig, err := ParseECDSASignature(pmSignResp.PolymarketSigned.SignatureHex)
	if err != nil {
		t.Fatal(err)
	}
	dBytes, err := hex.DecodeString(strings.TrimPrefix(pmSignResp.PolymarketSigned.TypedDataDigest, "0x"))
	if err != nil || len(dBytes) != 32 {
		t.Fatal("invalid typedDataDigest")
	}
	var d32 [32]byte
	copy(d32[:], dBytes)
	if err := VerifySecp256k1Signature(pmStResp.PolymarketStatus.SignerAddress, d32, sig); err != nil {
		t.Fatalf("Polymarket IPC signature verify failed: %v", err)
	}

	// App signs Kalshi Demo order via IPC + verify RSA-PSS signature.
	kSignResp, err := hlSocketCall(t, cfg, false, Request{
		Action:       "kalshi-sign-request",
		AgentVaultID: vaultID,
		KalshiSign: &KalshiSignIntent{
			Environment:       "demo",
			Method:            "POST",
			Path:              "/trade-api/v2/portfolio/events/orders",
			TimestampMs:       time.Now().UnixMilli(),
			Ticker:            "KXTEST-26",
			Asset:             "USD",
			OperationID:       "ipc-k-1",
			EstimatedMicroUSD: 1_500_000,
			ExpiresAt:         now + 300,
		},
	})
	if err != nil || kSignResp.KalshiSigned == nil {
		t.Fatalf("app kalshi-sign-request failed: resp=%+v err=%v", kSignResp, err)
	}
	if err := VerifyKalshiPSSSignature(
		kStResp.KalshiStatus.PublicKeyPEM,
		kSignResp.KalshiSigned.TimestampMs,
		kSignResp.KalshiSigned.Method,
		kSignResp.KalshiSigned.Path,
		kSignResp.KalshiSigned.SignatureBase64,
	); err != nil {
		t.Fatalf("Kalshi IPC PSS signature verify failed: %v", err)
	}
}
