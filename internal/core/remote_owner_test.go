package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestRemoteOwnerEnrollmentPreservationAndReplay(t *testing.T) {
	t.Parallel()
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "legacy"}[legacy], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "fased")
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			code, err := s.IssueClaimCode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var oldToken string
			var oldSalt, oldHash []byte
			if legacy {
				oldToken, err = s.Claim(t.Context(), code.Code, "existing-password-retained")
				if err != nil {
					t.Fatal(err)
				}
				if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&oldSalt, &oldHash); err != nil {
					t.Fatal(err)
				}
			}
			id := tailnet.Identity{UserID: 7, NodeID: "node-first", Login: "owner@example.test"}
			if _, err = s.remoteSession(t.Context(), id, "bad", ""); err == nil {
				t.Fatal("tailnet membership claimed owner")
			}
			if !legacy {
				code, err = s.IssueRemoteClaim(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
			}
			token, err := s.remoteSession(t.Context(), id, code.Code, oldToken)
			if err != nil {
				t.Fatal(err)
			}
			var schema int
			s.db.QueryRow("PRAGMA user_version").Scan(&schema)
			if schema != RemoteOwnerStateSchema {
				t.Fatal(schema)
			}
			if legacy {
				var salt, hash []byte
				s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&salt, &hash)
				if string(salt) != string(oldSalt) || string(hash) != string(oldHash) || !s.Session(t.Context(), oldToken) {
					t.Fatal("legacy identity/session changed")
				}
			}
			h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.tail-test.ts.net", nil)
			if err != nil {
				t.Fatal(err)
			}
			request := func(id tailnet.Identity) *http.Request {
				return httptest.NewRequest("GET", "https://node.tail-test.ts.net/api/v1/setup", nil).WithContext(context.WithValue(t.Context(), remoteIdentityKey{}, id))
			}
			if !h.ownerSession(request(id), token) {
				t.Fatal("approved device denied")
			}
			other := id
			other.NodeID = "node-second"
			if h.ownerSession(request(other), token) {
				t.Fatal("session replayed by another device")
			}
			if _, err = s.remoteSession(t.Context(), other, code.Code, oldToken); err == nil {
				t.Fatal("old claim or password session enrolled another device")
			}
			// Restart preserves owner/device/session and snapshot requirements.
			before, err := s.UpdateSnapshot(t.Context(), strings.Repeat("a", 32))
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			after, err := s.UpdateSnapshot(t.Context(), strings.Repeat("a", 32))
			if err != nil || before.State.Anchor != after.State.Anchor {
				t.Fatal("owner snapshot drift", err)
			}
			owner, err := s.remoteOwner(t.Context())
			if err != nil || !remoteDeviceAllowed(owner, id) || !s.Session(t.Context(), token) {
				t.Fatal(owner, err)
			}
			// Revocation changes snapshot identity and immediately rejects a saved token.
			owner.Devices = []tailnet.Identity{other}
			data, _ := json.Marshal(owner)
			s.db.Exec("UPDATE remote_owner SET document=? WHERE id=1", string(data))
			h.store = s
			if h.ownerSession(request(id), token) {
				t.Fatal("revoked device retained session")
			}
			revoked, err := s.UpdateSnapshot(t.Context(), strings.Repeat("a", 32))
			if err != nil || revoked.State.Anchor == after.State.Anchor {
				t.Fatal("device revocation missing from snapshot", err)
			}
		})
	}
}

func TestRemotePackageSetupUsesPrivateIPCAndOriginalDevice(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "fased"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.tail-test.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation, h.instanceOwnerUID, h.instanceOwnerKind, h.remoteIngress = "remote", 1000, "custody", true
	s.protectedOwnerUID = 1000
	request := func(peer tailnet.Identity, ipc bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"device": peer})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/remote-setup", bytes.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		if ipc {
			r = r.WithContext(context.WithValue(r.Context(), privateIPCKey{}, true))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	owner := tailnet.Identity{UserID: 7, NodeID: "approved-device", Login: "owner@example.test"}
	if w := request(owner, false); w.Code != http.StatusForbidden {
		t.Fatalf("ordinary HTTP gained Remote setup: %d", w.Code)
	}
	w := request(owner, true)
	var result struct{ InstanceID, Code string }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.InstanceID != s.Health().InstanceID || len(result.Code) != 43 {
		t.Fatalf("approved setup did not reach the existing instance: HTTP %d", w.Code)
	}
	other := owner
	other.NodeID = "different-device"
	if w = request(other, true); w.Code == http.StatusOK {
		t.Fatal("a different device replaced the pending owner")
	}
	if w = request(owner, true); w.Code != http.StatusOK {
		t.Fatalf("the original device could not retry: %d", w.Code)
	}
}

func TestRemoteClaimRetryCannotReplacePendingDevice(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	first := tailnet.Identity{UserID: 7, NodeID: "first", Login: "owner@example.test"}
	other := first
	other.NodeID = "other"
	if _, err := s.IssueRemoteClaim(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow("SELECT claim_hash FROM instance WHERE id=1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueRemoteClaim(t.Context(), other); err == nil {
		t.Fatal("different device replaced pending claimant")
	}
	var after string
	if err := s.db.QueryRow("SELECT claim_hash FROM instance WHERE id=1").Scan(&after); err != nil || before != after {
		t.Fatal("pending claim changed", err)
	}
	if _, err := s.IssueRemoteClaim(t.Context(), first); err != nil {
		t.Fatal("original device retry refused", err)
	}
}

func TestStaleClaimWriterCannotRemoveRemoteBinding(t *testing.T) {
	t.Parallel()
	first := fixtureStore(t)
	second, err := OpenStore(first.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var old string
	if err = first.db.QueryRow("SELECT COALESCE(claim_hash,'') FROM instance WHERE id=1").Scan(&old); err != nil {
		t.Fatal(err)
	}
	device := tailnet.Identity{UserID: 7, NodeID: "device", Login: "owner@example.test"}
	if _, err = second.IssueRemoteClaim(t.Context(), device); err != nil {
		t.Fatal(err)
	}
	updated, err := first.compareAndSetUnclaimedCode(t.Context(), old, "stale-unscoped-code", 1)
	if err != nil || updated {
		t.Fatal("stale writer replaced protected claim", err)
	}
	if _, err = first.IssueClaimCode(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := second.PendingRemoteClaim(t.Context())
	if err != nil || got.UserID != device.UserID || got.NodeID != device.NodeID {
		t.Fatal("claim rotation lost device binding", err)
	}
}
func TestRemoteHeadersNeverGrantLocalOrProxyAuthority(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "fased"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.tail-test.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress = true
	calls := 0
	proxy := h.remoteProxy(func(context.Context, string) (tailnet.Identity, error) {
		calls++
		return tailnet.Identity{UserID: 7, NodeID: "node-first", Login: "owner@example.test"}, nil
	})
	for _, handler := range []http.Handler{h, privateIPCHandler(h), proxy} {
		r := httptest.NewRequest("GET", "https://node.tail-test.ts.net/api/v1/auth/state", nil)
		r.Header.Set("X-Forwarded-For", "100.101.102.103")
		r.Header.Set("X-Forwarded-Host", "node.tail-test.ts.net")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Tailscale-User-Login", "owner@example.test")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("spoofed proxy acquired authority: %d", w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("untrusted request reached daemon")
	}
}
func TestRemoteExpiredHandoffAndPausedEnrollment(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "fased"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	code, _ := s.IssueClaimCode(t.Context())
	now := s.now()
	s.now = func() time.Time { return now.Add(ClaimLifetime + time.Second) }
	_, err = s.remoteSession(t.Context(), tailnet.Identity{UserID: 7, NodeID: "node-first", Login: "owner@example.test"}, code.Code, "")
	if err == nil {
		t.Fatal("expired handoff accepted")
	}
}

func TestRemoteEnrollmentRespectsDurablePause(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "fased"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	code, _ := s.IssueClaimCode(t.Context())
	legacy, err := s.Claim(t.Context(), code.Code, "preserved-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	pause := pauseCommand(t, s, 1)
	if _, err = s.UpdatePause(t.Context(), pause); err != nil {
		t.Fatal(err)
	}
	before, err := s.UpdateSnapshot(t.Context(), pause.ProbeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.remoteSession(t.Context(), tailnet.Identity{UserID: 7, NodeID: "node-first", Login: "owner@example.test"}, "", legacy); !errors.Is(err, ErrCorePaused) {
		t.Fatal("enrollment escaped pause", err)
	}
	after, err := s.UpdateSnapshot(t.Context(), pause.ProbeID)
	if err != nil || before.State.Anchor != after.State.Anchor {
		t.Fatal("paused identity changed", err)
	}
}
func TestRemoteReenrollmentCannotResurrectRevokedToken(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "fased"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := tailnet.Identity{UserID: 7, NodeID: "first", Login: "owner@example.test"}
	second := first
	second.NodeID = "second"
	code, _ := s.IssueRemoteClaim(t.Context(), first)
	one, err := s.remoteSession(t.Context(), first, code.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := s.changeRemoteDevices(t.Context(), first, "invite", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.remoteSession(t.Context(), tailnet.Identity{UserID: 8, NodeID: "stranger", Login: "other@example.test"}, invite, ""); err == nil {
		t.Fatal("another owner used invite")
	}
	two, err := s.remoteSession(t.Context(), second, invite, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.changeRemoteDevices(t.Context(), first, "revoke", second.NodeID); err != nil {
		t.Fatal(err)
	}
	if s.Session(t.Context(), two) || !s.Session(t.Context(), one) {
		t.Fatal("revocation did not invalidate only its node")
	}
	invite, err = s.changeRemoteDevices(t.Context(), first, "invite", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.remoteSession(t.Context(), second, invite, ""); err != nil {
		t.Fatal(err)
	}
	h, _ := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.tail-test.ts.net", nil)
	request := httptest.NewRequest("GET", "https://node.tail-test.ts.net/api/v1/setup", nil).WithContext(context.WithValue(t.Context(), remoteIdentityKey{}, second))
	if h.ownerSession(request, two) {
		t.Fatal("revoked token resurrected")
	}
	loopback := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/setup", nil)
	if h.ownerSession(loopback, one) {
		t.Fatal("Remote token replayed without private identity")
	}
}
func TestRemoteInitialHandoffIsBoundToPredeploymentDevice(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "fased"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := tailnet.Identity{UserID: 7, NodeID: "first", Login: "owner@example.test"}
	claim, err := s.IssueRemoteClaim(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	other := owner
	other.NodeID = "second"
	if _, err = s.remoteSession(t.Context(), other, claim.Code, ""); err == nil {
		t.Fatal("another device used initial proof")
	}
	// Restart-style code rotation preserves the original binding.
	rotated, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(t.Context(), rotated.Code, "should-never-create-password"); err == nil {
		t.Fatal("bound Remote setup became a password claim")
	}
	if _, err = s.remoteSession(t.Context(), other, rotated.Code, ""); err == nil {
		t.Fatal("rotation lost node binding")
	}
	if _, err = s.remoteSession(t.Context(), owner, rotated.Code, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSecuritySeamsRateCapLogoutAndLoopbackClosure(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	owner := tailnet.Identity{UserID: 7, NodeID: "owner-node", Login: "owner@example.test"}
	claim, err := s.IssueRemoteClaim(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	initialToken, err := s.remoteSession(t.Context(), owner, claim.Code, "")
	if err != nil {
		t.Fatal(err)
	}

	const origin = "https://node.owner.ts.net"
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress, h.instanceLocation = true, "remote"

	// 1. Proxy rejects multiple X-Forwarded-For / X-Forwarded-Host / X-Forwarded-Proto header lines.
	resolveCalls := 0
	proxy := h.remoteProxy(func(context.Context, string) (tailnet.Identity, error) {
		resolveCalls++
		return owner, nil
	})
	baseProxyReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, origin+"/api/v1/auth/state", nil)
		r.Header.Set("X-Forwarded-For", "100.101.102.103")
		r.Header.Set("X-Forwarded-Host", "node.owner.ts.net")
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	if peer, ok := remoteProxyPeer(baseProxyReq(), "node.owner.ts.net"); !ok || peer != "100.101.102.103" {
		t.Fatalf("valid proxy headers rejected: ok=%v peer=%q", ok, peer)
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Add("X-Forwarded-For", "100.101.102.104") },
		func(r *http.Request) { r.Header.Set("X-Forwarded-For", "100.101.102.103, 100.101.102.104") },
		func(r *http.Request) { r.Header.Add("X-Forwarded-Host", "node.owner.ts.net") },
		func(r *http.Request) { r.Header.Add("X-Forwarded-Proto", "https") },
		func(r *http.Request) { r.Header.Set("Tailscale-Funnel-Request", "1") },
	} {
		r := baseProxyReq()
		mutate(r)
		if _, ok := remoteProxyPeer(r, "node.owner.ts.net"); ok {
			t.Fatal("duplicate or unsafe proxy header accepted by remoteProxyPeer")
		}
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("duplicate forwarded header accepted: %d", w.Code)
		}
	}
	if resolveCalls != 0 {
		t.Fatalf("duplicate header request reached tailnet resolver %d times", resolveCalls)
	}

	// 2. Remote mode closes all loopback HTTP routes except GET /api/v1/health.
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", http.StatusForbidden},
		{http.MethodGet, "/.fased-private-access", http.StatusForbidden},
		{http.MethodGet, "/api/v1/auth/state", http.StatusForbidden},
		{http.MethodPost, "/api/v1/health", http.StatusForbidden},
		{http.MethodGet, "/api/v1/health", http.StatusOK},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331"+tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("loopback %s %s got %d, want %d", tc.method, tc.path, w.Code, tc.want)
		}
	}
	h.loopbackPeerUID = func(*net.TCPAddr, *net.TCPAddr) (int, bool, error) {
		return os.Getuid() + 1, true, nil
	}
	rForeignHealth := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/health", nil)
	wForeignHealth := httptest.NewRecorder()
	h.ServeHTTP(wForeignHealth, rForeignHealth)
	if wForeignHealth.Code != http.StatusForbidden {
		t.Fatalf("foreign loopback UID accessed Remote health: %d", wForeignHealth.Code)
	}
	h.loopbackPeerUID = nil

	// 3. Server-side explicit logout blocks silent {} re-login and exposes remoteSignedOut in auth/state.
	doRemote := func(method, path, body, cookieVal, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(body)).WithContext(context.WithValue(t.Context(), remoteIdentityKey{}, owner))
		if method == http.MethodPost {
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
		}
		if cookieVal != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookieVal})
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := doRemote(http.MethodPost, "/api/v1/auth/logout", `{}`, initialToken, csrfToken(initialToken)); w.Code != http.StatusOK {
		t.Fatalf("remote logout failed: %d %s", w.Code, w.Body.String())
	}
	stateRes := doRemote(http.MethodGet, "/api/v1/auth/state", "", "", "")
	var authState map[string]any
	if stateRes.Code != http.StatusOK || json.Unmarshal(stateRes.Body.Bytes(), &authState) != nil || authState["authenticated"] != false || authState["remoteSignedOut"] != true {
		t.Fatalf("auth/state missing remoteSignedOut after logout: %d %s", stateRes.Code, stateRes.Body.String())
	}
	if w := doRemote(http.MethodPost, "/api/v1/auth/remote", `{}`, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("silent re-login bypassed server logout confirmation: %d", w.Code)
	}
	confirmRes := doRemote(http.MethodPost, "/api/v1/auth/remote", `{"confirm":true}`, "", "")
	var confirmBody map[string]any
	if confirmRes.Code != http.StatusOK || json.Unmarshal(confirmRes.Body.Bytes(), &confirmBody) != nil || confirmBody["sessionToken"] != nil {
		t.Fatalf("explicit confirm remote login failed or leaked sessionToken: %d %s", confirmRes.Code, confirmRes.Body.String())
	}
	cookies := confirmRes.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != hostSessionCookie {
		t.Fatalf("missing remote session cookie: %+v", cookies)
	}

	// 4. POST /api/v1/auth/remote rate limit returns 429 on burst.
	for i := 0; i < 18; i++ {
		if w := doRemote(http.MethodPost, "/api/v1/auth/remote", `{"confirm":true}`, "", ""); w.Code != http.StatusOK {
			t.Fatalf("unexpected remote login failure before rate limit at %d: %d", i, w.Code)
		}
	}
	if w := doRemote(http.MethodPost, "/api/v1/auth/remote", `{"confirm":true}`, "", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("POST /api/v1/auth/remote not rate limited: %d", w.Code)
	}

	// 5. Session expiry pruning and per-node active session cap (<= maxRemoteSessionsPerNode).
	if _, err = s.db.Exec("INSERT INTO sessions(token_hash,expires) VALUES(?,?)", tokenHash(strings.Repeat("z", 43)), s.now().Unix()-10); err != nil {
		t.Fatal(err)
	}
	issued := make([]string, 0, maxRemoteSessionsPerNode+4)
	for i := 0; i < maxRemoteSessionsPerNode+4; i++ {
		tok, e := s.remoteSession(t.Context(), owner, "", "")
		if e != nil {
			t.Fatal(e)
		}
		issued = append(issued, tok)
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE expires<=?", s.now().Unix()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired sessions not pruned: count=%d err=%v", count, err)
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM remote_sessions WHERE node_id=?", owner.NodeID).Scan(&count); err != nil || count != maxRemoteSessionsPerNode {
		t.Fatalf("per-node remote session cap mismatch: count=%d err=%v", count, err)
	}
	if s.Session(t.Context(), issued[0]) || !s.Session(t.Context(), issued[len(issued)-1]) {
		t.Fatal("oldest/newest session eviction did not preserve latest active sessions")
	}
}
