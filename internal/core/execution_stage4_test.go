package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
)

func stage4Req(h http.Handler, method, baseURL, path, body, token, origin, csrf string) *httptest.ResponseRecorder {
	return stage4ReqWithStepUp(h, method, baseURL, path, body, token, origin, csrf, "")
}

func stage4ReqWithStepUp(h http.Handler, method, baseURL, path, body, token, origin, csrf, stepUp string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, baseURL+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if stepUp != "" {
		r.Header.Set("X-Fased-Step-Up", stepUp)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func stage4StepUpToken(t *testing.T, h *HTTPHandler, token, action, walletID string) string {
	t.Helper()
	tok, _, err := h.issueStepUpToken(token, action, walletID, "")
	if err != nil {
		t.Fatalf("issueStepUpToken(%s/%s): %v", action, walletID, err)
	}
	return tok
}

func stage4TestSignerRelay(v *signer.Vault) func(context.Context, signer.Request) (signer.Response, error) {
	base := appSignerRelay(v)
	return func(ctx context.Context, r signer.Request) (signer.Response, error) {
		switch r.Action {
		case "status":
			return signer.Response{Status: v.Status()}, nil
		case "agent-vault-list":
			vaults, err := v.AgentVaults()
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{AgentVaults: vaults, Status: v.Status()}, nil
		case "solana-devnet-sign":
			if r.SolanaDevnetSign == nil {
				return signer.Response{}, errors.New("solana devnet sign intent required")
			}
			signed, err := v.SignSolanaDevnetIPC(*r.SolanaDevnetSign, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{SolanaDevnetSigned: signed, Status: v.Status()}, nil
		case "solana-mainnet-capped-sign":
			if r.SolanaMainnetCappedSign == nil {
				return signer.Response{}, errors.New("solana mainnet-capped sign intent required")
			}
			signed, err := v.SignSolanaMainnetCappedIPC(*r.SolanaMainnetCappedSign, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{SolanaDevnetSigned: signed, Status: v.Status()}, nil
		case "polymarket-status":
			st, err := v.PolymarketStatus()
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{PolymarketStatus: st, Status: v.Status()}, nil
		case "polymarket-sign-order":
			if r.PolymarketOrder == nil {
				return signer.Response{}, errors.New("polymarket order intent required")
			}
			signed, err := v.SignPolymarketOrderIPC(*r.PolymarketOrder, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{PolymarketSigned: signed, Status: v.Status()}, nil
		case "kalshi-status":
			st, err := v.KalshiStatus()
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{KalshiStatus: st, Status: v.Status()}, nil
		case "kalshi-sign-request":
			if r.KalshiSign == nil {
				return signer.Response{}, errors.New("kalshi sign intent required")
			}
			signed, err := v.SignKalshiRequestIPC(*r.KalshiSign, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{KalshiSigned: signed, Status: v.Status()}, nil
		case "lighter-status":
			st, err := v.LighterStatus()
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{LighterStatus: st, Status: v.Status()}, nil
		case "lighter-sign-order":
			if r.LighterOrder == nil {
				return signer.Response{}, errors.New("lighter order intent required")
			}
			signed, err := v.SignLighterOrderIPC(*r.LighterOrder, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{LighterSigned: signed, Status: v.Status()}, nil
		case "lighter-sign-cancel":
			if r.LighterCancel == nil {
				return signer.Response{}, errors.New("lighter cancel intent required")
			}
			signed, err := v.SignLighterCancelIPC(*r.LighterCancel, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{LighterSigned: signed, Status: v.Status()}, nil
		case "lighter-sign-leverage":
			if r.LighterLeverage == nil {
				return signer.Response{}, errors.New("lighter leverage intent required")
			}
			signed, err := v.SignLighterUpdateLeverageIPC(*r.LighterLeverage, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{LighterSigned: signed, Status: v.Status()}, nil
		default:
			return base(ctx, r)
		}
	}
}

type mockSolanaDevnetRT struct {
	blockhash string
	sentSig   string
	sendCount int
}

func (m *mockSolanaDevnetRT) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(req.Body)
	var rpc struct {
		ID     uint64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(raw, &rpc)
	var result any
	switch rpc.Method {
	case "getGenesisHash":
		result = onchain.SolanaDevnetGenesisHash
	case "getLatestBlockhash":
		result = map[string]any{
			"context": map[string]any{"slot": 200},
			"value":   map[string]any{"blockhash": m.blockhash, "lastValidBlockHeight": 900},
		}
	case "getRecentPrioritizationFees":
		result = []map[string]any{{"slot": 199, "prioritizationFee": 500}, {"slot": 200, "prioritizationFee": 1500}}
	case "simulateTransaction":
		result = map[string]any{
			"context": map[string]any{"slot": 201},
			"value":   map[string]any{"err": nil},
		}
	case "getBlockHeight":
		result = uint64(800)
	case "sendTransaction":
		m.sendCount++
		var params []any
		_ = json.Unmarshal(rpc.Params, &params)
		// Parse expected signature if unset from wire packet? Just return sentSig if pre-populated, or derive.
		result = m.sentSig
	case "getSignatureStatuses":
		result = map[string]any{
			"value": []any{map[string]any{"err": nil, "confirmationStatus": "confirmed"}},
		}
	}
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}, nil
}

func TestStage4DefaultOffParityAndMainnetRefusal(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	healthResp := apiRequest(h, "GET", "/api/v1/health", "", token, "", "")
	if healthResp.Code != 200 {
		t.Fatalf("health status=%d", healthResp.Code)
	}
	var health Health
	if err := json.Unmarshal(healthResp.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Capabilities["walletSigner"] || health.Capabilities["financialExecution"] || health.Capabilities["updater"] {
		t.Fatalf("expected default capabilities false, got %+v", health.Capabilities)
	}

	signerResp := apiRequest(h, "GET", "/api/v1/signer/status", "", token, "", "")
	if signerResp.Code != 200 {
		t.Fatalf("signer status=%d", signerResp.Code)
	}
	wantLegacy := `{"available":false,"executionEnabled":false,"reason":"real wallets unavailable; public fixture references are managed through accounts"}`
	if strings.TrimSpace(signerResp.Body.String()) != wantLegacy {
		t.Fatalf("expected exact legacy signer status JSON %s, got %s", wantLegacy, signerResp.Body.String())
	}

	execResp := apiRequest(h, "GET", "/api/v1/execution/status", "", token, "", "")
	if execResp.Code != 200 {
		t.Fatalf("execution status=%d", execResp.Code)
	}
	var execView ExecutionStatusView
	if err := json.Unmarshal(execResp.Body.Bytes(), &execView); err != nil {
		t.Fatal(err)
	}
	if execView.Profile != "off" || execView.Available || !execView.MainnetLocked || !execView.WENHeld {
		t.Fatalf("unexpected default execution status: %+v", execView)
	}

	vaultsResp := apiRequest(h, "GET", "/api/v1/vaults", "", token, "", "")
	if vaultsResp.Code != 200 || !strings.Contains(vaultsResp.Body.String(), `"vaults":[]`) {
		t.Fatalf("unexpected default vaults response (%d): %s", vaultsResp.Code, vaultsResp.Body.String())
	}

	// Unauthenticated requests refused.
	if unauth := apiRequest(h, "GET", "/api/v1/execution/status", "", "", "", ""); unauth.Code != 401 {
		t.Fatalf("expected 401 on unauthenticated execution/status, got %d", unauth.Code)
	}

	// Non-testnet profiles refused cleanly.
	if err := s.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{Profile: "mainnet"}); err == nil {
		t.Fatal("expected error for mainnet profile")
	}
}

func TestStage4TestnetActivationDevnetRehearsalAndTools(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	signerDir := fixtureDir(t)
	if err := os.Chmod(signerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cassette, err := os.ReadFile(filepath.Join("..", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		cassette, err = os.ReadFile(filepath.Join("internal", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	}
	if err != nil {
		t.Fatal(err)
	}

	signerID := "0123456789abcdef0123456789abcdef"
	pass := []byte("stage4-testnet-core-passphrase-2026")
	vaultID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

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
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       5_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.SetAgentVaultPolicy(signer.AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "stage4-desk-agent",
		AllowedVenues:      []string{"solana", "hyperliquid"},
		AllowedAssets:      []string{"SOL", "ETH"},
		MaxOrderMicroUSD:   2_000_000_000,
		DailySpendMicroUSD: 3_000_000_000,
		DailyLossMicroUSD:  500_000_000,
		MaxLeverage:        5,
		ExpiresAt:          now + 3600,
	}); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}

	hlClient, err := hyperliquid.NewControlledClient("testnet", &coreHyperliquidMockTransport{
		marketsRaw:   cassette,
		nextOid:      9000,
		orderStates:  map[uint64]string{},
		orderByCloid: map[string]uint64{},
		cloidByOid:   map[uint64]string{},
		appliedLev: map[string]struct {
			leverage uint32
			isCross  bool
		}{},
	})
	if err != nil {
		_ = v.Close()
		t.Fatal(err)
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

	var bh [32]byte
	bh[0] = 0x11
	bh[31] = 0x22
	solanaRT := &mockSolanaDevnetRT{blockhash: onchain.Base58(bh[:])}
	secretKey := "secret-devnet-key-do-not-leak"

	relay := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		resp, err := stage4TestSignerRelay(v)(ctx, r)
		if err == nil && r.Action == "solana-devnet-sign" && resp.SolanaDevnetSigned != nil {
			solanaRT.sentSig = resp.SolanaDevnetSigned.Signature
		}
		return resp, err
	}

	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile:            signer.ExecutionProfileTestnet,
		SignerCall:         relay,
		AgentVaultID:       vaultID,
		SolanaDevnetRPC:    "https://devnet.example.invalid/?api-key=" + secretKey,
		SolanaRoundTripper: solanaRT,
		HyperliquidClient:  hlClient,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution failed: %v", err)
	}

	h := srv.HTTP.Handler.(*HTTPHandler)

	// Health reflects opt-in testnet capabilities cleanly (updater stays false).
	health := srv.Store.Health()
	if !health.Capabilities["walletSigner"] || !health.Capabilities["financialExecution"] || health.Capabilities["updater"] {
		t.Fatalf("unexpected active health capabilities: %+v", health.Capabilities)
	}

	// Signer status reports active testnet execution.
	sResp := stage4Req(h, "GET", srv.URL, "/api/v1/signer/status", "", token, "", "")
	if sResp.Code != 200 || !strings.Contains(sResp.Body.String(), `"executionEnabled":true`) {
		t.Fatalf("unexpected active signer status (%d): %s", sResp.Code, sResp.Body.String())
	}

	// Execution status never leaks secret API key and preserves Mainnet + WEN locks.
	eResp := stage4Req(h, "GET", srv.URL, "/api/v1/execution/status", "", token, "", "")
	if eResp.Code != 200 || strings.Contains(eResp.Body.String(), secretKey) {
		t.Fatalf("unexpected/leaky execution status (%d): %s", eResp.Code, eResp.Body.String())
	}
	var eView ExecutionStatusView
	if err := json.Unmarshal(eResp.Body.Bytes(), &eView); err != nil {
		t.Fatal(err)
	}
	if !eView.Available || !eView.SolanaDevnetReady || eView.SolanaDevnetHost != "https://devnet.example.invalid" || !eView.HyperliquidTestnet || !eView.MainnetLocked || !eView.WENHeld {
		t.Fatalf("unexpected execution view: %+v", eView)
	}

	// Hyperliquid LiveExecution is true for testnet when profile active.
	agentsView, err := srv.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if agentsView.Hyperliquid == nil || !agentsView.Hyperliquid.LiveExecution {
		t.Fatalf("expected LiveExecution=true on testnet Hyperliquid view, got %+v", agentsView.Hyperliquid)
	}
	if agentsView.WENMining != nil || miningActivationEnabled {
		t.Fatal("WEN mining must stay held under Stage 4 testnet activation")
	}

	// Missing step-up on claimed owner instance fails closed with 403 step_up_required.
	noStepResp := stage4Req(h, "POST", srv.URL, "/api/v1/execution/solana-devnet/rehearse", `{"memo":"sim-check","estimatedMicroUsd":1000,"simulateOnly":true}`, token, srv.URL, csrfToken(token))
	if noStepResp.Code != 403 || !strings.Contains(noStepResp.Body.String(), "step_up_required") {
		t.Fatalf("expected 403 step_up_required without step-up, got (%d): %s", noStepResp.Code, noStepResp.Body.String())
	}

	// Simulate-only rehearsal succeeds with step-up.
	simResp := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/execution/solana-devnet/rehearse", `{"memo":"sim-check","estimatedMicroUsd":1000,"simulateOnly":true}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "execute", "solana-devnet"))
	if simResp.Code != 200 || strings.Contains(simResp.Body.String(), secretKey) {
		t.Fatalf("simulate rehearse failed (%d): %s", simResp.Code, simResp.Body.String())
	}
	var simRes SolanaDevnetRehearseResult
	if err := json.Unmarshal(simResp.Body.Bytes(), &simRes); err != nil || !simRes.SimulatedOK || simRes.Broadcasted || simRes.RPCHost != "https://devnet.example.invalid" {
		t.Fatalf("unexpected simulate result: %+v err=%v", simRes, err)
	}

	// Live mock SendWithRebroadcast rehearsal succeeds with fresh single-use step-up.
	liveResp := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/execution/solana-devnet/rehearse", `{"memo":"live-mock","estimatedMicroUsd":2000}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "execute", "solana-devnet"))
	if liveResp.Code != 200 || strings.Contains(liveResp.Body.String(), secretKey) {
		t.Fatalf("live rehearse failed (%d): %s", liveResp.Code, liveResp.Body.String())
	}
	var liveRes SolanaDevnetRehearseResult
	if err := json.Unmarshal(liveResp.Body.Bytes(), &liveRes); err != nil || !liveRes.Broadcasted || !liveRes.Confirmed || liveRes.Attempts != 1 {
		t.Fatalf("unexpected live result: %+v err=%v", liveRes, err)
	}

	// Read-only agent tools return expected data.
	toolStatus, err := toolGetExecutionStatus(t.Context(), srv.Store, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolGetExecutionStatus failed: %v", err)
	}
	if stView, ok := toolStatus.(ExecutionStatusView); !ok || !stView.HyperliquidTestnet {
		t.Fatalf("unexpected toolGetExecutionStatus output: %#v", toolStatus)
	}
	toolVaults, err := toolListAgentVaults(t.Context(), srv.Store, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolListAgentVaults failed: %v", err)
	}
	if m, ok := toolVaults.(map[string]any); !ok || m["count"] != 1 {
		t.Fatalf("unexpected toolListAgentVaults output: %#v", toolVaults)
	}

	// Clearing runtime restores default off capabilities.
	_ = srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{Profile: signer.ExecutionProfileOff})
	offHealth := srv.Store.Health()
	if offHealth.Capabilities["walletSigner"] || offHealth.Capabilities["financialExecution"] {
		t.Fatalf("expected capabilities cleared on off profile: %+v", offHealth.Capabilities)
	}
}

func TestStage4LockedSignerLazyHyperliquidBinding(t *testing.T) {
	dir := fixtureDir(t)
	signerDir := fixtureDir(t)
	if err := os.Chmod(signerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	signerID := strings.Repeat("b", 32)
	pass := []byte("locked-signer-lazy-hl-pass-2026")
	vaultID := strings.Repeat("c", 32)

	v, err := signer.Open(signerDir, signerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Create(pass); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.Unlock(pass, time.Minute); err != nil {
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
		AllowedAssets:             []string{"BTC"},
		MaxLeverage:               3,
		MaxNotionalMicroUSD:       10_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	// Lock signer BEFORE starting Core: HyperliquidStatus now reports Address == "" and Enabled == false.
	v.Lock()

	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = srv.Close(context.Background())
		_ = v.Close()
	}()
	cassette, err := os.ReadFile(filepath.Join("..", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		cassette, err = os.ReadFile(filepath.Join("internal", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	hlClient, err := hyperliquid.NewControlledClient("testnet", &coreHyperliquidMockTransport{
		marketsRaw: cassette,
	})
	if err != nil {
		t.Fatal(err)
	}
	relay := stage4TestSignerRelay(v)
	// Startup MUST succeed even while signer is locked with a persisted testnet policy.
	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile:           signer.ExecutionProfileTestnet,
		SignerCall:        relay,
		AgentVaultID:      vaultID,
		HyperliquidClient: hlClient,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution should tolerate locked signer at boot: %v", err)
	}
	if status := srv.Store.executionStatus(t.Context()); status.HyperliquidTestnet {
		t.Fatal("expected HyperliquidTestnet=false while signer remains locked")
	}

	// Unlock signer: executionStatus and Agents must lazily bind Hyperliquid IPC.
	if err := v.Unlock(pass, time.Minute); err != nil {
		t.Fatal(err)
	}
	if status := srv.Store.executionStatus(t.Context()); !status.HyperliquidTestnet {
		t.Fatalf("expected lazy HyperliquidTestnet=true after unlock, got %+v", status)
	}
	agentsView, err := srv.Store.Agents(t.Context(), "", "")
	if err != nil || agentsView.Hyperliquid == nil || !agentsView.Hyperliquid.LiveExecution {
		t.Fatalf("expected Agents().Hyperliquid lazily wired after unlock, got view=%+v err=%v", agentsView.Hyperliquid, err)
	}
}
