package core

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/desk"
	"github.com/fased-ai/fased-app/internal/quantlab"
)

func TestTradeRouterAccountsBotsDeskUnificationAndKillSwitch(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// 1. Default /api/v1/trade/accounts includes Paper.
	accResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/trade/accounts", "", token, "", "", "application/json")
	rawAcc, _ := io.ReadAll(accResp.Body)
	accResp.Body.Close()
	if accResp.StatusCode != http.StatusOK || !strings.Contains(string(rawAcc), `"id":"paper"`) {
		t.Fatalf("GET /api/v1/trade/accounts failed: code=%d body=%s", accResp.StatusCode, rawAcc)
	}

	// 2. Create Solana trading wallet (via custody) + enable Hyperliquid Feed & Trading -> appear in trade/accounts.
	wResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/create", ts.URL, token, csrf, `{"name":"Desk Hot SOL","role":"trading","rpcUrl":"https://api.mainnet-beta.solana.com"}`, "application/json")
	wResp.Body.Close()
	if wResp.StatusCode != http.StatusOK {
		t.Fatalf("create wallet failed: %d", wResp.StatusCode)
	}
	hlResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/data-plugins/hyperliquid", ts.URL, token, csrf, `{"enabled":true,"dataFeedEnabled":true,"tradingEnabled":true}`, "application/json")
	hlResp.Body.Close()
	if hlResp.StatusCode != http.StatusOK {
		t.Fatalf("enable hyperliquid plugin failed: %d", hlResp.StatusCode)
	}

	accResp2 := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/trade/accounts", "", token, "", "", "application/json")
	rawAcc2, _ := io.ReadAll(accResp2.Body)
	accResp2.Body.Close()
	if !strings.Contains(string(rawAcc2), `"kind":"solana"`) || !strings.Contains(string(rawAcc2), `"id":"plugin:hyperliquid"`) {
		t.Fatalf("expected solana wallet + hyperliquid exchange in trade/accounts: %s", rawAcc2)
	}

	// 3. Live order when --execution-profile=off MUST return an explicit error (no fake paper fill).
	liveResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/orders", ts.URL, token, csrf, `{
		"venue":"hyperliquid","symbol":"BTC","side":"buy","quantity":0.1,"strategy":"market","mode":"manual","accountId":"plugin:hyperliquid","executionMode":"live"
	}`, "application/json")
	rawLive, _ := io.ReadAll(liveResp.Body)
	liveResp.Body.Close()
	if liveResp.StatusCode == http.StatusOK {
		t.Fatalf("expected live order rejection when execution-profile=off, got 200: %s", rawLive)
	}

	// 4. Save a FasedScript signal -> create bot -> start bot -> evaluate -> check unified trade/state.
	ql, err := s.quantLabService()
	if err != nil {
		t.Fatalf("quantLabService: %v", err)
	}
	scriptRec, err := ql.SaveScript(t.Context(), quantlab.ScriptInput{
		ID:     "always-long-test",
		Name:   "Always Long Test",
		Source: "strategy({ long: close > 0 });",
	})
	if err != nil {
		t.Fatalf("SaveScript: %v", err)
	}

	botResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/bots", ts.URL, token, csrf, `{
		"name":"Test Script Bot","kind":"fasedscript","accountId":"paper","venue":"hyperliquid","symbol":"BTC","interval":"1h","scriptId":"`+scriptRec.ID+`","maxNotionalUsd":100
	}`, "application/json")
	rawBot, _ := io.ReadAll(botResp.Body)
	botResp.Body.Close()
	if botResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/trade/bots failed: code=%d body=%s", botResp.StatusCode, rawBot)
	}
	var createdBot struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawBot, &createdBot); err != nil || createdBot.ID == "" {
		t.Fatalf("decode bot failed: %v body=%s", err, rawBot)
	}

	startResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/bots/"+createdBot.ID+"/start", ts.URL, token, csrf, `{}`, "application/json")
	startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start bot failed: %d", startResp.StatusCode)
	}

	tradeSvc, err := s.tradeService()
	if err != nil {
		t.Fatalf("tradeService: %v", err)
	}
	botSum, err := tradeSvc.EvaluateBots(t.Context())
	if err != nil || botSum.Submitted != 1 {
		t.Fatalf("EvaluateBots expected 1 submitted order, got sum=%+v err=%v", botSum, err)
	}

	// 5. Desk position opened callback unifies into trade/state with source == "desk".
	ds, err := s.deskService()
	if err != nil {
		t.Fatalf("deskService: %v", err)
	}
	if _, err := ds.UpdateLimits(t.Context(), func() desk.RiskLimits {
		lim := desk.DefaultRiskLimits()
		lim.RequirePMTap = false
		lim.RequireFourGatePass = false
		return lim
	}()); err != nil {
		t.Fatalf("UpdateRiskLimits: %v", err)
	}
	cyc, err := ds.RunCycle(t.Context(), desk.CycleRequest{
		Venue:    "hyperliquid",
		Symbol:   "ETH",
		Interval: "1h",
		Mode:     desk.ModePaper,
	})
	if err != nil {
		t.Fatalf("desk.RunCycle: %v", err)
	}

	stResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/trade/state", "", token, "", "", "application/json")
	rawSt, _ := io.ReadAll(stResp.Body)
	stResp.Body.Close()
	if stResp.StatusCode != http.StatusOK || !strings.Contains(string(rawSt), `"botId":"`+createdBot.ID+`"`) {
		t.Fatalf("expected bot order in trade/state: %s", rawSt)
	}
	if cyc.Position != nil && !strings.Contains(string(rawSt), `"source":"desk"`) {
		t.Fatalf("expected unified desk fill in trade/state: %s", rawSt)
	}

	// 6. Global kill switch immediately pauses running bots and keeps EvaluateBots halted.
	killResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/trade/kill", ts.URL, token, csrf, `{"active":true,"reason":"emergency stop"}`, "application/json")
	killResp.Body.Close()
	if killResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/trade/kill failed: %d", killResp.StatusCode)
	}
	botsAfterKill, _ := tradeSvc.ListBots(t.Context())
	if len(botsAfterKill) != 1 || botsAfterKill[0].Status != "paused" {
		t.Fatalf("expected kill switch to immediately pause running bot, got %+v", botsAfterKill)
	}
	killSum, _ := tradeSvc.EvaluateBots(t.Context())
	if !killSum.Halted {
		t.Fatalf("expected EvaluateBots halted=true after kill, got %+v", killSum)
	}
}
