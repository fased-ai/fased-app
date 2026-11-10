package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestPrivateAccessDeviceProof(t *testing.T) {
	s := fixtureStore(t)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.owner.ts.net:8443", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress, h.instanceLocation = true, "remote"
	value := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	control := func(action string, ipc bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/private-access/control", strings.NewReader(`{"action":"`+action+`","token":"`+value+`"}`))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if ipc {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w
	}
	confirm := func(id tailnet.Identity, origin, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://node.owner.ts.net:8443/api/v1/private-access/confirm", strings.NewReader(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(r.Context(), remoteIdentityKey{}, id))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	peer := tailnet.Identity{UserID: 7, NodeID: "approved-device", Login: "owner@example.test"}
	if w := control("begin", false); w.Code != 403 {
		t.Fatal("public control admitted", w.Code)
	}
	if w := control("begin", true); w.Code != 200 {
		t.Fatal("begin", w.Code, w.Body)
	}
	spoof := httptest.NewRequest(http.MethodPost, "https://node.owner.ts.net:8443/api/v1/private-access/confirm", strings.NewReader(`{"token":"`+value+`"}`))
	spoof.Header.Set("Origin", "https://node.owner.ts.net:8443")
	spoof.Header.Set("Content-Type", "application/json")
	spoof.Header.Set("X-Forwarded-For", "100.101.102.103")
	spoofed := httptest.NewRecorder()
	h.ServeHTTP(spoofed, spoof)
	if spoofed.Code != 403 {
		t.Fatal("spoofed device accepted", spoofed.Code)
	}
	if w := confirm(peer, "https://foreign.ts.net", value); w.Code != 403 {
		t.Fatal("cross-origin", w.Code)
	}
	if w := confirm(peer, "https://node.owner.ts.net:8443", strings.Repeat("A", 43)); w.Code != 409 {
		t.Fatal("unregistered challenge", w.Code)
	}
	if w := confirm(peer, "https://node.owner.ts.net:8443", value); w.Code != 204 {
		t.Fatal("confirmation", w.Code, w.Body)
	}
	if w := confirm(peer, "https://node.owner.ts.net:8443", value); w.Code != 409 {
		t.Fatal("replay", w.Code)
	}
	w := control("check", true)
	var proof struct {
		State      string `json:"state"`
		UserID     int64  `json:"userId"`
		NodeID     string `json:"nodeId"`
		InstanceID string `json:"instanceId"`
		OwnerBound bool   `json:"ownerBound"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &proof) != nil || proof.State != "verified" || proof.UserID != peer.UserID || proof.NodeID != peer.NodeID || proof.InstanceID != s.instanceID || proof.OwnerBound {
		t.Fatal("proof", w.Code, w.Body)
	}
	if w = control("check", true); w.Code != 409 {
		t.Fatal("consumed proof reused", w.Code)
	}
}

func TestPrivateAccessProofRejectsRevokedDevice(t *testing.T) {
	s := fixtureStore(t)
	peer := tailnet.Identity{UserID: 7, NodeID: "owner-device", Login: "owner@example.test"}
	claim, err := s.IssueRemoteClaim(t.Context(), peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.remoteSession(t.Context(), peer, claim.Code, ""); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.owner.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress, h.instanceLocation = true, "remote"
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("c", 32)))
	control := func(action string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/private-access/control", strings.NewReader(`{"action":"`+action+`","token":"`+token+`"}`))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		privateIPCHandler(h).ServeHTTP(w, r)
		return w
	}
	if w := control("begin"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	r := httptest.NewRequest(http.MethodPost, "https://node.owner.ts.net/api/v1/private-access/confirm", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://node.owner.ts.net")
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), remoteIdentityKey{}, peer))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	owner := remoteOwner{UserID: 7, Devices: []tailnet.Identity{{UserID: 7, NodeID: "replacement", Login: "owner@example.test"}}}
	data, _ := json.Marshal(owner)
	if _, err = s.db.Exec("UPDATE remote_owner SET document=? WHERE id=1", string(data)); err != nil {
		t.Fatal(err)
	}
	if w = control("check"); w.Code != 403 {
		t.Fatal("revoked proof accepted", w.Code, w.Body)
	}
}

func TestPrivateAccessProofExpires(t *testing.T) {
	s := fixtureStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.owner.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress, h.instanceLocation = true, "remote"
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	call := func(action string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/private-access/control", strings.NewReader(`{"action":"`+action+`","token":"`+token+`"}`))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		privateIPCHandler(h).ServeHTTP(w, r)
		return w
	}
	if w := call("begin"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	s.now = func() time.Time { return now.Add(3 * time.Minute) }
	if w := call("check"); w.Code != 409 {
		t.Fatal("expired challenge", w.Code)
	}
}

func TestPrivateAccessProofWaitResumesOnDeviceConfirmation(t *testing.T) {
	s := fixtureStore(t)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.owner.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress, h.instanceLocation = true, "remote"
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("d", 32)))
	control := func(action string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/private-access/control", strings.NewReader(`{"action":"`+action+`","token":"`+token+`"}`))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		privateIPCHandler(h).ServeHTTP(w, r)
		return w
	}
	if w := control("begin"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- control("wait") }()
	r := httptest.NewRequest(http.MethodPost, "https://node.owner.ts.net/api/v1/private-access/confirm", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://node.owner.ts.net")
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), remoteIdentityKey{}, tailnet.Identity{UserID: 7, NodeID: "approved", Login: "owner@example.test"}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	select {
	case w = <-result:
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"verified"`) {
			t.Fatal(w.Code, w.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("device proof did not resume waiter")
	}
}
