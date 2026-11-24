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

func TestWorldIntelHTTPAndAgentMCPIntegration(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request rejected cleanly.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/intel/events", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated /api/v1/intel/events, got %d", unauth.StatusCode)
	}

	// Read endpoints return deterministic multi-region fixture data + arbitrage spreads.
	for _, path := range []string{
		"/api/v1/intel/events?limit=10",
		"/api/v1/intel/events?region=mena&ticker=OIL",
		"/api/v1/intel/map?hours=72",
		"/api/v1/intel/predictions",
		"/api/v1/intel/sources",
	} {
		resp := doV2JSONRequest(t, http.MethodGet, ts.URL+path, "", token, "", "", "application/json")
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("GET %s expected 200, got %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Prediction markets expose Polymarket/Kalshi arbitrage spreads in fixture mode.
	predResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/intel/predictions", "", token, "", "", "application/json")
	var predBody map[string]any
	_ = json.NewDecoder(predResp.Body).Decode(&predBody)
	predResp.Body.Close()
	if int(predBody["count"].(float64)) < 3 || len(predBody["arbitrage"].([]any)) < 1 {
		t.Fatalf("expected >=3 predictions and >=1 arbitrage spread, got %+v", predBody)
	}

	// CSRF enforced on PUT sources & POST sync.
	badCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/intel/sources", ts.URL, token, "", `{"id":"c1","name":"C1","kind":"rss","endpoint":"https://c1.invalid/rss.xml","category":"macro","region":"na"}`, "application/json")
	badCSRF.Body.Close()
	if badCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF on PUT /api/v1/intel/sources, got %d", badCSRF.StatusCode)
	}

	// Custom source PUT -> sync -> built-in delete refusal -> custom DELETE -> 404.
	putResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/intel/sources", ts.URL, token, csrf, `{"id":"custom-news","name":"Custom News","kind":"rss","endpoint":"https://custom.intel.invalid/rss.xml","category":"crypto","region":"global"}`, "application/json")
	if putResp.StatusCode != http.StatusOK {
		putResp.Body.Close()
		t.Fatalf("PUT custom source expected 200, got %d", putResp.StatusCode)
	}
	putResp.Body.Close()

	syncResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/intel/sync", ts.URL, token, csrf, `{"sourceId":"custom-news"}`, "application/json")
	if syncResp.StatusCode != http.StatusOK {
		syncResp.Body.Close()
		t.Fatalf("POST /api/v1/intel/sync expected 200, got %d", syncResp.StatusCode)
	}
	syncResp.Body.Close()

	builtInDel := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/intel/sources/gdelt-global", ts.URL, token, csrf, "", "")
	builtInDel.Body.Close()
	if builtInDel.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting built-in source, got %d", builtInDel.StatusCode)
	}

	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/intel/sources/custom-news", ts.URL, token, csrf, "", "")
	if delResp.StatusCode != http.StatusOK {
		delResp.Body.Close()
		t.Fatalf("DELETE custom source expected 200, got %d", delResp.StatusCode)
	}
	delResp.Body.Close()

	missingDel := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/intel/sources/custom-news", ts.URL, token, csrf, "", "")
	missingDel.Body.Close()
	if missingDel.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 deleting already-deleted source, got %d", missingDel.StatusCode)
	}

	// SSE stream emits initial snapshot event cleanly.
	streamCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(streamCtx, http.MethodGet, ts.URL+"/api/v1/intel/stream?region=global", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	streamResp, err := ts.Client().Do(req)
	if err != nil || streamResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/intel/stream failed: err=%v resp=%v", err, streamResp)
	}
	defer streamResp.Body.Close()
	sc := bufio.NewScanner(streamResp.Body)
	sawSnapshot := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: snapshot") {
			sawSnapshot = true
			break
		}
	}
	if !sawSnapshot {
		t.Fatalf("expected SSE snapshot event from /api/v1/intel/stream")
	}

	// MCP tools & fased://intel/events resource work cleanly.
	mcpToolResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"get_prediction_markets","arguments":{"limit":5}}}`, "application/json")
	if mcpToolResp.StatusCode != http.StatusOK {
		mcpToolResp.Body.Close()
		t.Fatalf("MCP get_prediction_markets failed: %d", mcpToolResp.StatusCode)
	}
	mcpToolResp.Body.Close()

	mcpIntelResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"get_world_intel","arguments":{"ticker":"BTC","limit":5}}}`, "application/json")
	if mcpIntelResp.StatusCode != http.StatusOK {
		mcpIntelResp.Body.Close()
		t.Fatalf("MCP get_world_intel failed: %d", mcpIntelResp.StatusCode)
	}
	mcpIntelResp.Body.Close()

	mcpResResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":32,"method":"resources/read","params":{"uri":"fased://intel/events"}}`, "application/json")
	if mcpResResp.StatusCode != http.StatusOK {
		mcpResResp.Body.Close()
		t.Fatalf("MCP fased://intel/events failed: %d", mcpResResp.StatusCode)
	}
	var resRPC map[string]any
	_ = json.NewDecoder(mcpResResp.Body).Decode(&resRPC)
	mcpResResp.Body.Close()
	text := resRPC["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "Federal Reserve") || !strings.Contains(text, `"untrusted":true`) {
		t.Fatalf("unexpected fased://intel/events content: %s", text)
	}
}
