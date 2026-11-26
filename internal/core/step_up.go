package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
)

const stepUpTTL = 90 * time.Second

type stepUpEntry struct {
	sessionHash string
	action      string
	walletID    string
	signerProof string
	expiresAt   time.Time
}

func normalizeStepUpScope(action, walletID string) (string, string) {
	act := strings.ToLower(strings.TrimSpace(action))
	wid := strings.TrimPrefix(strings.TrimSpace(walletID), "@wallet:")
	if wid == "" {
		wid = "*"
	}
	return act, wid
}

func writeAuthCodeError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, map[string]string{
		"error": message,
		"code":  code,
	})
}

func (h *HTTPHandler) ownerSessionToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	token := h.session(r)
	if !h.ownerSession(r, token) {
		writeError(w, 401, "owner authentication required")
		return "", false
	}
	return token, true
}

func (h *HTTPHandler) pruneStepUpLocked(now time.Time) {
	for k, v := range h.stepUpTokens {
		if !now.Before(v.expiresAt) {
			delete(h.stepUpTokens, k)
		}
	}
}

func (h *HTTPHandler) clearStepUpForSession(sessionToken string) {
	if h == nil || sessionToken == "" {
		return
	}
	sessHash := tokenHash(sessionToken)
	h.stepUpMu.Lock()
	defer h.stepUpMu.Unlock()
	for k, v := range h.stepUpTokens {
		if v.sessionHash == sessHash {
			delete(h.stepUpTokens, k)
		}
	}
}

func (h *HTTPHandler) issueStepUpToken(sessionToken, action, walletID, signerProof string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	now := time.Now()
	if h.store != nil && h.store.now != nil {
		now = h.store.now()
	}
	exp := now.Add(stepUpTTL)
	act, wid := normalizeStepUpScope(action, walletID)
	h.stepUpMu.Lock()
	if h.stepUpTokens == nil {
		h.stepUpTokens = make(map[string]stepUpEntry)
	}
	h.pruneStepUpLocked(now)
	h.stepUpTokens[tokenHash(tok)] = stepUpEntry{
		sessionHash: tokenHash(sessionToken),
		action:      act,
		walletID:    wid,
		signerProof: strings.TrimSpace(signerProof),
		expiresAt:   exp,
	}
	h.stepUpMu.Unlock()
	return tok, exp, nil
}

// requireStepUpCtx validates and atomically consumes a single-use, action-and-wallet-scoped
// step-up token, returning a context carrying the paired in-signer owner proof.
func (h *HTTPHandler) requireStepUpCtx(w http.ResponseWriter, r *http.Request, sessionToken, action, walletID string) (context.Context, bool) {
	if sessionToken == "" {
		writeError(w, 401, "owner authentication required")
		return nil, false
	}
	act, wid := normalizeStepUpScope(action, walletID)
	actLimit := 5
	if act == "policy" {
		actLimit = 8
	}
	// Enforce global sensitive ceiling first using isolated authLimiter, and key per-action
	// (never on unvalidated URL walletIDs) so fake wallet paths cannot exhaust rate buckets.
	if !h.allowAuthRate(r, "wallets-sensitive-total", 20) || !h.allowAuthRate(r, "wallets-sensitive:"+act, actLimit) {
		writeError(w, 429, ErrRateLimited.Error())
		return nil, false
	}
	hasPW, err := h.store.hasPassword(r.Context())
	if err != nil {
		writeError(w, 500, "auth state unavailable")
		return nil, false
	}
	if !hasPW {
		writeAuthCodeError(w, 403, "Set an owner password before authorizing sensitive wallet actions", "owner_password_required")
		return nil, false
	}
	presented := strings.TrimSpace(r.Header.Get("X-Fased-Step-Up"))
	if presented == "" {
		writeAuthCodeError(w, 403, "Owner password confirmation required", "step_up_required")
		return nil, false
	}
	now := time.Now()
	if h.store != nil && h.store.now != nil {
		now = h.store.now()
	}
	key := tokenHash(presented)
	sessHash := tokenHash(sessionToken)
	h.stepUpMu.Lock()
	h.pruneStepUpLocked(now)
	entry, ok := h.stepUpTokens[key]
	valid := ok && subtle.ConstantTimeCompare([]byte(entry.sessionHash), []byte(sessHash)) == 1 && now.Before(entry.expiresAt) &&
		entry.action == act && (entry.walletID == wid || entry.walletID == "*")
	if valid {
		// Single-use: consume immediately upon matching scope.
		delete(h.stepUpTokens, key)
	}
	h.stepUpMu.Unlock()
	if !valid {
		writeAuthCodeError(w, 403, "Step-up confirmation expired, already used, or invalid for this action; confirm owner password again", "step_up_required")
		return nil, false
	}
	ctx := r.Context()
	if entry.signerProof != "" {
		ctx = custody.WithOwnerProof(ctx, entry.signerProof)
	}
	return ctx, true
}

func (h *HTTPHandler) ownerPasswordAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	token, ok := h.ownerSessionToken(w, r)
	if !ok {
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	if !h.allowRate(r, "auth-password", 5) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !httpapi.DecodeStrictJSON(w, r, 4*1024, &req) {
		return
	}
	if err := h.store.setLocalOwnerPasswordKeeping(r.Context(), req.Password, tokenHash(token)); err != nil {
		if errors.Is(err, ErrClaimed) {
			writeError(w, 409, "owner password is already configured")
			return
		}
		writeError(w, 400, err.Error())
		return
	}
	h.clearStepUpForSession(token)
	// Mirror independent Argon2id owner verifier into fased-signer whenever custody backend is reachable.
	if cs, cErr := h.store.custodyBackend(); cErr == nil && cs != nil {
		if has, hErr := cs.HasOwnerAuth(r.Context()); hErr == nil && !has {
			if cfgErr := cs.ConfigureOwnerAuth(r.Context(), req.Password); cfgErr != nil && !errors.Is(cfgErr, custody.ErrOwnerAuthRequired) {
				writeError(w, 500, "failed to enroll signer owner authorization")
				return
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "hasPassword": true})
}

func (h *HTTPHandler) stepUpAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	token, ok := h.ownerSessionToken(w, r)
	if !ok {
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	if !h.allowRate(r, "auth-step-up", 10) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	hasPW, err := h.store.hasPassword(r.Context())
	if err != nil {
		writeError(w, 500, "auth state unavailable")
		return
	}
	if !hasPW {
		writeAuthCodeError(w, 403, "Set an owner password before authorizing sensitive wallet actions", "owner_password_required")
		return
	}
	var req struct {
		Password string `json:"password"`
		Action   string `json:"action"`
		WalletID string `json:"walletId"`
	}
	if !httpapi.DecodeStrictJSON(w, r, 4*1024, &req) {
		return
	}
	act, wid := normalizeStepUpScope(req.Action, req.WalletID)
	if act == "" {
		writeError(w, 400, "step-up action scope required")
		return
	}
	select {
	case h.authSlots <- struct{}{}:
		defer func() { <-h.authSlots }()
	default:
		writeError(w, 429, "authentication is busy; retry shortly")
		return
	}
	if err := h.store.VerifyOwnerPassword(r.Context(), req.Password); err != nil {
		writeAuthCodeError(w, 403, "incorrect owner password", "wrong_password")
		return
	}
	var signerProof string
	if cs, cErr := h.store.custodyBackend(); cErr == nil && cs != nil {
		proof, _, aErr := cs.AuthorizeOwnerAction(r.Context(), req.Password, act, wid)
		if aErr != nil {
			if errors.Is(aErr, custody.ErrWrongPassword) {
				writeAuthCodeError(w, 403, "incorrect signer owner password", "wrong_password")
				return
			}
			// If signer owner auth is not yet enrolled (e.g. legacy Stage 4 mainnet-capped route or
			// external signer awaiting owner-socket enrollment), omit signerProof so fased-signer
			// still fails closed for custody actions while non-custody step-up routes succeed.
			if !errors.Is(aErr, custody.ErrOwnerAuthRequired) {
				writeError(w, 500, "failed to obtain signer owner authorization")
				return
			}
		} else {
			signerProof = proof
		}
	}
	stepTok, exp, err := h.issueStepUpToken(token, act, wid, signerProof)
	if err != nil {
		writeError(w, 500, "failed to issue step-up confirmation")
		return
	}
	writeJSON(w, 200, map[string]any{
		"stepUpToken": stepTok,
		"action":      act,
		"walletId":    wid,
		"expiresAt":   exp.UTC().Unix(),
	})
}
