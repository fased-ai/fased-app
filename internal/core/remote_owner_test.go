package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestRemoteOwnerEnrollmentPreservationAndReplay(t *testing.T) {
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

func TestRemoteClaimRetryCannotReplacePendingDevice(t *testing.T) {
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
