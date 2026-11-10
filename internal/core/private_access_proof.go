package core

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

// The proof is only a transient observation of the device that reached this
// instance through Tailscale. It creates no login, wallet or OS authority.
type PrivateAccessProof struct {
	State      string `json:"state"`
	InstanceID string `json:"instanceId"`
	UserID     int64  `json:"userId,omitempty"`
	NodeID     string `json:"nodeId,omitempty"`
	OwnerBound bool   `json:"ownerBound,omitempty"`
}

type PrivateAccessProofRequest struct {
	Action string `json:"action"`
	Token  string `json:"token"`
}

func (r PrivateAccessProofRequest) Validate() error {
	if r.Action != "begin" && r.Action != "check" && r.Action != "wait" {
		return errors.New("unsupported private access action")
	}
	_, err := privateAccessDigest(r.Token)
	return err
}

type privateAccessProofEntry struct {
	peer       tailnet.Identity
	expires    time.Time
	confirmed  bool
	ownerBound bool
	done       chan struct{}
}

type privateAccessProofCache struct {
	sync.Mutex
	entries map[[32]byte]privateAccessProofEntry
}

func privateAccessDigest(token string) ([32]byte, error) {
	var empty [32]byte
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != token {
		return empty, errors.New("invalid private access proof")
	}
	return sha256.Sum256(b), nil
}

func (h *HTTPHandler) privateAccessProofControl(w http.ResponseWriter, r *http.Request) {
	if !h.remoteIngress || h.instanceLocation != "remote" || r.Context().Value(privateIPCKey{}) != true || r.Context().Value(remoteIdentityKey{}) != nil {
		writeError(w, 403, "protected owner access required")
		return
	}
	if !h.mutation(w, r) {
		return
	}
	var request PrivateAccessProofRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Validate() != nil {
		writeError(w, 400, "invalid private access request")
		return
	}
	digest, err := privateAccessDigest(request.Token)
	if err != nil {
		writeError(w, 400, "invalid private access proof")
		return
	}
	// A bounded wait keeps the existing owner process and IPC open while the
	// user opens the link; it avoids rehashing both installed binaries every
	// second on small VPS hosts.
	if request.Action == "wait" {
		h.privateProof.Lock()
		entry, found := h.privateProof.entries[digest]
		h.privateProof.Unlock()
		if found && !entry.confirmed && h.store.now().Before(entry.expires) {
			timer := time.NewTimer(6 * time.Second)
			select {
			case <-entry.done:
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return
			}
			timer.Stop()
		}
	}
	now := h.store.now()
	h.privateProof.Lock()
	defer h.privateProof.Unlock()
	if h.privateProof.entries == nil {
		h.privateProof.entries = make(map[[32]byte]privateAccessProofEntry)
	}
	for key, entry := range h.privateProof.entries {
		if !now.Before(entry.expires) {
			delete(h.privateProof.entries, key)
		}
	}
	entry, found := h.privateProof.entries[digest]
	switch request.Action {
	case "begin":
		if found || len(h.privateProof.entries) >= 8 {
			writeError(w, 409, "private access proof unavailable")
			return
		}
		h.privateProof.entries[digest] = privateAccessProofEntry{expires: now.Add(2 * time.Minute), done: make(chan struct{})}
		writeJSON(w, 200, PrivateAccessProof{State: "pending", InstanceID: h.store.instanceID})
	case "check", "wait":
		if !found {
			writeError(w, 409, "private access proof expired or consumed")
			return
		}
		if !entry.confirmed {
			writeJSON(w, 200, PrivateAccessProof{State: "pending", InstanceID: h.store.instanceID})
			return
		}
		owner, err := h.store.remoteOwner(r.Context())
		if err != nil || owner.UserID == 0 && entry.ownerBound || owner.UserID != 0 && !remoteDeviceAllowed(owner, entry.peer) {
			delete(h.privateProof.entries, digest)
			writeError(w, 403, "device approval changed")
			return
		}
		delete(h.privateProof.entries, digest)
		writeJSON(w, 200, PrivateAccessProof{State: "verified", InstanceID: h.store.instanceID, UserID: entry.peer.UserID, NodeID: entry.peer.NodeID, OwnerBound: owner.UserID != 0})
	default:
		writeError(w, 400, "unsupported private access action")
	}
}

func (h *HTTPHandler) privateAccessProofConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity)
	if !h.remoteIngress || h.instanceLocation != "remote" || !ok || id.UserID <= 0 || id.NodeID == "" || len(id.NodeID) > 128 || r.Host != h.publicHost || r.Header.Get("Origin") != "https://"+h.publicHost {
		writeError(w, 403, "approved Tailscale device required")
		return
	}
	if !h.mutation(w, r) {
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	digest, err := privateAccessDigest(request.Token)
	if err != nil {
		writeError(w, 400, "invalid private access proof")
		return
	}
	owner, err := h.store.remoteOwner(r.Context())
	if err != nil || owner.UserID != 0 && !remoteDeviceAllowed(owner, id) {
		writeError(w, 403, "device approval required")
		return
	}
	h.privateProof.Lock()
	defer h.privateProof.Unlock()
	entry, found := h.privateProof.entries[digest]
	if !found || entry.confirmed || !h.store.now().Before(entry.expires) {
		writeError(w, 409, "private access proof unavailable")
		return
	}
	entry.peer, entry.confirmed, entry.ownerBound = id, true, owner.UserID != 0
	h.privateProof.entries[digest] = entry
	close(entry.done)
	w.WriteHeader(http.StatusNoContent)
}

const privateAccessProofScript = `const token=location.hash.slice(1);history.replaceState(null,"",location.pathname);fetch("/api/v1/private-access/confirm",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token})}).then(r=>{if(!r.ok)throw Error();location.replace("/")}).catch(()=>{document.body.textContent="Private access could not be verified."});`

func (h *HTTPHandler) privateAccessProofPage(w http.ResponseWriter, r *http.Request) {
	id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity)
	if !h.remoteIngress || h.instanceLocation != "remote" || !ok || id.UserID <= 0 || id.NodeID == "" || r.Host != h.publicHost || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256([]byte(privateAccessProofScript))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><title>Fased</title><body>Connecting…<script>", privateAccessProofScript, "</script></body></html>")
}
