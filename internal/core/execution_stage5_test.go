package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/cex"
	"github.com/fased-ai/fased-app/internal/equities"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestStage5CEXAndEquitiesCoreRoutesAndTools(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())
	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)

	// 1. SQLite optional state registry includes cex and equities DBs, excluding keys.
	paths := OptionalStateSQLitePaths()
	if !slices.Contains(paths, "cex/cex.sqlite") || !slices.Contains(paths, "equities/equities.sqlite") {
		t.Fatalf("missing stage5 sqlite paths: %v", paths)
	}

	// 2. WEN remains held and AllowedAgentVaultVenue includes Stage 5 venues only.
	if signer.AllowedAgentVaultVenue("wen") || !signer.AllowedAgentVaultVenue("binance") || !signer.AllowedAgentVaultVenue("alpaca") {
		t.Fatal("unexpected AllowedAgentVaultVenue behavior")
	}

	// 3. CEX venues + status + withdrawal permission rejection + secret redaction + paper order.
	venuesResp := stage4Req(h, "GET", srv.URL, "/api/v1/cex/venues", "", token, "", "")
	if venuesResp.Code != 200 || !strings.Contains(venuesResp.Body.String(), `"mainnetLocked":true`) {
		t.Fatalf("GET /api/v1/cex/venues failed (%d): %s", venuesResp.Code, venuesResp.Body.String())
	}
	badCredResp := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/cex/credentials", `{
		"venue":"bybit","environment":"testnet","apiKey":"bybit-key-123456","apiSecret":"bybit-secret-123456","permissions":["trade","withdraw"]
	}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "cex-credentials", "*"))
	if badCredResp.Code != 403 {
		t.Fatalf("expected 403 for withdrawal scope, got %d: %s", badCredResp.Code, badCredResp.Body.String())
	}
	goodCredResp := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/cex/credentials", `{
		"venue":"bybit","label":"bybit-paper","environment":"testnet","apiKey":"bybit-key-123456","apiSecret":"bybit-secret-value-xyz","permissions":["spot_trade"]
	}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "cex-credentials", "*"))
	if goodCredResp.Code != 200 {
		t.Fatalf("POST /api/v1/cex/credentials failed (%d): %s", goodCredResp.Code, goodCredResp.Body.String())
	}
	statusResp := stage4Req(h, "GET", srv.URL, "/api/v1/cex/status", "", token, "", "")
	if statusResp.Code != 200 || strings.Contains(statusResp.Body.String(), "bybit-secret-value-xyz") {
		t.Fatalf("GET /api/v1/cex/status invalid or leaked secret (%d): %s", statusResp.Code, statusResp.Body.String())
	}

	mainnetCEXResp := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/cex/orders", `{
		"venue":"bybit","symbol":"BTCUSDT","side":"BUY","orderType":"LIMIT","quantity":0.1,"price":50000,"environment":"mainnet"
	}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "execute", "cex"))
	if mainnetCEXResp.Code != 403 {
		t.Fatalf("expected 403 for mainnet CEX order while locked, got %d: %s", mainnetCEXResp.Code, mainnetCEXResp.Body.String())
	}

	paperCEXResp := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/cex/orders", `{
		"venue":"bybit","symbol":"BTCUSDT","side":"BUY","orderType":"LIMIT","quantity":0.1,"price":50000,"environment":"testnet"
	}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "execute", "cex"))
	if paperCEXResp.Code != 200 {
		t.Fatalf("POST /api/v1/cex/orders testnet dryrun failed (%d): %s", paperCEXResp.Code, paperCEXResp.Body.String())
	}
	var cexRec cex.OrderReceipt
	if err := json.Unmarshal(paperCEXResp.Body.Bytes(), &cexRec); err != nil || !cexRec.Simulated {
		t.Fatalf("unexpected CEX receipt: %+v err=%v", cexRec, err)
	}

	// 4. Equities status, quotes, OCC options chain, live lock, and DU* paper order.
	eqStatus := stage4Req(h, "GET", srv.URL, "/api/v1/equities/status", "", token, "", "")
	if eqStatus.Code != 200 || !strings.Contains(eqStatus.Body.String(), `"liveLocked":true`) {
		t.Fatalf("GET /api/v1/equities/status failed (%d): %s", eqStatus.Code, eqStatus.Body.String())
	}
	optResp := stage4Req(h, "GET", srv.URL, "/api/v1/equities/options?symbol=NVDA", "", token, "", "")
	if optResp.Code != 200 {
		t.Fatalf("GET /api/v1/equities/options failed (%d): %s", optResp.Code, optResp.Body.String())
	}
	var chain equities.OptionsChainView
	if err := json.Unmarshal(optResp.Body.Bytes(), &chain); err != nil || len(chain.Contracts) == 0 {
		t.Fatalf("unexpected options chain: %+v err=%v", chain, err)
	}
	badIBKR := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/equities/orders", `{
		"venue":"ibkr","accountId":"U123456","symbol":"NVDA","side":"BUY","orderType":"MARKET","quantity":1
	}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "execute", "equities"))
	if badIBKR.Code != 400 {
		t.Fatalf("expected 400 for non-DU IBKR account, got %d: %s", badIBKR.Code, badIBKR.Body.String())
	}
	goodEq := stage4ReqWithStepUp(h, "POST", srv.URL, "/api/v1/equities/orders", `{
		"venue":"alpaca","symbol":"SPY","side":"BUY","orderType":"LIMIT","quantity":1,"limitPrice":590
	}`, token, srv.URL, csrfToken(token), stage4StepUpToken(t, h, token, "execute", "equities"))
	if goodEq.Code != 200 {
		t.Fatalf("POST /api/v1/equities/orders failed (%d): %s", goodEq.Code, goodEq.Body.String())
	}

	// 5. MCP tools & resources expose Stage 5 read/draft surfaces.
	if !mcpReadTools["get_cex_status"] || !mcpReadTools["get_options_chain"] || !mcpDraftTools["draft_cex_order"] || !mcpDraftTools["draft_equities_order"] {
		t.Fatal("missing Stage 5 MCP tool registrations")
	}
	if _, err := readMCPResource(t.Context(), srv.Store, "fased://cex/status"); err != nil {
		t.Fatalf("readMCPResource(fased://cex/status) failed: %v", err)
	}
	if _, err := readMCPResource(t.Context(), srv.Store, "fased://equities/status"); err != nil {
		t.Fatalf("readMCPResource(fased://equities/status) failed: %v", err)
	}
}
