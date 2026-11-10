package core

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

const RemoteOwnerStateSchema = 38
const remoteOwnerFeature = "remote-device-owner-v1"

var remoteOwnerSalt = []byte("fased-tail-owner-v1")

type remoteIdentityKey struct{}
type remoteOwner struct {
	UserID        int64              `json:"userId"`
	Devices       []tailnet.Identity `json:"devices"`
	InviteHash    string             `json:"inviteHash,omitempty"`
	InviteExpires int64              `json:"inviteExpires,omitempty"`
}

func (s *Store) remoteOwner(ctx context.Context) (remoteOwner, error) {
	return readRemoteOwner(ctx, s.db)
}
func readRemoteOwner(ctx context.Context, r snapshotReader) (remoteOwner, error) {
	var version int
	if err := r.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return remoteOwner{}, err
	}
	if version < RemoteOwnerStateSchema {
		return remoteOwner{}, nil
	}
	var data string
	if err := r.QueryRowContext(ctx, "SELECT document FROM remote_owner WHERE id=1").Scan(&data); err != nil {
		return remoteOwner{}, err
	}
	var owner remoteOwner
	if json.Unmarshal([]byte(data), &owner) != nil || owner.UserID <= 0 || len(owner.Devices) == 0 {
		return owner, errors.New("Remote owner recovery required")
	}
	return owner, nil
}
func remoteDeviceAllowed(owner remoteOwner, id tailnet.Identity) bool {
	if owner.UserID != id.UserID {
		return false
	}
	for _, device := range owner.Devices {
		if device.NodeID == id.NodeID {
			return true
		}
	}
	return false
}

// First enrollment requires the protected initial handoff or the existing
// owner's authenticated session. A tailnet login alone cannot claim an instance.
// Old password hashes and session rows are never rewritten or deleted.
func (s *Store) remoteSession(ctx context.Context, id tailnet.Identity, code, existing string) (string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if id.UserID <= 0 || id.NodeID == "" || len(id.NodeID) > 128 || id.Login == "" {
		return "", ErrUnauthorized
	}
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
	owner, err := readRemoteOwner(ctx, tx)
	if err != nil {
		return "", err
	}
	if owner.UserID == 0 || !remoteDeviceAllowed(owner, id) {
		operation, _, e := readApplicationUpdate(ctx, tx)
		if e != nil {
			return "", e
		}
		if oneOf(operation.Phase, "applying", "recovery") {
			return "", errors.New("finish Update recovery before enrolling a device")
		}

		phase, _, e := readPause(ctx, tx)
		if e != nil {
			return "", e
		}
		if phase == "paused" {
			return "", ErrCorePaused
		}
	}
	if owner.UserID == 0 {
		var hash string
		var expires int64
		var password []byte
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(claim_hash,''),COALESCE(claim_expires,0),password_hash FROM instance WHERE id=1").Scan(&hash, &expires, &password); err != nil {
			return "", err
		}
		if password == nil {
			expected := remoteClaimScope(id) + tokenHash(code)
			// Plain old-format claims are retained only for authenticated legacy readers;
			// the new Remote wizard always issues a device-bound initial handoff.
			if len(code) != 43 || hash == "" || expires <= s.now().Unix() || subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) != 1 {
				return "", ErrUnauthorized
			}
		} else {
			var until int64
			if existing == "" || tx.QueryRowContext(ctx, "SELECT expires FROM sessions WHERE token_hash=?", tokenHash(existing)).Scan(&until) != nil || until <= s.now().Unix() {
				return "", ErrUnauthorized
			}
		}
		owner = remoteOwner{UserID: id.UserID, Devices: []tailnet.Identity{id}}
		document, _ := json.Marshal(owner)
		if _, err = tx.ExecContext(ctx, `CREATE TABLE remote_owner(id INTEGER PRIMARY KEY CHECK(id=1),document TEXT NOT NULL); CREATE TABLE remote_sessions(token_hash TEXT PRIMARY KEY REFERENCES sessions(token_hash) ON DELETE CASCADE,node_id TEXT NOT NULL); PRAGMA user_version=38;`); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO remote_owner VALUES(1,?)", string(document)); err != nil {
			return "", err
		}
		if password == nil {
			if _, err = tx.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=?,claim_hash=NULL,claim_expires=NULL WHERE id=1 AND password_hash IS NULL", remoteOwnerSalt, []byte(tokenHash(marker))); err != nil {
				return "", err
			}
		}
	} else if !remoteDeviceAllowed(owner, id) {
		if owner.UserID != id.UserID || len(owner.Devices) >= 16 || owner.InviteExpires <= s.now().Unix() || len(code) != 43 || subtle.ConstantTimeCompare([]byte(owner.InviteHash), []byte(tokenHash(code))) != 1 {
			return "", ErrUnauthorized
		}
		owner.Devices = append(owner.Devices, id)
		owner.InviteHash = ""
		owner.InviteExpires = 0
		data, _ := json.Marshal(owner)
		if _, err = tx.ExecContext(ctx, "UPDATE remote_owner SET document=? WHERE id=1", string(data)); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires) VALUES(?,?)", tokenHash(token), s.now().Add(SessionLifetime).Unix()); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO remote_sessions(token_hash,node_id) VALUES(?,?)", tokenHash(token), id.NodeID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}
func (h *HTTPHandler) ownerSession(r *http.Request, token string) bool {
	if h.custodyAccountsRequest(r) || h.custodyOnchainRequest(r) {
		return true
	}
	if !h.store.Session(r.Context(), token) {
		return false
	}
	id, private := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity)
	if !private {
		if r.Context().Value(privateIPCKey{}) == true {
			return true
		}
		owner, err := h.store.remoteOwner(r.Context())
		if err != nil {
			return false
		}
		if owner.UserID != 0 {
			var node string
			if h.store.db.QueryRowContext(r.Context(), "SELECT node_id FROM remote_sessions WHERE token_hash=?", tokenHash(token)).Scan(&node) == nil {
				return false
			}
		}
		return true
	}
	owner, err := h.store.remoteOwner(r.Context())
	if err != nil || !remoteDeviceAllowed(owner, id) {
		return false
	}
	var node string
	return h.store.db.QueryRowContext(r.Context(), "SELECT node_id FROM remote_sessions WHERE token_hash=?", tokenHash(token)).Scan(&node) == nil && node == id.NodeID
}
func (h *HTTPHandler) remoteOwnerAPI(w http.ResponseWriter, r *http.Request) {
	if !h.mutation(w, r) {
		return
	}
	id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity)
	if !ok {
		writeError(w, 403, "Open Fased through Tailscale.")
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	owner, err := h.store.remoteOwner(r.Context())
	existing := h.session(r)
	if err != nil {
		writeError(w, 409, "Remote owner recovery required")
		return
	}
	if owner.UserID == 0 && h.store.Session(r.Context(), existing) && !h.csrf(w, r, existing) {
		return
	}
	token, err := h.store.remoteSession(r.Context(), id, request.Code, existing)
	if err != nil {
		writeError(w, 401, "Use an approved device or a setup link.")
		return
	}
	h.setCookie(w, r, token, int(SessionLifetime.Seconds()))
	writeJSON(w, 200, map[string]any{"authenticated": true, "csrfToken": csrfToken(token), "sessionToken": token, "sessionMode": h.sessionMode(r)})
}

// Only the separate root-peer Unix ingress may supply proxy identity. Loopback
// HTTP, private maintenance IPC and caller-provided headers grant no such role.
func (h *HTTPHandler) remoteProxy(resolve tailnet.Resolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tailnet.Trusted(r) || r.Header.Get("Tailscale-Funnel-Request") != "" || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Host") != h.publicHost {
			writeError(w, 403, "Private Tailscale access required")
			return
		}
		peer := r.Header.Get("X-Forwarded-For")
		if peer == "" || strings.Contains(peer, ",") {
			writeError(w, 403, "Private client identity required")
			return
		}
		if r.URL.Path == "/api/v1/health" && r.Method == http.MethodGet && tailnet.PrivateAddress(peer) {
			copy := r.Clone(context.WithValue(r.Context(), remoteIdentityKey{}, tailnet.Identity{}))
			copy.Host = h.publicHost
			h.ServeHTTP(w, copy)
			return
		}
		id, err := resolve(r.Context(), peer)
		if err != nil {
			writeError(w, 403, "Connect your approved Tailscale device.")
			return
		}
		owner, err := h.store.remoteOwner(r.Context())
		if err != nil || r.URL.Path != "/api/v1/health" && owner.UserID != 0 && !remoteDeviceAllowed(owner, id) && !(id.UserID == owner.UserID && (r.URL.Path == "/api/v1/auth/remote" || r.URL.Path == "/api/v1/auth/state" || !strings.HasPrefix(r.URL.Path, "/api/"))) {
			writeError(w, 403, "This device is not approved.")
			return
		}
		// Unix Serve backends use Host: localhost. The daemon supplies the exact
		// external host separately; only restore it after verifying daemon provenance.
		copy := r.Clone(context.WithValue(r.Context(), remoteIdentityKey{}, id))
		copy.Host = h.publicHost
		h.ServeHTTP(w, copy)
	})
}

// Remote owner metadata participates in the existing snapshot/backup contract.
func remoteOwnerSnapshot(ctx context.Context, tx snapshotReader, version int) (string, error) {
	if version < RemoteOwnerStateSchema {
		return "", nil
	}
	var document string
	err := tx.QueryRowContext(ctx, "SELECT document FROM remote_owner WHERE id=1").Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		err = errors.New("Remote owner metadata missing")
	}
	return document, err
}

func (h *HTTPHandler) remoteDevicesAPI(w http.ResponseWriter, r *http.Request) {
	id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity)
	token := h.session(r)
	if !ok || !h.ownerSession(r, token) {
		writeError(w, 403, "Use your approved device.")
		return
	}
	if r.Method == http.MethodGet {
		owner, err := h.store.remoteOwner(r.Context())
		if err != nil {
			writeError(w, 500, "Device list unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"devices": owner.Devices, "current": id.NodeID})
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var request struct {
		Action string `json:"action"`
		NodeID string `json:"nodeId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	link, err := h.store.changeRemoteDevices(r.Context(), id, request.Action, request.NodeID)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	result := map[string]string{}
	if link != "" {
		result["url"] = "https://" + h.publicHost + "/?instance=" + h.store.instanceID + "#setup=" + link
	}
	writeJSON(w, 200, result)
}
func (s *Store) changeRemoteDevices(ctx context.Context, id tailnet.Identity, action, node string) (string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	owner, err := s.remoteOwner(ctx)
	if err != nil {
		return "", err
	}
	if !remoteDeviceAllowed(owner, id) {
		return "", ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	phase, _, err := readPause(ctx, tx)
	if err != nil {
		return "", err
	}
	if phase == "paused" {
		return "", ErrCorePaused
	}
	proof := ""
	switch action {
	case "invite":
		if len(owner.Devices) >= 16 {
			return "", errors.New("Remove an unused device first")
		}
		proof, err = randomToken()
		if err != nil {
			return "", err
		}
		owner.InviteHash = tokenHash(proof)
		owner.InviteExpires = s.now().Add(ClaimLifetime).Unix()
	case "revoke":
		if node == "" || node == id.NodeID {
			return "", errors.New("Use another approved device before removing this one")
		}
		found := false
		devices := make([]tailnet.Identity, 0, len(owner.Devices))
		for _, device := range owner.Devices {
			if device.NodeID == node {
				found = true
			} else {
				devices = append(devices, device)
			}
		}
		if !found {
			return "", errors.New("Device not found")
		}
		owner.Devices = devices
		if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM remote_sessions WHERE node_id=?)", node); err != nil {
			return "", err
		}
		// Revoking access also invalidates any outstanding enrollment link.
		owner.InviteHash = ""
		owner.InviteExpires = 0
	default:
		return "", errors.New("Unsupported device action")
	}
	document, _ := json.Marshal(owner)
	if _, err = tx.ExecContext(ctx, "UPDATE remote_owner SET document=? WHERE id=1", string(document)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return proof, nil
}

func remoteClaimScope(id tailnet.Identity) string {
	return "remote-v1:" + strconv.FormatInt(id.UserID, 10) + ":" + base64.RawURLEncoding.EncodeToString([]byte(id.NodeID)) + ":"
}

// IssueRemoteClaim is used only by the existing verified data-owner OS helper.
// It carries the predeployment owner/device binding through initial setup without
// adding a password or changing an existing claimed identity.
func (s *Store) IssueRemoteClaim(ctx context.Context, id tailnet.Identity) (ClaimCode, error) {
	if id.UserID <= 0 || id.NodeID == "" || len(id.NodeID) > 128 {
		return ClaimCode{}, ErrUnauthorized
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	code, err := randomToken()
	if err != nil {
		return ClaimCode{}, err
	}
	result := ClaimCode{Code: code, ExpiresAt: s.now().Add(ClaimLifetime)}
	query, err := s.db.ExecContext(ctx, "UPDATE instance SET claim_hash=?,claim_expires=? WHERE id=1 AND password_hash IS NULL", remoteClaimScope(id)+tokenHash(code), result.ExpiresAt.Unix())
	if err != nil {
		return ClaimCode{}, err
	}
	count, err := query.RowsAffected()
	if err != nil {
		return ClaimCode{}, err
	}
	if count != 1 {
		return ClaimCode{}, ErrClaimed
	}
	if err = WritePrivateJSON(filepath.Join(s.dir, "claim-code.json"), result); err != nil {
		return ClaimCode{}, err
	}
	return result, nil
}

// PendingRemoteClaim is read only by the verified setup helper when reopening
// interrupted initial setup. It preserves the predeployment identity binding.
func (s *Store) PendingRemoteClaim(ctx context.Context) (tailnet.Identity, error) {
	var hash string
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(claim_hash,'') FROM instance WHERE id=1 AND password_hash IS NULL").Scan(&hash); err != nil {
		return tailnet.Identity{}, err
	}
	parts := strings.Split(hash, ":")
	if len(parts) != 4 || parts[0] != "remote-v1" {
		return tailnet.Identity{}, ErrUnauthorized
	}
	user, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || user <= 0 {
		return tailnet.Identity{}, ErrUnauthorized
	}
	node, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(node) == 0 || len(node) > 128 {
		return tailnet.Identity{}, ErrUnauthorized
	}
	return tailnet.Identity{UserID: user, NodeID: string(node)}, nil
}

// A published predecessor's existing password session keeps its original
// private HTTP route only until guided same-instance Serve migration. It cannot
// use headers to acquire device authority, claim a new owner or bypass format38.
func (h *HTTPHandler) legacyRemoteAccess(ctx context.Context) bool {
	var version int
	var salt, hash []byte
	if h.store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil || version >= RemoteOwnerStateSchema {
		return false
	}
	if h.store.db.QueryRowContext(ctx, "SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&salt, &hash) != nil || len(hash) == 0 || bytes.Equal(salt, localOwnerSalt) || bytes.Equal(salt, remoteOwnerSalt) {
		return false
	}
	return true
}
