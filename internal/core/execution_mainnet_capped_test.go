package core

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/cex"
	"github.com/fased-ai/fased-app/internal/equities"
	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/swarm"
)

func TestStage456MainnetCappedGateAndDefaultOffInvariants(t *testing.T) {
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Close(context.Background())

	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)

	// 1. Default runtime keeps health capabilities and Stage 4/5/6 locked OFF and WEN held.
	healthResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/health", "", "", "", "")
	if healthResp.Code != 200 {
		t.Fatalf("GET /api/v1/health status=%d body=%s", healthResp.Code, healthResp.Body.String())
	}
	var hv Health
	if err := json.Unmarshal(healthResp.Body.Bytes(), &hv); err != nil {
		t.Fatalf("decode health failed: %v", err)
	}
	if hv.Capabilities["walletSigner"] || hv.Capabilities["financialExecution"] || hv.Capabilities["updater"] {
		t.Fatalf("default runtime must keep live capabilities false, got %+v", hv.Capabilities)
	}

	execResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/execution/status", "", token, "", "")
	if execResp.Code != 200 {
		t.Fatalf("GET /api/v1/execution/status status=%d", execResp.Code)
	}
	var ev ExecutionStatusView
	if err := json.Unmarshal(execResp.Body.Bytes(), &ev); err != nil {
		t.Fatalf("decode execution status failed: %v", err)
	}
	if ev.Profile != signer.ExecutionProfileOff || !ev.MainnetLocked || !ev.WENHeld || ev.MainnetCapped {
		t.Fatalf("unexpected default execution status: %+v", ev)
	}

	// 2. Mainnet-capped fails closed without confirmation or with >$250 cap.
	badSigner := func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: signer.Status{
			MainnetCapped:           true,
			MainnetMaxOrderMicroUSD: 50_000_000,
			Account:                 &signer.AccountRef{Network: "mainnet-beta", PublicKey: "11111111111111111111111111111111"},
		}}, nil
	}
	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile:       signer.ExecutionProfileMainnetCapped,
		MainnetPolicy: signer.MainnetCappedPolicy{Confirmed: false, MaxOrderMicroUSD: 50_000_000, MaxDailyMicroUSD: 100_000_000},
		AgentVaultID:  "0123456789abcdef0123456789abcdef",
		SignerCall:    badSigner,
	}); err == nil {
		t.Fatal("expected unconfirmed mainnet-capped config to fail")
	}

	// 3. Valid opt-in mainnet-capped configuration unlocks bounded Stage 4/5 while keeping WEN & Swarm live copy locked.
	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile: signer.ExecutionProfileMainnetCapped,
		MainnetPolicy: signer.MainnetCappedPolicy{
			Confirmed:        true,
			MaxOrderMicroUSD: 50_000_000,
			MaxDailyMicroUSD: 200_000_000,
		},
		AgentVaultID: "0123456789abcdef0123456789abcdef",
		SignerCall:   badSigner,
	}); err != nil {
		t.Fatalf("valid ConfigureTestnetExecution(mainnet-capped) failed: %v", err)
	}
	defer srv.Store.clearStage4Runtime()

	// Execution status reflects mainnet-capped limits while keeping mainnetLocked and financialExecution honest when zero Stage 4 mainnet venues are wired.
	execResp2 := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/execution/status", "", token, "", "")
	if execResp2.Code != 200 {
		t.Fatalf("GET /api/v1/execution/status after enable status=%d", execResp2.Code)
	}
	var ev2 ExecutionStatusView
	if err := json.Unmarshal(execResp2.Body.Bytes(), &ev2); err != nil {
		t.Fatalf("decode execution status failed: %v", err)
	}
	if ev2.Profile != signer.ExecutionProfileMainnetCapped || !ev2.MainnetLocked || !ev2.MainnetCapped || !ev2.WENHeld || ev2.FinancialExecution || ev2.WalletSigner || ev2.MainnetMaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected mainnet-capped execution status: %+v", ev2)
	}

	// Stage 5 CEX & Equities reflect capped limits cleanly.
	cexResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/cex/status", "", token, "", "")
	var csv cex.StatusView
	if cexResp.Code != 200 || json.Unmarshal(cexResp.Body.Bytes(), &csv) != nil || csv.MainnetLocked || !csv.CappedMode || csv.MaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected cex capped status (%d): %s", cexResp.Code, cexResp.Body.String())
	}

	eqResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/equities/status", "", token, "", "")
	var esv equities.StatusView
	if eqResp.Code != 200 || json.Unmarshal(eqResp.Body.Bytes(), &esv) != nil || esv.LiveLocked || !esv.CappedMode || esv.MaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected equities capped status (%d): %s", eqResp.Code, eqResp.Body.String())
	}

	// Stage 6 Swarm retains LiveCopyLocked=true and WENHeld=true even with CappedMode metadata.
	swResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/swarm/status", "", token, "", "")
	var ssv swarm.StatusView
	if swResp.Code != 200 || json.Unmarshal(swResp.Body.Bytes(), &ssv) != nil || !ssv.LiveCopyLocked || !ssv.WENHeld || !ssv.CappedMode {
		t.Fatalf("unexpected swarm status (%d): %s", swResp.Code, swResp.Body.String())
	}

	// Signer-status keeps executionEnabled:false and mainnetLocked:true until Stage 4 mainnet venues are wired.
	sigResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/signer/status", "", token, "", "")
	if sigResp.Code != 200 {
		t.Fatalf("GET /api/v1/signer/status failed: %d", sigResp.Code)
	}
	var ss map[string]any
	_ = json.Unmarshal(sigResp.Body.Bytes(), &ss)
	if ss["mainnetCapped"] != true || ss["mainnetLocked"] != true || ss["executionEnabled"] != false || ss["wenHeld"] != true {
		t.Fatalf("unexpected signer-status: %+v", ss)
	}
}

func TestMainnetCappedSolanaAndHyperliquidEndToEnd(t *testing.T) {
	dir := fixtureDir(t)
	vaultParent := fixtureDir(t)
	vaultID := strings.Repeat("e", 32)
	pass := []byte("mainnet-capped-e2e-pass-2026")

	v, err := signer.OpenControlledOwnerCustody(vaultParent, vaultID, "mainnet-beta")
	if err != nil {
		t.Fatalf("OpenControlledOwnerCustody: %v", err)
	}
	defer func() { _ = v.Close() }()
	if err := v.Create(pass); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := v.ConfigureMainnetCappedExecution(signer.MainnetCappedPolicy{
		Confirmed:        true,
		MaxOrderMicroUSD: 100_000_000, // $100
		MaxDailyMicroUSD: 250_000_000, // $250
	}); err != nil {
		t.Fatalf("ConfigureMainnetCappedExecution: %v", err)
	}
	if err := v.SetHyperliquidPolicy(signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "mainnet",
		AllowedAssets:             []string{"BTC", "ETH"},
		MaxLeverage:               5,
		MaxNotionalMicroUSD:       100_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SetHyperliquidPolicy(mainnet): %v", err)
	}
	agentVaultID := hex.EncodeToString([]byte("mainnetcapvault1"))
	if err := v.SetAgentVaultPolicy(signer.AgentVaultPolicy{
		Version:            1,
		VaultID:            agentVaultID,
		AgentID:            "operator",
		AllowedVenues:      []string{"solana", "hyperliquid"},
		AllowedAssets:      []string{"SOL", "BTC", "ETH"},
		MaxOrderMicroUSD:   100_000_000,
		DailySpendMicroUSD: 250_000_000,
		DailyLossMicroUSD:  50_000_000,
		MaxLeverage:        5,
		ExpiresAt:          time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SetAgentVaultPolicy: %v", err)
	}

	var bh [32]byte
	for i := range bh {
		bh[i] = byte(i + 5)
	}
	bhB58 := onchain.Base58(bh[:])
	rpcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "getGenesisHash":
			result = onchain.SolanaMainnetGenesisHash
		case "getLatestBlockhash":
			result = map[string]any{
				"context": map[string]any{"slot": 200},
				"value":   map[string]any{"blockhash": bhB58, "lastValidBlockHeight": 999999},
			}
		case "getRecentPrioritizationFees":
			result = []map[string]any{{"slot": 200, "prioritizationFee": 500}}
		case "simulateTransaction":
			result = map[string]any{"context": map[string]any{"slot": 201}, "value": map[string]any{"err": nil}}
		case "getBlockHeight":
			result = uint64(999000)
		case "sendTransaction":
			result = ""
		case "getSignatureStatuses":
			result = map[string]any{"value": []map[string]any{{"err": nil, "confirmationStatus": "confirmed"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer rpcSrv.Close()

	cassette, err := os.ReadFile(filepath.Join("..", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		t.Fatal(err)
	}
	hlTransport := &coreHyperliquidMockTransport{
		marketsRaw:   cassette,
		nextOid:      9000,
		orderStates:  map[uint64]string{},
		orderByCloid: map[string]uint64{},
		cloidByOid:   map[uint64]string{},
		appliedLev: map[string]struct {
			leverage uint32
			isCross  bool
		}{},
	}
	hlClient, err := hyperliquid.NewControlledMainnetCappedClient(hlTransport)
	if err != nil {
		t.Fatalf("NewControlledMainnetCappedClient: %v", err)
	}

	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())
	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)

	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile: signer.ExecutionProfileMainnetCapped,
		MainnetPolicy: signer.MainnetCappedPolicy{
			Confirmed:        true,
			MaxOrderMicroUSD: 100_000_000,
			MaxDailyMicroUSD: 250_000_000,
		},
		AgentVaultID:       agentVaultID,
		SignerCall:         stage4TestSignerRelay(v),
		SolanaMainnetRPC:   rpcSrv.URL,
		SolanaRoundTripper: rpcSrv.Client().Transport,
		HyperliquidClient:  hlClient,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution(mainnet-capped) failed: %v", err)
	}
	defer srv.Store.clearStage4Runtime()
	defer func() { _ = srv.Store.clearHyperliquidRuntime() }()

	execResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/execution/status", "", token, "", "")
	var ev ExecutionStatusView
	if execResp.Code != 200 || json.Unmarshal(execResp.Body.Bytes(), &ev) != nil {
		t.Fatalf("GET /api/v1/execution/status failed (%d): %s", execResp.Code, execResp.Body.String())
	}
	if !ev.WalletSigner || !ev.FinancialExecution || ev.MainnetLocked || !ev.MainnetCapped || !ev.SolanaMainnetReady || !ev.HyperliquidMainnet || !ev.WENHeld {
		t.Fatalf("unexpected wired mainnet-capped status: %+v", ev)
	}

	// 1. Solana Mainnet-Beta endpoint enforces owner step-up, blocks raw messageBase64, and enforces $100 cap on memo execution.
	st := v.Status()
	pubRaw, _ := hex.DecodeString(st.Account.PublicKey)
	jupKey, _ := onchain.Key(onchain.Jupiter)
	altKey, _, _ := ed25519.GenerateKey(nil)
	jupData := make([]byte, 40)
	copy(jupData[:8], []byte{187, 100, 250, 204, 49, 196, 175, 20})
	v0Msg := make([]byte, 0, 256)
	v0Msg = append(v0Msg, 0x80, 1, 0, 1, 2)
	v0Msg = append(v0Msg, pubRaw...)
	v0Msg = append(v0Msg, jupKey...)
	v0Msg = append(v0Msg, bh[:]...)
	v0Msg = append(v0Msg, 1, 1, 1, 2, byte(len(jupData)))
	v0Msg = append(v0Msg, jupData...)
	v0Msg = append(v0Msg, 1)
	v0Msg = append(v0Msg, altKey...)
	v0Msg = append(v0Msg, 1, 0, 0)

	// Missing step-up confirmation MUST be rejected (403).
	rawBody, _ := json.Marshal(SolanaMainnetExecuteRequest{
		Asset:             "SOL",
		MessageBase64:     base64.StdEncoding.EncodeToString(v0Msg),
		EstimatedMicroUSD: 40_000_000,
	})
	noStepResp := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/execution/solana-mainnet/execute", string(rawBody), token, srv.URL, csrfToken(token))
	if noStepResp.Code != 403 {
		t.Fatalf("expected 403 without step-up on /solana-mainnet/execute, got %d: %s", noStepResp.Code, noStepResp.Body.String())
	}

	getMainnetStepUp := func() string {
		t.Helper()
		body := `{"password":"` + testPassword + `","action":"execute","walletId":"solana-mainnet"}`
		rec := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/auth/step-up", body, token, srv.URL, csrfToken(token))
		if rec.Code != 200 {
			t.Fatalf("POST /api/v1/auth/step-up failed (%d): %s", rec.Code, rec.Body.String())
		}
		var out struct {
			StepUpToken string `json:"stepUpToken"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.StepUpToken
	}
	stage4StepReq := func(path, body, stepTok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-CSRF-Token", csrfToken(token))
		req.Header.Set("X-Fased-Step-Up", stepTok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Raw caller-supplied messageBase64 MUST be rejected (400) even with valid step-up.
	rawMsgResp := stage4StepReq("/api/v1/execution/solana-mainnet/execute", string(rawBody), getMainnetStepUp())
	if rawMsgResp.Code != 400 {
		t.Fatalf("expected 400 rejecting raw messageBase64 on /solana-mainnet/execute, got %d: %s", rawMsgResp.Code, rawMsgResp.Body.String())
	}

	// Signer-built memo rehearsal succeeds under cap with valid step-up and rejects >$100.
	execBody, _ := json.Marshal(SolanaMainnetExecuteRequest{
		Asset:             "SOL",
		Memo:              "fased-mainnet-capped-check",
		EstimatedMicroUSD: 40_000_000,
	})
	solResp := stage4StepReq("/api/v1/execution/solana-mainnet/execute", string(execBody), getMainnetStepUp())
	if solResp.Code != 200 {
		t.Fatalf("POST /api/v1/execution/solana-mainnet/execute failed (%d): %s", solResp.Code, solResp.Body.String())
	}
	overBody, _ := json.Marshal(SolanaMainnetExecuteRequest{
		Asset:             "SOL",
		Memo:              "fased-mainnet-over-cap",
		EstimatedMicroUSD: 150_000_000,
	})
	overResp := stage4StepReq("/api/v1/execution/solana-mainnet/execute", string(overBody), getMainnetStepUp())
	if overResp.Code != 400 {
		t.Fatalf("expected >$100 Solana mainnet order to return 400, got %d: %s", overResp.Code, overResp.Body.String())
	}

	// 2. Hyperliquid Mainnet-Capped order review + submit enforces $100 cap.
	agView, err := srv.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if agView.Hyperliquid == nil || !agView.Hyperliquid.LiveExecution || agView.Hyperliquid.Network != "mainnet" {
		t.Fatalf("expected live mainnet Hyperliquid view, got %+v", agView.Hyperliquid)
	}
	hlRev, err := srv.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: agView.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "ETH",
				IsBuy:       true,
				LimitPrice:  "3200",
				Size:        "0.02",
				Leverage:    2,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatalf("mainnet-capped hyperliquid-review failed: %v", err)
	}
	hlSub, err := srv.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    agView.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: hlRev.Hyperliquid.Review.Digest},
	})
	if err != nil || len(hlSub.Hyperliquid.Orders) != 1 || hlSub.Hyperliquid.Orders[0].State != "resting" {
		t.Fatalf("mainnet-capped hyperliquid-submit failed: orders=%+v err=%v", hlSub, err)
	}
}
