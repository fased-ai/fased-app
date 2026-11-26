package core

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/trade"
)

func TestUnifiedTradeEngineHTTPKillPropagationAndMCP(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	// Clean boot must not eagerly initialize trade.sqlite.
	tradeDB := filepath.Join(s.dir, "trade", "trade.sqlite")
	if _, err := os.Stat(tradeDB); !os.IsNotExist(err) {
		t.Fatalf("expected lazy trade.sqlite before first /api/v1/trade request, stat=%v", err)
	}

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request rejected.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/trade/state", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated /api/v1/trade/state, got %d", unauth.StatusCode)
	}

	// GET /api/v1/trade/state returns empty snapshot with default limits and inactive kill.
	stResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/trade/state", "", token, "", "", "application/json")
	if stResp.StatusCode != http.StatusOK {
		stResp.Body.Close()
		t.Fatalf("GET /api/v1/trade/state got %d", stResp.StatusCode)
	}
	var snap trade.Snapshot
	if err := json.NewDecoder(stResp.Body).Decode(&snap); err != nil {
		stResp.Body.Close()
		t.Fatalf("decode trade state: %v", err)
	}
	stResp.Body.Close()
	if snap.KillSwitchActive || snap.Policy.MaxOrderNotionalUSD <= 0 {
		t.Fatalf("unexpected initial snapshot: %+v", snap)
	}

	// CSRF required on POST /api/v1/trade/orders.
	noCSRF := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/orders", ts.URL, token, "", `{"venue":"hyperliquid","symbol":"BTC","strategy":"market","side":"buy","quantity":0.1}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF on POST /api/v1/trade/orders, got %d", noCSRF.StatusCode)
	}

	// Submit Trailing ATR plan & Bracket plan.
	trResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/orders", ts.URL, token, csrf, `{
		"venue":"hyperliquid","symbol":"ETH","interval":"1h","mode":"bot","strategy":"trailing_atr","side":"buy","quantity":1,"atrMultiplier":1.5
	}`, "application/json")
	if trResp.StatusCode != http.StatusOK {
		trResp.Body.Close()
		t.Fatalf("POST /api/v1/trade/orders trailing_atr got %d", trResp.StatusCode)
	}
	var trPlan trade.OrderPlan
	_ = json.NewDecoder(trResp.Body).Decode(&trPlan)
	trResp.Body.Close()
	if trPlan.ID == "" || trPlan.TrailingStopPrice <= 0 || !trPlan.Simulated {
		t.Fatalf("unexpected trailing_atr plan: %+v", trPlan)
	}

	// Evaluate active plans endpoint succeeds.
	evalResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/evaluate", ts.URL, token, csrf, `{}`, "application/json")
	evalResp.Body.Close()
	if evalResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/trade/evaluate got %d", evalResp.StatusCode)
	}

	// Cancel active plan succeeds.
	canResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/orders/"+trPlan.ID+"/cancel", ts.URL, token, csrf, `{}`, "application/json")
	if canResp.StatusCode != http.StatusOK {
		canResp.Body.Close()
		t.Fatalf("POST cancel got %d", canResp.StatusCode)
	}
	var cancelled trade.OrderPlan
	_ = json.NewDecoder(canResp.Body).Decode(&cancelled)
	canResp.Body.Close()
	if cancelled.Status != trade.StatusCancelled {
		t.Fatalf("expected cancelled status, got %q", cancelled.Status)
	}

	// Update policy bounds cleanly.
	polResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/trade/policy", ts.URL, token, csrf, `{"maxOrderNotionalUsd":5000,"maxOpenPlans":8,"maxDailyLossUsd":500}`, "application/json")
	polResp.Body.Close()
	if polResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/v1/trade/policy got %d", polResp.StatusCode)
	}

	// Submit active DCA plan, then activate Global Kill Switch via POST /api/v1/trade/kill.
	dcaResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/orders", ts.URL, token, csrf, `{
		"venue":"binance","symbol":"BTCUSDT","mode":"bot","strategy":"dca","side":"buy","quantity":0.02,"slices":4,"intervalSeconds":60
	}`, "application/json")
	dcaResp.Body.Close()
	if dcaResp.StatusCode != http.StatusOK {
		t.Fatalf("POST dca plan got %d", dcaResp.StatusCode)
	}

	killResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/kill", ts.URL, token, csrf, `{"active":true,"reason":"test emergency"}`, "application/json")
	if killResp.StatusCode != http.StatusOK {
		killResp.Body.Close()
		t.Fatalf("POST /api/v1/trade/kill got %d", killResp.StatusCode)
	}
	killResp.Body.Close()

	// Verify Stage 5 CEX order is blocked by Global Kill Switch.
	cexBlocked := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/cex/orders", ts.URL, token, csrf, `{
		"venue":"binance","environment":"paper","symbol":"BTCUSDT","side":"buy","orderType":"limit","quantity":0.01,"price":50000,"simulateOnly":true
	}`, "application/json")
	cexBlocked.Body.Close()
	if cexBlocked.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on /api/v1/cex/orders while global kill active, got %d", cexBlocked.StatusCode)
	}

	// Verify /api/v1/trade/orders is blocked while global kill is active.
	ordBlocked := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/orders", ts.URL, token, csrf, `{
		"venue":"hyperliquid","symbol":"BTC","strategy":"market","side":"buy","quantity":0.01
	}`, "application/json")
	ordBlocked.Body.Close()
	if ordBlocked.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on /api/v1/trade/orders while global kill active, got %d", ordBlocked.StatusCode)
	}

	// MCP tools & resource check while kill is active, then after clearing kill.
	mcpKill, err := toolGetKillSwitchStatus(t.Context(), s, "", []byte(`{}`))
	if err != nil {
		t.Fatalf("toolGetKillSwitchStatus: %v", err)
	}
	rawKill, _ := json.Marshal(mcpKill)
	if !strings.Contains(string(rawKill), `"globalKillActive":true`) {
		t.Fatalf("expected globalKillActive=true in MCP tool output, got %s", rawKill)
	}

	if _, err := toolListWatchlists(t.Context(), s, "", []byte(`{}`)); err != nil {
		t.Fatalf("toolListWatchlists: %v", err)
	}
	if _, err := readMCPResource(t.Context(), s, "fased://trade/state"); err != nil {
		t.Fatalf("readMCPResource fased://trade/state: %v", err)
	}

	// Clear global kill switch -> orders succeed again cleanly.
	unkillResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/kill", ts.URL, token, csrf, `{"active":false,"reason":"all clear"}`, "application/json")
	unkillResp.Body.Close()
	if unkillResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/trade/kill false got %d", unkillResp.StatusCode)
	}

	draft, err := previewDraftTradeOrder(t.Context(), s, "", []byte(`{"venue":"hyperliquid","symbol":"BTC","strategy":"market","side":"buy","quantity":0.01}`))
	if err != nil || draft.Title == "" {
		t.Fatalf("previewDraftTradeOrder failed: %+v err=%v", draft, err)
	}
	execRes, err := executeDraftTradeOrder(t.Context(), s, "", []byte(`{"venue":"hyperliquid","symbol":"BTC","strategy":"market","side":"buy","quantity":0.01}`))
	if err != nil || execRes == nil {
		t.Fatalf("executeDraftTradeOrder after unkill failed: %v", err)
	}
}
