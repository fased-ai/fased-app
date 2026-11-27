package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/wenlv1x"
)

var (
	wenLV1XMu      sync.RWMutex
	wenLV1XEngines = map[*Store]*wenlv1x.Engine{}
)

func init() {
	registerStoreCloseHook(func(s *Store) {
		wenLV1XMu.Lock()
		eng := wenLV1XEngines[s]
		delete(wenLV1XEngines, s)
		wenLV1XMu.Unlock()
		if eng != nil {
			_ = eng.Close()
		}
	})
}

func (s *Store) loadedWENLV1XEngine() *wenlv1x.Engine {
	if s == nil {
		return nil
	}
	wenLV1XMu.RLock()
	eng := wenLV1XEngines[s]
	wenLV1XMu.RUnlock()
	return eng
}

func (s *Store) wenLV1XEngine() (*wenlv1x.Engine, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	if existing := s.loadedWENLV1XEngine(); existing != nil {
		return existing, nil
	}
	wenLV1XMu.Lock()
	defer wenLV1XMu.Unlock()
	if existing := wenLV1XEngines[s]; existing != nil {
		return existing, nil
	}
	cust, _ := s.custodyBackend()
	eng, err := wenlv1x.NewEngine(wenlv1x.Config{
		DataDir:     s.dir,
		Custody:     cust,
		FixtureMode: s.fixture != nil || !s.rpcAdmission,
	})
	if err != nil {
		return nil, err
	}
	wenLV1XEngines[s] = eng
	return eng, nil
}

func (h *HTTPHandler) miningLV1XAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "mining", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	eng, err := h.store.wenLV1XEngine()
	if err != nil {
		writeError(w, 500, "LV1X mining engine unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/mining"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	switch {
	case rel == "" || rel == "status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		st, sErr := eng.Status(ctx, r.URL.Query().Get("cluster"))
		if sErr != nil {
			writeError(w, 500, sErr.Error())
			return
		}
		writeJSON(w, 200, st)

	case rel == "round":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, eng.CurrentRound(ctx, r.URL.Query().Get("cluster")))

	case rel == "presets":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, map[string]any{"presets": wenlv1x.BuiltInPresets()})

	case rel == "history":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		streak, sErr := eng.ListStreak(ctx, limit)
		if sErr != nil {
			writeError(w, 500, sErr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"streak": streak})

	case rel == "preview":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.CommitRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		writeJSON(w, 200, eng.PreviewCommit(ctx, in))

	case rel == "commit":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.CommitRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		if !in.DryRunPreview && !in.Autonomous {
			if hasPW, _ := h.store.hasPassword(ctx); hasPW {
				if _, ok := h.requireStepUpCtx(w, r, token, "mining-commit", in.WalletID); !ok {
					return
				}
			}
		}
		rev, led, cErr := eng.SubmitCommit(ctx, in)
		if cErr != nil {
			code := 400
			if errors.Is(cErr, custody.ErrPolicyDenied) || errors.Is(cErr, custody.ErrRoleDenied) {
				code = 403
			}
			writeError(w, code, cErr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev, "ledger": led})

	case rel == "claim":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.ClaimRequest
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		rev, led, cErr := eng.SubmitClaim(ctx, in)
		if cErr != nil {
			code := 400
			if errors.Is(cErr, custody.ErrPolicyDenied) || errors.Is(cErr, custody.ErrRoleDenied) {
				code = 403
			}
			writeError(w, code, cErr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev, "ledger": led})

	case rel == "sessions":
		switch r.Method {
		case http.MethodGet:
			list, lErr := eng.ListSessions(ctx)
			if lErr != nil {
				writeError(w, 500, lErr.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"sessions": list})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in wenlv1x.SessionRecord
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			if in.Mode == "auto" {
				if hasPW, _ := h.store.hasPassword(ctx); hasPW {
					if _, ok := h.requireStepUpCtx(w, r, token, "mining-session", nonEmpty(in.WalletID, "session")); !ok {
						return
					}
				}
			}
			sess, sErr := eng.CreateSession(ctx, in)
			if sErr != nil {
				code := 400
				if errors.Is(sErr, custody.ErrPolicyDenied) {
					code = 403
				}
				writeError(w, code, sErr.Error())
				return
			}
			writeJSON(w, 201, map[string]any{"session": sess})
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "sessions/"):
		parts := strings.Split(rel, "/")
		if len(parts) != 3 || (parts[2] != "stop" && parts[2] != "close") {
			writeError(w, 404, "not found")
			return
		}
		if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
			return
		}
		nextStatus := "stopped"
		if parts[2] == "close" {
			nextStatus = "closed"
		}
		sess, sErr := eng.SetSessionStatus(ctx, parts[1], nextStatus)
		if sErr != nil {
			writeError(w, 404, sErr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"session": sess})

	case rel == "policy":
		cust, cErr := h.store.custodyBackend()
		if cErr != nil || cust == nil {
			writeError(w, 503, "custody signer unavailable")
			return
		}
		walletID := strings.TrimSpace(r.URL.Query().Get("walletId"))
		switch r.Method {
		case http.MethodGet:
			if walletID == "" {
				writeError(w, 400, "walletId required")
				return
			}
			wPub, gErr := cust.GetWallet(ctx, walletID)
			if gErr != nil {
				writeError(w, 404, gErr.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"wallet": wPub})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in struct {
				WalletID           string               `json:"walletId"`
				ApprovalMode       string               `json:"approvalMode"`
				AutoApproveMaxUSD  float64              `json:"autoApproveMaxUsd"`
				MaxPerTxLamports   uint64               `json:"maxPerTxLamports,string"`
				MaxDailyLamports   uint64               `json:"maxDailyLamports,string"`
				Mining             custody.MiningPolicy `json:"mining"`
				ConfirmLoosen      bool                 `json:"confirmLoosen"`
				ExpectedPolicyHash string               `json:"expectedPolicyHash"`
			}
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			if strings.TrimSpace(in.WalletID) == "" {
				writeError(w, 400, "walletId is required")
				return
			}
			if hasPW, _ := h.store.hasPassword(ctx); hasPW {
				if _, ok := h.requireStepUpCtx(w, r, token, "mining-policy", in.WalletID); !ok {
					return
				}
			}
			cur, gErr := cust.GetWallet(ctx, in.WalletID)
			if gErr != nil {
				writeError(w, 404, gErr.Error())
				return
			}
			nextPol := cur.Policy
			if in.ApprovalMode != "" {
				nextPol.ApprovalMode = in.ApprovalMode
			}
			if in.AutoApproveMaxUSD > 0 {
				nextPol.AutoApproveMaxUSD = in.AutoApproveMaxUSD
			}
			nextPol.Operations = []string{custody.MiningOpCommit, custody.MiningOpClaim, custody.MiningOpSession}
			prog := nonEmpty(in.Mining.ProgramID, onchain.LV1XDevnetProgramID)
			nextPol.Programs = []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget, prog}
			if in.MaxPerTxLamports > 0 {
				nextPol.SOLCap.MaxPerTxRaw = in.MaxPerTxLamports
			}
			if in.MaxDailyLamports > 0 {
				nextPol.SOLCap.MaxDailyRaw = in.MaxDailyLamports
			}
			nextPol.Mining = &in.Mining
			expHash := nonEmpty(in.ExpectedPolicyHash, cur.Policy.PolicyHash)
			upW, uErr := cust.UpdatePolicy(ctx, in.WalletID, nextPol, in.ConfirmLoosen, expHash, "owner")
			if uErr != nil {
				code := 400
				if errors.Is(uErr, custody.ErrPolicyDenied) || errors.Is(uErr, custody.ErrRoleDenied) {
					code = 403
				}
				writeError(w, code, uErr.Error())
				return
			}
			raw, _ := json.Marshal(upW)
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			writeJSON(w, 200, map[string]any{"wallet": out})
		default:
			writeError(w, 405, "method not allowed")
		}

	default:
		writeError(w, 404, "not found")
	}
}
