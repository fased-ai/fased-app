package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/automation"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/trade"
)

var errGlobalKillSwitchActive = errors.New("global kill switch is active")

func init() {
	tradeGlobalKillActiveHook = func(ctx context.Context, s *Store) bool {
		return s.isGlobalKillActive(ctx)
	}
}

func (s *Store) tradeService() (*trade.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.tradeMu.Lock()
	if s.tradeSvc != nil {
		svc := s.tradeSvc
		s.tradeMu.Unlock()
		return svc, nil
	}
	svc, err := trade.NewService(trade.Config{
		DataDir:     s.dir,
		FixtureMode: s.providerFixture,
		Now:         s.now,
		MarketReader: func(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error) {
			md, mErr := s.marketDataService()
			if mErr != nil {
				return nil, mErr
			}
			series, cErr := md.GetCandles(ctx, venue, symbol, interval, marketdata.TransformOptions{Style: marketdata.StyleStandard}, limit)
			if cErr != nil {
				return nil, cErr
			}
			return series.Candles, nil
		},
		ExternalKillActive: s.isExternalKillActiveForTrade,
		ExecutionProfile: func() string {
			if rt := s.stage4Runtime(); rt != nil {
				return string(rt.profile)
			}
			return "off"
		},
		LiveExecutor:    s.executeLiveTradeRoute,
		ScriptEvaluator: s.evaluateFasedScriptBot,
		AgentEvaluator:  s.evaluateAIAgentBot,
		OnAlert: func(ctx context.Context, severity, title, body string) {
			autoSvc, aErr := s.automationService()
			if aErr != nil {
				return
			}
			urgency := 0.55
			if severity == "critical" {
				urgency = 0.95
			} else if severity == "warn" {
				urgency = 0.75
			}
			_, _ = autoSvc.RecordNotification(ctx, automation.Notification{
				Kind:      "trade",
				Severity:  severity,
				Title:     title,
				Body:      body,
				Urgency:   urgency,
				Untrusted: false,
			})
		},
	})
	if err != nil {
		s.tradeMu.Unlock()
		return nil, err
	}
	s.tradeSvc = svc
	s.tradeMu.Unlock()
	return svc, nil
}

func (s *Store) loadedOrExistingTradeService() *trade.Service {
	if s == nil {
		return nil
	}
	s.tradeMu.Lock()
	initialized := s.tradeSvc != nil
	existing := s.tradeSvc
	s.tradeMu.Unlock()
	if initialized {
		return existing
	}
	if s.dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.dir, "trade", "trade.sqlite")); err != nil {
		return nil
	}
	svc, err := s.tradeService()
	if err != nil {
		return nil
	}
	return svc
}

func (s *Store) loadedOrExistingAutomationService() *automation.Service {
	if s == nil {
		return nil
	}
	s.automationMu.Lock()
	initialized := s.automationSvc != nil
	existing := s.automationSvc
	s.automationMu.Unlock()
	if initialized {
		return existing
	}
	if s.dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.dir, "automation", "automation.sqlite")); err != nil {
		return nil
	}
	svc, err := s.automationService()
	if err != nil {
		return nil
	}
	return svc
}

func (s *Store) isExternalKillActiveForTrade(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if autoSvc := s.loadedOrExistingAutomationService(); autoSvc != nil {
		if st, err := autoSvc.GetState(ctx); err == nil && st.KillSwitchActive {
			return true
		}
	}
	if ds := s.loadedOrExistingDeskService(); ds != nil {
		if snap, err := ds.GetSnapshot(ctx); err == nil && snap.Control.KillSwitchActive {
			return true
		}
	}
	return false
}

func (s *Store) isGlobalKillActive(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.isExternalKillActiveForTrade(ctx) {
		return true
	}
	if ts := s.loadedOrExistingTradeService(); ts != nil {
		if snap, err := ts.GetSnapshot(ctx); err == nil && snap.KillSwitchActive {
			return true
		}
	}
	return false
}

func (s *Store) syncTradeKill(ctx context.Context, active bool, reason, actor string) {
	if s == nil {
		return
	}
	if !active && s.loadedOrExistingTradeService() == nil {
		return
	}
	ts, err := s.tradeService()
	if err != nil || ts == nil {
		return
	}
	if active {
		_ = ts.SyncExternalKill(ctx, reason, actor)
		return
	}
	_, _ = ts.SetKillSwitch(ctx, false, reason, actor)
}

func (s *Store) syncGlobalKill(ctx context.Context, active bool, reason, actor string) (trade.KillState, error) {
	ts, err := s.tradeService()
	if err != nil {
		return trade.KillState{}, err
	}
	if active {
		s.syncDeskKill(ctx, reason, actor)
		if autoSvc, aErr := s.automationService(); aErr == nil {
			_, _ = autoSvc.SetState(ctx, automation.StateActionKill, reason, actor)
		}
		return ts.SetKillSwitch(ctx, true, reason, actor)
	}
	if autoSvc := s.loadedOrExistingAutomationService(); autoSvc != nil {
		_, _ = autoSvc.SetState(ctx, automation.StateActionUnkill, reason, actor)
	}
	if ds := s.loadedOrExistingDeskService(); ds != nil {
		_, _ = ds.SetControl(ctx, "unkill", "CONFIRM", reason, actor)
	}
	return ts.SetKillSwitch(ctx, false, reason, actor)
}

func (h *HTTPHandler) tradeAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "trade", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.tradeService()
	if err != nil {
		writeError(w, 500, "trade engine unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/trade/"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	switch {
	case rel == "state":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		snap, err := svc.GetSnapshot(ctx)
		if err != nil {
			writeError(w, 500, "trade state unavailable")
			return
		}
		writeJSON(w, 200, snap)

	case rel == "orders":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var req trade.OrderRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &req) {
			return
		}
		orderCtx := ctx
		if (strings.HasPrefix(req.AccountID, "@wallet:") || strings.HasPrefix(req.AccountID, "wallet:")) &&
			req.Mode != trade.ModeBot && req.Mode != trade.ModeAIAgent {
			wid := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(req.AccountID), "@wallet:"), "wallet:")
			var ok bool
			orderCtx, ok = h.requireStepUpCtx(w, r, token, "execute", wid)
			if !ok {
				return
			}
		}
		plan, err := svc.SubmitOrder(orderCtx, req)
		if err != nil {
			if errors.Is(err, trade.ErrKillSwitchActive) || errors.Is(err, trade.ErrRiskRejected) {
				writeError(w, 403, err.Error())
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, plan)

	case strings.HasPrefix(rel, "orders/") && strings.HasSuffix(rel, "/cancel"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(rel, "orders/"), "/cancel")
		plan, err := svc.CancelPlan(ctx, id)
		if err != nil {
			if errors.Is(err, trade.ErrPlanNotFound) {
				writeError(w, 404, "order plan not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, plan)

	case rel == "evaluate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		sum, err := svc.EvaluateActive(ctx)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, sum)

	case rel == "policy":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in trade.RiskPolicy
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		saved, err := svc.UpdatePolicy(ctx, in)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, saved)

	case rel == "kill":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			Active bool   `json:"active"`
			Reason string `json:"reason"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		ks, err := h.store.syncGlobalKill(ctx, in.Active, in.Reason, "owner")
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, ks)

	case rel == "accounts":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		accounts := h.store.tradeAccountsView(ctx)
		writeJSON(w, 200, map[string]any{"accounts": accounts, "count": len(accounts)})

	case rel == "bots":
		switch r.Method {
		case http.MethodGet:
			bots, err := svc.ListBots(ctx)
			if err != nil {
				writeError(w, 500, "trade bots unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"bots": bots, "count": len(bots)})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in trade.CreateBotRequest
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			bot, err := svc.CreateBot(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, bot)
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "bots/"):
		rest := strings.TrimPrefix(rel, "bots/")
		if strings.Contains(rest, "/") {
			parts := strings.SplitN(rest, "/", 2)
			if len(parts) != 2 || parts[0] == "" {
				writeError(w, 404, "unknown bot route")
				return
			}
			if r.Method != http.MethodPost {
				writeError(w, 405, "method not allowed")
				return
			}
			if !h.v2Mutation(w, r, token, false) {
				return
			}
			bot, err := svc.SetBotStatus(ctx, parts[0], parts[1])
			if err != nil {
				if errors.Is(err, trade.ErrBotNotFound) {
					writeError(w, 404, "trade bot not found")
					return
				}
				if errors.Is(err, trade.ErrKillSwitchActive) {
					writeError(w, 403, err.Error())
					return
				}
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, bot)
			return
		}
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		deleted, err := svc.DeleteBot(ctx, rest)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "trade bot not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": rest})

	default:
		writeError(w, 404, "unknown trade operation")
	}
}

func toolGetTradeState(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	svc, err := s.tradeService()
	if err != nil {
		return nil, err
	}
	return svc.GetSnapshot(ctx)
}

func toolListWatchlists(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	md, err := s.marketDataService()
	if err != nil {
		return nil, err
	}
	lists, err := md.ListWatchlists(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"watchlists": lists, "count": len(lists)}, nil
}

func toolGetKillSwitchStatus(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	globalActive := s.isGlobalKillActive(ctx)
	var tradeKill map[string]any
	if ts := s.loadedOrExistingTradeService(); ts != nil {
		if snap, err := ts.GetSnapshot(ctx); err == nil {
			tradeKill = map[string]any{
				"active": snap.KillSwitchActive,
				"reason": snap.KillReason,
			}
		}
	}
	var autoKill bool
	if autoSvc := s.loadedOrExistingAutomationService(); autoSvc != nil {
		if st, err := autoSvc.GetState(ctx); err == nil {
			autoKill = st.KillSwitchActive
		}
	}
	var deskKill bool
	if ds := s.loadedOrExistingDeskService(); ds != nil {
		if snap, err := ds.GetSnapshot(ctx); err == nil {
			deskKill = snap.Control.KillSwitchActive
		}
	}
	return map[string]any{
		"globalKillActive":     globalActive,
		"automationKillActive": autoKill,
		"deskKillActive":       deskKill,
		"tradeKill":            tradeKill,
	}, nil
}

func previewDraftTradeOrder(_ context.Context, _ *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var req trade.OrderRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return approvalDraft{}, err
	}
	if req.Mode == "" {
		req.Mode = trade.ModeAIAgent
	}
	norm, err := trade.NormalizeOrderRequest(req)
	if err != nil {
		return approvalDraft{}, err
	}
	payload, _ := json.Marshal(norm)
	return approvalDraft{
		Title:       fmt.Sprintf("Draft Trade Order (%s %s %.6g %s)", norm.Venue, norm.Strategy, norm.Quantity, norm.Symbol),
		Summary:     fmt.Sprintf("%s %s (%s mode, simulated paper execution)", strings.ToUpper(norm.Side), norm.Symbol, norm.Mode),
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftTradeOrder(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var req trade.OrderRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return nil, err
	}
	if req.Mode == "" {
		req.Mode = trade.ModeAIAgent
	}
	svc, err := s.tradeService()
	if err != nil {
		return nil, err
	}
	return svc.SubmitOrder(ctx, req)
}
