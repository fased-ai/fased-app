package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/hub"
	"github.com/fased-ai/fased-app/internal/swarm"
)

func TestStage6HubSwarmCoreRoutesWSAndMCP(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())
	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)

	// 1. Optional SQLite registry covers hub and swarm databases.
	paths := OptionalStateSQLitePaths()
	if !slices.Contains(paths, "hub/hub.sqlite") || !slices.Contains(paths, "swarm/swarm.sqlite") {
		t.Fatalf("missing stage6 sqlite paths: %v", paths)
	}

	// 2. Hub verified packages list + WEN held + install route.
	hubListResp := stage4Req(h, "GET", srv.URL, "/api/v1/hub/packages", "", token, "", "")
	if hubListResp.Code != 200 || !strings.Contains(hubListResp.Body.String(), `"wenHeld":true`) {
		t.Fatalf("GET /api/v1/hub/packages failed (%d): %s", hubListResp.Code, hubListResp.Body.String())
	}
	var view hub.ListView
	if err := json.Unmarshal(hubListResp.Body.Bytes(), &view); err != nil || len(view.Packages) == 0 {
		t.Fatalf("unexpected hub list view: %+v err=%v", view, err)
	}
	instResp := stage4Req(h, "POST", srv.URL, "/api/v1/hub/install", `{"id":"`+view.Packages[0].ID+`"}`, token, srv.URL, csrfToken(token))
	if instResp.Code != 200 || !strings.Contains(instResp.Body.String(), `"installed":true`) {
		t.Fatalf("POST /api/v1/hub/install failed (%d): %s", instResp.Code, instResp.Body.String())
	}

	// 3. Swarm opt-in disabled by default, live copy blocked, shadow/paper broadcast marks untrusted=true.
	swStatus := stage4Req(h, "GET", srv.URL, "/api/v1/swarm/status", "", token, "", "")
	if swStatus.Code != 200 {
		t.Fatalf("GET /api/v1/swarm/status failed (%d): %s", swStatus.Code, swStatus.Body.String())
	}
	var st swarm.StatusView
	if err := json.Unmarshal(swStatus.Body.Bytes(), &st); err != nil || st.Config.Enabled || !st.LiveCopyLocked || !st.WENHeld {
		t.Fatalf("unexpected default swarm status: %+v err=%v", st, err)
	}
	liveResp := stage4Req(h, "PUT", srv.URL, "/api/v1/swarm/config", `{"copyMode":"live"}`, token, srv.URL, csrfToken(token))
	if liveResp.Code != 403 {
		t.Fatalf("expected 403 for live copy mode, got %d: %s", liveResp.Code, liveResp.Body.String())
	}
	disabledSig := stage4Req(h, "POST", srv.URL, "/api/v1/swarm/signals", `{"symbol":"BTC","direction":"LONG","thesis":"Should fail","confidence":0.7}`, token, srv.URL, csrfToken(token))
	if disabledSig.Code != 403 {
		t.Fatalf("expected 403 while swarm disabled, got %d", disabledSig.Code)
	}
	enableResp := stage4Req(h, "PUT", srv.URL, "/api/v1/swarm/config", `{"enabled":true,"copyMode":"paper"}`, token, srv.URL, csrfToken(token))
	if enableResp.Code != 200 || !strings.Contains(enableResp.Body.String(), `"enabled":true`) {
		t.Fatalf("PUT /api/v1/swarm/config failed (%d): %s", enableResp.Code, enableResp.Body.String())
	}
	sigResp := stage4Req(h, "POST", srv.URL, "/api/v1/swarm/signals", `{"symbol":"SOL","direction":"LONG","thesis":"Volume impulse","confidence":0.8}`, token, srv.URL, csrfToken(token))
	if sigResp.Code != 200 || !strings.Contains(sigResp.Body.String(), `"untrusted":true`) {
		t.Fatalf("POST /api/v1/swarm/signals failed (%d): %s", sigResp.Code, sigResp.Body.String())
	}

	// 4. Stream WS topic parsing supports swarm, and MCP tools/resources expose Stage 6.
	if p, err := parseStreamTopic("swarm"); err != nil || p.kind != "swarm" {
		t.Fatalf("parseStreamTopic(swarm) failed: %+v err=%v", p, err)
	}
	if !mcpReadTools["list_hub_packages"] || !mcpReadTools["get_swarm_status"] || !mcpDraftTools["draft_hub_install"] || !mcpDraftTools["draft_swarm_signal"] {
		t.Fatal("missing Stage 6 MCP tools")
	}
	if _, err := readMCPResource(t.Context(), srv.Store, "fased://hub/packages"); err != nil {
		t.Fatalf("readMCPResource(fased://hub/packages) failed: %v", err)
	}
	if _, err := readMCPResource(t.Context(), srv.Store, "fased://swarm/status"); err != nil {
		t.Fatalf("readMCPResource(fased://swarm/status) failed: %v", err)
	}
}
