package core

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/quantlab"
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

	// Drawing PUT requires CSRF, persists across GET filter, and DELETE removes it.
	badDrawingCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/market/drawings", ts.URL, token, "", `{"id":"draw-1","venue":"hyperliquid","symbol":"BTC","kind":"hline","p1":{"time":1764000000,"price":95000}}`, "application/json")
	badDrawingCSRF.Body.Close()
	if badDrawingCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF on PUT drawing, got %d", badDrawingCSRF.StatusCode)
	}
	putDrawResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/market/drawings", ts.URL, token, csrf, `{"id":"draw-1","venue":"hyperliquid","symbol":"BTC","kind":"hline","p1":{"time":1764000000,"price":95000},"label":"Pivot"}`, "application/json")
	if putDrawResp.StatusCode != http.StatusOK {
		putDrawResp.Body.Close()
		t.Fatalf("PUT drawing expected 200, got %d", putDrawResp.StatusCode)
	}
	putDrawResp.Body.Close()

	getDrawResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/drawings?venue=hyperliquid&symbol=BTC&interval=1h", "", token, "", "", "application/json")
	if getDrawResp.StatusCode != http.StatusOK {
		getDrawResp.Body.Close()
		t.Fatalf("GET drawings expected 200, got %d", getDrawResp.StatusCode)
	}
	var listedDrawings struct {
		Count    int                       `json:"count"`
		Drawings []marketdata.ChartDrawing `json:"drawings"`
	}
	if err := json.NewDecoder(getDrawResp.Body).Decode(&listedDrawings); err != nil {
		getDrawResp.Body.Close()
		t.Fatalf("decode GET drawings: %v", err)
	}
	getDrawResp.Body.Close()
	if listedDrawings.Count != 1 || listedDrawings.Drawings[0].ID != "draw-1" {
		t.Fatalf("unexpected listed drawings: %+v", listedDrawings)
	}

	delDrawResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/market/drawings/draw-1", ts.URL, token, csrf, "", "application/json")
	if delDrawResp.StatusCode != http.StatusOK {
		delDrawResp.Body.Close()
		t.Fatalf("DELETE drawing expected 200, got %d", delDrawResp.StatusCode)
	}
	delDrawResp.Body.Close()

	// Custom CSV feed ingest requires CSRF, persists across custom-feeds list, and serves transformed candles.
	noCSRFCsv := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/market/ingest", ts.URL, token, "", `{"symbol":"BACKTEST1","interval":"1h","format":"csv","csv":"time,open,high,low,close,volume\n1795262400,10,12,9,11,50\n"}`, "application/json")
	noCSRFCsv.Body.Close()
	if noCSRFCsv.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF on POST ingest, got %d", noCSRFCsv.StatusCode)
	}
	ingestResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/market/ingest", ts.URL, token, csrf, `{"symbol":"BACKTEST1","interval":"1h","format":"csv","csv":"time,open,high,low,close,volume\n1795262400,10,12,9,11,50\n1795266000,11,14,10,13,80\n"}`, "application/json")
	if ingestResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(ingestResp.Body)
		ingestResp.Body.Close()
		t.Fatalf("POST /api/v1/market/ingest = %d: %s", ingestResp.StatusCode, raw)
	}
	ingestResp.Body.Close()

	feedsResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/custom-feeds", "", token, "", "", "application/json")
	if feedsResp.StatusCode != http.StatusOK {
		feedsResp.Body.Close()
		t.Fatalf("GET custom-feeds expected 200, got %d", feedsResp.StatusCode)
	}
	feedsRaw, _ := io.ReadAll(feedsResp.Body)
	feedsResp.Body.Close()
	if !strings.Contains(string(feedsRaw), "BACKTEST1") {
		t.Fatalf("expected BACKTEST1 in custom-feeds, got %s", feedsRaw)
	}

	customCandlesResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/market/candles?venue=custom&symbol=BACKTEST1&interval=1h&limit=10", "", token, "", "", "application/json")
	if customCandlesResp.StatusCode != http.StatusOK {
		customCandlesResp.Body.Close()
		t.Fatalf("GET custom candles expected 200, got %d", customCandlesResp.StatusCode)
	}
	customCandlesRaw, _ := io.ReadAll(customCandlesResp.Body)
	customCandlesResp.Body.Close()
	if !strings.Contains(string(customCandlesRaw), `"source":"custom"`) {
		t.Fatalf("expected custom source candles, got %s", customCandlesRaw)
	}

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

func TestStage1To3SSEMultiplexAndShutdown(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	server, err := StartAgentFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatalf("StartAgentFixture failed: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()

	closed := false
	t.Cleanup(func() {
		if !closed {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Close(ctx)
			<-serveDone
		}
	})

	token := claimFixture(t, server.Store)
	mdSvc, err := server.Store.marketDataService()
	if err != nil {
		t.Fatalf("marketDataService failed: %v", err)
	}

	streamURL := server.URL + "/api/v1/market/stream?noSnapshot=1&subs=hyperliquid:BTC-PERP:1h:standard,bybit:BTCUSDT:5m:heikin_ashi"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET multiplexed stream failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("unexpected SSE status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(resp.Body)
	readSSEData := func() (string, string) {
		var eventName string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read SSE stream failed: %v", err)
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			} else if strings.HasPrefix(trimmed, "data:") {
				return eventName, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			}
		}
	}

	readyEvt, _ := readSSEData()
	if readyEvt != "ready" {
		t.Fatalf("expected noSnapshot=1 stream to emit event: ready first, got %q", readyEvt)
	}

	// Publish two matching events and two filtered events across interval/style boundaries.
	now := time.Now().Unix()
	mdSvc.Publish(marketdata.Event{
		Type: "candle", Venue: "hyperliquid", Symbol: "BTC-PERP", Interval: "5m", Style: "standard",
		Timestamp: now, Payload: map[string]any{"close": 90001.0},
	})
	mdSvc.Publish(marketdata.Event{
		Type: "candle", Venue: "hyperliquid", Symbol: "BTC-PERP", Interval: "1h", Style: "standard",
		Timestamp: now, Payload: map[string]any{"close": 91000.0},
	})
	mdSvc.Publish(marketdata.Event{
		Type: "candle", Venue: "bybit", Symbol: "BTCUSDT", Interval: "5m", Style: "standard",
		Timestamp: now, Payload: map[string]any{"close": 90002.0},
	})
	mdSvc.Publish(marketdata.Event{
		Type: "candle", Venue: "bybit", Symbol: "BTCUSDT", Interval: "5m", Style: "heikin_ashi",
		Timestamp: now, Payload: map[string]any{"close": 92000.0},
	})

	var received []marketdata.Event
	for len(received) < 2 {
		_, raw := readSSEData()
		var evt marketdata.Event
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("decode SSE event %q: %v", raw, err)
		}
		received = append(received, evt)
	}
	if received[0].Venue != "hyperliquid" || received[0].Interval != "1h" || received[0].Style != "standard" {
		t.Fatalf("unexpected first filtered SSE event: %+v", received[0])
	}
	if received[1].Venue != "bybit" || received[1].Interval != "5m" || received[1].Style != "heikin_ashi" {
		t.Fatalf("unexpected second filtered SSE event: %+v", received[1])
	}

	// Close server while the SSE connection is still actively open; shutdown must return promptly.
	closeStart := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()
	if err := server.Close(closeCtx); err != nil {
		t.Fatalf("Server.Close with open SSE connection failed: %v", err)
	}
	closed = true
	if elapsed := time.Since(closeStart); elapsed >= 3*time.Second {
		t.Fatalf("Server.Close with open SSE took too long: %s", elapsed)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("server.Serve returned error after Close: %v", err)
	}

	// Verify Stage 1-3 optional SQLite WAL checkpoint includes quantlab/lab.sqlite.
	walStore := fixtureStore(t)
	labSvc, err := walStore.quantLabService()
	if err != nil {
		t.Fatalf("quantLabService failed: %v", err)
	}
	if _, err := labSvc.SaveScript(t.Context(), quantlab.ScriptInput{
		ID:     "wal-check",
		Name:   "WAL Check",
		Source: "close > open",
	}); err != nil {
		t.Fatalf("SaveScript failed: %v", err)
	}
	if err := walStore.CheckpointWAL(context.Background()); err != nil {
		t.Fatalf("CheckpointWAL with quantlab/lab.sqlite failed: %v", err)
	}
	labHealth := InspectSQLiteHealth(context.Background(), filepath.Join(walStore.dir, "quantlab", "lab.sqlite"))
	if !labHealth.Exists || !labHealth.Healthy {
		t.Fatalf("expected healthy quantlab/lab.sqlite after CheckpointWAL, got %+v", labHealth)
	}
}
