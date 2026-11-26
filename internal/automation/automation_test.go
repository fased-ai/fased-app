package automation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func sampleRisingCandles(count int, startPrice float64) []marketdata.Candle {
	out := make([]marketdata.Candle, count)
	baseTs := int64(1795400000)
	for i := 0; i < count; i++ {
		open := startPrice + float64(i)*10
		closePrice := open + 8
		vol := 100.0 + float64(i%5)*10
		if i == count-1 {
			vol = 600.0 // spike on final bar
		}
		out[i] = marketdata.Candle{
			Time:   baseTs + int64(i)*3600,
			Open:   open,
			High:   closePrice + 4,
			Low:    open - 3,
			Close:  closePrice,
			Volume: vol,
		}
	}
	return out
}

func TestIndicatorsAndCompareMetric(t *testing.T) {
	candles := sampleRisingCandles(30, 100)

	rsiCurr, rsiPrev, ok := ComputeRSI14(candles)
	if !ok || rsiCurr <= 70 || rsiPrev <= 70 {
		t.Fatalf("expected high RSI on strictly rising candles, got curr=%.2f prev=%.2f ok=%v", rsiCurr, rsiPrev, ok)
	}

	emaCurr, emaPrev, ok := ComputeEMACross(candles, 9, 21)
	if !ok || emaCurr <= 0 || emaCurr <= emaPrev {
		t.Fatalf("expected widening positive EMA spread, got curr=%.4f prev=%.4f ok=%v", emaCurr, emaPrev, ok)
	}

	vwapCurr, vwapPrev, ok := ComputeVWAPDelta(candles)
	if !ok || vwapCurr <= 0 || vwapPrev <= 0 {
		t.Fatalf("expected positive close-VWAP delta on rising series, got curr=%.4f prev=%.4f ok=%v", vwapCurr, vwapPrev, ok)
	}

	spikeCurr, spikePrev, ok := ComputeVolumeSpike(candles, 20)
	if !ok || spikeCurr < 4.0 || spikePrev >= spikeCurr {
		t.Fatalf("expected >4x volume spike on final bar, got curr=%.2f prev=%.2f ok=%v", spikeCurr, spikePrev, ok)
	}

	cvdCurr, _, ok := ComputeCVDDelta(candles, 10)
	if !ok || cvdCurr <= 0 {
		t.Fatalf("expected positive CVD delta, got curr=%.2f ok=%v", cvdCurr, ok)
	}

	if !CompareMetric(CompCrossAbove, 1.5, -0.5, 0) {
		t.Fatal("expected cross_above match")
	}
	if CompareMetric(CompCrossAbove, 1.5, 0.5, 0) {
		t.Fatal("did not expect cross_above when previous already above threshold")
	}
	if !CompareMetric(CompPctChangeGTE, 110, 100, 10) {
		t.Fatal("expected pct_change_gte 10% match")
	}
}

func TestServiceTriggersControlsCooldownDedupAndWebhook(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 12, 15, 0, 0, time.UTC)

	var (
		mu             sync.Mutex
		webhookHeaders http.Header
		webhookBody    []byte
		agentPrompts   []string
	)

	candles := sampleRisingCandles(30, 68000)
	events := []worldintel.IntelEvent{
		{
			ID:         "ev-hormuz-1",
			SourceName: "GDELT Macro",
			Category:   worldintel.CategoryEnergy,
			Region:     worldintel.RegionMENA,
			Title:      "Urgent Strait of Hormuz oil tanker disruption",
			Urgency:    0.92,
			Sentiment:  "bearish",
			Tickers:    []string{"OIL", "BTC"},
			URL:        "https://example.org/oil",
			Untrusted:  true,
		},
	}
	preds := worldintel.PredictionList{
		Arbitrage: []worldintel.PredictionArbitrage{
			{
				ID:             "arb-fed",
				Topic:          "Fed 25bps cut",
				PolymarketProb: 0.62,
				KalshiProb:     0.48,
				SpreadPct:      14.0,
				Tickers:        []string{"DXY"},
			},
		},
	}

	svc, err := NewService(Config{
		DataDir: dir,
		Now:     func() time.Time { return now },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			webhookHeaders = r.Header.Clone()
			webhookBody = raw
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Header:     make(http.Header),
			}, nil
		}),
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
		IntelEvents: func(_ context.Context, _ worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			return events, nil
		},
		IntelPredictions: func(_ context.Context, _ worldintel.PredictionFilter) (worldintel.PredictionList, error) {
			return preds, nil
		},
		Scorer: func(_ context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error) {
			return decision.EvaluateFallback(req, now)
		},
		OnAgentPrompt: func(_ context.Context, tr Trigger, prompt string, untrusted bool) {
			mu.Lock()
			agentPrompts = append(agentPrompts, tr.ID+":"+prompt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	// Configure HTTPS webhook with signing secret and verify masking.
	whSecret := "super-webhook-hmac-secret-9876"
	wh, err := svc.SaveWebhookConfig(t.Context(), WebhookInput{
		Enabled:  true,
		Endpoint: "https://hooks.example.invalid/fased",
		Secret:   whSecret,
	})
	if err != nil {
		t.Fatalf("SaveWebhookConfig: %v", err)
	}
	if !wh.HasSecret || strings.Contains(wh.SecretMasked, whSecret) {
		t.Fatalf("unexpected webhook secret masking: %+v", wh)
	}

	// Reject non-https webhook URL.
	if _, err := svc.SaveWebhookConfig(t.Context(), WebhookInput{
		Enabled:  true,
		Endpoint: "http://127.0.0.1:8080/hook",
	}); err == nil {
		t.Fatal("expected http:// webhook rejection")
	}

	// Subscribe to SSE bus before firing.
	subCh, unsub := svc.Subscribe()
	defer unsub()

	// Create triggers of all 5 kinds.
	inputs := []TriggerInput{
		{
			ID:              "btc-price",
			Name:            "BTC Above 68k",
			Kind:            KindPrice,
			CooldownSeconds: 300,
			Condition:       TriggerCondition{Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", Comparator: CompAbove, Threshold: 68000},
			Action:          TriggerAction{Type: ActionNotify, SendWebhook: true, Severity: SeverityInfo},
		},
		{
			ID:              "btc-rsi",
			Name:            "BTC Overbought RSI",
			Kind:            KindIndicator,
			CooldownSeconds: 300,
			Condition:       TriggerCondition{Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", Indicator: IndRSI14, Comparator: CompAbove, Threshold: 70},
			Action:          TriggerAction{Type: ActionJevTriage, Severity: SeverityWarn},
		},
		{
			ID:              "mena-energy",
			Name:            "MENA Energy Shock",
			Kind:            KindNews,
			CooldownSeconds: 300,
			Condition:       TriggerCondition{Category: worldintel.CategoryEnergy, Region: worldintel.RegionMENA, MinUrgency: 0.70},
			Action:          TriggerAction{Type: ActionAgentPrompt, PromptTemplate: "Assess crude oil contagion.", Severity: SeverityCritical},
		},
		{
			ID:              "pred-arb",
			Name:            "Fed Odds Divergence",
			Kind:            KindPrediction,
			CooldownSeconds: 300,
			Condition:       TriggerCondition{MinSpreadPct: 8.0},
			Action:          TriggerAction{Type: ActionPaperAlert, Severity: SeverityWarn},
		},
		{
			ID:              "cron-desk",
			Name:            "Quarter Hour Brief",
			Kind:            KindCron,
			CooldownSeconds: 60,
			Condition:       TriggerCondition{CronExpr: "15 12 * * *"},
			Action:          TriggerAction{Type: ActionNotify, Severity: SeverityInfo},
		},
	}
	for _, in := range inputs {
		if _, err := svc.SaveTrigger(t.Context(), in); err != nil {
			t.Fatalf("SaveTrigger %s: %v", in.ID, err)
		}
	}

	sweep1, err := svc.EvaluateNow(t.Context())
	if err != nil {
		t.Fatalf("EvaluateNow: %v", err)
	}
	if sweep1.Evaluated != 5 || sweep1.Fired != 5 {
		t.Fatalf("expected 5 evaluated & 5 fired, got %+v", sweep1)
	}

	// Verify HMAC webhook signature was sent cleanly.
	mu.Lock()
	gotSig := webhookHeaders.Get("X-Fased-Signature-256")
	gotBody := append([]byte(nil), webhookBody...)
	gotPrompts := append([]string(nil), agentPrompts...)
	mu.Unlock()
	mac := hmac.New(sha256.New, []byte(whSecret))
	_, _ = mac.Write(gotBody)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != wantSig {
		t.Fatalf("webhook signature mismatch: got %q want %q", gotSig, wantSig)
	}
	if len(gotPrompts) != 2 { // ActionAgentPrompt + ActionPaperAlert
		t.Fatalf("expected 2 agent prompts, got %d", len(gotPrompts))
	}
	if !strings.Contains(gotPrompts[0], "untrusted=true") {
		t.Fatalf("expected untrusted tag in external news prompt, got %q", gotPrompts[0])
	}

	// Drain at least 1 notification SSE event from bus.
	select {
	case evt := <-subCh:
		if evt.Type == "" {
			t.Fatal("empty stream event type")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for automation stream event")
	}

	// Immediate re-sweep inside cooldown suppresses duplicates cleanly.
	sweep2, err := svc.EvaluateNow(t.Context())
	if err != nil {
		t.Fatalf("EvaluateNow #2: %v", err)
	}
	if sweep2.Fired != 0 {
		t.Fatalf("expected 0 fired during cooldown, got %+v", sweep2)
	}

	// Test Pause, Kill-Switch, Resume refusal while killed, and Unkill persistence.
	if _, err := svc.SetState(t.Context(), StateActionPause, "maintenance", "owner"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	pausedSweep, err := svc.EvaluateNow(t.Context())
	if err != nil || pausedSweep.Skipped != "paused" {
		t.Fatalf("expected skipped=paused, got %+v err=%v", pausedSweep, err)
	}

	if _, err := svc.SetState(t.Context(), StateActionKill, "drawdown guard", "telegram:777"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := svc.SetState(t.Context(), StateActionResume, "", "owner"); err == nil {
		t.Fatal("expected resume refusal while kill switch is active")
	}

	// Verify kill-switch state & notifications persist across reopen.
	_ = svc.Close()
	reopened, err := NewService(Config{DataDir: dir, FixtureMode: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("reopen NewService: %v", err)
	}
	defer reopened.Close()

	stateAfterReopen, err := reopened.GetState(t.Context())
	if err != nil || !stateAfterReopen.KillSwitchActive || !stateAfterReopen.Paused {
		t.Fatalf("expected persisted kill-switch after reopen, got %+v err=%v", stateAfterReopen, err)
	}

	ntfs, err := reopened.ListNotifications(t.Context(), NotificationFilter{Limit: 20})
	if err != nil || ntfs.Total < 6 || ntfs.UnreadCount < 6 {
		t.Fatalf("expected >=6 notifications persisted (5 triggers + kill switch), got %+v err=%v", ntfs, err)
	}

	// Mark all read and verify unread becomes 0.
	if _, err := reopened.MarkNotificationsRead(t.Context(), "", true); err != nil {
		t.Fatalf("MarkNotificationsRead: %v", err)
	}
	ntfsAfterRead, _ := reopened.ListNotifications(t.Context(), NotificationFilter{UnreadOnly: true})
	if ntfsAfterRead.UnreadCount != 0 || len(ntfsAfterRead.Notifications) != 0 {
		t.Fatalf("expected 0 unread after mark all, got %+v", ntfsAfterRead)
	}

	// Unkill restores active state cleanly.
	unkilled, err := reopened.SetState(t.Context(), StateActionUnkill, "", "owner")
	if err != nil || unkilled.KillSwitchActive || unkilled.Paused {
		t.Fatalf("expected clean unkill state, got %+v err=%v", unkilled, err)
	}
}

func TestCVDRealBuySellAndUTCVWAPReset(t *testing.T) {
	t.Parallel()
	day1 := time.Date(2026, 11, 24, 22, 0, 0, 0, time.UTC).Unix()
	candles := make([]marketdata.Candle, 16)
	for i := 0; i < 16; i++ {
		ts := day1 + int64(i)*3600
		buyV, sellV := 10.0, 40.0 // net bearish flow despite green candle closes
		if i == 15 {
			buyV, sellV = 100.0, 20.0
		}
		candles[i] = marketdata.Candle{
			Time:       ts,
			Open:       100,
			High:       110,
			Low:        95,
			Close:      105, // always green vs open!
			Volume:     buyV + sellV,
			BuyVolume:  buyV,
			SellVolume: sellV,
		}
	}
	cvdCurr, cvdPrev, ok := ComputeCVDDelta(candles, 4)
	if !ok || cvdPrev >= 0 || cvdCurr <= cvdPrev {
		t.Fatalf("expected ComputeCVDDelta to use BuyVolume-SellVolume (negative prior, improving current), got curr=%.2f prev=%.2f ok=%v", cvdCurr, cvdPrev, ok)
	}

	// Huge day-1 outlier volume at elevated price must NOT poison UTC day-2 session VWAP.
	vwapCandles := []marketdata.Candle{
		{Time: time.Date(2026, 11, 24, 23, 0, 0, 0, time.UTC).Unix(), Open: 500, High: 505, Low: 495, Close: 500, Volume: 1_000_000},
		{Time: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix(), Open: 100, High: 102, Low: 98, Close: 100, Volume: 10},
		{Time: time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC).Unix(), Open: 100, High: 105, Low: 100, Close: 104, Volume: 20},
	}
	deltaCurr, _, ok := ComputeVWAPDelta(vwapCandles)
	if !ok || deltaCurr <= 0 {
		t.Fatalf("expected positive close-VWAP delta after UTC day reset, got %.4f ok=%v", deltaCurr, ok)
	}
}

func TestFasedScriptTriggerDeskCycleAndPermanentDedup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mu := sync.Mutex{}
	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)

	candles := sampleRisingCandles(40, 50000)
	var deskCalls []string
	publishedSec := time.Date(2026, 11, 24, 13, 55, 0, 0, time.UTC).Unix()
	newsEvents := []worldintel.IntelEvent{
		{
			ID:          "ev-perm-1",
			SourceName:  "GDELT",
			Category:    worldintel.CategoryEnergy,
			Region:      worldintel.RegionMENA,
			Title:       "Permanent dedup news test",
			Urgency:     0.9,
			Sentiment:   "bearish",
			PublishedAt: publishedSec,
			URL:         "https://example.org/perm",
			Untrusted:   true,
		},
	}

	svc, err := NewService(Config{
		DataDir: dir,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
		IntelEvents: func(_ context.Context, _ worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			return newsEvents, nil
		},
		ScriptReader: func(_ context.Context, scriptID string) (string, error) {
			return "strategy({ long: close > 0, stopLossPct: 2.0 });", nil
		},
		OnDeskCycle: func(_ context.Context, _ Trigger, venue, symbol, interval, scriptID, mode string) (string, error) {
			deskCalls = append(deskCalls, venue+":"+symbol+":"+interval+":"+scriptID+":"+mode)
			return "Desk cycle cyc-1 (" + mode + "): SHADOW", nil
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	// ActionDeskCycle strictly rejects live deskMode.
	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "bad-live-desk",
		Name:            "Reject live",
		Kind:            KindFasedScript,
		CooldownSeconds: 30,
		Condition:       TriggerCondition{ScriptID: "always-long", Symbol: "BTC"},
		Action:          TriggerAction{Type: ActionDeskCycle, DeskMode: "live"},
	}); err == nil {
		t.Fatal("expected deskMode=live rejection")
	}

	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "script-desk",
		Name:            "Always Long Script",
		Kind:            KindFasedScript,
		CooldownSeconds: 15,
		Condition:       TriggerCondition{ScriptID: "always-long", Venue: "hyperliquid", Symbol: "BTC", Interval: "1h"},
		Action:          TriggerAction{Type: ActionDeskCycle, DeskMode: "shadow", Severity: SeverityWarn},
	}); err != nil {
		t.Fatalf("SaveTrigger script-desk: %v", err)
	}

	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "news-perm",
		Name:            "News Perm",
		Kind:            KindNews,
		CooldownSeconds: 15,
		Condition:       TriggerCondition{Category: worldintel.CategoryEnergy, MinUrgency: 0.5},
		Action:          TriggerAction{Type: ActionNotify, Severity: SeverityInfo},
	}); err != nil {
		t.Fatalf("SaveTrigger news-perm: %v", err)
	}

	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "cron-min-dedup",
		Name:            "Cron 1m Dedup",
		Kind:            KindCron,
		CooldownSeconds: 10,
		Condition:       TriggerCondition{CronExpr: "* * * * *"},
		Action:          TriggerAction{Type: ActionNotify, Severity: SeverityInfo},
	}); err != nil {
		t.Fatalf("SaveTrigger cron-min-dedup: %v", err)
	}

	sweep1, err := svc.EvaluateNow(t.Context())
	if err != nil || sweep1.Fired != 3 {
		t.Fatalf("expected 3 fired on initial sweep, got %+v err=%v", sweep1, err)
	}
	if len(deskCalls) != 1 || deskCalls[0] != "hyperliquid:BTC:1h:always-long:shadow" {
		t.Fatalf("unexpected OnDeskCycle invocation: %v", deskCalls)
	}
	var scriptRes *EvalResult
	for i := range sweep1.Results {
		if sweep1.Results[i].TriggerID == "script-desk" {
			scriptRes = &sweep1.Results[i]
			break
		}
	}
	if scriptRes == nil || scriptRes.Notification == nil || !strings.Contains(scriptRes.Notification.Body, "Desk cycle cyc-1") {
		t.Fatalf("expected Desk cycle summary in script-desk notification body, got %+v", scriptRes)
	}

	// Advance 25s (< 60s minute bucket, > 2x 10s cron cooldown, > 15s script/news cooldown) -> cron STILL duplicates!
	mu.Lock()
	now = now.Add(25 * time.Second)
	mu.Unlock()
	sweep2, err := svc.EvaluateNow(t.Context())
	if err != nil || sweep2.Fired != 0 {
		t.Fatalf("expected 0 fired (permanent fingerprint dedup for fasedscript, news, same-minute cron), got %+v err=%v", sweep2, err)
	}

	// Advance 5 minutes (> 2x cooldown) -> same candle/news STILL suppressed!
	mu.Lock()
	now = now.Add(5 * time.Minute)
	mu.Unlock()
	evScript, err := svc.EvaluateTrigger(t.Context(), "script-desk", false, false)
	if err != nil || evScript.Fired || evScript.Suppressed != "duplicate" {
		t.Fatalf("expected script-desk permanent duplicate suppression on same candle, got %+v err=%v", evScript, err)
	}
	evNews, err := svc.EvaluateTrigger(t.Context(), "news-perm", false, false)
	if err != nil || evNews.Fired {
		t.Fatalf("expected news-perm not to re-fire same news article after cooldown*2, got %+v err=%v", evNews, err)
	}
}

func TestAutomationPersistsLastFiredAtBeforeAgentPromptCompletion(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	candles := sampleRisingCandles(25, 90000)

	var (
		mu          sync.Mutex
		promptCalls int
		svc         *Service
	)
	var err error
	svc, err = NewService(Config{
		DataDir: dir,
		Now:     func() time.Time { return now },
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
		OnAgentPrompt: func(ctx context.Context, tr Trigger, _ string, _ bool) {
			mu.Lock()
			promptCalls++
			mu.Unlock()
			if ctx.Err() != nil {
				t.Fatalf("expected promptHook to receive non-cancelled detached context, got %v", ctx.Err())
			}
			// Verify runtime state was already durably updated in SQLite before promptHook returned.
			stored, err := svc.store.listTriggers(context.Background())
			if err != nil || len(stored) != 1 || stored[0].LastFiredAt == 0 || stored[0].FireCount != 1 {
				t.Fatalf("expected LastFiredAt & FireCount=1 persisted before promptHook, got %+v err=%v", stored, err)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "btc-agent-slow",
		Name:            "Slow Agent Prompt",
		Kind:            KindPrice,
		CooldownSeconds: 300,
		Condition:       TriggerCondition{Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", Comparator: CompAbove, Threshold: 100},
		Action:          TriggerAction{Type: ActionAgentPrompt, Severity: SeverityInfo},
	}); err != nil {
		t.Fatalf("SaveTrigger failed: %v", err)
	}

	// Simulate an EvaluateNow call with a short deadline that expires slightly after firing.
	sweepCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sw1, err := svc.EvaluateNow(sweepCtx)
	if err != nil || sw1.Fired != 1 {
		t.Fatalf("expected 1 trigger fired, got %+v err=%v", sw1, err)
	}
	// Trigger must not re-fire 15s later within cooldown.
	now = now.Add(15 * time.Second)
	sw2, err := svc.EvaluateNow(t.Context())
	if err != nil || sw2.Fired != 0 {
		t.Fatalf("expected 0 triggers on 15s follow-up sweep, got %+v err=%v", sw2, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if promptCalls != 1 {
		t.Fatalf("expected exactly 1 promptHook invocation, got %d", promptCalls)
	}
}

func TestKindDrawingBreachAndEvaluateMatching(t *testing.T) {
	now := time.Date(2026, 11, 25, 20, 0, 0, 0, time.UTC)
	drawings := map[string]marketdata.ChartDrawing{
		"hline-1": {
			ID:     "hline-1",
			Venue:  marketdata.VenueHyperliquid,
			Symbol: "BTC",
			Kind:   marketdata.DrawingKindHLine,
			P1:     marketdata.DrawingPoint{Time: 1000, Price: 95000},
			Label:  "Resistance",
		},
		"trend-1": {
			ID:     "trend-1",
			Venue:  marketdata.VenueHyperliquid,
			Symbol: "BTC",
			Kind:   marketdata.DrawingKindTrendline,
			P1:     marketdata.DrawingPoint{Time: 1000, Price: 90000},
			P2:     &marketdata.DrawingPoint{Time: 2000, Price: 92000},
		},
	}
	candles := []marketdata.Candle{
		{Time: 2000, Open: 91000, High: 92500, Low: 90500, Close: 91800, Volume: 100},
		{Time: 3000, Open: 91800, High: 95600, Low: 91700, Close: 95500, Volume: 150},
	}

	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return now },
		MarketReader: func(_ context.Context, _ string, _ string, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
		DrawingReader: func(_ context.Context, id string) (marketdata.ChartDrawing, bool, error) {
			d, ok := drawings[id]
			return d, ok, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// Reject pct_change_gte and missing drawingId.
	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:        "bad-pct",
		Name:      "Bad Pct",
		Kind:      KindDrawingBreach,
		Condition: TriggerCondition{Venue: "hyperliquid", Symbol: "BTC", DrawingID: "hline-1", Comparator: CompPctChangeGTE},
		Action:    TriggerAction{Type: ActionNotify},
	}); err == nil {
		t.Fatal("expected drawing_breach pct_change_gte rejection")
	}
	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:        "missing-did",
		Name:      "Missing DID",
		Kind:      KindDrawingBreach,
		Condition: TriggerCondition{Venue: "hyperliquid", Symbol: "BTC"},
		Action:    TriggerAction{Type: ActionNotify},
	}); err == nil {
		t.Fatal("expected drawing_breach empty drawingId rejection")
	}

	// Save sloped trendline breach (at t=2000 level=92000 close=91800 <= 92000; at t=3000 level=94000 close=95500 > 94000).
	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "trend-breach",
		Name:            "Trendline Cross",
		Kind:            KindDrawingBreach,
		CooldownSeconds: 60,
		Condition:       TriggerCondition{Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", DrawingID: "trend-1", Comparator: CompCrossAbove},
		Action:          TriggerAction{Type: ActionNotify, Severity: SeverityWarn},
	}); err != nil {
		t.Fatalf("SaveTrigger trend-breach: %v", err)
	}
	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:              "cron-skip",
		Name:            "Cron Should Be Filtered",
		Kind:            KindCron,
		CooldownSeconds: 60,
		Condition:       TriggerCondition{CronExpr: "* * * * *"},
		Action:          TriggerAction{Type: ActionNotify},
	}); err != nil {
		t.Fatalf("SaveTrigger cron: %v", err)
	}

	// EvaluateMatching only evaluates KindDrawingBreach, leaving KindCron untouched.
	sw, err := svc.EvaluateMatching(t.Context(), KindDrawingBreach)
	if err != nil {
		t.Fatalf("EvaluateMatching: %v", err)
	}
	if sw.Evaluated != 1 || sw.Fired != 1 {
		t.Fatalf("expected Evaluated=1 Fired=1 for KindDrawingBreach, got %+v", sw)
	}

	// Venue/symbol mismatch fails cleanly during test evaluation.
	drawings["hline-1"] = marketdata.ChartDrawing{
		ID:     "hline-1",
		Venue:  marketdata.VenueHyperliquid,
		Symbol: "ETH",
		Kind:   marketdata.DrawingKindHLine,
		P1:     marketdata.DrawingPoint{Time: 1000, Price: 3200},
	}
	if _, err := svc.SaveTrigger(t.Context(), TriggerInput{
		ID:        "mismatch-breach",
		Name:      "Mismatch",
		Kind:      KindDrawingBreach,
		Condition: TriggerCondition{Venue: "hyperliquid", Symbol: "BTC", DrawingID: "hline-1", Comparator: CompAbove},
		Action:    TriggerAction{Type: ActionNotify},
	}); err != nil {
		t.Fatalf("SaveTrigger mismatch: %v", err)
	}
	if _, err := svc.EvaluateTrigger(t.Context(), "mismatch-breach", true, true); err == nil {
		t.Fatal("expected venue/symbol mismatch error from EvaluateTrigger")
	}
}
