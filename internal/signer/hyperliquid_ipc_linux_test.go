//go:build linux

package signer

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func hlProtocolFixture(t *testing.T, stateDir string, pass []byte, createNew bool) (Config, *Vault, *Server) {
	t.Helper()
	sockets := fixtureDir(t)
	signerID := "11111111111111111111111111111111"
	v, err := Open(stateDir, signerID)
	if err != nil {
		t.Fatal(err)
	}
	if createNew {
		if err := v.Create(pass); err != nil {
			_ = v.Close()
			t.Fatal(err)
		}
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.ConfigureHyperliquidControlled(true); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	uid := os.Geteuid()
	c := Config{
		Version:   1,
		Mode:      FixtureNetwork,
		SignerID:  signerID,
		OwnerUID:  uid,
		AppUID:    uid,
		SignerUID: uid,
		StateDir:  stateDir,
		SocketDir: sockets,
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := serveListeners(ctx, c, v)
	if err != nil {
		cancel()
		_ = v.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	return c, v, srv
}

func hlSocketCall(t *testing.T, c Config, owner bool, req Request) (Response, error) {
	t.Helper()
	resp := protocolRequest(t, c, owner, req)
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

func verifySignedHyperliquidDigest(t *testing.T, addr string, signed *HyperliquidSignedAction) {
	t.Helper()
	sig, err := ParseECDSASignature(signed.SignatureHex)
	if err != nil {
		t.Fatalf("ParseECDSASignature: %v", err)
	}
	dHex := strings.TrimPrefix(signed.TypedDataDigest, "0x")
	dBytes, err := hex.DecodeString(dHex)
	if err != nil || len(dBytes) != 32 {
		t.Fatalf("invalid digest %q", signed.TypedDataDigest)
	}
	var d32 [32]byte
	copy(d32[:], dBytes)
	if err := VerifySecp256k1Signature(addr, d32, sig); err != nil {
		t.Fatalf("ecrecover verify failed: %v", err)
	}
}

func TestHyperliquidJournaledPolicyNonceAndIPC(t *testing.T) {
	t.Parallel()
	// Ordinary fixture (without ConfigureHyperliquidControlled) must reject Hyperliquid IPC fail-closed.
	ordCfg, _ := protocolFixture(t, os.Geteuid(), os.Geteuid())
	if _, err := hlSocketCall(t, ordCfg, true, Request{Action: "create", Password: []byte("ord-hl-pass-2026")}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"hyperliquid-status", "agent-vault-list"} {
		if _, err := hlSocketCall(t, ordCfg, false, Request{Action: action}); err == nil {
			t.Fatalf("ordinary fixture accepted %s", action)
		}
	}

	// ServeControlledHyperliquid rejects unprivileged non-signer callers cleanly.
	badCfg := Config{
		Version:   1,
		Mode:      FixtureNetwork,
		SignerID:  "11111111111111111111111111111111",
		OwnerUID:  os.Geteuid() + 10,
		AppUID:    os.Geteuid() + 11,
		SignerUID: os.Geteuid() + 12,
		StateDir:  fixtureDir(t),
		SocketDir: fixtureDir(t),
	}
	if _, err := ServeControlledHyperliquid(t.Context(), badCfg); err == nil {
		t.Fatal("ServeControlledHyperliquid accepted non-matching unprivileged UID")
	}

	stateDir := fixtureDir(t)
	pass := []byte("hl-ipc-journal-passphrase-2026")
	c, v, srv := hlProtocolFixture(t, stateDir, pass, true)

	now := time.Now().Unix()
	policy := HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       5_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}

	// App peer CANNOT configure policy or mutate agent vaults.
	for _, ownerReq := range []Request{
		{Action: "hyperliquid-policy", HyperliquidPolicy: &policy},
		{Action: "agent-vault-set", AgentVault: &AgentVaultPolicy{
			Version:            1,
			VaultID:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AgentID:            "agent-1",
			AllowedVenues:      []string{"hyperliquid"},
			AllowedAssets:      []string{"ETH"},
			MaxOrderMicroUSD:   1_000_000_000,
			DailySpendMicroUSD: 2_000_000_000,
			DailyLossMicroUSD:  200_000_000,
			MaxLeverage:        5,
			ExpiresAt:          now + 3600,
		}},
		{Action: "agent-vault-revoke", AgentVaultID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Action: "agent-vault-record-loss", AgentVaultSpend: &AgentVaultSpendRecord{
			VaultID:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			OperationID:  "loss-1",
			Venue:        "hyperliquid",
			Asset:        "ETH",
			DayUTC:       time.Now().UTC().Format("2006-01-02"),
			LossMicroUSD: 100,
		}},
	} {
		resp, err := hlSocketCall(t, c, false, ownerReq)
		if err == nil || !strings.Contains(resp.Error, "owner administration denied") {
			t.Fatalf("app channel unexpectedly allowed owner action %s: resp=%+v err=%v", ownerReq.Action, resp, err)
		}
	}

	// Unconfigured policy must block app signing.
	if _, err := hlSocketCall(t, c, false, Request{
		Action: "hyperliquid-sign-order",
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.5", ReferenceMarkPrice: "3200",
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", Nonce: 999, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted app sign before owner configured signer policy")
	}

	// Owner sets Hyperliquid policy in operations.log.
	if _, err := hlSocketCall(t, c, true, Request{Action: "hyperliquid-policy", HyperliquidPolicy: &policy}); err != nil {
		t.Fatalf("owner hyperliquid-policy failed: %v", err)
	}

	// Piggybacking Hyperliquid fields onto status must be rejected strictly.
	if _, err := hlSocketCall(t, c, false, Request{Action: "status", HyperliquidPolicy: &policy}); err == nil {
		t.Fatal("accepted piggybacked hyperliquidPolicy on status action")
	}

	// App inspects status (gets derived address, signer policy, and LastNonce=0).
	stResp, err := hlSocketCall(t, c, false, Request{Action: "hyperliquid-status"})
	if err != nil || stResp.HyperliquidStatus == nil || !stResp.HyperliquidStatus.Enabled || stResp.HyperliquidStatus.Policy == nil || stResp.HyperliquidStatus.LastNonce != 0 {
		t.Fatalf("unexpected hyperliquid-status: resp=%+v err=%v", stResp, err)
	}
	addr := stResp.HyperliquidStatus.Address
	if _, err := ValidateEthereumAddress(addr); err != nil {
		t.Fatalf("invalid derived address %q: %v", addr, err)
	}

	// App signs updateLeverage (1000), order (1001), and cancel (1002) over IPC without passing policy.
	levResp, err := hlSocketCall(t, c, false, Request{
		Action: "hyperliquid-sign-leverage",
		HyperliquidLeverage: &HyperliquidUpdateLeverageIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, Leverage: 5, IsCross: false, Nonce: 1000, ExpiresAt: now + 300,
		},
	})
	if err != nil || levResp.HyperliquidSigned == nil || levResp.HyperliquidSigned.SignerAddress != addr {
		t.Fatalf("hyperliquid-sign-leverage failed: %+v err=%v", levResp, err)
	}
	verifySignedHyperliquidDigest(t, addr, levResp.HyperliquidSigned)

	ordResp, err := hlSocketCall(t, c, false, Request{
		Action: "hyperliquid-sign-order",
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.5", ReferenceMarkPrice: "3200",
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", ClientOrderID: "0x11111111111111111111111111111111",
			Nonce: 1001, ExpiresAt: now + 300,
		},
	})
	if err != nil || ordResp.HyperliquidSigned == nil || ordResp.HyperliquidSigned.SignerAddress != addr {
		t.Fatalf("hyperliquid-sign-order failed: %+v err=%v", ordResp, err)
	}
	verifySignedHyperliquidDigest(t, addr, ordResp.HyperliquidSigned)

	canResp, err := hlSocketCall(t, c, false, Request{
		Action: "hyperliquid-sign-cancel",
		HyperliquidCancel: &HyperliquidCancelIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, OrderID: 5001, Nonce: 1002, ExpiresAt: now + 300,
		},
	})
	if err != nil || canResp.HyperliquidSigned == nil {
		t.Fatalf("hyperliquid-sign-cancel failed: %+v err=%v", canResp, err)
	}
	verifySignedHyperliquidDigest(t, addr, canResp.HyperliquidSigned)

	// Compaction must preserve hlPolicy & hlLastNonce.
	if _, err := v.CompactOperationsJournal(0); err != nil {
		t.Fatalf("CompactOperationsJournal: %v", err)
	}
	_ = srv.Close()

	// Reopen brand-new signer server against same stateDir -> journal replay restores policy & nonce 1002!
	c2, _, _ := hlProtocolFixture(t, stateDir, pass, false)
	st2, err := hlSocketCall(t, c2, false, Request{Action: "hyperliquid-status"})
	if err != nil || st2.HyperliquidStatus == nil || st2.HyperliquidStatus.Policy == nil || st2.HyperliquidStatus.LastNonce != 1002 {
		t.Fatalf("journal replay did not restore Hyperliquid policy and nonce=1002: %+v err=%v", st2, err)
	}
	// Replaying nonce 1002 after restart must fail fail-closed.
	if _, err := hlSocketCall(t, c2, false, Request{
		Action: "hyperliquid-sign-order",
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.5", ReferenceMarkPrice: "3200",
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", Nonce: 1002, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted replayed nonce 1002 after signer restart")
	}
}

func TestTypedAgentVaultPolicySpendDailyLossAndWENRejection(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()

	// WEN venues MUST always be rejected in typed AgentVaultPolicy.
	for _, badVenue := range []string{"wen", "sat", "satcoin", "wenpm1", "unknown-dex"} {
		err := ValidateAgentVaultPolicy(AgentVaultPolicy{
			Version:            1,
			VaultID:            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			AgentID:            "agent-wen-block",
			AllowedVenues:      []string{badVenue},
			AllowedAssets:      []string{"SOL"},
			MaxOrderMicroUSD:   1_000_000_000,
			DailySpendMicroUSD: 2_000_000_000,
			DailyLossMicroUSD:  500_000_000,
			MaxLeverage:        3,
			ExpiresAt:          now + 3600,
		}, now)
		if err == nil {
			t.Fatalf("ValidateAgentVaultPolicy accepted forbidden/WEN venue %q", badVenue)
		}
	}

	stateDir := fixtureDir(t)
	pass := []byte("agent-vault-passphrase-2026")
	c, _, srv := hlProtocolFixture(t, stateDir, pass, true)

	hlPolicy := HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       10_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}
	if _, err := hlSocketCall(t, c, true, Request{Action: "hyperliquid-policy", HyperliquidPolicy: &hlPolicy}); err != nil {
		t.Fatal(err)
	}

	vaultID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	vaultPolicy := AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "desk-seat-1",
		AllowedVenues:      []string{"hyperliquid"},
		AllowedAssets:      []string{"ETH"},
		MaxOrderMicroUSD:   2_000_000_000, // $2,000
		DailySpendMicroUSD: 3_000_000_000, // $3,000
		DailyLossMicroUSD:  500_000_000,   // $500
		MaxLeverage:        5,
		ExpiresAt:          now + 3600,
	}
	if _, err := hlSocketCall(t, c, true, Request{Action: "agent-vault-set", AgentVault: &vaultPolicy}); err != nil {
		t.Fatalf("agent-vault-set: %v", err)
	}

	// Enforce asset allowlist ("BTC" not in vault AllowedAssets), leverage (6 > 5), and max order ($2,400 > $2,000).
	if _, err := hlSocketCall(t, c, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "BTC", AssetIndex: 0, IsBuy: true,
			LimitPrice: "96000", Size: "0.01", ReferenceMarkPrice: "96000",
			Leverage: 3, TimeInForce: "Gtc", Grouping: "na", Nonce: 100, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted asset forbidden by agent vault")
	}
	if _, err := hlSocketCall(t, c, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.5", ReferenceMarkPrice: "3200",
			Leverage: 6, TimeInForce: "Gtc", Grouping: "na", Nonce: 101, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted leverage exceeding agent vault MaxLeverage")
	}
	if _, err := hlSocketCall(t, c, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.75", ReferenceMarkPrice: "3200", // $2,400 > $2,000
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", Nonce: 102, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted single order exceeding MaxOrderMicroUSD")
	}

	// Order #1 ($1,600) passes and accumulates $1,600 daily spend.
	if _, err := hlSocketCall(t, c, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.5", ReferenceMarkPrice: "3200",
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", Nonce: 103, ExpiresAt: now + 300,
		},
	}); err != nil {
		t.Fatalf("vault order #1 failed: %v", err)
	}

	// Order #2 ($1,600 -> $3,200 cumulative > $3,000 daily cap) fails.
	if _, err := hlSocketCall(t, c, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.5", ReferenceMarkPrice: "3200",
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", Nonce: 104, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted order exceeding cumulative DailySpendMicroUSD")
	}

	// Order #3 ($1,000 -> $2,600 cumulative <= $3,000) succeeds.
	if _, err := hlSocketCall(t, c, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.3125", ReferenceMarkPrice: "3200",
			Leverage: 5, TimeInForce: "Gtc", Grouping: "na", Nonce: 105, ExpiresAt: now + 300,
		},
	}); err != nil {
		t.Fatalf("vault order #3 failed: %v", err)
	}

	// Restart signer -> durable daily spend ($2,600) persists!
	_ = srv.Close()
	c2, _, _ := hlProtocolFixture(t, stateDir, pass, false)
	vaultsResp, err := hlSocketCall(t, c2, false, Request{Action: "agent-vault-list"})
	if err != nil || len(vaultsResp.AgentVaults) != 1 || vaultsResp.AgentVaults[0].DailySpentMicroUSD != 2_600_000_000 {
		t.Fatalf("daily spend lost across restart: %+v err=%v", vaultsResp, err)
	}

	// Owner records $500 realized loss -> daily-loss breaker trips for opening orders, while ReduceOnly flatten passes.
	if _, err := hlSocketCall(t, c2, true, Request{
		Action: "agent-vault-record-loss",
		AgentVaultSpend: &AgentVaultSpendRecord{
			VaultID:      vaultID,
			OperationID:  "realized-loss-1",
			Venue:        "hyperliquid",
			Asset:        "ETH",
			DayUTC:       time.Now().UTC().Format("2006-01-02"),
			LossMicroUSD: 500_000_000,
		},
	}); err != nil {
		t.Fatalf("agent-vault-record-loss: %v", err)
	}
	if _, err := hlSocketCall(t, c2, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: true,
			LimitPrice: "3200", Size: "0.0625", ReferenceMarkPrice: "3200", // $200 opening order
			Leverage: 5, ReduceOnly: false, TimeInForce: "Gtc", Grouping: "na", Nonce: 106, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted opening order after daily-loss circuit breaker tripped")
	}
	if _, err := hlSocketCall(t, c2, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: false,
			LimitPrice: "3200", Size: "0.0625", ReferenceMarkPrice: "3200", // $200 ReduceOnly flatten
			Leverage: 5, ReduceOnly: true, TimeInForce: "Gtc", Grouping: "na", Nonce: 107, ExpiresAt: now + 300,
		},
	}); err != nil {
		t.Fatalf("ReduceOnly flatten rejected after daily-loss breaker: %v", err)
	}

	// Revoking vault permanently blocks even ReduceOnly orders and prevents re-enabling.
	if _, err := hlSocketCall(t, c2, true, Request{Action: "agent-vault-revoke", AgentVaultID: vaultID}); err != nil {
		t.Fatalf("agent-vault-revoke: %v", err)
	}
	if _, err := hlSocketCall(t, c2, false, Request{
		Action:       "hyperliquid-sign-order",
		AgentVaultID: vaultID,
		HyperliquidOrder: &HyperliquidOrderIntent{
			Network: "testnet", Asset: "ETH", AssetIndex: 1, IsBuy: false,
			LimitPrice: "3200", Size: "0.0625", ReferenceMarkPrice: "3200",
			Leverage: 5, ReduceOnly: true, TimeInForce: "Gtc", Grouping: "na", Nonce: 108, ExpiresAt: now + 300,
		},
	}); err == nil {
		t.Fatal("accepted ReduceOnly order on revoked agent vault")
	}
	if _, err := hlSocketCall(t, c2, true, Request{Action: "agent-vault-set", AgentVault: &vaultPolicy}); err == nil {
		t.Fatal("re-enabled permanently revoked agent vault")
	}
}

func TestPlatformSignerSupportGating(t *testing.T) {
	t.Parallel()
	sup := CurrentPlatformSupport()
	if sup.OS != "linux" || !sup.NativeStorageSupported || !sup.PeerCredIPCSupported || sup.LiveDesktopSigningEligible {
		t.Fatalf("unexpected platform signer support on linux: %+v", sup)
	}
}
