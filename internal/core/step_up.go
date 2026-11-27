package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
)

const stepUpTTL = 90 * time.Second

type stepUpEntry struct {
	sessionHash   string
	action        string
	walletID      string
	bindingDigest string
	signerProof   string
	expiresAt     time.Time
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
	return h.issueStepUpTokenBound(sessionToken, action, walletID, "", signerProof)
}

func (h *HTTPHandler) issueStepUpTokenBound(sessionToken, action, walletID, bindingDigest, signerProof string) (string, time.Time, error) {
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
		sessionHash:   tokenHash(sessionToken),
		action:        act,
		walletID:      wid,
		bindingDigest: strings.ToLower(strings.TrimSpace(bindingDigest)),
		signerProof:   strings.TrimSpace(signerProof),
		expiresAt:     exp,
	}
	h.stepUpMu.Unlock()
	return tok, exp, nil
}

// requireStepUpCtx validates and atomically consumes a single-use, action-and-wallet-scoped
// step-up token, returning a context carrying the paired in-signer owner proof.
func (h *HTTPHandler) requireStepUpCtx(w http.ResponseWriter, r *http.Request, sessionToken, action, walletID string) (context.Context, bool) {
	return h.requireStepUpCtxBound(w, r, sessionToken, action, walletID, "")
}

// requireStepUpCtxBound validates and atomically consumes a single-use step-up token,
// verifying cryptographic digest binding (txDigest or nextPolicyHash) whenever present.
func (h *HTTPHandler) requireStepUpCtxBound(w http.ResponseWriter, r *http.Request, sessionToken, action, walletID, expectedBindingDigest string) (context.Context, bool) {
	if sessionToken == "" {
		writeError(w, 401, "owner authentication required")
		return nil, false
	}
	act, wid := normalizeStepUpScope(action, walletID)
	wantDigest := strings.ToLower(strings.TrimSpace(expectedBindingDigest))
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
	hasPasskey := false
	if !hasPW {
		if cs, cErr := h.store.custodyBackend(); cErr == nil && cs != nil {
			if pks, pErr := cs.ListOwnerPasskeys(r.Context()); pErr == nil && len(pks) > 0 {
				hasPasskey = true
			}
		}
	}
	if !hasPW && !hasPasskey {
		writeAuthCodeError(w, 403, "Set an owner password or register a passkey before authorizing sensitive wallet actions", "owner_password_required")
		return nil, false
	}
	presented := strings.TrimSpace(r.Header.Get("X-Fased-Step-Up"))
	if presented == "" {
		writeAuthCodeError(w, 403, "Owner confirmation required", "step_up_required")
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
	digestOK := entry.bindingDigest == "" || subtle.ConstantTimeCompare([]byte(entry.bindingDigest), []byte(wantDigest)) == 1
	valid := ok && subtle.ConstantTimeCompare([]byte(entry.sessionHash), []byte(sessHash)) == 1 && now.Before(entry.expiresAt) &&
		entry.action == act && (entry.walletID == wid || entry.walletID == "*") && digestOK
	if valid {
		// Single-use: consume immediately upon matching scope.
		delete(h.stepUpTokens, key)
	}
	h.stepUpMu.Unlock()
	if !valid {
		writeAuthCodeError(w, 403, "Step-up confirmation expired, already used, or invalid for this action; confirm again", "step_up_required")
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
	h.closeStreamsExceptSessionHash(tokenHash(token))
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

func (h *HTTPHandler) webAuthnRPIDAndOrigin(r *http.Request) (string, string, error) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		if pub := h.publicOriginURL(); pub != "" && strings.EqualFold(r.Host, h.publicHost) {
			origin = pub
		} else {
			scheme := "http"
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				scheme = "https"
			}
			origin = scheme + "://" + r.Host
		}
	}
	if !h.origins[origin] {
		return "", "", errors.New("origin not permitted for WebAuthn")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return "", "", errors.New("invalid WebAuthn origin")
	}
	return strings.ToLower(u.Hostname()), origin, nil
}

// passkeysAPI manages FIDO2/WebAuthn passkeys stored directly inside fased-signer.
// fased-app acts solely as an authenticated relay; attestation verification and public keys
// live exclusively inside fased-signer.
func (h *HTTPHandler) passkeysAPI(w http.ResponseWriter, r *http.Request) {
	token, ok := h.ownerSessionToken(w, r)
	if !ok {
		return
	}
	cs, err := h.store.custodyBackend()
	if err != nil || cs == nil {
		writeError(w, 503, "custody signer unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/auth/passkeys"), "/")
	switch {
	case rel == "":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		list, err := cs.ListOwnerPasskeys(r.Context())
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"passkeys": list, "count": len(list)})

	case rel == "register/begin":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		if !h.allowAuthRate(r, "auth-passkey-reg", 8) {
			writeError(w, 429, ErrRateLimited.Error())
			return
		}
		var req struct {
			Label string `json:"label"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 4*1024, &req) {
			return
		}
		rpID, origin, err := h.webAuthnRPIDAndOrigin(r)
		if err != nil {
			writeError(w, 403, err.Error())
			return
		}
		callCtx := r.Context()
		hasPW, _ := h.store.hasPassword(r.Context())
		if hasPW {
			var stepOK bool
			callCtx, stepOK = h.requireStepUpCtx(w, r, token, "passkey-register", "*")
			if !stepOK {
				return
			}
		}
		opts, err := cs.BeginPasskeyRegistration(callCtx, rpID, origin, req.Label)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, opts)

	case rel == "register/finish":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		if !h.allowAuthRate(r, "auth-passkey-reg", 8) {
			writeError(w, 429, ErrRateLimited.Error())
			return
		}
		var req custody.PasskeyRegisterFinishRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &req) {
			return
		}
		callCtx := r.Context()
		hasPW, _ := h.store.hasPassword(r.Context())
		if hasPW {
			var stepOK bool
			callCtx, stepOK = h.requireStepUpCtx(w, r, token, "passkey-register", "*")
			if !stepOK {
				return
			}
		}
		pk, err := cs.FinishPasskeyRegistration(callCtx, req)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"passkey": pk})

	case !strings.Contains(rel, "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		authCtx, stepOK := h.requireStepUpCtx(w, r, token, "passkey-delete", "*")
		if !stepOK {
			return
		}
		credID, err := url.PathUnescape(rel)
		if err != nil {
			writeError(w, 400, "invalid credential id")
			return
		}
		deleted, err := cs.DeleteOwnerPasskey(authCtx, credID)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		if !deleted {
			writeError(w, 404, "passkey not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "credentialId": credID})

	default:
		writeError(w, 404, "unknown passkey operation")
	}
}

// stepUpPasskeyAPI relays WebAuthn/FIDO2 assertion challenges and signed assertions between the
// browser and fased-signer, binding every proof to exact (action, walletId, bindingDigest) so
// zero plaintext passwords ever transit fased-app.
func (h *HTTPHandler) stepUpPasskeyAPI(w http.ResponseWriter, r *http.Request) {
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
	if !h.allowAuthRate(r, "auth-step-up-passkey", 12) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	cs, err := h.store.custodyBackend()
	if err != nil || cs == nil {
		writeError(w, 503, "custody signer unavailable")
		return
	}

	switch r.URL.Path {
	case "/api/v1/auth/step-up/passkey/begin":
		var req struct {
			Action        string `json:"action"`
			WalletID      string `json:"walletId"`
			BindingDigest string `json:"bindingDigest"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 4*1024, &req) {
			return
		}
		rpID, origin, err := h.webAuthnRPIDAndOrigin(r)
		if err != nil {
			writeError(w, 403, err.Error())
			return
		}
		opts, err := cs.BeginPasskeyAssertion(r.Context(), rpID, origin, req.Action, req.WalletID, req.BindingDigest)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, opts)

	case "/api/v1/auth/step-up/passkey/finish":
		var req struct {
			Action            string `json:"action"`
			WalletID          string `json:"walletId"`
			Challenge         string `json:"challenge"`
			CredentialID      string `json:"credentialId"`
			ClientDataJSON    string `json:"clientDataJSON"`
			AuthenticatorData string `json:"authenticatorData"`
			Signature         string `json:"signature"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &req) {
			return
		}
		act, wid := normalizeStepUpScope(req.Action, req.WalletID)
		if act == "" {
			writeError(w, 400, "step-up action scope required")
			return
		}
		proof, _, bindingDigest, err := cs.FinishPasskeyAssertion(r.Context(), custody.PasskeyAssertFinishRequest{
			Challenge:         req.Challenge,
			CredentialID:      req.CredentialID,
			ClientDataJSON:    req.ClientDataJSON,
			AuthenticatorData: req.AuthenticatorData,
			Signature:         req.Signature,
		})
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		stepTok, exp, err := h.issueStepUpTokenBound(token, act, wid, bindingDigest, proof)
		if err != nil {
			writeError(w, 500, "failed to issue step-up confirmation")
			return
		}
		writeJSON(w, 200, map[string]any{
			"stepUpToken":   stepTok,
			"action":        act,
			"walletId":      wid,
			"bindingDigest": bindingDigest,
			"method":        "passkey",
			"expiresAt":     exp.UTC().Unix(),
		})

	default:
		writeError(w, 404, "unknown passkey step-up route")
	}
}
