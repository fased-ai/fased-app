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
