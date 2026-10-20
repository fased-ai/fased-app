package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestUpdateStatusObservationsNeverGrantAuthority(t *testing.T) {
	s := fixtureStore(t)
	before, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	view := s.ReadUpdateStatus(t.Context())
	if view.State != "not-configured" || view.Core.Phase != "idle" || view.Signer.State != "not-configured" || view.Signer.Locked != nil {
		t.Fatal(view)
	}
	status := signer.Status{FixtureOnly: true, Locked: true}
	calls := 0
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		calls++
		if r.Action != "status" {
			t.Fatalf("administrative channel requested: %s", r.Action)
		}
		return signer.Response{Status: status}, nil
	}
	for _, tc := range []struct {
		held, recovery bool
		state          string
	}{{false, false, "observed"}, {true, false, "signer-held"}, {true, true, "recovery-required"}} {
		status.UpdateHeld, status.RecoveryRequired = tc.held, tc.recovery
		view = s.ReadUpdateStatus(t.Context())
		if view.State != tc.state || !view.ReadOnly || view.WalletSigner || view.FinancialExecution || view.AutomaticUpdates || view.Signer.Locked == nil || !*view.Signer.Locked {
			t.Fatal(view)
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	after, e := s.UpdateSnapshot(t.Context(), before.ProbeID)
	if e != nil || update.SnapshotDigest("core", before) != update.SnapshotDigest("core", after) {
		t.Fatal("status changed metadata", e)
	}
	for _, unsupported := range []signer.Status{{}, {FixtureOnly: true, WalletSigner: true}, {FixtureOnly: true, FinancialExecution: true}, {FixtureOnly: true, UnattendedUnlock: true}} {
		status = unsupported
		view = s.ReadUpdateStatus(t.Context())
		if view.State != "unavailable" || view.Signer.State != "unavailable" || view.Signer.Locked != nil {
			t.Fatal(view)
		}
	}
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{}, errors.New("private signer diagnostic")
	}
	view = s.ReadUpdateStatus(t.Context())
	b, _ := json.Marshal(view)
	if view.State != "unavailable" || strings.Contains(string(b), "private signer diagnostic") {
		t.Fatal(string(b))
	}
}

func TestUpdateStatusPauseReleaseRestartAndDamagedMetadata(t *testing.T) {
	s := fixtureStore(t)
	c := pauseCommand(t, s, 1)
	if _, e := s.UpdatePause(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	view := s.ReadUpdateStatus(t.Context())
	if view.State != "paused" || view.Core.Phase != "paused" || view.Core.IntentID != c.Request.IntentID {
		t.Fatal(view)
	}
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	view = other.ReadUpdateStatus(t.Context())
	other.Close()
	if view.Core.Phase != "paused" {
		t.Fatal("restart lost observation", view)
	}
	if _, e = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(c)}); e != nil {
		t.Fatal(e)
	}
	view = s.ReadUpdateStatus(t.Context())
	if view.Core.Phase != "released" || view.State != "not-configured" {
		t.Fatal(view)
	}
	// A released app-owned tombstone never means an installed release or clears a new signer hold.
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, UpdateHeld: true}}, nil
	}
	if v := s.ReadUpdateStatus(t.Context()); v.State != "signer-held" {
		t.Fatal(v)
	}
	if _, e = s.db.Exec("UPDATE update_pause SET document='{}' WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	view = s.ReadUpdateStatus(t.Context())
	if view.Core.Phase != "unavailable" || view.State != "unavailable" || view.Core.IntentID != "" {
		t.Fatal(view)
	}
}

func TestUpdateStatusHTTPAuthenticatedReadOnlyAndRedacted(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		method, token string
		code          int
	}{{"GET", "", 401}, {"GET", token, 200}, {"POST", token, 405}, {"PUT", token, 405}, {"DELETE", token, 405}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/updates/status", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc.method, w.Code, w.Body.String())
		}
		if tc.code == 200 {
			for _, secret := range []string{"password", "sessionToken", "anchor", "authority", "manifestSha256", "snapshot", "publicKey", "reservedSequenceFloor", "installedSequence"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("status leaked", secret, w.Body.String())
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable status")
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/status", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://foreign.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
