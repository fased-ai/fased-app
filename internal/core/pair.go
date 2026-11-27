package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

const (
	// DefaultPairingLifetime bounds how long a one-time pairing code remains valid.
	DefaultPairingLifetime = 10 * time.Minute
	// MaxPairingLifetime caps caller-requested pairing code TTLs.
	MaxPairingLifetime = 30 * time.Minute

	maxPendingPairingCodes = 8
	maxPairedDevices       = 32
	pairingStateFile       = "pairing.json"
)

var pairedOwnerSalt = []byte("fased-paired-owner-v1")

// PairingCode is returned once when issuing a transport-agnostic Remote pairing grant.
type PairingCode struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	Label     string    `json:"label,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// PairedDevice describes an active browser/device session created via pairing.
type PairedDevice struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Transport  string    `json:"transport,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Current    bool      `json:"current,omitempty"`
}

type pairingCodeRecord struct {
	ID        string `json:"id"`
	CodeHash  string `json:"codeHash"`
	Label     string `json:"label,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

type pairedDeviceRecord struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Transport  string `json:"transport,omitempty"`
	NodeID     string `json:"nodeId,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt int64  `json:"lastSeenAt"`
	TokenHash  string `json:"tokenHash"`
}

type pairingState struct {
	Version int                  `json:"version"`
	Codes   []pairingCodeRecord  `json:"codes,omitempty"`
	Devices []pairedDeviceRecord `json:"devices,omitempty"`
}

func randomHexID(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sanitizePairLabel(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, raw)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if utf8.RuneCountInString(cleaned) > 80 {
		runes := []rune(cleaned)
		cleaned = strings.TrimSpace(string(runes[:80]))
	}
	return cleaned
}

func pairDeviceLabelFromUA(ua string) string {
	lower := strings.ToLower(ua)
	browser := "Browser"
	switch {
	case strings.Contains(lower, "edg/") || strings.Contains(lower, "edge/"):
		browser = "Edge"
	case strings.Contains(lower, "firefox/") && !strings.Contains(lower, "seamonkey"):
		browser = "Firefox"
	case strings.Contains(lower, "chrome/") && !strings.Contains(lower, "chromium"):
		browser = "Chrome"
	case strings.Contains(lower, "chromium/"):
		browser = "Chromium"
	case strings.Contains(lower, "safari/") && !strings.Contains(lower, "chrome"):
		browser = "Safari"
	case strings.Contains(lower, "playwright"):
		browser = "Playwright"
	case strings.Contains(lower, "curl/"):
		browser = "CLI"
	}
	osName := ""
	switch {
	case strings.Contains(lower, "iphone"):
		osName = "iPhone"
	case strings.Contains(lower, "ipad"):
		osName = "iPad"
	case strings.Contains(lower, "android"):
		osName = "Android"
	case strings.Contains(lower, "mac os x") || strings.Contains(lower, "macintosh"):
		osName = "macOS"
	case strings.Contains(lower, "windows"):
		osName = "Windows"
	case strings.Contains(lower, "linux"):
		osName = "Linux"
	}
	if osName == "" {
		return browser
	}
	return browser + " on " + osName
}

func (s *Store) loadPairingStateLocked() (pairingState, error) {
	path := filepath.Join(s.dir, pairingStateFile)
	data, err := readPrivateFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pairingState{Version: 1}, nil
		}
		return pairingState{}, err
	}
	var st pairingState
	if err = json.Unmarshal(data, &st); err != nil {
		return pairingState{}, err
	}
	if st.Version == 0 {
		st.Version = 1
	}
	return st, nil
}

func (s *Store) savePairingStateLocked(st pairingState) error {
	st.Version = 1
	return WritePrivateJSON(filepath.Join(s.dir, pairingStateFile), st)
}

func pruneExpiredCodes(codes []pairingCodeRecord, nowUnix int64) ([]pairingCodeRecord, bool) {
	kept := make([]pairingCodeRecord, 0, len(codes))
	changed := false
	for _, c := range codes {
		if c.ExpiresAt > nowUnix && c.CodeHash != "" && c.ID != "" {
			kept = append(kept, c)
		} else {
			changed = true
		}
	}
	if len(kept) > maxPendingPairingCodes {
		kept = kept[len(kept)-maxPendingPairingCodes:]
		changed = true
	}
	return kept, changed
}

func (s *Store) pruneStaleDevicesLocked(ctx context.Context, devices []pairedDeviceRecord, nowUnix int64) ([]pairedDeviceRecord, bool) {
	kept := make([]pairedDeviceRecord, 0, len(devices))
	changed := false
	for _, d := range devices {
		if d.ID == "" || d.TokenHash == "" {
			changed = true
			continue
		}
		var expires int64
		err := s.db.QueryRowContext(ctx, "SELECT expires FROM sessions WHERE token_hash=?", d.TokenHash).Scan(&expires)
		if err != nil || expires <= nowUnix {
			changed = true
			continue
		}
		kept = append(kept, d)
	}
	return kept, changed
}

// IssuePairingCode generates a single-use, time-bounded pairing code whose SHA-256
// digest is stored in pairing.json without exposing plaintext credentials at rest.
func (s *Store) IssuePairingCode(ctx context.Context, label string, ttl time.Duration) (PairingCode, error) {
	if ttl <= 0 {
		ttl = DefaultPairingLifetime
	}
	if ttl > MaxPairingLifetime {
		ttl = MaxPairingLifetime
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()

	code, err := randomToken()
	if err != nil {
		return PairingCode{}, err
	}
	id, err := randomHexID(8)
	if err != nil {
		return PairingCode{}, err
	}
	now := s.now()
	expiresAt := now.Add(ttl)

	st, err := s.loadPairingStateLocked()
	if err != nil {
		return PairingCode{}, err
	}
	st.Codes, _ = pruneExpiredCodes(st.Codes, now.Unix())
	if len(st.Codes) >= maxPendingPairingCodes {
		st.Codes = st.Codes[len(st.Codes)-maxPendingPairingCodes+1:]
	}
	cleanLabel := sanitizePairLabel(label)
	st.Codes = append(st.Codes, pairingCodeRecord{
		ID:        id,
		CodeHash:  tokenHash(code),
		Label:     cleanLabel,
		CreatedAt: now.Unix(),
		ExpiresAt: expiresAt.Unix(),
	})
	if err = s.savePairingStateLocked(st); err != nil {
		return PairingCode{}, err
	}
	return PairingCode{
		ID:        id,
		Code:      code,
		Label:     cleanLabel,
		ExpiresAt: expiresAt,
	}, nil
}

// ExchangePairingCode atomically consumes a single-use pairing code, claims an
// unclaimed instance without setting a password, and records the paired device session.
func (s *Store) ExchangePairingCode(ctx context.Context, code, deviceLabel, fallbackLabel, transport, nodeID string) (string, PairedDevice, error) {
	if len(code) != 43 {
		return "", PairedDevice{}, ErrUnauthorized
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()

	now := s.now()
	nowUnix := now.Unix()
	st, err := s.loadPairingStateLocked()
	if err != nil {
		return "", PairedDevice{}, err
	}

	wantHash := tokenHash(code)
	matchIdx := -1
	matchedLabel := ""
	activeCodes := make([]pairingCodeRecord, 0, len(st.Codes))
	codesChanged := false
	for _, rec := range st.Codes {
		if rec.ExpiresAt <= nowUnix || rec.CodeHash == "" || rec.ID == "" {
			codesChanged = true
			continue
		}
		if matchIdx == -1 && subtle.ConstantTimeCompare([]byte(rec.CodeHash), []byte(wantHash)) == 1 {
			matchIdx = len(activeCodes)
			matchedLabel = rec.Label
			codesChanged = true
			continue // consume on match
		}
		activeCodes = append(activeCodes, rec)
	}
	if matchIdx == -1 {
		if codesChanged {
			st.Codes = activeCodes
			_ = s.savePairingStateLocked(st)
		}
		return "", PairedDevice{}, ErrUnauthorized
	}

	// Consume the code on disk immediately so even an interrupted exchange cannot replay it.
	st.Codes = activeCodes
	if err = s.savePairingStateLocked(st); err != nil {
		return "", PairedDevice{}, err
	}

	token, err := randomToken()
	if err != nil {
		return "", PairedDevice{}, err
	}
	marker, err := randomToken()
	if err != nil {
		return "", PairedDevice{}, err
	}
	deviceID, err := randomHexID(8)
	if err != nil {
		return "", PairedDevice{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", PairedDevice{}, err
	}
	defer tx.Rollback()

	claimed, err := tx.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=?,claim_hash=NULL,claim_expires=NULL WHERE id=1 AND password_hash IS NULL", pairedOwnerSalt, []byte(tokenHash(marker)))
	if err != nil {
		return "", PairedDevice{}, metadataWriteError(err)
	}
	claimedNow, err := claimed.RowsAffected()
	if err != nil {
		return "", PairedDevice{}, err
	}
	if err = pruneSessionsLocked(ctx, tx, nowUnix, ""); err != nil {
		return "", PairedDevice{}, err
	}
	newTokenHash := tokenHash(token)
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires) VALUES(?,?)", newTokenHash, now.Add(SessionLifetime).Unix()); err != nil {
		return "", PairedDevice{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", PairedDevice{}, err
	}
	if claimedNow == 1 {
		_ = os.Remove(filepath.Join(s.dir, "claim-code.json"))
	}

	s.restoreOSProtectedConnections(ctx)

	label := sanitizePairLabel(deviceLabel)
	if label == "" {
		label = sanitizePairLabel(matchedLabel)
	}
	if label == "" {
		label = sanitizePairLabel(fallbackLabel)
	}
	if label == "" {
		label = "Device"
	}

	st.Devices, _ = s.pruneStaleDevicesLocked(ctx, st.Devices, nowUnix)
	if len(st.Devices) >= maxPairedDevices {
		evictCount := len(st.Devices) - maxPairedDevices + 1
		for i := 0; i < evictCount; i++ {
			_, _ = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", st.Devices[i].TokenHash)
		}
		st.Devices = st.Devices[evictCount:]
	}
	rec := pairedDeviceRecord{
		ID:         deviceID,
		Label:      label,
		Transport:  transport,
		NodeID:     nodeID,
		CreatedAt:  nowUnix,
		LastSeenAt: nowUnix,
		TokenHash:  newTokenHash,
	}
	st.Devices = append(st.Devices, rec)
	if err = s.savePairingStateLocked(st); err != nil {
		return "", PairedDevice{}, err
	}
	return token, PairedDevice{
		ID:         rec.ID,
		Label:      rec.Label,
		Transport:  rec.Transport,
		CreatedAt:  now,
		LastSeenAt: now,
		Current:    true,
	}, nil
}

// ListPairedDevices returns all active paired device sessions after pruning expired sessions.
func (s *Store) ListPairedDevices(ctx context.Context, currentToken string) ([]PairedDevice, string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	nowUnix := s.now().Unix()
	st, err := s.loadPairingStateLocked()
	if err != nil {
		return nil, "", err
	}
	var changed bool
	st.Codes, changed = pruneExpiredCodes(st.Codes, nowUnix)
	prunedDevices, devChanged := s.pruneStaleDevicesLocked(ctx, st.Devices, nowUnix)
	st.Devices = prunedDevices
	if changed || devChanged {
		if err = s.savePairingStateLocked(st); err != nil {
			return nil, "", err
		}
	}

	currentHash := ""
	if len(currentToken) == 43 {
		currentHash = tokenHash(currentToken)
	}
	out := make([]PairedDevice, 0, len(st.Devices))
	currentID := ""
	for _, d := range st.Devices {
		isCurrent := currentHash != "" && subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(currentHash)) == 1
		if isCurrent {
			currentID = d.ID
		}
		out = append(out, PairedDevice{
			ID:         d.ID,
			Label:      d.Label,
			Transport:  d.Transport,
			CreatedAt:  time.Unix(d.CreatedAt, 0).UTC(),
			LastSeenAt: time.Unix(d.LastSeenAt, 0).UTC(),
			Current:    isCurrent,
		})
	}
	return out, currentID, nil
}

// RevokePairedDevice removes a paired device entry and invalidates its active session immediately.
func (s *Store) RevokePairedDevice(ctx context.Context, deviceID string) error {
	_, err := s.revokePairedDeviceReturningHash(ctx, deviceID)
	return err
}

func (s *Store) revokePairedDeviceReturningHash(ctx context.Context, deviceID string) (string, error) {
	if deviceID == "" {
		return "", errors.New("device id required")
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()

	st, err := s.loadPairingStateLocked()
	if err != nil {
		return "", err
	}
	targetHash := ""
	remaining := make([]pairedDeviceRecord, 0, len(st.Devices))
	for _, d := range st.Devices {
		if d.ID == deviceID && targetHash == "" {
			targetHash = d.TokenHash
			continue
		}
		remaining = append(remaining, d)
	}
	if targetHash == "" {
		return "", errors.New("paired device not found")
	}
	st.Devices = remaining
	if err = s.savePairingStateLocked(st); err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", targetHash)
	return targetHash, err
}

// RevokeAllPairedDevices invalidates every active paired session on this instance.
func (s *Store) RevokeAllPairedDevices(ctx context.Context) (int, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	st, err := s.loadPairingStateLocked()
	if err != nil {
		return 0, err
	}
	count := len(st.Devices)
	hashes := make([]string, 0, len(st.Devices))
	for _, d := range st.Devices {
		if d.TokenHash != "" {
			hashes = append(hashes, d.TokenHash)
		}
	}
	st.Devices = nil
	if err = s.savePairingStateLocked(st); err != nil {
		return 0, err
	}
	for _, h := range hashes {
		if _, err = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", h); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (s *Store) matchPairedSessionNode(tokenHashValue, nodeID string) bool {
	if tokenHashValue == "" || nodeID == "" {
		return false
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()

	st, err := s.loadPairingStateLocked()
	if err != nil {
		return false
	}
	for _, d := range st.Devices {
		if d.NodeID == nodeID && subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(tokenHashValue)) == 1 {
			return true
		}
	}
	return false
}

func (s *Store) removePairedDeviceByTokenHash(tokenHashValue string) {
	if tokenHashValue == "" || s.dir == "" {
		return
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()

	st, err := s.loadPairingStateLocked()
	if err != nil {
		return
	}
	remaining := make([]pairedDeviceRecord, 0, len(st.Devices))
	changed := false
	for _, d := range st.Devices {
		if subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(tokenHashValue)) == 1 {
			changed = true
			continue
		}
		remaining = append(remaining, d)
	}
	if changed {
		st.Devices = remaining
		_ = s.savePairingStateLocked(st)
	}
}

func (h *HTTPHandler) effectiveRemoteTransport() string {
	if h.instanceLocation != "remote" {
		return ""
	}
	if h.remoteTransport != "" {
		return h.remoteTransport
	}
	if h.publicHost != "" {
		return "tailscale"
	}
	return "ssh"
}

func (h *HTTPHandler) publicOriginURL() string {
	if h.publicHost == "" {
		return ""
	}
	return "https://" + h.publicHost
}

func (h *HTTPHandler) pairIssueAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if r.Context().Value(privateIPCKey{}) != true || r.Context().Value(remoteIdentityKey{}) != nil {
		writeError(w, 403, "owner IPC access required")
		return
	}
	if !h.mutation(w, r) {
		return
	}
	var req struct {
		Label      string `json:"label"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ttl := DefaultPairingLifetime
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	issued, err := h.store.IssuePairingCode(r.Context(), req.Label, ttl)
	if err != nil {
		writeError(w, 500, "cannot issue pairing code")
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":              issued.ID,
		"code":            issued.Code,
		"label":           issued.Label,
		"expiresAt":       issued.ExpiresAt,
		"instanceId":      h.store.instanceID,
		"publicOrigin":    h.publicOriginURL(),
		"remoteTransport": h.effectiveRemoteTransport(),
	})
}

func (h *HTTPHandler) pairManageAPI(w http.ResponseWriter, r *http.Request) {
	if r.Context().Value(privateIPCKey{}) != true || r.Context().Value(remoteIdentityKey{}) != nil {
		writeError(w, 403, "owner IPC access required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		devices, _, err := h.store.ListPairedDevices(r.Context(), "")
		if err != nil {
			writeError(w, 500, "device list unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"devices": devices})
	case http.MethodPost:
		if !h.mutation(w, r) {
			return
		}
		var req struct {
			Action   string `json:"action"`
			DeviceID string `json:"deviceId"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		switch req.Action {
		case "revoke":
			revokedHash, err := h.store.revokePairedDeviceReturningHash(r.Context(), req.DeviceID)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			h.closeStreamsForSessionHash(revokedHash)
			writeJSON(w, 200, map[string]any{"revoked": 1})
		case "revoke-all":
			count, err := h.store.RevokeAllPairedDevices(r.Context())
			if err != nil {
				writeError(w, 500, "revocation failed")
				return
			}
			h.closeAllStreams()
			writeJSON(w, 200, map[string]any{"revoked": count})
		default:
			writeError(w, 400, "unsupported device action")
		}
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (h *HTTPHandler) pairExchangeAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if h.instanceLocation != "remote" && h.publicHost == "" {
		writeError(w, 403, "Remote pairing is unavailable on this instance")
		return
	}
	if !h.mutation(w, r) {
		return
	}
	if r.Header.Get("X-Fased-Pair") != "1" {
		writeError(w, 403, "pairing header confirmation required")
		return
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		writeError(w, 403, "same-origin request required")
		return
	}
	if !h.allowRate(r, "auth-pair", 8) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	select {
	case h.authSlots <- struct{}{}:
		defer func() { <-h.authSlots }()
	default:
		writeError(w, 429, "authentication is busy; retry shortly")
		return
	}
	var req struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	nodeID := ""
	transport := h.effectiveRemoteTransport()
	if id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity); ok && id.NodeID != "" {
		owner, err := h.store.remoteOwner(r.Context())
		if err != nil {
			writeError(w, 409, "Remote owner recovery required")
			return
		}
		if owner.UserID != 0 && owner.UserID != id.UserID {
			writeError(w, 403, "This device is not approved.")
			return
		}
		nodeID = id.NodeID
		transport = "tailscale"
	}
	fallbackLabel := pairDeviceLabelFromUA(r.Header.Get("User-Agent"))
	token, _, err := h.store.ExchangePairingCode(r.Context(), strings.TrimSpace(req.Code), req.Label, fallbackLabel, transport, nodeID)
	if err != nil {
		writeError(w, 401, "Invalid, expired, or already used pairing code.")
		return
	}
	if nodeID != "" {
		h.store.clearRemoteSignedOut(nodeID)
	}
	h.writeSessionResponse(w, r, token)
}

func (h *HTTPHandler) pairDevicesAPI(w http.ResponseWriter, r *http.Request, token string) {
	switch r.Method {
	case http.MethodGet:
		devices, current, err := h.store.ListPairedDevices(r.Context(), token)
		if err != nil {
			writeError(w, 500, "device list unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{
			"devices":      devices,
			"current":      current,
			"transport":    h.effectiveRemoteTransport(),
			"publicOrigin": h.publicOriginURL(),
		})
	case http.MethodPost:
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var req struct {
			Action     string `json:"action"`
			DeviceID   string `json:"deviceId"`
			Label      string `json:"label"`
			TTLSeconds int    `json:"ttlSeconds"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		switch req.Action {
		case "issue":
			if h.instanceLocation != "remote" && h.publicHost == "" {
				writeError(w, 403, "Remote pairing is unavailable on this instance")
				return
			}
			ttl := DefaultPairingLifetime
			if req.TTLSeconds > 0 {
				ttl = time.Duration(req.TTLSeconds) * time.Second
			}
			issued, err := h.store.IssuePairingCode(r.Context(), req.Label, ttl)
			if err != nil {
				writeError(w, 500, "cannot issue pairing code")
				return
			}
			origin := h.publicOriginURL()
			if origin == "" {
				origin = r.Header.Get("Origin")
			}
			if origin == "" {
				scheme := "http"
				if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
					scheme = "https"
				}
				origin = scheme + "://" + r.Host
			}
			pairURL := strings.TrimSuffix(origin, "/") + "/?instance=" + h.store.instanceID + "#pair=" + issued.Code
			writeJSON(w, 200, map[string]any{
				"id":        issued.ID,
				"code":      issued.Code,
				"label":     issued.Label,
				"expiresAt": issued.ExpiresAt,
				"url":       pairURL,
			})
		case "revoke":
			if req.DeviceID == "" {
				writeError(w, 400, "device id required")
				return
			}
			_, current, err := h.store.ListPairedDevices(r.Context(), token)
			if err != nil {
				writeError(w, 500, "device list unavailable")
				return
			}
			if current != "" && current == req.DeviceID {
				writeError(w, 400, "use sign out to disconnect this device")
				return
			}
			revokedHash, err := h.store.revokePairedDeviceReturningHash(r.Context(), req.DeviceID)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			h.closeStreamsForSessionHash(revokedHash)
			writeJSON(w, 200, map[string]bool{"revoked": true})
		default:
			writeError(w, 400, "unsupported device action")
		}
	default:
		writeError(w, 405, "method not allowed")
	}
}
