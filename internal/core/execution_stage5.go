package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/fased-ai/fased-app/internal/cex"
	"github.com/fased-ai/fased-app/internal/equities"
	"github.com/fased-ai/fased-app/internal/signer"
)

type stage5Runtime struct {
	mu       sync.Mutex
	cex      *cex.Service
	equities *equities.Service
}

var (
	stage5RuntimeMu sync.RWMutex
	stage5Runtimes  = map[*Store]*stage5Runtime{}
)

func init() {
	registerStoreCloseHook(func(s *Store) {
		_ = s.closeStage5Runtime()
	})
	stage5RouteHook = func(h *HTTPHandler, w http.ResponseWriter, r *http.Request, token string) bool {
		return h.handleStage5Route(w, r, token)
	}
}

func (s *Store) ensureStage5Runtime() (*stage5Runtime, error) {
	if s == nil {
		return nil, signer.ErrUnavailable
	}
	stage5RuntimeMu.RLock()
	rt := stage5Runtimes[s]
	stage5RuntimeMu.RUnlock()
	if rt != nil {
		return rt, nil
	}
	stage5RuntimeMu.Lock()
	defer stage5RuntimeMu.Unlock()
	if rt := stage5Runtimes[s]; rt != nil {
		return rt, nil
	}
	cexSvc, err := cex.NewService(cex.Config{
		DataDir:     s.dir,
		FixtureMode: s.providerFixture || s.dir == "",
		Now:         s.now,
	})
	if err != nil {
		return nil, err
	}
	eqSvc, err := equities.NewService(equities.Config{
		DataDir:     s.dir,
		FixtureMode: s.providerFixture || s.dir == "",
		Now:         s.now,
	})
	if err != nil {
		_ = cexSvc.Close()
		return nil, err
	}
	rt = &stage5Runtime{cex: cexSvc, equities: eqSvc}
	stage5Runtimes[s] = rt
	return rt, nil
}

func (s *Store) configureStage5MainnetCapped(maxOrderMicroUSD uint64) error {
	rt, err := s.ensureStage5Runtime()
	if err != nil {
		return err
	}
	if err := rt.cex.ConfigureMainnetCapped(maxOrderMicroUSD); err != nil {
		return err
	}
	return rt.equities.ConfigureMainnetCapped(maxOrderMicroUSD)
}

func (s *Store) closeStage5Runtime() error {
	if s == nil {
		return nil
	}
	stage5RuntimeMu.Lock()
	rt := stage5Runtimes[s]
	delete(stage5Runtimes, s)
	stage5RuntimeMu.Unlock()
	if rt == nil {
		return nil
	}
	var firstErr error
	if rt.cex != nil {
		firstErr = rt.cex.Close()
	}
	if rt.equities != nil {
		if err := rt.equities.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *HTTPHandler) handleStage5Route(w http.ResponseWriter, r *http.Request, token string) bool {
	switch r.URL.Path {
	case "/api/v1/cex/status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		st, err := rt.cex.Status(r.Context())
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		writeJSON(w, 200, st)
		return true

	case "/api/v1/cex/venues":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		writeJSON(w, 200, map[string]any{
			"activationEnabled": cex.CEXActivationEnabled,
			"mainnetLocked":     !cex.CEXActivationEnabled,
			"wenHeld":           !miningActivationEnabled,
			"venues":            cex.SupportedVenues(),
		})
		return true

	case "/api/v1/cex/credentials":
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		switch r.Method {
		case http.MethodPost:
			if !h.mutation(w, r) || !h.csrf(w, r, token) {
				return true
			}
			var in cex.CredentialInput
			if !decodeJSON(w, r, &in) {
				return true
			}
			st, saveErr := rt.cex.SaveCredential(r.Context(), in)
			if saveErr != nil {
				code := 400
				if errors.Is(saveErr, cex.ErrExecutionDisabled) || errors.Is(saveErr, cex.ErrWithdrawalScopeDenied) {
					code = 403
				}
				writeError(w, code, saveErr.Error())
				return true
			}
			writeJSON(w, 200, st)
			return true
		case http.MethodDelete:
			if !h.mutation(w, r) || !h.csrf(w, r, token) {
				return true
			}
			venue := strings.TrimSpace(r.URL.Query().Get("venue"))
			if delErr := rt.cex.DeleteCredential(r.Context(), venue); delErr != nil {
				writeError(w, 400, delErr.Error())
				return true
			}
			writeJSON(w, 200, map[string]any{"deleted": true, "venue": venue})
			return true
		default:
			writeError(w, 405, "method not allowed")
			return true
		}

	case "/api/v1/cex/orders":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return true
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return true
		}
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		var req cex.OrderRequest
		if !decodeJSON(w, r, &req) {
			return true
		}
		rec, subErr := rt.cex.SubmitOrder(r.Context(), req)
		if subErr != nil {
			code := 400
			if errors.Is(subErr, cex.ErrExecutionDisabled) || errors.Is(subErr, cex.ErrWithdrawalScopeDenied) {
				code = 403
			}
			writeError(w, code, subErr.Error())
			return true
		}
		writeJSON(w, 200, rec)
		return true

	case "/api/v1/equities/status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		st, err := rt.equities.Status(r.Context())
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		writeJSON(w, 200, st)
		return true

	case "/api/v1/equities/quotes":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		sym := strings.TrimSpace(r.URL.Query().Get("symbol"))
		if sym == "" {
			sym = "SPY"
		}
		q, err := rt.equities.GetQuote(r.Context(), sym)
		if err != nil {
			writeError(w, 400, err.Error())
			return true
		}
		writeJSON(w, 200, q)
		return true

	case "/api/v1/equities/options":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		sym := strings.TrimSpace(r.URL.Query().Get("symbol"))
		if sym == "" {
			sym = "SPY"
		}
		chain, err := rt.equities.FetchOptionsChain(r.Context(), sym)
		if err != nil {
			writeError(w, 400, err.Error())
			return true
		}
		writeJSON(w, 200, chain)
		return true

	case "/api/v1/equities/orders":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return true
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return true
		}
		rt, err := h.store.ensureStage5Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		var req equities.OrderRequest
		if !decodeJSON(w, r, &req) {
			return true
		}
		rec, subErr := rt.equities.SubmitOrder(r.Context(), req)
		if subErr != nil {
			code := 400
			if errors.Is(subErr, equities.ErrExecutionDisabled) {
				code = 403
			}
			writeError(w, code, subErr.Error())
			return true
		}
		writeJSON(w, 200, rec)
		return true
	}
	return false
}

func toolGetCEXStatus(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage5Runtime()
	if err != nil {
		return nil, err
	}
	return rt.cex.Status(ctx)
}

func toolGetEquitiesStatus(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage5Runtime()
	if err != nil {
		return nil, err
	}
	return rt.equities.Status(ctx)
}

type getOptionsChainArgs struct {
	Symbol string `json:"symbol"`
}

func toolGetOptionsChain(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var a getOptionsChainArgs
	if err := decodeToolArgs(rawArgs, &a); err != nil {
		return nil, err
	}
	sym := strings.TrimSpace(a.Symbol)
	if sym == "" {
		sym = "SPY"
	}
	rt, err := s.ensureStage5Runtime()
	if err != nil {
		return nil, err
	}
	return rt.equities.FetchOptionsChain(ctx, sym)
}

func previewDraftCEXOrder(_ context.Context, _ *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var req cex.OrderRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return approvalDraft{}, err
	}
	req.Environment = "paper"
	req.SimulateOnly = true
	if err := cex.ValidateOrderRequest(&req); err != nil {
		return approvalDraft{}, err
	}
	payload, _ := json.Marshal(req)
	return approvalDraft{
		Title:       fmt.Sprintf("Draft CEX Order (%s %s %.6g %s)", req.Venue, req.Side, req.Quantity, req.Symbol),
		Summary:     "Paper/sandbox CEX order draft (mainnet locked).",
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftCEXOrder(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var req cex.OrderRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return nil, err
	}
	req.Environment = "paper"
	req.SimulateOnly = true
	rt, err := s.ensureStage5Runtime()
	if err != nil {
		return nil, err
	}
	return rt.cex.SubmitOrder(ctx, req)
}

func previewDraftEquitiesOrder(_ context.Context, _ *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var req equities.OrderRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return approvalDraft{}, err
	}
	req.Environment = "paper"
	req.SimulateOnly = true
	if _, err := equities.ValidateOrderRequest(&req); err != nil {
		return approvalDraft{}, err
	}
	payload, _ := json.Marshal(req)
	return approvalDraft{
		Title:       fmt.Sprintf("Draft Equities Order (%s %s %.6g %s)", req.Venue, req.Side, req.Quantity, req.Symbol),
		Summary:     "Paper equities/OCC order draft (live brokerage locked).",
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftEquitiesOrder(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var req equities.OrderRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return nil, err
	}
	req.Environment = "paper"
	req.SimulateOnly = true
	rt, err := s.ensureStage5Runtime()
	if err != nil {
		return nil, err
	}
	return rt.equities.SubmitOrder(ctx, req)
}
