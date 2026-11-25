package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/scheduler"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

// Config parameterizes the event-driven automation and notification Service.
type Config struct {
	DataDir          string
	FixtureMode      bool
	Now              func() time.Time
	HTTPTransport    http.RoundTripper // Tests only.
	MarketReader     func(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error)
	IntelEvents      func(ctx context.Context, filter worldintel.EventFilter) ([]worldintel.IntelEvent, error)
	IntelPredictions func(ctx context.Context, filter worldintel.PredictionFilter) (worldintel.PredictionList, error)
	Scorer           func(ctx context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error)
	ScriptReader     func(ctx context.Context, scriptID string) (string, error)
	OnDeskCycle      func(ctx context.Context, tr Trigger, venue, symbol, interval, scriptID, mode string) (string, error)
	OnAgentPrompt    func(ctx context.Context, tr Trigger, prompt string, untrusted bool)
	OnNotify         func(ctx context.Context, n Notification) string
}

// Service manages event-driven triggers, pause/kill-switch controls, bounded
// notifications, encrypted webhook/telegram KV storage, and live SSE broadcasts.
type Service struct {
	store         *store
	bus           *Bus
	webhookClient *http.Client
	fixtureMode   bool
	now           func() time.Time
	cfg           Config
	notifyMu      sync.RWMutex
	onNotify      func(ctx context.Context, n Notification) string
	promptMu      sync.RWMutex
	onAgentPrompt func(ctx context.Context, tr Trigger, prompt string, untrusted bool)
}

// NewService opens isolated <dataDir>/automation/automation.sqlite storage.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	st, err := openStore(cfg.DataDir, nowFn)
	if err != nil {
		return nil, err
	}
	return &Service{
		store:         st,
		bus:           NewBus(),
		webhookClient: newWebhookHTTPClient(cfg.HTTPTransport),
		fixtureMode:   cfg.FixtureMode,
		now:           nowFn,
		cfg:           cfg,
		onNotify:      cfg.OnNotify,
		onAgentPrompt: cfg.OnAgentPrompt,
	}, nil
}

// SetNotifyHook registers or updates the downstream notification delivery hook.
func (s *Service) SetNotifyHook(fn func(ctx context.Context, n Notification) string) {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	s.onNotify = fn
}

// SetAgentPromptHook registers or updates the read-only Agent V2 prompt injection hook.
func (s *Service) SetAgentPromptHook(fn func(ctx context.Context, tr Trigger, prompt string, untrusted bool)) {
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	s.onAgentPrompt = fn
}

// Close closes the pub/sub bus and isolated SQLite database cleanly.
func (s *Service) Close() error {
	if s.bus != nil {
		s.bus.Close()
	}
	if s.store != nil {
		return s.store.close()
	}
	return nil
}

// Subscribe attaches a live automation stream listener.
func (s *Service) Subscribe() (<-chan StreamEvent, func()) {
	return s.bus.Subscribe()
}

// PublishStream broadcasts a raw StreamEvent to active subscribers.
func (s *Service) PublishStream(evt StreamEvent) {
	if evt.Timestamp == 0 {
		evt.Timestamp = s.now().UTC().UnixMilli()
	}
	s.bus.Publish(evt)
}

// GetState returns the persistent pause & kill-switch control state.
func (s *Service) GetState(ctx context.Context) (ControlState, error) {
	return s.store.getState(ctx)
}

// SetState updates persistent pause or kill-switch state and broadcasts/alerts on kill switch.
func (s *Service) SetState(ctx context.Context, action, reason, actor string) (ControlState, error) {
	state, err := s.store.setState(ctx, action, reason, actor)
	if err != nil {
		return ControlState{}, err
	}
	nowMs := s.now().UTC().UnixMilli()
	s.bus.Publish(StreamEvent{
		Type:      "state_changed",
		Timestamp: nowMs,
		State:     &state,
	})
	if strings.EqualFold(strings.TrimSpace(action), StateActionKill) {
		ntf, rErr := s.RecordNotification(ctx, Notification{
			Kind:      "kill_switch",
			Severity:  SeverityCritical,
			Title:     "Kill switch activated",
			Body:      fmt.Sprintf("All automated triggers paused by %s. Reason: %s", state.UpdatedBy, state.KillSwitchReason),
			Urgency:   1.0,
			Untrusted: false,
		})
		if rErr == nil {
			s.dispatchOutbound(ctx, ntf, true, true)
		}
	}
	return state, nil
}

// ListTriggers lists configured event-driven triggers.
func (s *Service) ListTriggers(ctx context.Context) ([]Trigger, error) {
	return s.store.listTriggers(ctx)
}

// SaveTrigger validates and upserts an event-driven trigger.
func (s *Service) SaveTrigger(ctx context.Context, in TriggerInput) (Trigger, error) {
	return s.store.saveTrigger(ctx, in)
}

// DeleteTrigger deletes a configured trigger by ID.
func (s *Service) DeleteTrigger(ctx context.Context, id string) (bool, error) {
	return s.store.deleteTrigger(ctx, id)
}

// ListNotifications returns filtered recent alerts plus unread count.
func (s *Service) ListNotifications(ctx context.Context, filter NotificationFilter) (NotificationList, error) {
	return s.store.listNotifications(ctx, filter)
}

// MarkNotificationsRead marks one or all unread notifications as read.
func (s *Service) MarkNotificationsRead(ctx context.Context, id string, all bool) (int, error) {
	return s.store.markNotificationsRead(ctx, id, all)
}

// RecordNotification persists an alert and broadcasts it to SSE listeners.
func (s *Service) RecordNotification(ctx context.Context, n Notification) (Notification, error) {
	saved, err := s.store.insertNotification(ctx, n)
	if err != nil {
		return Notification{}, err
	}
	s.bus.Publish(StreamEvent{
		Type:         "notification",
		Timestamp:    s.now().UTC().UnixMilli(),
		Notification: &saved,
	})
	return saved, nil
}

// NotifyApproval records a guarded approval notification and dispatches outbound Telegram alerts when enabled.
func (s *Service) NotifyApproval(ctx context.Context, title, body, metadataJSON string) (Notification, error) {
	ntf, err := s.RecordNotification(ctx, Notification{
		Kind:         "approval",
		Severity:     SeverityWarn,
		Title:        title,
		Body:         body,
		Urgency:      0.90,
		Untrusted:    false,
		MetadataJSON: metadataJSON,
	})
	if err != nil {
		return Notification{}, err
	}
	if status := s.dispatchOutbound(ctx, ntf, true, false); status != "" {
		ntf.DeliveryStatus = status
		_ = s.store.updateNotificationDelivery(ctx, ntf.ID, status)
	}
	return ntf, nil
}

// GetWebhookConfig returns the masked outbound webhook status and settings.
func (s *Service) GetWebhookConfig(ctx context.Context) (WebhookConfig, error) {
	var cfg persistedWebhookConfig
	_, _ = s.GetJSONValue(ctx, webhookConfigKey, &cfg)
	secret, hasSecret, _ := s.GetSealedValue(ctx, webhookSecretKey)
	return WebhookConfig{
		Enabled:      cfg.Enabled,
		Endpoint:     cfg.Endpoint,
		HasSecret:    hasSecret && secret != "",
		SecretMasked: maskSecret(secret),
		LastStatus:   cfg.LastStatus,
		LastError:    cfg.LastError,
		LastSentAt:   cfg.LastSentAt,
	}, nil
}

// SaveWebhookConfig updates outbound HTTPS webhook endpoint and encrypted signing secret.
func (s *Service) SaveWebhookConfig(ctx context.Context, in WebhookInput) (WebhookConfig, error) {
	var cur persistedWebhookConfig
	_, _ = s.GetJSONValue(ctx, webhookConfigKey, &cur)

	endpoint := strings.TrimSpace(in.Endpoint)
	if endpoint != "" || in.Enabled {
		validated, err := ValidateWebhookEndpoint(endpoint)
		if err != nil {
			return WebhookConfig{}, err
		}
		endpoint = validated
	}
	cur.Enabled = in.Enabled
	cur.Endpoint = endpoint
	if err := s.SetJSONValue(ctx, webhookConfigKey, cur); err != nil {
		return WebhookConfig{}, err
	}
	if in.ClearSecret {
		if err := s.DeleteSealedValue(ctx, webhookSecretKey); err != nil {
			return WebhookConfig{}, err
		}
	} else if trimmed := strings.TrimSpace(in.Secret); trimmed != "" {
		if len(trimmed) > 512 {
			return WebhookConfig{}, errors.New("webhook secret exceeds 512 characters")
		}
		if err := s.SetSealedValue(ctx, webhookSecretKey, trimmed); err != nil {
			return WebhookConfig{}, err
		}
	}
	return s.GetWebhookConfig(ctx)
}

// GetSealedValue retrieves an AES-256-GCM encrypted secret value.
func (s *Service) GetSealedValue(ctx context.Context, key string) (string, bool, error) {
	return s.store.getSealedValue(ctx, key)
}

// SetSealedValue seals an encrypted secret value into isolated storage.
func (s *Service) SetSealedValue(ctx context.Context, key, plaintext string) error {
	return s.store.setSealedValue(ctx, key, plaintext)
}

// DeleteSealedValue removes a stored encrypted key-value record.
func (s *Service) DeleteSealedValue(ctx context.Context, key string) error {
	return s.store.deleteSealedValue(ctx, key)
}

// GetJSONValue decodes a sealed JSON blob into dst.
func (s *Service) GetJSONValue(ctx context.Context, key string, dst any) (bool, error) {
	raw, ok, err := s.store.getSealedValue(ctx, key)
	if err != nil || !ok || raw == "" {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return false, err
	}
	return true, nil
}

// SetJSONValue marshals src and stores it sealed into kv_secrets.
func (s *Service) SetJSONValue(ctx context.Context, key string, src any) error {
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return s.store.setSealedValue(ctx, key, string(raw))
}

// EvaluateTrigger evaluates one rule by ID. When dryRun is true, runtime counters
// and outbound notifications are skipped; when ignoreCooldown is false, cooldown
// and identical fingerprints suppress duplicate firings.
func (s *Service) EvaluateTrigger(ctx context.Context, id string, dryRun, ignoreCooldown bool) (EvalResult, error) {
	tr, found, err := s.store.getTrigger(ctx, id)
	if err != nil {
		return EvalResult{}, err
	}
	if !found {
		return EvalResult{}, os.ErrNotExist
	}
	return s.evaluateTrigger(ctx, tr, dryRun, ignoreCooldown)
}

// EvaluateNow sweeps all enabled triggers unless paused or kill-switched.
func (s *Service) EvaluateNow(ctx context.Context) (SweepResult, error) {
	state, err := s.store.getState(ctx)
	if err != nil {
		return SweepResult{}, err
	}
	if state.KillSwitchActive {
		return SweepResult{Skipped: "kill_switch", Results: []EvalResult{}}, nil
	}
	if state.Paused {
		return SweepResult{Skipped: "paused", Results: []EvalResult{}}, nil
	}

	triggers, err := s.store.listTriggers(ctx)
	if err != nil {
		return SweepResult{}, err
	}
	sweep := SweepResult{Results: make([]EvalResult, 0, len(triggers))}
	for _, tr := range triggers {
		if !tr.Enabled {
			continue
		}
		res, eErr := s.evaluateTrigger(ctx, tr, false, false)
		sweep.Evaluated++
		if eErr != nil {
			sweep.Results = append(sweep.Results, EvalResult{
				TriggerID: tr.ID,
				Summary:   eErr.Error(),
			})
			continue
		}
		if res.Fired {
			sweep.Fired++
		}
		sweep.Results = append(sweep.Results, res)
	}
	return sweep, nil
}

type conditionEval struct {
	matched     bool
	metric      float64
	fingerprint string
	title       string
	body        string
	ticker      string
	venue       string
	urgency     float64
	untrusted   bool
	meta        map[string]any
}

func (s *Service) evaluateTrigger(ctx context.Context, tr Trigger, dryRun, ignoreCooldown bool) (EvalResult, error) {
	ev, err := s.evaluateCondition(ctx, tr, dryRun, ignoreCooldown)
	if err != nil {
		return EvalResult{TriggerID: tr.ID, Summary: err.Error()}, err
	}
	res := EvalResult{
		TriggerID:   tr.ID,
		Matched:     ev.matched,
		MetricValue: ev.metric,
		Summary:     ev.body,
	}
	if !ev.matched {
		res.Summary = fmt.Sprintf("%s condition not met", tr.Name)
		if !dryRun {
			tr.LastEvaluatedAt = s.now().UTC().Unix()
			tr.LastValue = ev.metric
			_ = s.store.updateTriggerRuntime(ctx, tr)
		}
		return res, nil
	}

	nowSec := s.now().UTC().Unix()
	if dryRun {
		return res, nil
	}

	tr.LastEvaluatedAt = nowSec
	tr.LastValue = ev.metric

	if !ignoreCooldown {
		if tr.LastFiredAt > 0 && nowSec-tr.LastFiredAt < int64(tr.CooldownSeconds) {
			res.Suppressed = "cooldown"
			_ = s.store.updateTriggerRuntime(ctx, tr)
			return res, nil
		}
		if ev.fingerprint != "" && ev.fingerprint == tr.LastFingerprint && tr.LastFiredAt > 0 {
			permanentDedup := tr.Kind == KindNews || tr.Kind == KindCron || tr.Kind == KindFasedScript
			if permanentDedup || nowSec-tr.LastFiredAt < int64(tr.CooldownSeconds)*2 {
				res.Suppressed = "duplicate"
				_ = s.store.updateTriggerRuntime(ctx, tr)
				return res, nil
			}
		}
	}

	title := ev.title
	body := ev.body
	urgency := ev.urgency
	if tr.Action.Type == ActionJevTriage && s.cfg.Scorer != nil {
		if scoredSummary, scoredUrgency := s.scoreWithJev(ctx, title+" — "+body); scoredSummary != "" {
			body = body + "\n" + scoredSummary
			if scoredUrgency > urgency {
				urgency = scoredUrgency
			}
		}
	}
	if tr.Action.Type == ActionDeskCycle && s.cfg.OnDeskCycle != nil {
		venue := tr.Condition.Venue
		if venue == "" {
			venue = marketdata.VenueHyperliquid
		}
		symbol := tr.Condition.Symbol
		if symbol == "" {
			symbol = tr.Condition.Ticker
		}
		if symbol == "" {
			symbol = ev.ticker
		}
		if symbol == "" {
			symbol = "BTC"
		}
		interval := tr.Condition.Interval
		if interval == "" {
			interval = "1h"
		}
		scriptID := tr.Action.ScriptID
		if scriptID == "" {
			scriptID = tr.Condition.ScriptID
		}
		if scriptID == "" {
			scriptID = "ema-rsi-trend"
		}
		mode := "shadow"
		if strings.EqualFold(tr.Action.DeskMode, "paper") {
			mode = "paper"
		}
		if summary, dErr := s.cfg.OnDeskCycle(ctx, tr, venue, symbol, interval, scriptID, mode); dErr != nil {
			body = body + "\nDesk cycle error: " + dErr.Error()
		} else if strings.TrimSpace(summary) != "" {
			body = body + "\n" + strings.TrimSpace(summary)
		}
	}

	metaBytes, _ := json.Marshal(ev.meta)
	ntf, err := s.RecordNotification(ctx, Notification{
		TriggerID:    tr.ID,
		TriggerName:  tr.Name,
		Kind:         tr.Kind,
		Severity:     tr.Action.Severity,
		Title:        title,
		Body:         body,
		Ticker:       ev.ticker,
		Venue:        ev.venue,
		Urgency:      urgency,
		Untrusted:    ev.untrusted,
		MetadataJSON: string(metaBytes),
	})
	if err != nil {
		return res, err
	}

	status := s.dispatchOutbound(ctx, ntf, tr.Action.SendTelegram, tr.Action.SendWebhook)
	if status != "" {
		ntf.DeliveryStatus = status
		_ = s.store.updateNotificationDelivery(ctx, ntf.ID, status)
	}

	// Persist LastFiredAt/FireCount before running synchronous LLM triage so a
	// slow or timed-out prompt cannot leave the trigger un-updated and re-fire every sweep.
	tr.LastFiredAt = nowSec
	tr.LastFingerprint = ev.fingerprint
	tr.FireCount++
	if err := s.store.updateTriggerRuntime(ctx, tr); err != nil {
		return res, err
	}

	if tr.Action.Type == ActionAgentPrompt || tr.Action.Type == ActionPaperAlert {
		prompt := s.buildAgentPrompt(tr, ntf)
		s.promptMu.RLock()
		promptHook := s.onAgentPrompt
		s.promptMu.RUnlock()
		if promptHook != nil {
			promptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			promptHook(promptCtx, tr, prompt, ev.untrusted)
			cancel()
		}
	}

	s.bus.Publish(StreamEvent{
		Type:      "trigger_fired",
		Timestamp: s.now().UTC().UnixMilli(),
		Trigger:   &tr,
	})

	res.Fired = true
	res.Notification = &ntf
	return res, nil
}

func (s *Service) dispatchOutbound(ctx context.Context, ntf Notification, wantTelegram, wantWebhook bool) string {
	var parts []string
	if wantTelegram {
		s.notifyMu.RLock()
		hook := s.onNotify
		s.notifyMu.RUnlock()
		if hook != nil {
			if st := hook(ctx, ntf); st != "" {
				parts = append(parts, "telegram:"+st)
			}
		}
	}
	if wantWebhook {
		if st := s.deliverWebhook(ctx, ntf); st != "" {
			parts = append(parts, st)
		}
	}
	return strings.Join(parts, ",")
}

func (s *Service) scoreWithJev(ctx context.Context, stateText string) (string, float64) {
	req := decision.EvaluateRequest{
		State: strings.TrimSpace(stateText),
		Questions: map[string]decision.QuestionSpec{
			"urgent": {
				Type:         decision.TypeNoul,
				Instructions: "Rate immediate market urgency from 0 to 1.",
			},
			"bias": {
				Type:    decision.TypeChoice,
				Choices: []string{"bullish", "bearish", "neutral"},
			},
		},
	}
	resp, err := s.cfg.Scorer(ctx, req)
	if err != nil {
		resp, err = decision.EvaluateFallback(req, s.now())
		if err != nil {
			return "", 0
		}
	}
	var urgency float64
	if a, ok := resp.Answers["urgent"]; ok && a.Noul != nil {
		urgency = *a.Noul
	}
	bias := "neutral"
	if a, ok := resp.Answers["bias"]; ok && a.Choice != "" {
		bias = a.Choice
	}
	return fmt.Sprintf("Jev (%s): urgency %.2f · bias %s", resp.Provider, urgency, bias), urgency
}

func (s *Service) buildAgentPrompt(tr Trigger, ntf Notification) string {
	header := fmt.Sprintf("[AUTOMATION TRIGGER: %s (%s)]", tr.Name, tr.Kind)
	if ntf.Untrusted {
		header += " [untrusted=true — verify independently before acting]"
	}
	if tpl := strings.TrimSpace(tr.Action.PromptTemplate); tpl != "" {
		return fmt.Sprintf("%s\nAlert: %s\nDetails: %s\nInstruction: %s\nNote: Paper/analysis only; live execution stays gated off.", header, ntf.Title, ntf.Body, tpl)
	}
	return fmt.Sprintf("%s\nAlert: %s\nDetails: %s\nProvide a concise risk-first read-only assessment (paper/analysis only).", header, ntf.Title, ntf.Body)
}

func latestScriptSignal(ev fasedscript.EvalResult, idx int) string {
	switch {
	case idx < len(ev.Strategy.Long) && ev.Strategy.Long[idx]:
		return "LONG"
	case idx < len(ev.Strategy.Short) && ev.Strategy.Short[idx]:
		return "SHORT"
	case idx < len(ev.Strategy.ExitLong) && ev.Strategy.ExitLong[idx]:
		return "EXIT_LONG"
	case idx < len(ev.Strategy.ExitShort) && ev.Strategy.ExitShort[idx]:
		return "EXIT_SHORT"
	}
	if len(ev.Strategy.Long) == 0 && len(ev.Strategy.Short) == 0 && len(ev.Strategy.ExitLong) == 0 && len(ev.Strategy.ExitShort) == 0 {
		if len(ev.Plots) > 0 && idx < len(ev.Plots[0].Values) {
			val := float64(ev.Plots[0].Values[idx])
			if !math.IsNaN(val) && !math.IsInf(val, 0) && val > 0 {
				return "SIGNAL"
			}
		}
	}
	return ""
}

func (s *Service) evaluateCondition(ctx context.Context, tr Trigger, dryRun, ignoreCooldown bool) (conditionEval, error) {
	cond := tr.Condition
	switch tr.Kind {
	case KindPrice, KindIndicator:
		if s.cfg.MarketReader == nil {
			return conditionEval{}, errors.New("market reader unavailable")
		}
		candles, err := s.cfg.MarketReader(ctx, cond.Venue, cond.Symbol, cond.Interval, 64)
		if err != nil {
			return conditionEval{}, err
		}
		if len(candles) < 2 {
			return conditionEval{}, errors.New("insufficient candle history")
		}
		latest := candles[len(candles)-1]
		prev := candles[len(candles)-2]

		var curVal, prevVal float64
		var metricLabel string
		var ok bool
		if tr.Kind == KindPrice {
			curVal = latest.Close
			prevVal = prev.Close
			metricLabel = fmt.Sprintf("Price %.4f", curVal)
			ok = true
		} else {
			switch cond.Indicator {
			case IndRSI14:
				curVal, prevVal, ok = ComputeRSI14(candles)
				metricLabel = fmt.Sprintf("RSI(14) %.2f", curVal)
			case IndEMACross:
				curVal, prevVal, ok = ComputeEMACross(candles, 9, 21)
				metricLabel = fmt.Sprintf("EMA(9-21) %.4f", curVal)
			case IndVWAPCross:
				curVal, prevVal, ok = ComputeVWAPDelta(candles)
				metricLabel = fmt.Sprintf("Close-VWAP %.4f", curVal)
			case IndVolumeSpike:
				curVal, prevVal, ok = ComputeVolumeSpike(candles, 20)
				metricLabel = fmt.Sprintf("VolSpike %.2fx", curVal)
			case IndCVDDelta:
				curVal, prevVal, ok = ComputeCVDDelta(candles, 10)
				metricLabel = fmt.Sprintf("CVD %.2f", curVal)
			}
		}
		if !ok {
			return conditionEval{}, errors.New("insufficient data for indicator")
		}
		matched := CompareMetric(cond.Comparator, curVal, prevVal, cond.Threshold)
		fp := hashFingerprint(fmt.Sprintf("%s:%s:%s:%s:%d", tr.Kind, cond.Venue, cond.Symbol, cond.Indicator, latest.Time))
		return conditionEval{
			matched:     matched,
			metric:      curVal,
			fingerprint: fp,
			title:       fmt.Sprintf("%s: %s %s %s", tr.Name, cond.Venue, cond.Symbol, metricLabel),
			body:        fmt.Sprintf("%s %s threshold %.4f (prev %.4f, current %.4f)", metricLabel, cond.Comparator, cond.Threshold, prevVal, curVal),
			ticker:      cond.Symbol,
			venue:       cond.Venue,
			urgency:     severityUrgency(tr.Action.Severity),
			untrusted:   false,
			meta: map[string]any{
				"venue":      cond.Venue,
				"symbol":     cond.Symbol,
				"interval":   cond.Interval,
				"indicator":  cond.Indicator,
				"comparator": cond.Comparator,
				"threshold":  cond.Threshold,
				"current":    curVal,
				"previous":   prevVal,
				"candleTime": latest.Time,
			},
		}, nil

	case KindNews:
		if s.cfg.IntelEvents == nil {
			return conditionEval{}, errors.New("worldintel event reader unavailable")
		}
		events, err := s.cfg.IntelEvents(ctx, worldintel.EventFilter{
			Category:   cond.Category,
			Region:     cond.Region,
			Ticker:     cond.Ticker,
			Query:      cond.Query,
			MinUrgency: cond.MinUrgency,
			Limit:      25,
		})
		if err != nil {
			return conditionEval{}, err
		}
		var best *worldintel.IntelEvent
		for i := range events {
			ev := events[i]
			if tr.LastFiredAt > 0 && !dryRun && !ignoreCooldown && ev.PublishedAt > 0 && ev.PublishedAt <= tr.LastFiredAt {
				continue
			}
			if cond.Sentiment != "" && !strings.EqualFold(ev.Sentiment, cond.Sentiment) {
				continue
			}
			if best == nil || ev.Urgency > best.Urgency {
				copyEv := ev
				best = &copyEv
			}
		}
		if best == nil {
			return conditionEval{matched: false}, nil
		}
		ticker := cond.Ticker
		if ticker == "" && len(best.Tickers) > 0 {
			ticker = best.Tickers[0]
		}
		return conditionEval{
			matched:     true,
			metric:      best.Urgency,
			fingerprint: hashFingerprint("news:" + best.ID),
			title:       fmt.Sprintf("%s: %s", tr.Name, best.Title),
			body:        fmt.Sprintf("Source: %s · Region: %s · Urgency: %.2f · Sentiment: %s (untrusted)", best.SourceName, best.Region, best.Urgency, best.Sentiment),
			ticker:      ticker,
			venue:       "worldintel",
			urgency:     best.Urgency,
			untrusted:   true,
			meta: map[string]any{
				"eventId":   best.ID,
				"category":  best.Category,
				"region":    best.Region,
				"sentiment": best.Sentiment,
				"url":       best.URL,
			},
		}, nil

	case KindPrediction:
		if s.cfg.IntelPredictions == nil {
			return conditionEval{}, errors.New("worldintel prediction reader unavailable")
		}
		list, err := s.cfg.IntelPredictions(ctx, worldintel.PredictionFilter{
			Category: cond.Category,
			Query:    cond.Query,
			Limit:    50,
		})
		if err != nil {
			return conditionEval{}, err
		}
		var bestArb *worldintel.PredictionArbitrage
		for i := range list.Arbitrage {
			arb := list.Arbitrage[i]
			if arb.SpreadPct+1e-9 < cond.MinSpreadPct {
				continue
			}
			if bestArb == nil || arb.SpreadPct > bestArb.SpreadPct {
				copyArb := arb
				bestArb = &copyArb
			}
		}
		if bestArb == nil {
			return conditionEval{matched: false}, nil
		}
		ticker := ""
		if len(bestArb.Tickers) > 0 {
			ticker = bestArb.Tickers[0]
		}
		return conditionEval{
			matched:     true,
			metric:      bestArb.SpreadPct,
			fingerprint: hashFingerprint(fmt.Sprintf("pred:%s:%.1f", bestArb.ID, math.Round(bestArb.SpreadPct*2))),
			title:       fmt.Sprintf("%s: %.1f%% Polymarket/Kalshi spread", tr.Name, bestArb.SpreadPct),
			body:        fmt.Sprintf("%s · Polymarket %.0f%% vs Kalshi %.0f%%", bestArb.Topic, bestArb.PolymarketProb*100, bestArb.KalshiProb*100),
			ticker:      ticker,
			venue:       "prediction",
			urgency:     math.Min(1.0, math.Max(0.55, bestArb.SpreadPct/25.0)),
			untrusted:   true,
			meta: map[string]any{
				"arbitrageId":    bestArb.ID,
				"polymarketProb": bestArb.PolymarketProb,
				"kalshiProb":     bestArb.KalshiProb,
				"spreadPct":      bestArb.SpreadPct,
			},
		}, nil

	case KindCron:
		rule, err := scheduler.ParseCron(cond.CronExpr)
		if err != nil {
			return conditionEval{}, err
		}
		nowUTC := s.now().UTC()
		if !rule.Matches(nowUTC) {
			return conditionEval{matched: false}, nil
		}
		minuteBucket := nowUTC.Unix() / 60
		return conditionEval{
			matched:     true,
			metric:      float64(minuteBucket),
			fingerprint: hashFingerprint(fmt.Sprintf("cron:%s:%d", tr.ID, minuteBucket)),
			title:       fmt.Sprintf("%s (%s)", tr.Name, cond.CronExpr),
			body:        fmt.Sprintf("Scheduled cron trigger matched UTC %s", nowUTC.Format("2006-01-02 15:04")),
			venue:       "cron",
			urgency:     severityUrgency(tr.Action.Severity),
			untrusted:   false,
			meta: map[string]any{
				"cronExpr": cond.CronExpr,
				"utc":      nowUTC.Format(time.RFC3339),
			},
		}, nil

	case KindFasedScript:
		if s.cfg.MarketReader == nil {
			return conditionEval{}, errors.New("market reader unavailable")
		}
		if s.cfg.ScriptReader == nil {
			return conditionEval{}, errors.New("script reader unavailable")
		}
		candles, err := s.cfg.MarketReader(ctx, cond.Venue, cond.Symbol, cond.Interval, 160)
		if err != nil {
			return conditionEval{}, err
		}
		if len(candles) < 30 {
			return conditionEval{}, errors.New("insufficient candle history for script")
		}
		src, err := s.cfg.ScriptReader(ctx, cond.ScriptID)
		if err != nil {
			return conditionEval{}, err
		}
		prog, err := fasedscript.Compile(src)
		if err != nil {
			return conditionEval{}, fmt.Errorf("compile script %q: %w", cond.ScriptID, err)
		}
		ev, err := prog.Eval(candles, nil)
		if err != nil {
			return conditionEval{}, fmt.Errorf("evaluate script %q: %w", cond.ScriptID, err)
		}
		idx := len(candles) - 1
		latest := candles[idx]
		signal := latestScriptSignal(ev, idx)
		if signal == "" {
			return conditionEval{matched: false, metric: latest.Close}, nil
		}
		fp := hashFingerprint(fmt.Sprintf("fasedscript:%s:%s:%s:%s:%s:%d", cond.ScriptID, cond.Venue, cond.Symbol, cond.Interval, signal, latest.Time))
		return conditionEval{
			matched:     true,
			metric:      latest.Close,
			fingerprint: fp,
			title:       fmt.Sprintf("%s: %s %s %s (%s)", tr.Name, cond.ScriptID, cond.Symbol, cond.Interval, signal),
			body:        fmt.Sprintf("FasedScript %s fired %s on %s %s (%s) @ %.4f", cond.ScriptID, signal, cond.Venue, cond.Symbol, cond.Interval, latest.Close),
			ticker:      cond.Symbol,
			venue:       cond.Venue,
			urgency:     severityUrgency(tr.Action.Severity),
			untrusted:   false,
			meta: map[string]any{
				"scriptId":   cond.ScriptID,
				"signal":     signal,
				"candleTime": latest.Time,
				"venue":      cond.Venue,
				"symbol":     cond.Symbol,
				"interval":   cond.Interval,
			},
		}, nil

	default:
		return conditionEval{}, fmt.Errorf("unsupported trigger kind %q", tr.Kind)
	}
}

func severityUrgency(severity string) float64 {
	switch severity {
	case SeverityCritical:
		return 0.95
	case SeverityWarn:
		return 0.75
	default:
		return 0.45
	}
}

func hashFingerprint(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
