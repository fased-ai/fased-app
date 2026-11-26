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
	"github.com/fased-ai/fased-app/internal/quantlab"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

func (s *Store) deskService() (*desk.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.deskMu.Lock()
	if s.deskSvc != nil {
		svc := s.deskSvc
		s.deskMu.Unlock()
		return svc, nil
	}
	svc, err := desk.NewService(desk.Config{
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
		OrderBookReader: func(ctx context.Context, venue, symbol string) (marketdata.OrderBookSnapshot, error) {
			md, mErr := s.marketDataService()
			if mErr != nil {
				return marketdata.OrderBookSnapshot{}, mErr
			}
			return md.GetOrderBook(ctx, venue, symbol, 10)
		},
		IntelEvents: func(ctx context.Context, filter worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			wi, wErr := s.worldIntelService()
			if wErr != nil {
				return nil, wErr
			}
			return wi.ListEvents(ctx, filter)
		},
		PredictionReader: func(ctx context.Context, filter worldintel.PredictionFilter) (worldintel.PredictionList, error) {
			wi, wErr := s.worldIntelService()
			if wErr != nil {
				return worldintel.PredictionList{}, wErr
			}
			return wi.ListPredictions(ctx, filter)
		},
		LabRunsReader: func(ctx context.Context, limit int) ([]quantlab.ValidationRunSummary, error) {
			ql, qErr := s.quantLabService()
			if qErr != nil {
				return nil, qErr
			}
			return ql.ListRuns(ctx, limit)
		},
		ScriptReader: func(ctx context.Context, scriptID string) (quantlab.ScriptRecord, bool, error) {
			ql, qErr := s.quantLabService()
			if qErr != nil {
				return quantlab.ScriptRecord{}, false, qErr
			}
			return ql.GetScript(ctx, scriptID)
		},
		Scorer: func(ctx context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error) {
			decSvc, dErr := s.decisionService()
			if dErr != nil {
				return decision.EvaluateFallback(req, s.now())
			}
			return decSvc.Evaluate(ctx, req)
		},
		ExternalKillActive: func(ctx context.Context) bool {
			autoSvc, aErr := s.automationService()
			if aErr != nil {
				return false
			}
			st, sErr := autoSvc.GetState(ctx)
			return sErr == nil && st.KillSwitchActive
		},
		OnAlert: func(ctx context.Context, severity, title, body string) {
			autoSvc, aErr := s.automationService()
			if aErr != nil {
				return
			}
			urgency := 0.5
			if severity == "critical" {
				urgency = 0.95
			} else if severity == "warn" {
				urgency = 0.75
			}
			_, _ = autoSvc.RecordNotification(ctx, automation.Notification{
				Kind:      "desk",
				Severity:  severity,
				Title:     title,
				Body:      body,
				Urgency:   urgency,
				Untrusted: false,
			})
		},
		OnApprovalTicket: func(ctx context.Context, ticket desk.OrderTicket) {
			s.notifyApprovalRequired(
				ctx,
				ticket.ID,
				fmt.Sprintf("PM approval needed: %s %s", strings.ToUpper(ticket.Side), ticket.Symbol),
				fmt.Sprintf("%s %s %.4f @ %.4f (R:R %.2f, risk $%.2f)", ticket.Venue, ticket.Symbol, ticket.Quantity, ticket.EntryPrice, ticket.RiskReward, ticket.RiskUSD),
				true,
			)
		},
	})
	if err != nil {
		s.deskMu.Unlock()
		return nil, err
	}
	s.deskSvc = svc
	s.deskMu.Unlock()
	return svc, nil
}

func (s *Store) syncDeskKill(ctx context.Context, reason, actor string) {
	if s == nil {
		return
	}
	svc, err := s.deskService()
	if err != nil || svc == nil {
		return
	}
	_ = svc.SyncExternalKill(ctx, reason, actor)
}

func (s *Store) loadedOrExistingDeskService() *desk.Service {
	if s == nil {
		return nil
	}
	s.deskMu.Lock()
	initialized := s.deskSvc != nil
	existing := s.deskSvc
	s.deskMu.Unlock()
	if initialized {
		return existing
	}
	if s.dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.dir, "desk", "desk.sqlite")); err != nil {
		return nil
	}
	svc, err := s.deskService()
	if err != nil {
		return nil
	}
	return svc
}

func (h *HTTPHandler) deskAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "desk", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.deskService()
	if err != nil {
		writeError(w, 500, "desk service unavailable")
		return
	}

	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/desk/"), "/")
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	switch {
	case rel == "state":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		snap, err := svc.GetSnapshot(ctx)
		if err != nil {
			writeError(w, 500, "desk state unavailable")
			return
		}
		writeJSON(w, 200, snap)

	case rel == "limits":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in desk.RiskLimits
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		saved, err := svc.UpdateLimits(ctx, in)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, saved)

	case rel == "control":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			Action  string `json:"action"`
			Confirm string `json:"confirm"`
			Reason  string `json:"reason"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		st, err := svc.SetControl(ctx, in.Action, in.Confirm, in.Reason, "owner")
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, st)

	case rel == "cycle":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in desk.CycleRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		res, err := svc.RunCycle(ctx, in)
		if err != nil {
			if errors.Is(err, desk.ErrKillSwitchActive) || errors.Is(err, desk.ErrDeskPaused) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "cycle": res})
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	case rel == "cycles":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 25, 100)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		items, err := svc.ListCycles(ctx, limit)
		if err != nil {
			writeError(w, 500, "desk cycles unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"cycles": items, "count": len(items)})

	case rel == "tickets":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 50, 200)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		items, err := svc.ListTickets(ctx, q.Get("status"), limit)
		if err != nil {
			writeError(w, 500, "desk tickets unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"tickets": items, "count": len(items)})

	case strings.HasPrefix(rel, "tickets/") && strings.HasSuffix(rel, "/approve"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(rel, "tickets/"), "/approve")
		pos, err := svc.ApproveTicket(ctx, id, "owner")
		if err != nil {
			if errors.Is(err, desk.ErrNotFound) {
				writeError(w, 404, "ticket not found")
				return
			}
			if errors.Is(err, desk.ErrKillSwitchActive) || errors.Is(err, desk.ErrDeskPaused) {
				writeError(w, 409, err.Error())
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, pos)

	case strings.HasPrefix(rel, "tickets/") && strings.HasSuffix(rel, "/reject"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(rel, "tickets/"), "/reject")
		var in struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength > 0 {
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
		}
		tkt, err := svc.RejectTicket(ctx, id, in.Reason, "owner")
		if err != nil {
			if errors.Is(err, desk.ErrNotFound) {
				writeError(w, 404, "ticket not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, tkt)

	case rel == "positions":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 50, 200)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		items, err := svc.ListPositions(ctx, q.Get("status"), limit)
		if err != nil {
			writeError(w, 500, "paper positions unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"positions": items, "count": len(items)})

	case rel == "positions/mark":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		items, err := svc.MarkPositions(ctx)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"positions": items, "count": len(items)})

	case strings.HasPrefix(rel, "positions/") && strings.HasSuffix(rel, "/close"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(rel, "positions/"), "/close")
		var in struct {
			ExitPrice float64 `json:"exitPrice"`
			Reason    string  `json:"reason"`
		}
		if r.ContentLength > 0 {
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
		}
		pos, err := svc.ClosePosition(ctx, id, in.ExitPrice, in.Reason)
		if err != nil {
			if errors.Is(err, desk.ErrNotFound) {
				writeError(w, 404, "position not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, pos)

	case rel == "journal":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 50, 250)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		items, err := svc.ListJournal(ctx, q.Get("symbol"), q.Get("outcome"), limit)
		if err != nil {
			writeError(w, 500, "desk journal unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"journal": items, "count": len(items)})

	case rel == "lessons":
		switch r.Method {
		case http.MethodGet:
			limit, err := queryPositiveInt(q.Get("limit"), 50, 100)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			items, err := svc.ListLessons(ctx, limit)
			if err != nil {
				writeError(w, 500, "desk lessons unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"lessons": items, "count": len(items)})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in desk.LessonInput
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			saved, err := svc.SaveLesson(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, saved)
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "lessons/") && !strings.Contains(strings.TrimPrefix(rel, "lessons/"), "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimPrefix(rel, "lessons/")
		deleted, err := svc.DeleteLesson(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "lesson not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})

	case rel == "report":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		rep, err := svc.GetDecayReport(ctx)
		if err != nil {
			writeError(w, 500, "decay report unavailable")
			return
		}
		writeJSON(w, 200, rep)

	case rel == "stream":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		h.deskStreamSSE(w, r, svc)

	default:
		writeError(w, 404, "unknown desk operation")
	}
}

func (h *HTTPHandler) deskStreamSSE(w http.ResponseWriter, r *http.Request, svc *desk.Service) {
	snapCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	snap, err := svc.GetSnapshot(snapCtx)
	cancel()
	if err != nil {
		writeError(w, 500, "desk snapshot unavailable")
		return
	}

	subCh, unsubscribe := svc.Subscribe()
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

	if !writeSSE("ready", snap) {
		return
	}

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
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

func toolGetDeskState(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct{}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.deskService()
	if err != nil {
		return nil, err
	}
	snap, err := svc.GetSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	openPositions := make([]desk.PaperPosition, 0, len(snap.Positions))
	recentClosedPositions := make([]desk.PaperPosition, 0, 5)
	for _, p := range snap.Positions {
		if p.Status == "OPEN" {
			if len(openPositions) < 25 {
				openPositions = append(openPositions, p)
			}
		} else if len(recentClosedPositions) < 5 {
			recentClosedPositions = append(recentClosedPositions, p)
		}
	}
	pendingTickets := make([]desk.OrderTicket, 0, 25)
	for _, tk := range snap.Tickets {
		if tk.Status == "PENDING_PM" && len(pendingTickets) < 25 {
			pendingTickets = append(pendingTickets, tk)
		}
	}
	compactSeats := make([]map[string]any, 0, len(snap.Seats))
	for _, seat := range snap.Seats {
		compactSeats = append(compactSeats, map[string]any{
			"id":              seat.ID,
			"title":           seat.Title,
			"canExecutePaper": seat.CanExecutePaper,
		})
	}
	return map[string]any{
		"control":               snap.Control,
		"limits":                snap.Limits,
		"account":               snap.Account,
		"openPositions":         openPositions,
		"recentClosedPositions": recentClosedPositions,
		"pendingTickets":        pendingTickets,
		"recentTicketsCount":    len(snap.Tickets),
		"bench":                 snap.Bench,
		"decay":                 snap.Decay,
		"lessonsCount":          snap.LessonsCount,
		"seats":                 compactSeats,
		"updatedAt":             snap.UpdatedAt,
	}, nil
}

func toolListDeskJournal(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Symbol  string `json:"symbol"`
		Outcome string `json:"outcome"`
		Limit   int    `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.deskService()
	if err != nil {
		return nil, err
	}
	items, err := svc.ListJournal(ctx, args.Symbol, args.Outcome, args.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"journal": items, "count": len(items)}, nil
}

func toolListDeskLessons(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.deskService()
	if err != nil {
		return nil, err
	}
	items, err := svc.ListLessons(ctx, args.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"lessons": items, "count": len(items)}, nil
}
