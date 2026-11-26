package core

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/httpapi"
)

const stepUpTTL = 5 * time.Minute

type stepUpEntry struct {
	sessionHash string
	expiresAt   time.Time
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

func (h *HTTPHandler) issueStepUpToken(sessionToken string) (string, time.Time, error) {
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
	h.stepUpMu.Lock()
	if h.stepUpTokens == nil {
		h.stepUpTokens = make(map[string]stepUpEntry)
	}
	h.pruneStepUpLocked(now)
	h.stepUpTokens[tokenHash(tok)] = stepUpEntry{
		sessionHash: tokenHash(sessionToken),
		expiresAt:   exp,
	}
	h.stepUpMu.Unlock()
	return tok, exp, nil
}

func (h *HTTPHandler) requireStepUp(w http.ResponseWriter, r *http.Request, sessionToken, scope string) bool {
	if sessionToken == "" {
		writeError(w, 401, "owner authentication required")
		return false
	}
	if !h.allowRate(r, "wallets-sensitive:"+scope, 5) {
		writeError(w, 429, ErrRateLimited.Error())
		return false
	}
	hasPW, err := h.store.hasPassword(r.Context())
	if err != nil {
		writeError(w, 500, "auth state unavailable")
		return false
	}
	if !hasPW {
		writeAuthCodeError(w, 403, "Set an owner password before authorizing sensitive wallet actions", "owner_password_required")
		return false
	}
	presented := strings.TrimSpace(r.Header.Get("X-Fased-Step-Up"))
	if presented == "" {
		writeAuthCodeError(w, 403, "Owner password confirmation required", "step_up_required")
		return false
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
	h.stepUpMu.Unlock()
	if !ok || entry.sessionHash != sessHash || !now.Before(entry.expiresAt) {
		writeAuthCodeError(w, 403, "Step-up confirmation expired or invalid; confirm owner password again", "step_up_required")
		return false
	}
	return true
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
	if err := h.store.SetLocalOwnerPassword(r.Context(), req.Password); err != nil {
		if errors.Is(err, ErrClaimed) {
			writeError(w, 409, "owner password is already configured")
			return
		}
		writeError(w, 400, err.Error())
		return
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
	if !h.allowRate(r, "auth-step-up", 5) {
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
	}
	if !httpapi.DecodeStrictJSON(w, r, 4*1024, &req) {
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
	stepTok, exp, err := h.issueStepUpToken(token)
	if err != nil {
		writeError(w, 500, "failed to issue step-up confirmation")
		return
	}
	writeJSON(w, 200, map[string]any{
		"stepUpToken": stepTok,
		"expiresAt":   exp.UTC().Unix(),
	})
}
