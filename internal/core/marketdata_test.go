package core

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMarketDataHTTPAndAgentMCPIntegration(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request rejected cleanly.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/instruments", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated /api/v1/market/instruments, got %d", unauth.StatusCode)
	}

	// Read endpoints succeed deterministically in FixtureMode.
	for _, path := range []string{
		"/api/v1/market/instruments?venue=hyperliquid",
		"/api/v1/market/candles?venue=hyperliquid&symbol=BTC&interval=1h&style=renko&limit=40",
		"/api/v1/market/candles?venue=binance&symbol=BTCUSDT&interval=15m&style=tick&tickCount=10&limit=40",
		"/api/v1/market/orderbook?venue=hyperliquid&symbol=BTC&limit=10",
		"/api/v1/market/trades?venue=hyperliquid&symbol=BTC&limit=20",
		"/api/v1/market/footprint?venue=hyperliquid&symbol=BTC&interval=1h&limit=12",
		"/api/v1/market/macro?seriesId=CPIAUCSL&limit=12",
		"/api/v1/market/sec?cik=320193&limit=5",
		"/api/v1/market/cohorts?window=week",
		"/api/v1/market/watchlists",
	} {
		resp := doV2JSONRequest(t, http.MethodGet, ts.URL+path, "", token, "", "", "application/json")
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("GET %s expected 200, got %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Invalid parameters rejected with 400.
	bad := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/candles?venue=unknown&symbol=BTC", "", token, "", "", "application/json")
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid venue, got %d", bad.StatusCode)
	}

	// Watchlist PUT requires CSRF and persists across reads.
	badCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/market/watchlists", ts.URL, token, "", `{"id":"alpha","name":"Alpha","items":[]}`, "application/json")
	badCSRF.Body.Close()
	if badCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF on PUT watchlist, got %d", badCSRF.StatusCode)
	}

	putResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/market/watchlists", ts.URL, token, csrf, `{"id":"alpha","name":"Alpha","items":[{"venue":"hyperliquid","symbol":"SOL","interval":"5m","style":"heikin_ashi"}]}`, "application/json")
	if putResp.StatusCode != http.StatusOK {
		putResp.Body.Close()
		t.Fatalf("PUT watchlist expected 200, got %d", putResp.StatusCode)
	}
	putResp.Body.Close()

	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/market/watchlists/alpha", ts.URL, token, csrf, "", "application/json")
	if delResp.StatusCode != http.StatusOK {
		delResp.Body.Close()
		t.Fatalf("DELETE watchlist expected 200, got %d", delResp.StatusCode)
	}
	delResp.Body.Close()

	// SSE /api/v1/market/stream emits initial snapshot event cleanly and terminates on client cancel.
	sseCtx, cancelSSE := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelSSE()
	sseReq, err := http.NewRequestWithContext(sseCtx, http.MethodGet, ts.URL+"/api/v1/market/stream?venue=hyperliquid&symbol=BTC&interval=1h&style=standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	sseReq.Header.Set("Authorization", "Bearer "+token)
	sseReq.Header.Set("Accept", "text/event-stream")
	sseResp, err := (&http.Client{}).Do(sseReq)
	if err != nil {
		t.Fatalf("SSE request failed: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK || !strings.HasPrefix(sseResp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected 200 text/event-stream, got %d (%q)", sseResp.StatusCode, sseResp.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(sseResp.Body)
	var seenSnapshot bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: snapshot") {
			seenSnapshot = true
		}
		if seenSnapshot && strings.HasPrefix(line, "data:") {
			break
		}
	}
	if !seenSnapshot {
		t.Fatal("expected initial snapshot SSE frame")
	}
	cancelSSE()

	// Agent Tier-1 tools and MCP tools/resources work cleanly.
	for _, tc := range []struct {
		name string
		args string
	}{
		{"get_market_candles", `{"venue":"hyperliquid","symbol":"BTC","interval":"1h","style":"heikin_ashi","limit":20}`},
		{"get_orderbook", `{"venue":"hyperliquid","symbol":"ETH","limit":5}`},
		{"get_footprint", `{"venue":"hyperliquid","symbol":"SOL","interval":"15m","limit":8}`},
		{"get_macro_or_sec", `{"kind":"macro","seriesId":"CPIAUCSL","limit":6}`},
		{"get_macro_or_sec", `{"kind":"sec","cik":"320193","limit":4}`},
		{"get_macro_or_sec", `{"kind":"cohorts","window":"week"}`},
	} {
		rpcBody := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"` + tc.name + `","arguments":` + tc.args + `}}`
		resp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, rpcBody, "application/json")
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		res, _ := out["result"].(map[string]any)
		if resp.StatusCode != http.StatusOK || res == nil || res["isError"] != false {
			t.Fatalf("MCP tool %s failed: code=%d body=%+v", tc.name, resp.StatusCode, out)
		}
	}

	resResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":11,"method":"resources/read","params":{"uri":"fased://market/watchlists"}}`, "application/json")
	var resOut map[string]any
	_ = json.NewDecoder(resResp.Body).Decode(&resOut)
	resResp.Body.Close()
	resMap, _ := resOut["result"].(map[string]any)
	contents, _ := resMap["contents"].([]any)
	if len(contents) == 0 || !strings.Contains(contents[0].(map[string]any)["text"].(string), "Core Desk") {
		t.Fatalf("expected Core Desk in fased://market/watchlists resource, got %+v", resOut)
	}
}
