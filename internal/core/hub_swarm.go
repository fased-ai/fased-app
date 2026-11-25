package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/fased-ai/fased-app/internal/hub"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/swarm"
)

type stage6Runtime struct {
	hub   *hub.Service
	swarm *swarm.Service
}

var (
	stage6RuntimeMu sync.RWMutex
	stage6Runtimes  = map[*Store]*stage6Runtime{}
)

func init() {
	registerStoreCloseHook(func(s *Store) {
		_ = s.closeStage6Runtime()
	})
	stage6RouteHook = func(h *HTTPHandler, w http.ResponseWriter, r *http.Request, token string) bool {
		return h.handleStage6Route(w, r, token)
	}
}

func (s *Store) ensureStage6Runtime() (*stage6Runtime, error) {
	if s == nil {
		return nil, signer.ErrUnavailable
	}
	stage6RuntimeMu.RLock()
	rt := stage6Runtimes[s]
	stage6RuntimeMu.RUnlock()
	if rt != nil {
		return rt, nil
	}
	stage6RuntimeMu.Lock()
	defer stage6RuntimeMu.Unlock()
	if rt := stage6Runtimes[s]; rt != nil {
		return rt, nil
	}
	hubSvc, err := hub.NewService(hub.Config{
		DataDir:     s.dir,
		FixtureMode: s.providerFixture || s.dir == "",
		Now:         s.now,
	})
	if err != nil {
		return nil, err
	}
	swarmSvc, err := swarm.NewService(swarm.Config{
		DataDir:     s.dir,
		FixtureMode: s.providerFixture || s.dir == "",
		Now:         s.now,
	})
	if err != nil {
		_ = hubSvc.Close()
		return nil, err
	}
	rt = &stage6Runtime{hub: hubSvc, swarm: swarmSvc}
	stage6Runtimes[s] = rt
	return rt, nil
}

func (s *Store) swarmService() (*swarm.Service, error) {
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	return rt.swarm, nil
}

func (s *Store) closeStage6Runtime() error {
	if s == nil {
		return nil
	}
	stage6RuntimeMu.Lock()
	rt := stage6Runtimes[s]
	delete(stage6Runtimes, s)
	stage6RuntimeMu.Unlock()
	if rt == nil {
		return nil
	}
	var firstErr error
	if rt.hub != nil {
		firstErr = rt.hub.Close()
	}
	if rt.swarm != nil {
		if err := rt.swarm.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *HTTPHandler) handleStage6Route(w http.ResponseWriter, r *http.Request, token string) bool {
	switch r.URL.Path {
	case "/api/v1/hub/packages":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		view, err := rt.hub.List(r.Context())
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		writeJSON(w, 200, view)
		return true

	case "/api/v1/hub/publish":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return true
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return true
		}
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		var req hub.PublishRequest
		if !decodeJSON(w, r, &req) {
			return true
		}
		pkg, pubErr := rt.hub.Publish(r.Context(), req)
		if pubErr != nil {
			code := 400
			if errors.Is(pubErr, hub.ErrSignatureInvalid) {
				code = 403
			} else if errors.Is(pubErr, hub.ErrPackageTooLarge) {
				code = 413
			}
			writeError(w, code, pubErr.Error())
			return true
		}
		writeJSON(w, 200, pkg)
		return true

	case "/api/v1/hub/install":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return true
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return true
		}
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		var req hub.InstallRequest
		if !decodeJSON(w, r, &req) {
			return true
		}
		pkg, instErr := rt.hub.Install(r.Context(), req.ID)
		if instErr != nil {
			code := 400
			if errors.Is(instErr, hub.ErrSignatureInvalid) {
				code = 403
			} else if errors.Is(instErr, hub.ErrPackageNotFound) {
				code = 404
			}
			writeError(w, code, instErr.Error())
			return true
		}
		writeJSON(w, 200, pkg)
		return true

	case "/api/v1/swarm/status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return true
		}
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		st, err := rt.swarm.Status(r.Context())
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		writeJSON(w, 200, st)
		return true

	case "/api/v1/swarm/config":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return true
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return true
		}
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		var patch swarm.ConfigPatch
		if !decodeJSON(w, r, &patch) {
			return true
		}
		cfg, cfgErr := rt.swarm.SetConfig(r.Context(), patch)
		if cfgErr != nil {
			code := 400
			if errors.Is(cfgErr, swarm.ErrLiveCopyDisabled) {
				code = 403
			}
			writeError(w, code, cfgErr.Error())
			return true
		}
		writeJSON(w, 200, cfg)
		return true

	case "/api/v1/swarm/peers":
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		switch r.Method {
		case http.MethodGet:
			peers, err := rt.swarm.ListPeers(r.Context())
			if err != nil {
				writeError(w, 500, err.Error())
				return true
			}
			writeJSON(w, 200, map[string]any{"peers": peers, "count": len(peers)})
			return true
		case http.MethodPost:
			if !h.mutation(w, r) || !h.csrf(w, r, token) {
				return true
			}
			var in swarm.PeerInput
			if !decodeJSON(w, r, &in) {
				return true
			}
			peer, err := rt.swarm.AddPeer(r.Context(), in)
			if err != nil {
				writeError(w, 400, err.Error())
				return true
			}
			writeJSON(w, 200, peer)
			return true
		default:
			writeError(w, 405, "method not allowed")
			return true
		}

	case "/api/v1/swarm/signals":
		rt, err := h.store.ensureStage6Runtime()
		if err != nil {
			writeError(w, 500, err.Error())
			return true
		}
		switch r.Method {
		case http.MethodGet:
			limit := 25
			if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
				if v, pErr := strconv.Atoi(raw); pErr == nil && v > 0 && v <= 200 {
					limit = v
				}
			}
			signals, err := rt.swarm.ListSignals(r.Context(), limit)
			if err != nil {
				writeError(w, 500, err.Error())
				return true
			}
			writeJSON(w, 200, map[string]any{"signals": signals, "count": len(signals), "untrusted": true})
			return true
		case http.MethodPost:
			if !h.mutation(w, r) || !h.csrf(w, r, token) {
				return true
			}
			var raw map[string]any
			if !decodeJSON(w, r, &raw) {
				return true
			}
			reEncoded, _ := json.Marshal(raw)
			if _, hasSig := raw["signatureHex"]; hasSig {
				var env swarm.Envelope
				if err := json.Unmarshal(reEncoded, &env); err != nil {
					writeError(w, 400, err.Error())
					return true
				}
				ing, err := rt.swarm.IngestEnvelope(r.Context(), env)
				if err != nil {
					code := 400
					if errors.Is(err, swarm.ErrSwarmDisabled) || errors.Is(err, swarm.ErrInvalidSignature) || errors.Is(err, swarm.ErrReplayDetected) || errors.Is(err, swarm.ErrTimestampSkew) {
						code = 403
					}
					writeError(w, code, err.Error())
					return true
				}
				writeJSON(w, 200, ing)
				return true
			}
			var in swarm.SignalInput
			if err := json.Unmarshal(reEncoded, &in); err != nil {
				writeError(w, 400, err.Error())
				return true
			}
			env, err := rt.swarm.BroadcastSignal(r.Context(), in)
			if err != nil {
				code := 400
				if errors.Is(err, swarm.ErrSwarmDisabled) {
					code = 403
				}
				writeError(w, code, err.Error())
				return true
			}
			writeJSON(w, 200, env)
			return true
		default:
			writeError(w, 405, "method not allowed")
			return true
		}
	}
	return false
}

func toolListHubPackages(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	return rt.hub.List(ctx)
}

func toolGetSwarmStatus(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	return rt.swarm.Status(ctx)
}

func toolListSwarmSignals(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	signals, err := rt.swarm.ListSignals(ctx, 25)
	if err != nil {
		return nil, err
	}
	return map[string]any{"signals": signals, "count": len(signals), "untrusted": true}, nil
}

func previewDraftHubInstall(_ context.Context, _ *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var req hub.InstallRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return approvalDraft{}, err
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		return approvalDraft{}, errors.New("hub package id required")
	}
	payload, _ := json.Marshal(req)
	return approvalDraft{
		Title:       fmt.Sprintf("Install Verified Hub Package (%s)", req.ID),
		Summary:     "Verify Ed25519 signature and AST safety before installing (WEN SAT bond held).",
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftHubInstall(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var req hub.InstallRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	return rt.hub.Install(ctx, req.ID)
}

func previewDraftSwarmSignal(_ context.Context, _ *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var in swarm.SignalInput
	if err := decodeToolArgs(rawArgs, &in); err != nil {
		return approvalDraft{}, err
	}
	if strings.TrimSpace(in.Symbol) == "" || strings.TrimSpace(in.Thesis) == "" {
		return approvalDraft{}, errors.New("swarm signal symbol and thesis required")
	}
	payload, _ := json.Marshal(in)
	return approvalDraft{
		Title:       fmt.Sprintf("Draft Swarm Signal (%s %s)", strings.ToUpper(strings.TrimSpace(in.Direction)), strings.ToUpper(strings.TrimSpace(in.Symbol))),
		Summary:     "Broadcast signed advisory signal (untrusted=true, shadow/paper only).",
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftSwarmSignal(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var in swarm.SignalInput
	if err := decodeToolArgs(rawArgs, &in); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	return rt.swarm.BroadcastSignal(ctx, in)
}
