package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/fased-ai/fased-app/internal/desk"
	"github.com/fased-ai/fased-app/internal/hub"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/quantlab"
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
	swarmRelayHook = func(h *HTTPHandler, w http.ResponseWriter, r *http.Request) {
		h.handleSwarmRelay(w, r)
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
		DataDir:      s.dir,
		FixtureMode:  s.providerFixture || s.dir == "",
		Now:          s.now,
		SecretSource: s.taskSecrets,
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

func (s *Store) configureStage6MainnetCapped(maxOrderMicroUSD uint64) error {
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return err
	}
	return rt.swarm.ConfigureMainnetCapped(maxOrderMicroUSD)
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
		var req hub.InstallRequest
		if !decodeJSON(w, r, &req) {
			return true
		}
		pkg, instErr := h.store.installAndDeployHubPackage(r.Context(), req.ID)
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
					if errors.Is(err, swarm.ErrSwarmDisabled) || errors.Is(err, swarm.ErrInvalidSignature) || errors.Is(err, swarm.ErrReplayDetected) || errors.Is(err, swarm.ErrTimestampSkew) || errors.Is(err, swarm.ErrUnknownPeer) {
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

func previewDraftHubInstall(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var req hub.InstallRequest
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return approvalDraft{}, err
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		return approvalDraft{}, errors.New("hub package id required")
	}
	summary := "Inspect self-signed Ed25519 signature and payload before installing (untrusted community package, WEN SAT bond held)."
	payloadObj := map[string]any{"id": req.ID}
	if s != nil {
		if rt, err := s.ensureStage6Runtime(); err == nil && rt != nil && rt.hub != nil {
			if listView, lErr := rt.hub.List(ctx); lErr == nil {
				for _, pkg := range listView.Packages {
					if pkg.ID == req.ID {
						sum := sha256.Sum256([]byte(pkg.Payload))
						digest := hex.EncodeToString(sum[:8])
						snippet := strings.TrimSpace(pkg.Payload)
						if len(snippet) > 220 {
							snippet = snippet[:220] + "…"
						}
						authorKey := pkg.AuthorPubKeyHex
						if len(authorKey) > 16 {
							authorKey = authorKey[:16] + "…"
						}
						summary = fmt.Sprintf("kind=%s name=%q pubkey=%s payloadSha=%s preview=%q (self-signed community package)", pkg.Kind, pkg.Name, authorKey, digest, snippet)
						payloadObj["kind"] = pkg.Kind
						payloadObj["name"] = pkg.Name
						payloadObj["authorPubKeyHex"] = pkg.AuthorPubKeyHex
						payloadObj["payloadSha256"] = hex.EncodeToString(sum[:])
						payloadObj["payloadPreview"] = snippet
						break
					}
				}
			}
		}
	}
	payload, _ := json.Marshal(payloadObj)
	return approvalDraft{
		Title:       fmt.Sprintf("Install Community Hub Package (%s)", req.ID),
		Summary:     summary,
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftHubInstall(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var req struct {
		ID              string `json:"id"`
		Kind            string `json:"kind,omitempty"`
		Name            string `json:"name,omitempty"`
		AuthorPubKeyHex string `json:"authorPubKeyHex,omitempty"`
		PayloadSHA256   string `json:"payloadSha256,omitempty"`
		PayloadPreview  string `json:"payloadPreview,omitempty"`
	}
	if err := decodeToolArgs(rawArgs, &req); err != nil {
		return nil, err
	}
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return nil, err
	}
	verifiedPkg, err := rt.hub.GetVerified(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if req.PayloadSHA256 != "" {
		actualSum := sha256.Sum256([]byte(verifiedPkg.Payload))
		if !strings.EqualFold(hex.EncodeToString(actualSum[:]), strings.TrimSpace(req.PayloadSHA256)) {
			return nil, errors.New("hub package payload changed since approval preview (TOCTOU blocked)")
		}
	}
	if req.AuthorPubKeyHex != "" && !strings.EqualFold(verifiedPkg.AuthorPubKeyHex, strings.TrimSpace(req.AuthorPubKeyHex)) {
		return nil, errors.New("hub package author key changed since approval preview (TOCTOU blocked)")
	}
	return s.installAndDeployHubPackage(ctx, req.ID)
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

func (s *Store) installAndDeployHubPackage(ctx context.Context, packageID string) (hub.Package, error) {
	rt, err := s.ensureStage6Runtime()
	if err != nil {
		return hub.Package{}, err
	}
	verifiedPkg, err := rt.hub.GetVerified(ctx, packageID)
	if err != nil {
		return hub.Package{}, err
	}
	target, summary, err := s.deployHubPackage(ctx, verifiedPkg)
	if err != nil {
		return hub.Package{}, err
	}
	pkg, err := rt.hub.Install(ctx, packageID)
	if err != nil {
		return hub.Package{}, err
	}
	pkg.DeployedTarget = target
	pkg.DeployedSummary = summary
	return pkg, nil
}

func (s *Store) deployHubPackage(ctx context.Context, pkg hub.Package) (string, string, error) {
	switch pkg.Kind {
	case hub.KindFasedScript:
		ql, err := s.quantLabService()
		if err != nil {
			return "", "", err
		}
		if _, err := ql.SaveScript(ctx, quantlab.ScriptInput{
			ID:          pkg.ID,
			Name:        pkg.Name,
			Description: fmt.Sprintf("Installed from Community Hub (%s)", pkg.Version),
			Source:      pkg.Payload,
		}); err != nil {
			return "", "", err
		}
		return "quantlab", fmt.Sprintf("Saved FasedScript %s", pkg.ID), nil

	case hub.KindWatchlistPack:
		md, err := s.marketDataService()
		if err != nil {
			return "", "", err
		}
		var rawPack struct {
			Items   []marketdata.WatchlistItem `json:"items"`
			Venues  []string                   `json:"venues"`
			Symbols []string                   `json:"symbols"`
		}
		if err := json.Unmarshal([]byte(pkg.Payload), &rawPack); err != nil {
			return "", "", fmt.Errorf("invalid watchlist pack JSON: %w", err)
		}
		items := make([]marketdata.WatchlistItem, 0, len(rawPack.Items)+len(rawPack.Symbols))
		for _, it := range rawPack.Items {
			v, err := marketdata.NormalizeVenue(it.Venue)
			if err != nil {
				continue
			}
			sym, err := marketdata.NormalizeSymbol(it.Symbol)
			if err != nil {
				continue
			}
			items = append(items, marketdata.WatchlistItem{
				Venue:    v,
				Symbol:   sym,
				Label:    it.Label,
				Interval: it.Interval,
				Style:    it.Style,
			})
		}
		if len(items) == 0 && len(rawPack.Symbols) > 0 {
			cryptoVenue := marketdata.VenueHyperliquid
			hasAlpaca := false
			for _, rv := range rawPack.Venues {
				v, err := marketdata.NormalizeVenue(rv)
				if err != nil {
					continue
				}
				if v == marketdata.VenueAlpaca {
					hasAlpaca = true
				} else if cryptoVenue == marketdata.VenueHyperliquid && v != marketdata.VenueFRED && v != marketdata.VenueSEC {
					cryptoVenue = v
				}
			}
			equityTickers := map[string]bool{"SPY": true, "NVDA": true, "AAPL": true, "QQQ": true, "MSFT": true, "AMZN": true, "GOOGL": true, "META": true, "TSLA": true}
			for _, rs := range rawPack.Symbols {
				sym, err := marketdata.NormalizeSymbol(rs)
				if err != nil {
					continue
				}
				v := cryptoVenue
				if hasAlpaca && equityTickers[strings.ToUpper(sym)] {
					v = marketdata.VenueAlpaca
				}
				items = append(items, marketdata.WatchlistItem{Venue: v, Symbol: sym, Interval: "1h", Style: marketdata.StyleStandard})
			}
		}
		if len(items) == 0 {
			return "", "", errors.New("watchlist pack contained no valid items")
		}
		if _, err := md.SaveWatchlist(ctx, marketdata.Watchlist{
			ID:    pkg.ID,
			Name:  pkg.Name,
			Items: items,
		}); err != nil {
			return "", "", err
		}
		return "marketdata", fmt.Sprintf("Saved watchlist %s (%d items)", pkg.ID, len(items)), nil

	case hub.KindDeskBlueprint:
		ds, err := s.deskService()
		if err != nil {
			return "", "", err
		}
		var bp struct {
			Category string `json:"category"`
			Symbol   string `json:"symbol"`
			Rule     string `json:"rule"`
		}
		_ = json.Unmarshal([]byte(pkg.Payload), &bp)
		rule := strings.TrimSpace(bp.Rule)
		if rule == "" {
			rule = strings.TrimSpace(pkg.Name + ": " + pkg.Payload)
		}
		if len(rule) > 512 {
			rule = rule[:512]
		}
		cat := strings.TrimSpace(bp.Category)
		if cat == "" {
			cat = "entry"
		}
		if _, err := ds.SaveLesson(ctx, desk.LessonInput{
			ID:       pkg.ID,
			Category: cat,
			Symbol:   bp.Symbol,
			Rule:     rule,
		}); err != nil {
			return "", "", err
		}
		return "desk", fmt.Sprintf("Saved desk blueprint %s", pkg.ID), nil

	case hub.KindSkill:
		content := strings.TrimSpace(pkg.Payload)
		if len(content) > 16384 {
			content = content[:16384]
		}
		// Never promote third-party self-signed Hub skill packages into pinned global system prompts.
		if _, err := s.CreateMemory(ctx, MemoryInput{
			Scope:   "skill",
			Title:   pkg.Name,
			Content: content,
			Tags:    "hub,untrusted," + pkg.ID,
			Source:  "hub_untrusted",
			Pinned:  false,
		}); err != nil {
			return "", "", err
		}
		return "agent_memory", fmt.Sprintf("Saved untrusted hub skill %s", pkg.ID), nil

	default:
		return "", "", fmt.Errorf("unsupported hub package kind %q", pkg.Kind)
	}
}

// handleSwarmRelay admits unauthenticated peer-to-peer HTTP POSTs authenticated solely by
// Ed25519 signature against the local opt-in trusted swarm_peers allowlist + replay nonce checks.
func (h *HTTPHandler) handleSwarmRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.allowRate(r, "swarm-relay", 30) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	if ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))); !strings.HasPrefix(ct, "application/json") {
		writeError(w, 415, "JSON content type required")
		return
	}
	if h.store.dir != "" {
		if _, statErr := os.Stat(filepath.Join(h.store.dir, "swarm", "swarm.sqlite")); statErr != nil {
			writeError(w, 403, swarm.ErrSwarmDisabled.Error())
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var env swarm.Envelope
	if !decodeJSON(w, r, &env) {
		return
	}
	rt, err := h.store.ensureStage6Runtime()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	ing, ingErr := rt.swarm.IngestEnvelope(r.Context(), env)
	if ingErr != nil {
		code := 400
		if errors.Is(ingErr, swarm.ErrSwarmDisabled) || errors.Is(ingErr, swarm.ErrInvalidSignature) || errors.Is(ingErr, swarm.ErrReplayDetected) || errors.Is(ingErr, swarm.ErrTimestampSkew) || errors.Is(ingErr, swarm.ErrUnknownPeer) {
			code = 403
		}
		writeError(w, code, ingErr.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"accepted":   true,
		"envelopeId": ing.EnvelopeID,
		"kind":       ing.Kind,
		"untrusted":  true,
	})
}
