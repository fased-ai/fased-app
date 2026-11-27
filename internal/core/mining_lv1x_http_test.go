package core

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestLV1XMiningCoreRoutesAndMainnetGate(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())

	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)
	csrf := csrfToken(token)

	if !slices.Contains(OptionalStateSQLitePaths(), "mining/mining.sqlite") {
		t.Fatalf("missing mining/mining.sqlite in OptionalStateSQLitePaths(): %v", OptionalStateSQLitePaths())
	}

	// 1. /api/v1/mining/status returns Devnet round telemetry, 24 streak chips, 6 presets, and mainnetEnabled=false.
	stResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/mining/status?cluster=devnet", "", token, "", "")
	if stResp.Code != http.StatusOK || !strings.Contains(stResp.Body.String(), `"mainnetEnabled":false`) || !strings.Contains(stResp.Body.String(), `"trend-up"`) {
		t.Fatalf("GET /api/v1/mining/status failed (%d): %s", stResp.Code, stResp.Body.String())
	}

	// 2. /api/v1/mining/preview flags mainnetBlocked=true for mainnet-beta.
	prevMain := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/preview", `{"cluster":"mainnet-beta","amountSol":0.05,"strategyId":"wide"}`, token, srv.URL, csrf)
	if prevMain.Code != http.StatusOK || !strings.Contains(prevMain.Body.String(), `"mainnetBlocked":true`) {
		t.Fatalf("expected mainnetBlocked preview, got %d: %s", prevMain.Code, prevMain.Body.String())
	}

	// 3. Session creation blocked on mainnet-beta (403) and succeeds on devnet (201).
	sessMain := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/sessions", `{"cluster":"mainnet-beta","strategyId":"wide","mode":"manual","perRoundSol":0.02}`, token, srv.URL, csrf)
	if sessMain.Code != http.StatusForbidden {
		t.Fatalf("expected 403 mainnet session denial, got %d: %s", sessMain.Code, sessMain.Body.String())
	}
	sessDev := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/sessions", `{"cluster":"devnet","strategyId":"center","mode":"semi_auto","perRoundSol":0.02}`, token, srv.URL, csrf)
	if sessDev.Code != http.StatusCreated || !strings.Contains(sessDev.Body.String(), `"strategyId":"center"`) {
		t.Fatalf("expected 201 devnet session, got %d: %s", sessDev.Code, sessDev.Body.String())
	}

	// 4. Stream WS topic "mining" parses cleanly.
	if p, err := parseStreamTopic("mining"); err != nil || p.kind != "mining" {
		t.Fatalf("parseStreamTopic(mining) failed: %+v err=%v", p, err)
	}
}
