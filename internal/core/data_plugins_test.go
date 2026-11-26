package core

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataPluginsHTTPAndZeroByDefaultGating(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true
	s.enforceDataPlugins = true // Strict 0-by-default enforcement even with fixture market data.

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// 1. Unauthenticated access rejected.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/data-plugins", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated /api/v1/data-plugins, got %d", unauth.StatusCode)
	}

	// 2. Fresh state starts with 0 enabled plugins.
	listResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/data-plugins", "", token, "", "", "application/json")
	if listResp.StatusCode != http.StatusOK {
		listResp.Body.Close()
		t.Fatalf("GET /api/v1/data-plugins expected 200, got %d", listResp.StatusCode)
	}
	var listed DataPluginsResponse
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		listResp.Body.Close()
		t.Fatalf("decode /api/v1/data-plugins: %v", err)
	}
	listResp.Body.Close()
	if listed.Summary.ActiveTotal != 0 || len(listed.Plugins) < 20 {
		t.Fatalf("expected 0 active plugins at install, got summary=%+v count=%d", listed.Summary, len(listed.Plugins))
	}

	// 3. Market endpoints refuse disabled venues cleanly (0 instruments, 412 on candles).
	instResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/instruments", "", token, "", "", "application/json")
	var instBody struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(instResp.Body).Decode(&instBody)
	instResp.Body.Close()
	if instResp.StatusCode != http.StatusOK || instBody.Count != 0 {
		t.Fatalf("expected 0 instruments when all exchanges disabled, got status=%d count=%d", instResp.StatusCode, instBody.Count)
	}
	candleBlocked := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/candles?venue=hyperliquid&symbol=BTC&interval=1h", "", token, "", "", "application/json")
	candleBlocked.Body.Close()
	if candleBlocked.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 when hyperliquid plugin disabled, got %d", candleBlocked.StatusCode)
	}

	// 4. CSRF enforced on PUT /api/v1/data-plugins/{id}.
	noCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/data-plugins/hyperliquid", ts.URL, token, "", `{"enabled":true}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", noCSRF.StatusCode)
	}

	// 5. Enable Hyperliquid + attach secret API key -> verify encryption at rest + no leak + market unlocked.
	secretKey := "fased-secret-exchange-api-key-9876"
	putHL := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/data-plugins/hyperliquid", ts.URL, token, csrf, `{"enabled":true,"dataFeedEnabled":true,"tradingEnabled":true,"apiKey":"`+secretKey+`"}`, "application/json")
	rawHL, _ := io.ReadAll(putHL.Body)
	putHL.Body.Close()
	if putHL.StatusCode != http.StatusOK {
		t.Fatalf("PUT hyperliquid expected 200, got %d: %s", putHL.StatusCode, rawHL)
	}
	if strings.Contains(string(rawHL), secretKey) || !strings.Contains(string(rawHL), `"hasApiKey":true`) || !strings.Contains(string(rawHL), `"maskedApiKey":"****9876"`) {
		t.Fatalf("unexpected secret handling in response: %s", rawHL)
	}
	onDisk, err := os.ReadFile(filepath.Join(s.dir, "data-plugins.json"))
	if err != nil || strings.Contains(string(onDisk), secretKey) {
		t.Fatalf("raw API key must not appear in data-plugins.json (err=%v)", err)
	}

	candleOK := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/candles?venue=hyperliquid&symbol=BTC&interval=1h&limit=20", "", token, "", "", "application/json")
	candleOK.Body.Close()
	if candleOK.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after enabling hyperliquid, got %d", candleOK.StatusCode)
	}

	// 6. Enable news plugin -> syncs enabled state to worldintel; custom RSS add + delete works.
	putNews := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/data-plugins/gdelt-global", ts.URL, token, csrf, `{"enabled":true}`, "application/json")
	putNews.Body.Close()
	if putNews.StatusCode != http.StatusOK {
		t.Fatalf("PUT gdelt-global expected 200, got %d", putNews.StatusCode)
	}
	if s.enabledWorldIntelPluginCount() != 1 {
		t.Fatalf("expected 1 enabled worldintel plugin, got %d", s.enabledWorldIntelPluginCount())
	}

	customAdd := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/data-plugins/custom", ts.URL, token, csrf, `{"name":"Desk RSS","category":"news","kind":"rss","endpoint":"https://feed.example.org/rss.xml"}`, "application/json")
	var customView DataPluginView
	_ = json.NewDecoder(customAdd.Body).Decode(&customView)
	customAdd.Body.Close()
	if customAdd.StatusCode != http.StatusOK || customView.ID == "" || !customView.Custom {
		t.Fatalf("POST custom plugin failed: status=%d view=%+v", customAdd.StatusCode, customView)
	}

	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/data-plugins/custom/"+customView.ID, ts.URL, token, csrf, "", "application/json")
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE custom plugin failed: %d", delResp.StatusCode)
	}

	// 7. Solana RPC plugin configuration + resolveSolanaRPC helper.
	rpcPut := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/data-plugins/solana-mainnet", ts.URL, token, csrf, `{"enabled":true,"endpoint":"https://mainnet.helius-rpc.com","apiKey":"helius-test-secret-key-01234567"}`, "application/json")
	rpcPut.Body.Close()
	if rpcPut.StatusCode != http.StatusOK {
		t.Fatalf("PUT solana-mainnet expected 200, got %d", rpcPut.StatusCode)
	}
	rawRPC, hostOnly, ok := s.resolveSolanaRPC("mainnet-beta")
	if !ok || !strings.Contains(rawRPC, "api-key=helius-test-secret-key-01234567") || strings.Contains(hostOnly, "helius-test-secret-key") {
		t.Fatalf("unexpected resolveSolanaRPC result: raw=%q host=%q ok=%v", rawRPC, hostOnly, ok)
	}

	testResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/data-plugins/solana-mainnet/test", ts.URL, token, csrf, `{}`, "application/json")
	testResp.Body.Close()
	if testResp.StatusCode != http.StatusOK {
		t.Fatalf("POST data-plugins test expected 200, got %d", testResp.StatusCode)
	}
}
