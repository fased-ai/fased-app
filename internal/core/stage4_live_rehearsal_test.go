package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestLiveStage4DevnetAndTestnetRehearsal(t *testing.T) {
	if os.Getenv("FASED_LIVE_STAGE4_REHEARSAL") != "1" {
		t.Skip("live Stage 4 rehearsal disabled unless FASED_LIVE_STAGE4_REHEARSAL=1")
	}
	devnetRPC := strings.TrimSpace(os.Getenv("FASED_DEVNET_RPC_URL"))
	if devnetRPC == "" {
		t.Fatal("FASED_DEVNET_RPC_URL required for live Stage 4 rehearsal")
	}

	dir := fixtureDir(t)
	signerDir := fixtureDir(t)
	if err := os.Chmod(signerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	signerID := "abcdef0123456789abcdef0123456789"
	pass := []byte("live-rehearsal-ephemeral-pass-2026")
	vaultID := "1234567890abcdef1234567890abcdef"

	v, err := signer.Open(signerDir, signerID)
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

	now := time.Now().Unix()
	if err := v.SetHyperliquidPolicy(signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH"},
		MaxLeverage:               5,
		MaxNotionalMicroUSD:       50_000_000, // $50 cap
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.SetAgentVaultPolicy(signer.AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "live-rehearsal-agent",
		AllowedVenues:      []string{"solana", "hyperliquid"},
		AllowedAssets:      []string{"SOL", "BTC", "ETH"},
		MaxOrderMicroUSD:   25_000_000, // $25 per-order cap
		DailySpendMicroUSD: 50_000_000, // $50 daily cap
		DailyLossMicroUSD:  10_000_000, // $10 daily loss breaker
		MaxLeverage:        5,
		ExpiresAt:          now + 3600,
	}); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}

	// Verify Mainnet compile-time lock even under ConfigureTestnetExecution(true).
	if err := v.SetHyperliquidPolicy(signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "mainnet",
		AllowedAssets:             []string{"BTC"},
		MaxLeverage:               2,
		MaxNotionalMicroUSD:       5_000_000,
		MaxOrderPriceBpsDeviation: 100,
		ExpiresAt:                 now + 3600,
	}); !errors.Is(err, signer.ErrUnavailable) {
		_ = v.Close()
		t.Fatalf("expected ErrUnavailable for mainnet policy under testnet execution, got %v", err)
	}

	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = srv.Close(context.Background())
		_ = v.Close()
	}()
	token := claimFixture(t, srv.Store)

	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile:         signer.ExecutionProfileTestnet,
		SignerCall:      stage4TestSignerRelay(v),
		AgentVaultID:    vaultID,
		SolanaDevnetRPC: devnetRPC,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution live failed: %v", err)
	}

	h := srv.HTTP.Handler.(*HTTPHandler)
	simResp := stage4Req(h, "POST", srv.URL, "/api/v1/execution/solana-devnet/rehearse", `{"memo":"fased-stage4-live-check","estimatedMicroUsd":1000,"simulateOnly":true}`, token, srv.URL, csrfToken(token))
	if simResp.Code != 200 {
		t.Fatalf("live solana devnet simulate failed (%d): %s", simResp.Code, simResp.Body.String())
	}
	var simRes SolanaDevnetRehearseResult
	if err := json.Unmarshal(simResp.Body.Bytes(), &simRes); err != nil {
		t.Fatal(err)
	}
	if simRes.GenesisHash != onchain.SolanaDevnetGenesisHash || simRes.SimulatedSlot == 0 || simRes.Signature == "" {
		t.Fatalf("unexpected live devnet rehearsal result: %+v", simRes)
	}
	if strings.Contains(simRes.RPCHost, "api-key=") || strings.Contains(simRes.RPCHost, "?") {
		t.Fatalf("RPCHost leaked query parameters: %q", simRes.RPCHost)
	}

	// Verify live Hyperliquid Testnet market data & EIP-712 agent vault order signing.
	agentsView, err := srv.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatalf("Store.Agents live failed: %v", err)
	}
	if agentsView.Hyperliquid == nil || !agentsView.Hyperliquid.LiveExecution || len(agentsView.Hyperliquid.Markets) == 0 {
		t.Fatalf("expected live Hyperliquid testnet markets, got %+v", agentsView.Hyperliquid)
	}
	var ethMeta *hyperliquid.MarketMeta
	for i := range agentsView.Hyperliquid.Markets {
		if agentsView.Hyperliquid.Markets[i].Symbol == "ETH" {
			ethMeta = &agentsView.Hyperliquid.Markets[i]
			break
		}
	}
	if ethMeta == nil {
		t.Fatal("ETH market missing from live Hyperliquid testnet metadata")
	}

	// Small-cap order review well within $25 AgentVault limit.
	rev, err := srv.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: agentsView.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "ETH",
				IsBuy:       true,
				LimitPrice:  ethMeta.MarkPrice,
				Size:        "0.005",
				Leverage:    2,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatalf("live hyperliquid-review failed: %v", err)
	}

	// Sign directly via signer IPC + verify EIP-712 signature recovers the vault address.
	nonce := uint64(time.Now().UnixMilli())
	intent := signer.HyperliquidOrderIntent{
		Network:            "testnet",
		Asset:              "ETH",
		AssetIndex:         ethMeta.AssetIndex,
		IsBuy:              true,
		LimitPrice:         rev.Hyperliquid.Review.Preview.NormalizedPrice,
		Size:               rev.Hyperliquid.Review.Preview.NormalizedSize,
		ReferenceMarkPrice: ethMeta.MarkPrice,
		Leverage:           2,
		ReduceOnly:         false,
		TimeInForce:        "Gtc",
		Grouping:           "na",
		ClientOrderID:      "0x" + strings.Repeat("1", 32),
		Nonce:              nonce,
		ExpiresAt:          time.Now().Unix() + 120,
	}
	signedOrder, err := v.SignHyperliquidOrderIPC(intent, vaultID)
	if err != nil {
		t.Fatalf("SignHyperliquidOrderIPC live failed: %v", err)
	}
	hlSig, err := signer.ParseECDSASignature(signedOrder.SignatureHex)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes, err := hex.DecodeString(strings.TrimPrefix(signedOrder.TypedDataDigest, "0x"))
	if err != nil || len(digestBytes) != 32 {
		t.Fatalf("invalid digest: %q", signedOrder.TypedDataDigest)
	}
	var d32 [32]byte
	copy(d32[:], digestBytes)
	if err := signer.VerifySecp256k1Signature(agentsView.Hyperliquid.SignerAddress, d32, hlSig); err != nil {
		t.Fatalf("Hyperliquid EIP-712 ecrecover verify failed: %v", err)
	}

	// Over-cap AgentVault order rejected cleanly by signer.
	overCap := intent
	overCap.Nonce = nonce + 1
	overCap.Size = "1.0"
	if _, err := v.SignHyperliquidOrderIPC(overCap, vaultID); err == nil {
		t.Fatal("expected over-cap Hyperliquid order to be rejected by AgentVaultPolicy")
	}

	// Real Testnet /exchange connectivity probe with signed order (ephemeral unfunded key expects clean upstream response).
	hlClient, err := hyperliquid.NewTestnetExecutionClient()
	if err != nil {
		t.Fatal(err)
	}
	subRes, exchangeErr := hlClient.SubmitSignedOrder(t.Context(), intent, signedOrder, "")
	exchangeProbe := subRes.State
	if subRes.Message != "" {
		exchangeProbe += ":" + subRes.Message
	}
	if exchangeErr != nil {
		exchangeProbe = "transport_error:" + exchangeErr.Error()
	}

	if summaryPath := strings.TrimSpace(os.Getenv("FASED_STAGE4_SUMMARY_PATH")); summaryPath != "" {
		if err := os.MkdirAll(filepath.Dir(summaryPath), 0o700); err != nil {
			t.Fatal(err)
		}
		summary := map[string]any{
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
			"profile":     "testnet",
			"mainnetLock": true,
			"wenHeld":     !miningActivationEnabled,
			"solanaDevnet": map[string]any{
				"rpcHost":           simRes.RPCHost,
				"genesisHash":       simRes.GenesisHash,
				"signerPublicKey":   simRes.SignerPublicKey,
				"priorityFeeMicroL": simRes.PriorityFeeMicroLamports,
				"simulatedSlot":     simRes.SimulatedSlot,
				"simulationStatus":  simRes.SimulationError,
				"signature":         simRes.Signature,
			},
			"hyperliquidTestnet": map[string]any{
				"signerAddress":   agentsView.Hyperliquid.SignerAddress,
				"marketsCount":    len(agentsView.Hyperliquid.Markets),
				"ethMarkPrice":    ethMeta.MarkPrice,
				"typedDataDigest": signedOrder.TypedDataDigest,
				"ecrecoverValid":  true,
				"overCapBlocked":  true,
				"exchangeProbe":   exchangeProbe,
			},
		}
		rawSummary, _ := json.MarshalIndent(summary, "", "  ")
		if err := os.WriteFile(summaryPath, append(rawSummary, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
