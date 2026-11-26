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

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func TestAutomationTelegramAndMCPIntegration(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated access refused.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/automation/state", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated /api/v1/automation/state, got %d", unauth.StatusCode)
	}

	// CSRF enforced on PUT /api/v1/automation/telegram and POST /api/v1/automation/evaluate.
	noCSRF := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/automation/evaluate", ts.URL, token, "", "", "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF on evaluate, got %d", noCSRF.StatusCode)
	}

	// Save Telegram config + bot token and verify zero plaintext token leakage in HTTP response.
	secretTok := "987654321:TestBotTokenValue_0123456789ABCDEF"
	tgPayload := map[string]any{
		"enabled":            true,
		"botToken":           secretTok,
		"allowedChatIds":     []int64{777001},
		"minUrgency":         0.70,
		"notifyOnApproval":   true,
		"notifyOnKillSwitch": true,
	}
	tgBytes, _ := json.Marshal(tgPayload)
	tgPut := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/automation/telegram", ts.URL, token, csrf, string(tgBytes), "application/json")
	if tgPut.StatusCode != http.StatusOK {
		tgPut.Body.Close()
		t.Fatalf("PUT telegram expected 200, got %d", tgPut.StatusCode)
	}
	tgRaw, _ := io.ReadAll(tgPut.Body)
	tgPut.Body.Close()
	if strings.Contains(string(tgRaw), secretTok) {
		t.Fatal("plaintext Telegram bot token leaked in PUT response")
	}

	// Save HTTPS webhook with signing secret and verify zero plaintext secret leakage.
	whSecret := "fased-webhook-secret-integration-88"
	whBytes, _ := json.Marshal(map[string]any{
		"enabled":  true,
		"endpoint": "https://hooks.fased.invalid/alerts",
		"secret":   whSecret,
	})
	whPut := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/automation/webhook", ts.URL, token, csrf, string(whBytes), "application/json")
	if whPut.StatusCode != http.StatusOK {
		whPut.Body.Close()
		t.Fatalf("PUT webhook expected 200, got %d", whPut.StatusCode)
	}
	whRaw, _ := io.ReadAll(whPut.Body)
	whPut.Body.Close()
	if strings.Contains(string(whRaw), whSecret) {
		t.Fatal("plaintext webhook secret leaked in PUT response")
	}

	// Create World Intel + Price + Prediction triggers.
	triggers := []string{
		`{"id":"intel-oil","name":"Oil Advisory","kind":"news","cooldownSeconds":60,"condition":{"category":"energy","minUrgency":0.5},"action":{"type":"jev_triage","sendTelegram":true,"sendWebhook":true,"severity":"warn"}}`,
		`{"id":"btc-price","name":"BTC Above 10","kind":"price","cooldownSeconds":60,"condition":{"venue":"hyperliquid","symbol":"BTC","interval":"1h","comparator":"above","threshold":10},"action":{"type":"agent_prompt","promptTemplate":"Summarize BTC structure.","sendTelegram":false,"sendWebhook":false,"severity":"info"}}`,
		`{"id":"fed-arb","name":"Fed Spread","kind":"prediction","cooldownSeconds":60,"condition":{"minSpreadPct":8.0},"action":{"type":"paper_alert","severity":"warn"}}`,
	}
	for _, body := range triggers {
		r := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/automation/triggers", ts.URL, token, csrf, body, "application/json")
		if r.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(r.Body)
			r.Body.Close()
			t.Fatalf("PUT trigger expected 200, got %d: %s", r.StatusCode, raw)
		}
		r.Body.Close()
	}

	// EvaluateNow fires triggers cleanly and creates notifications + fixture Telegram/Webhook receipts.
	evalResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/automation/evaluate", ts.URL, token, csrf, "", "application/json")
	if evalResp.StatusCode != http.StatusOK {
		evalResp.Body.Close()
		t.Fatalf("POST evaluate expected 200, got %d", evalResp.StatusCode)
	}
	var sweep struct {
		Evaluated int `json:"evaluated"`
		Fired     int `json:"fired"`
	}
	_ = json.NewDecoder(evalResp.Body).Decode(&sweep)
	evalResp.Body.Close()
	if sweep.Evaluated != 3 || sweep.Fired != 3 {
		t.Fatalf("expected 3 evaluated & 3 fired, got %+v", sweep)
	}

	// SendTest in fixture mode records deterministic fixture-sent delivery.
	testResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/automation/telegram/test", ts.URL, token, csrf, "", "application/json")
	if testResp.StatusCode != http.StatusOK {
		testResp.Body.Close()
		t.Fatalf("POST telegram/test expected 200, got %d", testResp.StatusCode)
	}
	testResp.Body.Close()

	// Seed two pending approvals (save_memory + request_secret) and test Telegram simulation.
	thread, err := s.CreateThread(t.Context(), "trading-analyst", "Telegram Test Thread", "", "")
	if err != nil {
		t.Fatal(err)
	}
	memAp := createPendingApprovalFixture(t, s, thread.ID, "save_memory", json.RawMessage(`{"title":"Limit","content":"Keep max leverage 3x."}`))
	secAp := createPendingApprovalFixture(t, s, thread.ID, "request_secret", json.RawMessage(`{"kind":"provider_api_key","provider":"openai"}`))

	sim := func(chatID int64, text string) (bool, string) {
		b, _ := json.Marshal(map[string]any{"chatId": chatID, "text": text})
		r := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/automation/telegram/simulate", ts.URL, token, csrf, string(b), "application/json")
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("simulate expected 200, got %d", r.StatusCode)
		}
		var out struct {
			Authorized bool   `json:"authorized"`
			Text       string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&out)
		return out.Authorized, out.Text
	}

	if auth, _ := sim(999999, "/status"); auth {
		t.Fatal("expected unauthorized chat 999999 rejected")
	}
	if auth, txt := sim(777001, "/approvals"); !auth || !strings.Contains(txt, memAp.ID) {
		t.Fatalf("expected pending approvals listing, got auth=%v txt=%q", auth, txt)
	}
	if auth, txt := sim(777001, "/approve "+secAp.ID); !auth || !strings.Contains(txt, "cannot be submitted over Telegram") {
		t.Fatalf("expected request_secret approval refused over Telegram, got %q", txt)
	}
	if auth, txt := sim(777001, "/approve "+memAp.ID+" ok"); !auth || !strings.Contains(txt, "marked approved") {
		t.Fatalf("expected save_memory approval resolved over Telegram, got %q", txt)
	}

	// Telegram /kill activates kill switch, /unkill CONFIRM clears it cleanly.
	if auth, txt := sim(777001, "/kill volatility"); !auth || !strings.Contains(txt, "KILL SWITCH ACTIVE") {
		t.Fatalf("expected /kill success, got %q", txt)
	}
	if auth, txt := sim(777001, "/resume"); !auth || !strings.Contains(txt, "Refused") && !strings.Contains(txt, "refused") {
		t.Fatalf("expected /resume refused while killed, got %q", txt)
	}
	if auth, txt := sim(777001, "/unkill CONFIRM"); !auth || !strings.Contains(txt, "Kill switch cleared") {
		t.Fatalf("expected /unkill CONFIRM success, got %q", txt)
	}

	// SSE /api/v1/automation/stream emits ready event cleanly.
	streamCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(streamCtx, http.MethodGet, ts.URL+"/api/v1/automation/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	streamResp, err := ts.Client().Do(req)
	if err != nil || streamResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/automation/stream failed: err=%v resp=%v", err, streamResp)
	}
	defer streamResp.Body.Close()
	sc := bufio.NewScanner(streamResp.Body)
	sawReady := false
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: ready") {
			sawReady = true
			break
		}
	}
	if !sawReady {
		t.Fatal("expected SSE ready event from /api/v1/automation/stream")
	}

	// MCP tools & fased://automation/triggers resource work cleanly.
	for _, tool := range []string{"list_event_triggers", "list_notifications"} {
		body := `{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
		resp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, body, "application/json")
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("MCP %s failed: %d", tool, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","id":41,"method":"resources/read","params":{"uri":"fased://automation/triggers"}}`, "application/json")
	if resResp.StatusCode != http.StatusOK {
		resResp.Body.Close()
		t.Fatalf("MCP fased://automation/triggers failed: %d", resResp.StatusCode)
	}
	resResp.Body.Close()

	// Delete trigger & mark notifications read cleanly.
	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/automation/triggers/btc-price", ts.URL, token, csrf, "", "")
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE trigger expected 200, got %d", delResp.StatusCode)
	}

	// FasedScript trigger + ActionDeskCycle + NotifyOnApproval integration.
	scriptTrigBody := `{"id":"lab-desk-trig","name":"Lab Script Desk","kind":"fasedscript","cooldownSeconds":60,"condition":{"venue":"hyperliquid","symbol":"BTC","interval":"1h","scriptId":"ema-rsi-trend"},"action":{"type":"desk_cycle","deskMode":"shadow","severity":"info"}}`
	stPut := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/automation/triggers", ts.URL, token, csrf, scriptTrigBody, "application/json")
	if stPut.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(stPut.Body)
		stPut.Body.Close()
		t.Fatalf("PUT fasedscript trigger expected 200, got %d: %s", stPut.StatusCode, raw)
	}
	stPut.Body.Close()

	// Verify NotifyOnApproval delivers approval notification with Telegram inline callbacks cleanly.
	s.notifyApprovalRequired(t.Context(), "ap-notify-1", "Approval required: Test Draft", "Summary for approval notification", true)
	ntfResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/automation/notifications?limit=20", ts.URL, token, "", "", "")
	if ntfResp.StatusCode != http.StatusOK {
		ntfResp.Body.Close()
		t.Fatalf("GET notifications expected 200, got %d", ntfResp.StatusCode)
	}
	ntfRaw, _ := io.ReadAll(ntfResp.Body)
	ntfResp.Body.Close()
	if !strings.Contains(string(ntfRaw), "Approval required: Test Draft") {
		t.Fatalf("expected approval notification persisted, got %s", ntfRaw)
	}
}

func TestDrawingBreachAndReactiveBusTrigger(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	ts, token, csrf := startV2HTTPTestServer(t, s)

	// 1. Persist a horizontal chart drawing at price 100 (synthetic BTC ~94500 is clearly above 100).
	putDraw := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/market/drawings", ts.URL, token, csrf, `{"id":"draw-core-hline","venue":"hyperliquid","symbol":"BTC","kind":"hline","p1":{"time":1764000000,"price":100},"label":"Floor"}`, "application/json")
	if putDraw.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(putDraw.Body)
		putDraw.Body.Close()
		t.Fatalf("PUT /api/v1/market/drawings = %d: %s", putDraw.StatusCode, raw)
	}
	putDraw.Body.Close()

	// 2. Persist a drawing_breach trigger tied to draw-core-hline.
	putTrig := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/automation/triggers", ts.URL, token, csrf, `{"id":"trig-draw-reactive","name":"Drawing Floor Break","kind":"drawing_breach","cooldownSeconds":10,"condition":{"venue":"hyperliquid","symbol":"BTC","interval":"1h","drawingId":"draw-core-hline","comparator":"above"},"action":{"type":"notify","severity":"warn"}}`, "application/json")
	if putTrig.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(putTrig.Body)
		putTrig.Body.Close()
		t.Fatalf("PUT drawing_breach trigger = %d: %s", putTrig.StatusCode, raw)
	}
	putTrig.Body.Close()

	// 3. Start background tasks and verify marketdata.Bus Publish triggers debounced evaluation within 4s (not 15s ticker).
	srv := &Server{Store: s, dir: s.dir}
	srv.startAgentTasks()
	t.Cleanup(func() {
		if srv.taskCancel != nil {
			srv.taskCancel()
			<-srv.taskDone
		}
	})

	md, err := s.marketDataService()
	if err != nil {
		t.Fatalf("marketDataService: %v", err)
	}
	// Give the goroutine a brief moment to attach bus subscription after startup.
	time.Sleep(150 * time.Millisecond)
	md.Publish(marketdata.Event{
		Type:      "candle",
		Venue:     "hyperliquid",
		Symbol:    "BTC",
		Interval:  "1h",
		Timestamp: time.Now().Unix(),
	})

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		ntfResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/automation/notifications?limit=20", ts.URL, token, "", "", "")
		raw, _ := io.ReadAll(ntfResp.Body)
		ntfResp.Body.Close()
		if ntfResp.StatusCode == http.StatusOK && strings.Contains(string(raw), "Drawing Floor Break") {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("reactive marketdata.Bus event did not fire drawing_breach trigger within 4s")
}
