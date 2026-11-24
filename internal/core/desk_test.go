package core

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeskHTTPMCPEndpointsAndKillSwitchSync(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated access blocked cleanly.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/desk/state", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated /api/v1/desk/state, got %d", unauth.StatusCode)
	}

	// GET /api/v1/desk/state returns 8 seats, shadow mode, and $100k paper capital.
	stateResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/desk/state", "", token, "", "", "application/json")
	if stateResp.StatusCode != http.StatusOK {
		stateResp.Body.Close()
		t.Fatalf("GET /api/v1/desk/state expected 200, got %d", stateResp.StatusCode)
	}
	var snap struct {
		Seats   []map[string]any `json:"seats"`
		Limits  map[string]any   `json:"limits"`
		Account struct {
			CashUSD float64 `json:"cashUsd"`
		} `json:"account"`
		LessonsCount int `json:"lessonsCount"`
	}
	_ = json.NewDecoder(stateResp.Body).Decode(&snap)
	stateResp.Body.Close()
	if len(snap.Seats) != 8 || snap.Limits["mode"] != "shadow" || snap.Account.CashUSD != 100000 || snap.LessonsCount < 3 {
		t.Fatalf("unexpected initial desk snapshot: %+v", snap)
	}

	// Reject PUT /api/v1/desk/limits without CSRF and reject "live" mode even with CSRF.
	noCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/desk/limits", ts.URL, token, "", `{"mode":"paper"}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF on PUT /api/v1/desk/limits, got %d", noCSRF.StatusCode)
	}
	liveLimits := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/desk/limits", ts.URL, token, csrf, `{"mode":"live","initialCapitalUsd":100000,"riskPerTradePct":1,"maxPositionPct":10,"maxOpenPositions":5,"maxDailyLossPct":2,"maxConsecutiveLosses":3,"cooldownHours":24,"maxSlippageBps":25,"feeBps":5,"minChartScore":7,"minRiskReward":1.5,"staleDataMaxAgeSec":3600}`, "application/json")
	liveLimits.Body.Close()
	if liveLimits.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting live mode on /api/v1/desk/limits, got %d", liveLimits.StatusCode)
	}

	// Switch to paper mode (with RequirePMTap=true) cleanly.
	paperPayload := `{"mode":"paper","initialCapitalUsd":100000,"riskPerTradePct":1,"maxPositionPct":10,"maxOpenPositions":5,"maxDailyLossPct":2,"maxConsecutiveLosses":3,"cooldownHours":24,"maxSlippageBps":25,"feeBps":5,"minChartScore":7,"minRiskReward":1.5,"staleDataMaxAgeSec":3600,"requirePmTap":true}`
	okLimits := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/desk/limits", ts.URL, token, csrf, paperPayload, "application/json")
	okLimits.Body.Close()
	if okLimits.StatusCode != http.StatusOK {
		t.Fatalf("PUT paper limits expected 200, got %d", okLimits.StatusCode)
	}

	// Run cycle -> queued as PENDING_PM, approve ticket -> opens paper position.
	cycResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/desk/cycle", ts.URL, token, csrf, `{"venue":"hyperliquid","symbol":"BTC","interval":"1h","mode":"paper"}`, "application/json")
	if cycResp.StatusCode != http.StatusOK {
		cycResp.Body.Close()
		t.Fatalf("POST /api/v1/desk/cycle expected 200, got %d", cycResp.StatusCode)
	}
	var cyc struct {
		FinalStatus string `json:"finalStatus"`
		Ticket      struct {
			ID string `json:"id"`
		} `json:"ticket"`
	}
	_ = json.NewDecoder(cycResp.Body).Decode(&cyc)
	cycResp.Body.Close()
	if cyc.Ticket.ID == "" || cyc.FinalStatus != "PENDING_PM" {
		t.Fatalf("expected PENDING_PM ticket from paper cycle, got %+v", cyc)
	}

	appResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/desk/tickets/"+cyc.Ticket.ID+"/approve", ts.URL, token, csrf, "", "")
	if appResp.StatusCode != http.StatusOK {
		appResp.Body.Close()
		t.Fatalf("POST approve ticket expected 200, got %d", appResp.StatusCode)
	}
	var pos struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(appResp.Body).Decode(&pos)
	appResp.Body.Close()
	if pos.ID == "" || pos.Status != "OPEN" {
		t.Fatalf("expected OPEN paper position, got %+v", pos)
	}

	// Automation/Telegram kill switch immediately flattens open paper positions & blocks desk cycle.
	autoKill := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/automation/state", ts.URL, token, csrf, `{"action":"kill","reason":"telegram panic"}`, "application/json")
	autoKill.Body.Close()
	if autoKill.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/automation/state kill expected 200, got %d", autoKill.StatusCode)
	}
	afterKillResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/desk/state", "", token, "", "", "application/json")
	var afterKill struct {
		Control struct {
			ExternalKillActive bool `json:"externalKillActive"`
			EffectiveHalted    bool `json:"effectiveHalted"`
		} `json:"control"`
		Account struct {
			OpenPositions int `json:"openPositions"`
		} `json:"account"`
	}
	_ = json.NewDecoder(afterKillResp.Body).Decode(&afterKill)
	afterKillResp.Body.Close()
	if !afterKill.Control.ExternalKillActive || !afterKill.Control.EffectiveHalted || afterKill.Account.OpenPositions != 0 {
		t.Fatalf("expected external kill switch to flatten open position and halt desk, got %+v", afterKill)
	}

	blockedCycle := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/desk/cycle", ts.URL, token, csrf, `{"symbol":"BTC","mode":"paper"}`, "application/json")
	blockedCycle.Body.Close()
	if blockedCycle.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when kill switch is active, got %d", blockedCycle.StatusCode)
	}

	// SSE stream emits initial ready event with control state.
	sseCtx, sseCancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, ts.URL+"/api/v1/desk/stream", nil)
	sseReq.Header.Set("Authorization", "Bearer "+token)
	sseResp, err := ts.Client().Do(sseReq)
	if err != nil {
		t.Fatalf("GET /api/v1/desk/stream failed: %v", err)
	}
	reader := bufio.NewReader(sseResp.Body)
	firstEvent, _ := reader.ReadString('\n')
	secondLine, _ := reader.ReadString('\n')
	sseResp.Body.Close()
	if !strings.Contains(firstEvent, "event: ready") || !strings.Contains(secondLine, `"externalKillActive":true`) {
		t.Fatalf("unexpected /api/v1/desk/stream ready event: %q %q", firstEvent, secondLine)
	}

	// Lessons CRUD & MCP tools/resources work cleanly.
	putLesson := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/desk/lessons", ts.URL, token, csrf, `{"id":"test-lesson","category":"risk","symbol":"BTC","rule":"Stay flat ahead of CPI"}`, "application/json")
	putLesson.Body.Close()
	if putLesson.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/v1/desk/lessons expected 200, got %d", putLesson.StatusCode)
	}
	for _, call := range []string{
		`{"jsonrpc":"2.0","id":70,"method":"tools/call","params":{"name":"get_desk_state","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":71,"method":"tools/call","params":{"name":"list_desk_journal","arguments":{"limit":10}}}`,
		`{"jsonrpc":"2.0","id":72,"method":"tools/call","params":{"name":"list_desk_lessons","arguments":{"limit":10}}}`,
		`{"jsonrpc":"2.0","id":73,"method":"resources/read","params":{"uri":"fased://desk/state"}}`,
	} {
		resp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, call, "application/json")
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("MCP desk call failed: %s -> %d", call, resp.StatusCode)
		}
		if strings.Contains(call, `"get_desk_state"`) {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var rpcOut struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(raw, &rpcOut); err != nil || len(rpcOut.Result.Content) == 0 {
				t.Fatalf("decode MCP get_desk_state: %s err=%v", raw, err)
			}
			text := rpcOut.Result.Content[0].Text
			if len(text) >= 12*1024 || strings.Contains(text, `"truncated":true`) || !strings.Contains(text, `"decay"`) || !strings.Contains(text, `"bench"`) {
				t.Fatalf("expected compact non-truncated get_desk_state DTO with decay/bench, got %d bytes: %s", len(text), text)
			}
		} else {
			resp.Body.Close()
		}
	}
	delLesson := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/desk/lessons/test-lesson", ts.URL, token, csrf, "", "")
	delLesson.Body.Close()
	if delLesson.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/v1/desk/lessons/test-lesson expected 200, got %d", delLesson.StatusCode)
	}
}
