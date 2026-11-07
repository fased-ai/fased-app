package core

import (
	"bytes"
	"context"
	"crypto/subtle"
	"net/http"
	"time"
)

// Local authorization comes only from the existing owner-only OS socket, never
// from a loopback address or a caller-supplied header. Launch proofs live in
// memory, expire quickly, are single-use and cannot survive a core restart.
const localLaunchLifetime = 2 * time.Minute

var localOwnerSalt = []byte("fased-local-os-owner-v1")

func (s *Store) issueLocalLaunch(_ context.Context) (string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	code, err := randomToken()
	if err != nil {
		return "", err
	}
	s.localLaunchHash, s.localLaunchExpires = tokenHash(code), s.now().Add(localLaunchLifetime)
	return code, nil
}

func (s *Store) exchangeLocalLaunch(ctx context.Context, code string) (string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if len(code) != 43 || s.localLaunchHash == "" || !s.now().Before(s.localLaunchExpires) || subtle.ConstantTimeCompare([]byte(s.localLaunchHash), []byte(tokenHash(code))) != 1 {
		return "", ErrUnauthorized
	}
	token, err := s.localOwnerSessionLocked(ctx)
	if err == nil {
		s.localLaunchHash = ""
	}
	return token, err
}

func (s *Store) localOwnerSession(ctx context.Context) (string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return s.localOwnerSessionLocked(ctx)
}

func (s *Store) localOwnerSessionLocked(ctx context.Context) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	marker, err := randomToken()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// Preserve existing password identities byte-for-byte. On first Local setup,
	// mark the owner as claimed without creating a customer password. The random
	// hash has no usable password, including for older password-only readers.
	claimed, err := tx.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=?,claim_hash=NULL,claim_expires=NULL WHERE id=1 AND password_hash IS NULL", localOwnerSalt, []byte(tokenHash(marker)))
	if err != nil {
		return "", metadataWriteError(err)
	}
	count, err := claimed.RowsAffected()
	if err != nil {
		return "", err
	}
	if count == 1 {
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=37"); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", s.now().Unix()); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires) VALUES(?,?)", tokenHash(token), s.now().Add(SessionLifetime).Unix()); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func (h *HTTPHandler) localOwnerAllowed() bool {
	return h.instanceLocation == "local" && h.instanceOwnerUID == 0 && h.publicHost == "" && h.store.fixture == nil
}

func (h *HTTPHandler) localOwnerAPI(w http.ResponseWriter, r *http.Request) {
	if !h.mutation(w, r) {
		return
	}
	if !h.localOwnerAllowed() {
		writeError(w, 403, "Local OS owner access required")
		return
	}
	var request struct {
		Action string `json:"action"`
		Code   string `json:"code"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Action != "exchange" && r.Context().Value(privateIPCKey{}) != true {
		writeError(w, 403, "Local OS owner access required")
		return
	}
	var token string
	var err error
	switch request.Action {
	case "launch":
		var code string
		code, err = h.store.issueLocalLaunch(r.Context())
		if err == nil {
			writeJSON(w, 200, map[string]string{"code": code})
			return
		}
	case "session":
		token, err = h.store.localOwnerSession(r.Context())
	case "exchange":
		token, err = h.store.exchangeLocalLaunch(r.Context(), request.Code)
	default:
		writeError(w, 400, "unsupported Local access request")
		return
	}
	if err != nil {
		writeError(w, 401, "Reopen Fased to connect.")
		return
	}
	h.setCookie(w, r, token, int(SessionLifetime.Seconds()))
	writeJSON(w, 200, map[string]any{"authenticated": true, "csrfToken": csrfToken(token), "sessionToken": token, "sessionMode": h.sessionMode(r)})
}

func localPasswordDisabled(salt []byte) bool { return bytes.Equal(salt, localOwnerSalt) }

// Existing app-password sessions remain valid; an OS-authorized Local command
// can also obtain a session without a separate login or stored credential.
func (c *Client) LocalOwner(ctx context.Context) error {
	return c.authenticate(ctx, "/api/v1/auth/local", map[string]string{"action": "session"})
}

// This route is not ordinary LocalOwner and cannot be called over HTTP. Only
// the fixed OS-owner-approved app-identity helper reaches the private IPC entry.
func (h *HTTPHandler) nativeLocalOwnerAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	if r.Context().Value(privateIPCKey{}) != true || h.instanceLocation != "local" || h.instanceOwnerUID <= 0 || !h.store.custodyOwnerSupported || h.store.custodyNetwork == "" {
		writeError(w, 403, "protected native owner access required")
		return
	}
	if !h.mutation(w, r) {
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Action != "session" {
		writeError(w, 400, "one native session required")
		return
	}
	token, err := h.store.localOwnerSession(r.Context())
	if err != nil {
		writeError(w, 409, "native owner session unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"sessionToken": token, "csrfToken": csrfToken(token)})
}

func (c *Client) NativeLocalOwner(ctx context.Context) error {
	return c.authenticate(ctx, "/api/v1/auth/native-local", map[string]string{"action": "session"})
}
func (c *Client) LocalLaunch(ctx context.Context) (string, error) {
	var grant struct {
		Code string `json:"code"`
	}
	if err := c.Do(ctx, http.MethodPost, "/api/v1/auth/local", map[string]string{"action": "launch"}, &grant); err != nil {
		return "", err
	}
	if len(grant.Code) != 43 {
		return "", ErrUnauthorized
	}
	return grant.Code, nil
}
