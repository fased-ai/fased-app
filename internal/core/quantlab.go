package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/quantlab"
)

func (s *Store) quantLabService() (*quantlab.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.quantLabMu.Lock()
	defer s.quantLabMu.Unlock()
	if s.quantLabSvc != nil {
		return s.quantLabSvc, nil
	}
	svc, err := quantlab.NewService(quantlab.Config{
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
	})
	if err != nil {
		return nil, err
	}
	s.quantLabSvc = svc
	return svc, nil
}

func (h *HTTPHandler) quantLabAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "quantlab", 120) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.quantLabService()
	if err != nil {
		writeError(w, 500, "quantlab service unavailable")
		return
	}

	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/lab/"), "/")
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	switch {
	case rel == "scripts":
		switch r.Method {
		case http.MethodGet:
			items, err := svc.ListScripts(ctx)
			if err != nil {
				writeError(w, 500, "scripts unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"scripts": items, "count": len(items)})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in quantlab.ScriptInput
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			saved, err := svc.SaveScript(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, saved)
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "scripts/") && !strings.Contains(strings.TrimPrefix(rel, "scripts/"), "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimPrefix(rel, "scripts/")
		deleted, err := svc.DeleteScript(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "script not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})

	case rel == "evaluate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var req quantlab.EvalIndicatorRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &req) {
			return
		}
		res, err := svc.EvaluateIndicator(ctx, req)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "script not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	case rel == "backtest":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var req quantlab.BacktestRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &req) {
			return
		}
		res, err := svc.RunBacktest(ctx, req)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "script not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	case rel == "validate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var req quantlab.ValidateRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &req) {
			return
		}
		rec, err := svc.RunValidation(ctx, req)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "script not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, rec)

	case rel == "runs":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 25, 100)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		runs, err := svc.ListRuns(ctx, limit)
		if err != nil {
			writeError(w, 500, "validation runs unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"runs": runs, "count": len(runs)})

	case strings.HasPrefix(rel, "runs/") && !strings.Contains(strings.TrimPrefix(rel, "runs/"), "/"):
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		id := strings.TrimPrefix(rel, "runs/")
		rec, found, err := svc.GetRun(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !found {
			writeError(w, 404, "validation run not found")
			return
		}
		writeJSON(w, 200, rec)

	default:
		writeError(w, 404, "unknown quantlab operation")
	}
}

func toolListLabScripts(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct{}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.quantLabService()
	if err != nil {
		return nil, err
	}
	scripts, err := svc.ListScripts(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"scripts": scripts, "count": len(scripts)}, nil
}

func toolRunFasedScriptBacktest(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		ScriptID string             `json:"scriptId"`
		Source   string             `json:"source"`
		Venue    string             `json:"venue"`
		Symbol   string             `json:"symbol"`
		Interval string             `json:"interval"`
		Limit    int                `json:"limit"`
		Params   map[string]float64 `json:"params"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.quantLabService()
	if err != nil {
		return nil, err
	}
	return svc.RunBacktest(ctx, quantlab.BacktestRequest{
		ScriptID: args.ScriptID,
		Source:   args.Source,
		Config: quantlab.BacktestConfig{
			Venue:    args.Venue,
			Symbol:   args.Symbol,
			Interval: args.Interval,
			Limit:    args.Limit,
			Params:   args.Params,
		},
	})
}

func toolRunFourGateValidation(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		ScriptID       string             `json:"scriptId"`
		Source         string             `json:"source"`
		Venue          string             `json:"venue"`
		Symbol         string             `json:"symbol"`
		Interval       string             `json:"interval"`
		Limit          int                `json:"limit"`
		Seed           uint64             `json:"seed"`
		Permutations   int                `json:"permutations"`
		WFPermutations int                `json:"wfPermutations"`
		Params         map[string]float64 `json:"params"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.quantLabService()
	if err != nil {
		return nil, err
	}
	return svc.RunValidation(ctx, quantlab.ValidateRequest{
		ScriptID: args.ScriptID,
		Source:   args.Source,
		Config: quantlab.BacktestConfig{
			Venue:          args.Venue,
			Symbol:         args.Symbol,
			Interval:       args.Interval,
			Limit:          args.Limit,
			Seed:           args.Seed,
			Permutations:   args.Permutations,
			WFPermutations: args.WFPermutations,
			Params:         args.Params,
		},
	})
}
