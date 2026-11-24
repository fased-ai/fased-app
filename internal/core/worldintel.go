package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

func (s *Store) worldIntelService() (*worldintel.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.worldIntelMu.Lock()
	defer s.worldIntelMu.Unlock()
	if s.worldIntelSvc != nil {
		return s.worldIntelSvc, nil
	}
	svc, err := worldintel.NewService(worldintel.Config{
		DataDir:       s.dir,
		FixtureMode:   s.providerFixture,
		Now:           s.now,
		HTTPTransport: s.worldIntelTransport,
		Scorer: func(ctx context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error) {
			decSvc, dErr := s.decisionService()
			if dErr != nil {
				return decision.EvaluateFallback(req, s.now())
			}
			return decSvc.Evaluate(ctx, req)
		},
	})
	if err != nil {
		return nil, err
	}
	s.worldIntelSvc = svc
	return svc, nil
}

func (h *HTTPHandler) worldIntelAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "worldintel", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.worldIntelService()
	if err != nil {
		writeError(w, 500, "world intelligence service unavailable")
		return
	}

	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/intel/"), "/")
	q := r.URL.Query()
	timeout := 20 * time.Second
	if rel == "sync" {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	switch {
	case rel == "events":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 100, 500)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		minUrgency, err := queryOptionalFloat(q.Get("minUrgency"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		var since int64
		if rawSince := strings.TrimSpace(q.Get("since")); rawSince != "" {
			parsedSince, pErr := strconv.ParseInt(rawSince, 10, 64)
			if pErr != nil || parsedSince < 0 {
				writeError(w, 400, "since must be a non-negative Unix timestamp")
				return
			}
			since = parsedSince
		}
		items, err := svc.ListEvents(ctx, worldintel.EventFilter{
			Category:   q.Get("category"),
			Region:     q.Get("region"),
			Ticker:     q.Get("ticker"),
			Query:      q.Get("q"),
			MinUrgency: minUrgency,
			Since:      since,
			Limit:      limit,
		})
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"events": items, "count": len(items)})

	case rel == "map":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		minUrgency, err := queryOptionalFloat(q.Get("minUrgency"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		hours, err := queryPositiveInt(q.Get("hours"), 72, 720)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		summary, err := svc.GetMapSummary(ctx, q.Get("category"), minUrgency, hours)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, summary)

	case rel == "predictions":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 100, 500)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		list, err := svc.ListPredictions(ctx, worldintel.PredictionFilter{
			Venue:    q.Get("venue"),
			Category: q.Get("category"),
			Query:    q.Get("q"),
			Limit:    limit,
		})
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, list)

	case rel == "sources":
		switch r.Method {
		case http.MethodGet:
			sources, err := svc.ListSources(ctx)
			if err != nil {
				writeError(w, 500, "intel sources unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"sources": sources, "count": len(sources)})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in worldintel.SourceInput
			if !decodeJSON(w, r, &in) {
				return
			}
			saved, err := svc.SaveSource(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, saved)
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "sources/") && !strings.Contains(strings.TrimPrefix(rel, "sources/"), "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimPrefix(rel, "sources/")
		deleted, err := svc.DeleteSource(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "intel source not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})

	case rel == "sync":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		var body struct {
			SourceID string `json:"sourceId"`
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") && r.ContentLength != 0 {
			if !decodeJSON(w, r, &body) {
				return
			}
		}
		res, err := svc.Sync(ctx, body.SourceID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "intel source not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	case rel == "stream":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		h.worldIntelStreamSSE(w, r, svc)

	default:
		writeError(w, 404, "unknown world intelligence operation")
	}
}

func (h *HTTPHandler) worldIntelStreamSSE(w http.ResponseWriter, r *http.Request, svc *worldintel.Service) {
	q := r.URL.Query()
	filter := worldintel.StreamFilter{
		Category: q.Get("category"),
		Region:   q.Get("region"),
		Ticker:   q.Get("ticker"),
	}

	snapCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	events, err := svc.ListEvents(snapCtx, worldintel.EventFilter{
		Category: filter.Category,
		Region:   filter.Region,
		Ticker:   filter.Ticker,
		Limit:    25,
	})
	preds, pErr := svc.ListPredictions(snapCtx, worldintel.PredictionFilter{
		Category: filter.Category,
		Limit:    15,
	})
	cancel()
	if err != nil || pErr != nil {
		writeError(w, 500, "world intelligence snapshot unavailable")
		return
	}

	subCh, unsubscribe := svc.Subscribe(filter)
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

	if !writeSSE("snapshot", map[string]any{
		"events":      events,
		"predictions": preds.Markets,
		"arbitrage":   preds.Arbitrage,
		"timestamp":   h.store.now().UTC().UnixMilli(),
	}) {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
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

func toolGetWorldIntel(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Category   string  `json:"category"`
		Region     string  `json:"region"`
		Ticker     string  `json:"ticker"`
		Query      string  `json:"query"`
		MinUrgency float64 `json:"minUrgency"`
		Hours      int     `json:"hours"`
		Limit      int     `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.worldIntelService()
	if err != nil {
		return nil, err
	}
	if args.Limit <= 0 {
		args.Limit = 25
	}
	var since int64
	if args.Hours > 0 {
		since = s.now().UTC().Unix() - int64(args.Hours)*3600
	}
	events, err := svc.ListEvents(ctx, worldintel.EventFilter{
		Category:   args.Category,
		Region:     args.Region,
		Ticker:     args.Ticker,
		Query:      args.Query,
		MinUrgency: args.MinUrgency,
		Since:      since,
		Limit:      args.Limit,
	})
	if err != nil {
		return nil, err
	}
	clusters := worldintel.BuildClusters(events)
	return map[string]any{
		"events":    events,
		"clusters":  clusters,
		"count":     len(events),
		"untrusted": true,
	}, nil
}

func toolGetPredictionMarkets(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Venue    string `json:"venue"`
		Category string `json:"category"`
		Query    string `json:"query"`
		Limit    int    `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.worldIntelService()
	if err != nil {
		return nil, err
	}
	if args.Limit <= 0 {
		args.Limit = 30
	}
	return svc.ListPredictions(ctx, worldintel.PredictionFilter{
		Venue:    args.Venue,
		Category: args.Category,
		Query:    args.Query,
		Limit:    args.Limit,
	})
}
