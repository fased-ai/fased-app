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
	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/desk"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/telegram"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

func (s *Store) automationService() (*automation.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.automationMu.Lock()
	if s.automationSvc != nil {
		svc := s.automationSvc
		s.automationMu.Unlock()
		return svc, nil
	}
	svc, err := automation.NewService(automation.Config{
		DataDir:       s.dir,
		FixtureMode:   s.providerFixture,
		Now:           s.now,
		SecretSource:  s.taskSecrets,
		HTTPTransport: s.automationTransport,
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
		DrawingReader: func(ctx context.Context, id string) (marketdata.ChartDrawing, bool, error) {
			md, mErr := s.marketDataService()
			if mErr != nil {
				return marketdata.ChartDrawing{}, false, mErr
			}
			return md.GetDrawing(ctx, id)
		},
		IntelEvents: func(ctx context.Context, filter worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			wi, wErr := s.worldIntelService()
			if wErr != nil {
				return nil, wErr
			}
			return wi.ListEvents(ctx, filter)
		},
		IntelPredictions: func(ctx context.Context, filter worldintel.PredictionFilter) (worldintel.PredictionList, error) {
			wi, wErr := s.worldIntelService()
			if wErr != nil {
				return worldintel.PredictionList{}, wErr
			}
			return wi.ListPredictions(ctx, filter)
		},
		Scorer: func(ctx context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error) {
			decSvc, dErr := s.decisionService()
			if dErr != nil {
				return decision.EvaluateFallback(req, s.now())
			}
			return decSvc.Evaluate(ctx, req)
		},
		ScriptReader: func(ctx context.Context, scriptID string) (string, error) {
			ql, qErr := s.quantLabService()
			if qErr != nil {
				return "", qErr
			}
			rec, found, err := ql.GetScript(ctx, scriptID)
			if err != nil {
				return "", err
			}
			if !found {
				return "", os.ErrNotExist
			}
			return rec.Source, nil
		},
		OnDeskCycle: func(ctx context.Context, _ automation.Trigger, venue, symbol, interval, scriptID, mode string) (string, error) {
			ds, dErr := s.deskService()
			if dErr != nil {
				return "", dErr
			}
			res, rErr := ds.RunCycle(ctx, desk.CycleRequest{
				Venue:    venue,
				Symbol:   symbol,
				Interval: interval,
				ScriptID: scriptID,
				Mode:     mode,
				Limit:    120,
			})
			if rErr != nil {
				return "", rErr
			}
			return fmt.Sprintf("Desk cycle %s (%s): %s", res.ID, res.Mode, res.FinalStatus), nil
		},
		OnAgentPrompt: func(ctx context.Context, tr automation.Trigger, prompt string, untrusted bool) {
			s.dispatchAutomationAgentPrompt(ctx, tr, prompt, untrusted)
		},
	})
	if err != nil {
		s.automationMu.Unlock()
		return nil, err
	}
	svc.SetNotifyHook(func(ctx context.Context, n automation.Notification) string {
		tg, tErr := s.telegramService()
		if tErr != nil {
			return ""
		}
		return tg.SendNotification(ctx, n)
	})
	s.automationSvc = svc
	s.automationMu.Unlock()
	s.notifyReactiveBus()
	return svc, nil
}

func (s *Store) telegramService() (*telegram.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	autoSvc, err := s.automationService()
	if err != nil {
		return nil, err
	}
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	if s.telegramSvc != nil {
		return s.telegramSvc, nil
	}
	tg, err := telegram.NewService(telegram.Config{
		Secrets:       autoSvc,
		Hooks:         &coreTelegramHooks{store: s, autoSvc: autoSvc},
		FixtureMode:   s.providerFixture,
		Now:           s.now,
		HTTPTransport: s.telegramTransport,
	})
	if err != nil {
		return nil, err
	}
	s.telegramSvc = tg
	return tg, nil
}

func (s *Store) dispatchAutomationAgentPrompt(ctx context.Context, tr automation.Trigger, prompt string, untrusted bool) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil || v2 == nil {
		return
	}
	threads, err := s.ListThreads(ctx, "", false)
	if err != nil {
		return
	}
	var targetID string
	var profileID string
	for _, th := range threads {
		if th.SourceKind == "automation" && th.Title == "Event Automation Feed" {
			targetID = th.ID
			profileID = th.ProfileID
			break
		}
	}
	if targetID == "" {
		created, cErr := s.CreateThread(ctx, "", "Event Automation Feed", "", "")
		if cErr != nil {
			return
		}
		targetID = created.ID
		profileID = created.ProfileID
		_, _ = v2.db.ExecContext(ctx, `UPDATE threads SET source_kind='automation', source_ref=? WHERE id=?`, tr.ID, targetID)
	}
	// Background automation triggers (especially untrusted news/world-intel events) MUST run
	// with tools disabled so an indirect prompt injection cannot exfiltrate workspace state or
	// spam action approval cards autonomously.
	toolsOff := false
	framedPrompt := prompt
	if untrusted {
		framedPrompt = wrapUntrustedContextBlock("untrusted_trigger_event", prompt)
	}
	var fallbackMsg string
	if _, pErr := resolveAgentV2Provider(ctx, s, "", "", "", "", profileID); pErr == nil {
		turnCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, tErr := s.RunAgentV2Turn(turnCtx, targetID, AgentV2PromptInput{Content: framedPrompt, ToolsEnabled: &toolsOff}, nil)
		if tErr == nil {
			return
		}
		fallbackMsg = "Trigger recorded. Autonomous AI triage failed: " + sanitizeDiagValue(tErr.Error(), 160)
	} else {
		fallbackMsg = "Trigger recorded. Connect an AI provider in Settings to run autonomous triage."
	}
	_, _ = s.AppendMessage(ctx, AgentV2Message{
		ThreadID: targetID,
		Role:     "user",
		Content:  prompt,
		Status:   "completed",
	})
	_, _ = s.AppendMessage(ctx, AgentV2Message{
		ThreadID: targetID,
		Role:     "assistant",
		Content:  fallbackMsg,
		Status:   "completed",
	})
}

func (s *Store) notifyApprovalRequired(ctx context.Context, approvalID, title, summary string, allowTelegramButtons bool) {
	if s == nil {
		return
	}
	s.automationMu.Lock()
	initialized := s.automationSvc != nil
	s.automationMu.Unlock()
	if !initialized {
		if _, err := os.Stat(filepath.Join(s.dir, "automation", "automation.sqlite")); err != nil {
			return
		}
	}
	autoSvc, err := s.automationService()
	if err != nil {
		return
	}
	_, _ = s.telegramService()
	metaBytes, _ := json.Marshal(map[string]any{
		"approvalId":           approvalID,
		"allowTelegramButtons": allowTelegramButtons,
	})
	_, _ = autoSvc.NotifyApproval(ctx, title, summary, string(metaBytes))
}

type coreTelegramHooks struct {
	store   *Store
	autoSvc *automation.Service
}

func (h *coreTelegramHooks) StatusSummary(ctx context.Context) (telegram.StatusSnapshot, error) {
	st, err := h.autoSvc.GetState(ctx)
	if err != nil {
		return telegram.StatusSnapshot{}, err
	}
	triggers, err := h.autoSvc.ListTriggers(ctx)
	if err != nil {
		return telegram.StatusSnapshot{}, err
	}
	enabledCount := 0
	for _, tr := range triggers {
		if tr.Enabled {
			enabledCount++
		}
	}
	ntfs, err := h.autoSvc.ListNotifications(ctx, automation.NotificationFilter{UnreadOnly: true, Limit: 1})
	if err != nil {
		return telegram.StatusSnapshot{}, err
	}
	approvals, err := h.store.ListPendingApprovals(ctx, 25)
	if err != nil {
		approvals = nil
	}
	pendingCount := len(approvals)
	if ds := h.store.loadedOrExistingDeskService(); ds != nil {
		if tickets, tErr := ds.ListTickets(ctx, "PENDING_PM", 25); tErr == nil {
			pendingCount += len(tickets)
		}
	}
	return telegram.StatusSnapshot{
		InstanceID:       h.store.instanceID,
		AutomationState:  st,
		EnabledTriggers:  enabledCount,
		UnreadAlerts:     ntfs.UnreadCount,
		PendingApprovals: pendingCount,
		FinancialLive:    false,
	}, nil
}

func (h *coreTelegramHooks) ListRecentAlerts(ctx context.Context, limit int) ([]automation.Notification, error) {
	res, err := h.autoSvc.ListNotifications(ctx, automation.NotificationFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	return res.Notifications, nil
}

func (h *coreTelegramHooks) ListPendingApprovals(ctx context.Context) ([]telegram.PendingApprovalBrief, error) {
	items, err := h.store.ListPendingApprovals(ctx, 10)
	if err != nil {
		items = nil
	}
	out := make([]telegram.PendingApprovalBrief, 0, len(items))
	for _, ap := range items {
		out = append(out, telegram.PendingApprovalBrief{
			ID:       ap.ID,
			ToolName: ap.ToolName,
			Title:    ap.Title,
			Summary:  ap.Summary,
		})
	}
	if ds := h.store.loadedOrExistingDeskService(); ds != nil {
		if tickets, tErr := ds.ListTickets(ctx, "PENDING_PM", 10); tErr == nil {
			for _, tk := range tickets {
				out = append(out, telegram.PendingApprovalBrief{
					ID:       tk.ID,
					ToolName: "desk_ticket",
					Title:    fmt.Sprintf("Desk %s %s (%s)", strings.ToUpper(tk.Side), tk.Symbol, tk.Venue),
					Summary:  fmt.Sprintf("qty %.4f @ %.4f · R:R %.2f · risk $%.2f", tk.Quantity, tk.EntryPrice, tk.RiskReward, tk.RiskUSD),
				})
			}
		}
	}
	return out, nil
}

func (h *coreTelegramHooks) ResolveApproval(ctx context.Context, id, decisionArg, note string) (string, error) {
	st, err := h.autoSvc.GetState(ctx)
	if err != nil {
		return "", err
	}
	if st.KillSwitchActive && decisionArg == "approve" {
		return "", errors.New("kill switch is active; clear kill switch before approving actions")
	}
	ap, err := h.store.GetApproval(ctx, id)
	if err == nil {
		if ap.ToolName == "request_secret" {
			return "", errors.New("request_secret approvals cannot be submitted over Telegram; enter secrets only in local Fased UI")
		}
		res, rErr := h.store.ResolveAgentV2Approval(ctx, id, decisionArg, note, false, nil)
		if rErr != nil {
			return "", rErr
		}
		return fmt.Sprintf("Approval %s marked %s (%s).", res.Approval.ID, res.Approval.Status, res.Approval.ToolName), nil
	}
	if ds := h.store.loadedOrExistingDeskService(); ds != nil {
		if tickets, tErr := ds.ListTickets(ctx, "PENDING_PM", 50); tErr == nil {
			for _, tk := range tickets {
				if tk.ID != id {
					continue
				}
				if decisionArg == "approve" {
					pos, aErr := ds.ApproveTicket(ctx, id, "telegram")
					if aErr != nil {
						return "", aErr
					}
					return fmt.Sprintf("Desk ticket %s approved; opened %s %s (%s).", id, strings.ToUpper(pos.Side), pos.Symbol, pos.ID), nil
				}
				rejNote := strings.TrimSpace(note)
				if rejNote == "" {
					rejNote = "Rejected via Telegram"
				}
				rej, rErr := ds.RejectTicket(ctx, id, rejNote, "telegram")
				if rErr != nil {
					return "", rErr
				}
				return fmt.Sprintf("Desk ticket %s rejected (%s).", rej.ID, rej.Symbol), nil
			}
		}
	}
	return "", err
}

func (h *coreTelegramHooks) BriefSummary(ctx context.Context) (string, error) {
	snap, err := h.StatusSummary(ctx)
	if err != nil {
		return "", err
	}
	stateLabel := "ACTIVE"
	if snap.AutomationState.KillSwitchActive {
		stateLabel = "KILL_SWITCH"
	} else if snap.AutomationState.Paused {
		stateLabel = "PAUSED"
	}
	lines := []string{
		fmt.Sprintf("Fased Brief (%s)", stateLabel),
		fmt.Sprintf("Triggers: %d enabled | Unread: %d | Approvals: %d", snap.EnabledTriggers, snap.UnreadAlerts, snap.PendingApprovals),
	}
	if ds := h.store.loadedOrExistingDeskService(); ds != nil {
		if dSnap, dErr := ds.GetSnapshot(ctx); dErr == nil {
			lines = append(lines, fmt.Sprintf(
				"Desk (%s): Equity $%.2f | Realized $%.2f | Open: %d | Pending PM: %d",
				dSnap.Limits.Mode, dSnap.Account.EquityUSD, dSnap.Account.RealizedPnLUSD, dSnap.Account.OpenPositions, dSnap.Account.PendingTickets,
			))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (h *coreTelegramHooks) PositionsSummary(ctx context.Context) (string, error) {
	ds := h.store.loadedOrExistingDeskService()
	if ds == nil {
		return "No open desk positions.", nil
	}
	positions, err := ds.ListPositions(ctx, "OPEN", 10)
	if err != nil {
		return "", err
	}
	if len(positions) == 0 {
		return "No open desk positions.", nil
	}
	lines := make([]string, 0, len(positions)+1)
	lines = append(lines, fmt.Sprintf("Open Desk Positions (%d):", len(positions)))
	for _, p := range positions {
		lines = append(lines, fmt.Sprintf("- %s %s (%s) %.4f @ %.4f | mark %.4f | uPnL $%.2f", strings.ToUpper(p.Side), p.Symbol, p.Venue, p.Quantity, p.EntryPrice, p.MarkPrice, p.UnrealizedPnL))
	}
	return strings.Join(lines, "\n"), nil
}

func (h *coreTelegramHooks) SetAutomationState(ctx context.Context, action, reason, actor string) (automation.ControlState, error) {
	st, err := h.autoSvc.SetState(ctx, action, reason, actor)
	if err == nil {
		act := strings.ToLower(strings.TrimSpace(action))
		if act == automation.StateActionKill {
			h.store.syncDeskKill(ctx, reason, actor)
			h.store.syncTradeKill(ctx, true, reason, actor)
		} else if act == automation.StateActionUnkill {
			h.store.syncTradeKill(ctx, false, reason, actor)
		}
	}
	return st, err
}

func (h *HTTPHandler) automationAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "automation", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	autoSvc, err := h.store.automationService()
	if err != nil {
		writeError(w, 500, "automation service unavailable")
		return
	}
	tgSvc, err := h.store.telegramService()
	if err != nil {
		writeError(w, 500, "telegram controller unavailable")
		return
	}

	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/automation/"), "/")
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	switch {
	case rel == "state":
		switch r.Method {
		case http.MethodGet:
			st, err := autoSvc.GetState(ctx)
			if err != nil {
				writeError(w, 500, "automation state unavailable")
				return
			}
			writeJSON(w, 200, st)
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in struct {
				Action string `json:"action"`
				Reason string `json:"reason"`
			}
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			st, err := autoSvc.SetState(ctx, in.Action, in.Reason, "owner")
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			act := strings.ToLower(strings.TrimSpace(in.Action))
			if act == automation.StateActionKill {
				h.store.syncDeskKill(ctx, in.Reason, "owner")
				h.store.syncTradeKill(ctx, true, in.Reason, "owner")
			} else if act == automation.StateActionUnkill {
				h.store.syncTradeKill(ctx, false, in.Reason, "owner")
			}
			writeJSON(w, 200, st)
		default:
			writeError(w, 405, "method not allowed")
		}

	case rel == "triggers":
		switch r.Method {
		case http.MethodGet:
			items, err := autoSvc.ListTriggers(ctx)
			if err != nil {
				writeError(w, 500, "triggers unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"triggers": items, "count": len(items)})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in automation.TriggerInput
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			saved, err := autoSvc.SaveTrigger(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, saved)
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "triggers/") && strings.HasSuffix(rel, "/test"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(rel, "triggers/"), "/test")
		var in struct {
			DryRun bool `json:"dryRun"`
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") && r.ContentLength != 0 {
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
		}
		res, err := autoSvc.EvaluateTrigger(ctx, id, in.DryRun, true)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "trigger not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	case strings.HasPrefix(rel, "triggers/") && !strings.Contains(strings.TrimPrefix(rel, "triggers/"), "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimPrefix(rel, "triggers/")
		deleted, err := autoSvc.DeleteTrigger(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "trigger not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})

	case rel == "evaluate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		sweep, err := autoSvc.EvaluateNow(ctx)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, sweep)

	case rel == "notifications":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 50, 200)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		unreadOnly := q.Get("unreadOnly") == "1" || strings.EqualFold(q.Get("unreadOnly"), "true")
		list, err := autoSvc.ListNotifications(ctx, automation.NotificationFilter{
			UnreadOnly: unreadOnly,
			Severity:   q.Get("severity"),
			Limit:      limit,
		})
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, list)

	case rel == "notifications/read":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			ID  string `json:"id"`
			All bool   `json:"all"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		updated, err := autoSvc.MarkNotificationsRead(ctx, in.ID, in.All)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]int{"updated": updated})

	case rel == "webhook":
		switch r.Method {
		case http.MethodGet:
			cfg, err := autoSvc.GetWebhookConfig(ctx)
			if err != nil {
				writeError(w, 500, "webhook configuration unavailable")
				return
			}
			writeJSON(w, 200, cfg)
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in automation.WebhookInput
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			cfg, err := autoSvc.SaveWebhookConfig(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, cfg)
		default:
			writeError(w, 405, "method not allowed")
		}

	case rel == "webhook/test":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		cfg, err := autoSvc.SendWebhookTest(ctx)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, cfg)

	case rel == "telegram":
		switch r.Method {
		case http.MethodGet:
			cfg, err := tgSvc.GetConfig(ctx)
			if err != nil {
				writeError(w, 500, "telegram configuration unavailable")
				return
			}
			writeJSON(w, 200, cfg)
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in telegram.ConfigInput
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			cfg, err := tgSvc.SaveConfig(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, cfg)
		default:
			writeError(w, 405, "method not allowed")
		}

	case rel == "telegram/test":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		cfg, err := tgSvc.SendTest(ctx)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, cfg)

	case rel == "telegram/simulate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			ChatID int64  `json:"chatId"`
			Text   string `json:"text"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		reply, err := tgSvc.SimulateCommand(ctx, in.ChatID, in.Text)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		autoSvc.PublishStream(automation.StreamEvent{
			Type:      "telegram_command",
			Timestamp: h.store.now().UTC().UnixMilli(),
			Command:   reply.Command,
		})
		writeJSON(w, 200, reply)

	case rel == "stream":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		h.automationStreamSSE(w, r, token, autoSvc)

	default:
		writeError(w, 404, "unknown automation operation")
	}
}

func (h *HTTPHandler) automationStreamSSE(w http.ResponseWriter, r *http.Request, token string, autoSvc *automation.Service) {
	streamCtx, releaseStream, ok := h.acquireSSEStream(w, r, token)
	if !ok {
		return
	}
	defer releaseStream()

	snapCtx, cancel := context.WithTimeout(streamCtx, 10*time.Second)
	st, err := autoSvc.GetState(snapCtx)
	ntfs, nErr := autoSvc.ListNotifications(snapCtx, automation.NotificationFilter{Limit: 15})
	cancel()
	if err != nil || nErr != nil {
		writeError(w, 500, "automation snapshot unavailable")
		return
	}

	subCh, unsubscribe := autoSvc.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	writeSSE := func(eventName string, payload any) bool {
		raw, mErr := json.Marshal(payload)
		if mErr != nil {
			return false
		}
		if _, wErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, raw); wErr != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	if !writeSSE("ready", map[string]any{
		"state":         st,
		"notifications": ntfs.Notifications,
		"unreadCount":   ntfs.UnreadCount,
		"timestamp":     h.store.now().UTC().UnixMilli(),
	}) {
		return
	}

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-streamCtx.Done():
			return
		case <-heartbeat.C:
			if !writeSSE("heartbeat", map[string]any{"timestamp": time.Now().UTC().UnixMilli()}) {
				return
			}
		case evt, ok := <-subCh:
			if !ok {
				return
			}
			evtName := evt.Type
			if evtName == "" {
				evtName = "event"
			}
			if !writeSSE(evtName, evt) {
				return
			}
		}
	}
}

func toolListEventTriggers(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct{}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.automationService()
	if err != nil {
		return nil, err
	}
	st, err := svc.GetState(ctx)
	if err != nil {
		return nil, err
	}
	items, err := svc.ListTriggers(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"state":    st,
		"triggers": items,
		"count":    len(items),
	}, nil
}

func toolListNotifications(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		UnreadOnly bool   `json:"unreadOnly"`
		Severity   string `json:"severity"`
		Limit      int    `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.automationService()
	if err != nil {
		return nil, err
	}
	return svc.ListNotifications(ctx, automation.NotificationFilter{
		UnreadOnly: args.UnreadOnly,
		Severity:   args.Severity,
		Limit:      args.Limit,
	})
}
