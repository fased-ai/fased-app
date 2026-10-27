package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func candidateFixture() UpdateCandidate {
	return UpdateCandidate{Release: "0.0.0-disposable", Sequence: "7", ManifestSHA256: strings.Repeat("a", 64), Target: update.Target{OS: "linux", Arch: "amd64"}}
}
func TestUpdatePreferencesPersistFenceAndPreserveOlderSetup(t *testing.T) {
	s := fixtureStore(t)
	v, e := s.UpdatePreferences(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if !v.Policy.Notify || len(v.Proposals) != 0 || v.Window != "unrestricted" || v.InstallAllowed || v.ResumeAllowed || v.AutomaticUpdates || v.FinancialExecution {
		t.Fatal(v)
	}
	policy := UpdatePolicy{Notify: false, Pin: &UpdatePin{Sequence: "7", ManifestSHA256: strings.Repeat("a", 64)}, Window: &MaintenanceWindow{TimeZone: "America/Chicago", Days: []int{1, 2, 3, 4, 5}, Start: "23:00", End: "01:00"}}
	v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "policy", Policy: &policy})
	if e != nil {
		t.Fatal(e)
	}
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	restarted, e := other.UpdatePreferences(t.Context())
	other.Close()
	if e != nil || !reflectJSON(v.Policy, restarted.Policy) || restarted.Revision != v.Revision {
		t.Fatal(restarted, e)
	}
	// New setup clients unaware of this optional extension retain it. A genuine
	// older SQL writer cannot silently discard it after executable rollback.
	setup, e := s.Setup(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	setup.Settings.Updates = nil
	setup.Settings.Host = "vps"
	if _, e = s.UpdateSetup(t.Context(), setup.Revision, setup.Settings); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE instance SET setup='{"step":0,"complete":false,"host":"local","walletMode":"skip","provider":"none"}',revision=revision+1 WHERE id=1`); e == nil {
		t.Fatal("older writer discarded update preferences")
	}
	v, e = s.UpdatePreferences(t.Context())
	if e != nil || !reflectJSON(v.Policy, policy) {
		t.Fatal(v, e)
	}
	command := pauseCommand(t, s, 1)
	if _, e = s.UpdatePause(t.Context(), command); e != nil {
		t.Fatal(e)
	}
	policy.Notify = true
	if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "policy", Policy: &policy}); !errors.Is(e, ErrCorePaused) {
		t.Fatal("preferences bypassed pause", e)
	}
	if _, e = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: command.ProbeID, Aborted: aborted(command)}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision - 1, Action: "policy", Policy: &policy}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale write", e)
	}
}
func reflectJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestReviewOnlyProposalsDeduplicateBindDigestAndNeverCallSigner(t *testing.T) {
	s := fixtureStore(t)
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		t.Fatal("preferences called signer")
		return signer.Response{}, nil
	}
	c := candidateFixture()
	v, e := s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: 0, Action: "propose", Candidate: &c})
	if e != nil {
		t.Fatal(e)
	}
	p := v.Proposals[0]
	changed := c
	changed.Release = "Different claim for same manifest"
	if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "propose", Candidate: &changed}); e == nil {
		t.Fatal("accepted conflicting manifest metadata")
	}
	if p.State != "pending" || p.Verified || len(v.Notifications) != 1 || v.InstallAllowed || v.ResumeAllowed || v.AutomaticUpdates || v.FinancialExecution {
		t.Fatal(v)
	}
	again, e := s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "propose", Candidate: &c})
	if e != nil || again.Revision != v.Revision || len(again.Proposals) != 1 || !reflectJSON(again.Proposals, v.Proposals) {
		t.Fatal("duplicate notification/proposal", again, e)
	}
	if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "review", Digest: strings.Repeat("f", 64)}); e == nil {
		t.Fatal("reviewed another digest")
	}
	v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "review", Digest: p.Digest})
	if e != nil || v.Proposals[0].State != "reviewed" || len(v.Notifications) != 0 {
		t.Fatal(v, e)
	}
	if v.Proposals[0].Verified || v.InstallAllowed || v.AutomaticUpdates {
		t.Fatal("review granted authority", v)
	}
	if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "remove", Digest: p.Digest}); e == nil {
		t.Fatal("removed undismissed proposal")
	}
	v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "dismiss", Digest: p.Digest})
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "remove", Digest: p.Digest})
	if e != nil || len(v.Proposals) != 0 {
		t.Fatal(v, e)
	}
}

func TestMaintenanceWindowTimezoneOvernightAndDST(t *testing.T) {
	w := MaintenanceWindow{TimeZone: "America/New_York", Days: []int{0}, Start: "01:00", End: "02:00"}
	for _, tc := range []struct {
		date   string
		within bool
	}{{"2026-11-01T05:30:00Z", true}, {"2026-11-01T06:30:00Z", true}, {"2026-11-01T07:00:00Z", false}, {"2026-03-08T06:30:00Z", true}, {"2026-03-08T07:00:00Z", false}} {
		now, _ := time.Parse(time.RFC3339, tc.date)
		if e := w.Validate(); e != nil {
			t.Fatal(e)
		}
		if w.Contains(now) != tc.within {
			t.Fatal(tc)
		}
	}
	w = MaintenanceWindow{TimeZone: "UTC", Days: []int{1}, Start: "23:00", End: "01:00"}
	for _, tc := range []struct {
		date   string
		within bool
	}{{"2026-10-19T23:00:00Z", true}, {"2026-10-20T00:59:59Z", true}, {"2026-10-20T01:00:00Z", false}, {"2026-10-20T23:30:00Z", false}} {
		now, _ := time.Parse(time.RFC3339, tc.date)
		if w.Contains(now) != tc.within {
			t.Fatal(tc)
		}
	}
	for _, bad := range []MaintenanceWindow{{TimeZone: "Local", Days: []int{1}, Start: "02:00", End: "03:00"}, {TimeZone: "Mars/Olympus", Days: []int{1}, Start: "02:00", End: "03:00"}, {TimeZone: "UTC", Days: []int{1, 1}, Start: "02:00", End: "03:00"}, {TimeZone: "UTC", Days: []int{7}, Start: "02:00", End: "03:00"}, {TimeZone: "UTC", Days: []int{1}, Start: "2:00", End: "03:00"}, {TimeZone: "UTC", Days: []int{1}, Start: "02:00", End: "02:00"}, {TimeZone: "UTC", Days: []int{1}, Start: "02:00", End: "02:05"}} {
		if bad.Validate() == nil {
			t.Fatal(bad)
		}
	}
}

func TestUpdatePreferencesNotifyPinBoundsAndSetupCannotForgeReview(t *testing.T) {
	s := fixtureStore(t)
	before, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	c := candidateFixture()
	v, e := s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: 0, Action: "propose", Candidate: &c})
	if e != nil {
		t.Fatal(e)
	}
	policy := UpdatePolicy{Notify: false, Pin: &UpdatePin{Sequence: "8", ManifestSHA256: strings.Repeat("b", 64)}}
	v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "policy", Policy: &policy})
	if e != nil || len(v.Notifications) != 0 || v.Proposals[0].PinMatch {
		t.Fatal(v, e)
	}
	policy.Notify = true
	policy.Pin = &UpdatePin{Sequence: c.Sequence, ManifestSHA256: c.ManifestSHA256}
	v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "policy", Policy: &policy})
	if e != nil || len(v.Notifications) != 1 || !v.Proposals[0].PinMatch {
		t.Fatal(v, e)
	}
	after, e := s.UpdateSnapshot(t.Context(), before.ProbeID)
	if e != nil || update.SameCoreMetadata(before, after) {
		t.Fatal("snapshot omitted policy", e)
	}
	setup, e := s.Setup(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	now := s.now()
	setup.Settings.Updates.Proposals[0].State = "reviewed"
	setup.Settings.Updates.Proposals[0].DecisionAt = &now
	if _, e = s.UpdateSetup(t.Context(), setup.Revision, setup.Settings); e == nil {
		t.Fatal("generic setup forged review")
	}
	for n := 1; n < maxUpdateProposals; n++ {
		next := c
		next.Sequence = strconv.Itoa(n + 10)
		v, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "propose", Candidate: &next})
		if e != nil {
			t.Fatal(e)
		}
	}
	c.Sequence = "99"
	if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: v.Revision, Action: "propose", Candidate: &c}); e == nil {
		t.Fatal("unbounded proposals")
	}
	for _, bad := range []string{"0", "01", "+1", "18446744073709551616"} {
		if validPin(UpdatePin{bad, c.ManifestSHA256}) {
			t.Fatal(bad)
		}
	}
}

func TestUpdatePreferencesHTTPClosedAuthenticatedRevisionChecked(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		method, body, auth, csrf string
		code                     int
	}{{"GET", "", "", "", 401}, {"GET", "", token, "", 200}, {"PUT", `{"revision":0,"action":"policy","policy":{"notify":false}}`, token, "", 403}, {"PUT", `{"revision":0,"action":"policy","policy":{"notify":false}}`, token, csrfToken(token), 200}, {"PUT", `{"revision":0,"action":"policy","policy":{"notify":false}}`, token, csrfToken(token), 409}, {"PUT", `{"revision":1,"action":"apply"}`, token, csrfToken(token), 400}, {"PUT", `{"revision":1,"action":"policy","policy":{"notify":false,"automaticUpdates":true}}`, token, csrfToken(token), 400}, {"PUT", `{"revision":1,"revision":1,"action":"policy","policy":{"notify":false}}`, token, csrfToken(token), 400}, {"DELETE", "", token, csrfToken(token), 405}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/updates/preferences", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.auth)
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", tc.csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc.body, w.Code, w.Body.String())
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Go(func() {
			p := UpdatePolicy{Notify: true}
			_, e := s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: 1, Action: "policy", Policy: &p})
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	ok, conflict := 0, 0
	for e := range errs {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}
