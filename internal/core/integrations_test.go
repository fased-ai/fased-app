package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type coreRTFunc func(*http.Request) (*http.Response, error)

func (f coreRTFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestDecisionAndMCPClientIntegration(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request rejected cleanly.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/decision/config", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated /api/v1/decision/config, got %d", unauth.StatusCode)
	}

	// Missing CSRF on PUT decision config rejected.
	noCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/decision/config", ts.URL, token, "", `{"model":"jev-latest"}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF on PUT /api/v1/decision/config, got %d", noCSRF.StatusCode)
	}

	// Default offline fallback evaluate works with zero external key configured.
	fallbackResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/decision/evaluate", ts.URL, token, csrf, `{"state":"Breaking spike on SOL","questions":{"urgency":{"type":"noul","instructions":"Urgent?"}}}`, "application/json")
	if fallbackResp.StatusCode != http.StatusOK {
		fallbackResp.Body.Close()
		t.Fatalf("expected 200 fallback evaluate, got %d", fallbackResp.StatusCode)
	}
	var fallbackBody map[string]any
	_ = json.NewDecoder(fallbackResp.Body).Decode(&fallbackBody)
	fallbackResp.Body.Close()
	if fallbackBody["provider"] != "deterministic-fallback" || fallbackBody["fallback"] != true {
		t.Fatalf("expected deterministic-fallback provider, got %+v", fallbackBody)
	}

	// Unknown external MCP server returns 404 on /sync and /call.
	sync404 := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp/clients/missing-id/sync", ts.URL, token, csrf, "", "")
	sync404.Body.Close()
	if sync404.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 syncing missing server, got %d", sync404.StatusCode)
	}
	call404 := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp/clients/missing-id/call", ts.URL, token, csrf, `{"tool":"quote","arguments":{}}`, "application/json")
	call404.Body.Close()
	if call404.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 calling missing server, got %d", call404.StatusCode)
	}

	// External MCP Client CRUD + sync + call (fixture endpoint -> zero outbound network).
	putClient := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/mcp/clients", ts.URL, token, csrf, `{"id":"ext-py","name":"Python Lab","endpoint":"https://lab.mcp.invalid/mcp","authToken":"ext-tok-1"}`, "application/json")
	if putClient.StatusCode != http.StatusOK {
		putClient.Body.Close()
		t.Fatalf("PUT /api/v1/mcp/clients expected 200, got %d", putClient.StatusCode)
	}
	putClient.Body.Close()

	syncResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp/clients/ext-py/sync", ts.URL, token, csrf, "", "")
	if syncResp.StatusCode != http.StatusOK {
		syncResp.Body.Close()
		t.Fatalf("POST sync expected 200, got %d", syncResp.StatusCode)
	}
	syncResp.Body.Close()

	callResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp/clients/ext-py/call", ts.URL, token, csrf, `{"tool":"fixture_quote_lookup","arguments":{"symbol":"BTC"}}`, "application/json")
	if callResp.StatusCode != http.StatusOK {
		callResp.Body.Close()
		t.Fatalf("POST call expected 200, got %d", callResp.StatusCode)
	}
	var callBody map[string]any
	_ = json.NewDecoder(callResp.Body).Decode(&callBody)
	callResp.Body.Close()
	if callBody["untrusted"] != true {
		t.Fatalf("expected untrusted=true on external MCP call result, got %+v", callBody)
	}

	// Fased's own MCP server exposes jev_evaluate, call_external_mcp_tool, and fased://mcp/clients.
	mcpJevResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":19,"method":"tools/call","params":{"name":"jev_evaluate","arguments":{"state":"SOL breakout","questions":{"bias":{"type":"choice","choices":["bullish","bearish"]}}}}}`, "application/json")
	if mcpJevResp.StatusCode != http.StatusOK {
		mcpJevResp.Body.Close()
		t.Fatalf("MCP jev_evaluate failed: %d", mcpJevResp.StatusCode)
	}
	mcpJevResp.Body.Close()

	mcpToolsResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"call_external_mcp_tool","arguments":{"serverId":"ext-py","tool":"fixture_quote_lookup","arguments":{"symbol":"SOL"}}}}`, "application/json")
	if mcpToolsResp.StatusCode != http.StatusOK {
		mcpToolsResp.Body.Close()
		t.Fatalf("MCP call_external_mcp_tool failed: %d", mcpToolsResp.StatusCode)
	}
	mcpToolsResp.Body.Close()

	mcpResResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":21,"method":"resources/read","params":{"uri":"fased://mcp/clients"}}`, "application/json")
	if mcpResResp.StatusCode != http.StatusOK {
		mcpResResp.Body.Close()
		t.Fatalf("MCP resource fased://mcp/clients failed: %d", mcpResResp.StatusCode)
	}
	var resRPC map[string]any
	_ = json.NewDecoder(mcpResResp.Body).Decode(&resRPC)
	mcpResResp.Body.Close()
	text := resRPC["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "ext-py") || !strings.Contains(text, "fixture_quote_lookup") {
		t.Fatalf("unexpected fased://mcp/clients content: %s", text)
	}

	// Delete client.
	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/mcp/clients/ext-py", ts.URL, token, csrf, "", "")
	if delResp.StatusCode != http.StatusOK {
		delResp.Body.Close()
		t.Fatalf("DELETE /api/v1/mcp/clients/ext-py expected 200, got %d", delResp.StatusCode)
	}
	delResp.Body.Close()
}

func TestDecisionLiveTransportAndClearKey(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = false

	var sawAuth string
	s.decisionTransport = coreRTFunc(func(req *http.Request) (*http.Response, error) {
		sawAuth = req.Header.Get("Authorization")
		body := `{"model":"jev-latest","answers":{"urgency":{"type":"noul","noul":0.91}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})

	ts, token, csrf := startV2HTTPTestServer(t, s)

	cfgResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/decision/config", ts.URL, token, csrf, `{"apiKey":"jev-secret-999","model":"jev-latest"}`, "application/json")
	if cfgResp.StatusCode != http.StatusOK {
		cfgResp.Body.Close()
		t.Fatalf("PUT /api/v1/decision/config expected 200, got %d", cfgResp.StatusCode)
	}
	var cfgBody map[string]any
	_ = json.NewDecoder(cfgResp.Body).Decode(&cfgBody)
	cfgResp.Body.Close()
	if cfgBody["configured"] != true || strings.Contains(cfgBody["endpoint"].(string), "jev-secret-999") {
		t.Fatalf("unexpected decision status payload: %+v", cfgBody)
	}

	liveResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/decision/evaluate", ts.URL, token, csrf, `{"state":"Fed holds rates","questions":{"urgency":{"type":"noul","instructions":"Urgent?"}}}`, "application/json")
	if liveResp.StatusCode != http.StatusOK {
		liveResp.Body.Close()
		t.Fatalf("POST /api/v1/decision/evaluate expected 200, got %d", liveResp.StatusCode)
	}
	var liveBody map[string]any
	_ = json.NewDecoder(liveResp.Body).Decode(&liveBody)
	liveResp.Body.Close()
	if liveBody["provider"] != "typesafe-jev" || liveBody["fallback"] != false || sawAuth != "Bearer jev-secret-999" {
		t.Fatalf("expected typesafe-jev live response with bearer token, got body=%+v auth=%q", liveBody, sawAuth)
	}

	clearResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/decision/config", ts.URL, token, csrf, `{"clearKey":true}`, "application/json")
	if clearResp.StatusCode != http.StatusOK {
		clearResp.Body.Close()
		t.Fatalf("clearKey expected 200, got %d", clearResp.StatusCode)
	}
	var clearBody map[string]any
	_ = json.NewDecoder(clearResp.Body).Decode(&clearBody)
	clearResp.Body.Close()
	if clearBody["configured"] != false {
		t.Fatalf("expected configured=false after clearKey, got %+v", clearBody)
	}
}
