package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestSetupMenuHasUpdate(t *testing.T) {
	var out bytes.Buffer
	choice, e := installationChoice(bufio.NewReader(strings.NewReader("update\n")), &out)
	if e != nil || choice != "update" || !strings.Contains(out.String(), "Update") {
		t.Fatal(choice, e, out.String())
	}
}
func TestOrdinaryUpdateWaitsWithoutDuplicateApply(t *testing.T) {
	for _, phases := range [][]string{{"check-queued", "checking", "available", "queued", "applying", "completed"}, {"checking", "current"}} {
		n, applies, waits := 0, 0, 0
		var out, progress bytes.Buffer
		call := func(action string, w io.Writer) error {
			if action == "apply" {
				applies++
			}
			if n >= len(phases) {
				t.Fatal("too many polls", action)
			}
			view := core.ApplicationUpdateView{}
			view.Operation.Phase = phases[n]
			n++
			return json.NewEncoder(w).Encode(view)
		}
		if e := runUpdateWizard(call, &out, &progress, func() { waits++ }, 20); e != nil {
			t.Fatal(e)
		}
		if phases[len(phases)-1] == "completed" && applies != 1 {
			t.Fatal(applies)
		}
		if phases[len(phases)-1] == "current" && (applies != 0 || !strings.Contains(out.String(), "Already current")) {
			t.Fatal(applies, out.String())
		}
	}
}
func TestOrdinaryUpdateFailureAndBoundedWait(t *testing.T) {
	for _, phase := range []string{"recovery", "check-failed", "rolled-back", "idle", "applying"} {
		calls := 0
		call := func(action string, w io.Writer) error {
			calls++
			v := core.ApplicationUpdateView{}
			v.Operation.Phase = phase
			return json.NewEncoder(w).Encode(v)
		}
		e := runUpdateWizard(call, io.Discard, io.Discard, func() {}, 2)
		if e == nil || calls > 3 {
			t.Fatal(phase, calls, e)
		}
		if phase == "applying" && (!strings.Contains(e.Error(), "same Fased instance") || strings.Contains(e.Error(), "fased update status")) {
			t.Fatal("pending update must not suggest an ambiguous command belonging to another installation", e)
		}
	}
}

func TestOrdinaryUpdateReconnectsOnlyAvailability(t *testing.T) {
	const operation = "11111111111111111111111111111111"
	release := core.ApplicationRelease{Version: "0.1.20", Sequence: 1000021, ManifestSHA256: strings.Repeat("a", 64)}
	for _, failure := range []error{core.ErrCoreUnavailable, core.ErrUnauthorized} {
		calls, applies := 0, 0
		var out bytes.Buffer
		call := func(action string, w io.Writer) error {
			calls++
			if action == "apply" {
				applies++
			}
			if calls == 3 {
				if action != "status" {
					t.Fatalf("expected a status read after Apply, got %s", action)
				}
				return failure
			}
			v := core.ApplicationUpdateView{Operation: core.ApplicationUpdateOperation{ID: operation, Release: &release}}
			switch calls {
			case 1:
				v.Operation.Phase = "available"
			case 2:
				v.Operation.Phase = "applying"
			default:
				v.Operation.Phase = "completed"
			}
			return json.NewEncoder(w).Encode(v)
		}
		e := runUpdateWizard(call, &out, io.Discard, func() {}, 6)
		if failure == core.ErrCoreUnavailable && (e != nil || calls != 4 || out.String() != "Updated\n") {
			t.Fatal("the original update did not finish after reconnect", calls, out.String(), e)
		}
		if failure == core.ErrUnauthorized && (!errors.Is(e, core.ErrUnauthorized) || calls != 3 || out.Len() != 0) {
			t.Fatal("authentication failure retried or reported completion", calls, out.String(), e)
		}
		if applies != 1 {
			t.Fatal("apply was repeated", applies)
		}
	}
}

func TestOrdinaryUpdateResumesSameOperationAfterRemoteGatewayError(t *testing.T) {
	const original = "54f295b654bc708f793637cd5834434c"
	release := core.ApplicationRelease{Version: "0.1.19", Sequence: 1000020, ManifestSHA256: strings.Repeat("a", 64)}
	for _, tc := range []struct {
		name, finalID, finalVersion, wantError string
		applyUnacknowledged                    bool
	}{
		{name: "original completes", finalID: original, finalVersion: release.Version},
		{name: "different operation", finalID: strings.Repeat("b", 32), finalVersion: release.Version, wantError: "update operation changed"},
		{name: "different release", finalID: original, finalVersion: "0.1.20", wantError: "approved update release changed"},
		{name: "apply not acknowledged", applyUnacknowledged: true, wantError: core.ErrRemoteUpdateStatusUnavailable.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applying := core.ApplicationUpdateOperation{ID: original, Phase: "applying", Release: &release}
			returnedRelease := release
			returnedRelease.Version = tc.finalVersion
			completed := core.ApplicationUpdateOperation{ID: tc.finalID, Phase: "completed", Release: &returnedRelease}
			completed.Receipt = &core.ApplicationUpdateReceipt{
				ID: completed.ID, Phase: completed.Phase, Sequence: returnedRelease.Sequence, ManifestSHA256: returnedRelease.ManifestSHA256,
			}
			steps := []struct {
				action string
				op     core.ApplicationUpdateOperation
				err    error
			}{
				{action: "check", op: core.ApplicationUpdateOperation{Phase: "available", Release: &release}},
				{action: "apply", op: applying},
				{action: "status", err: core.ErrRemoteUpdateStatusUnavailable},
				{action: "status", op: completed},
			}
			if tc.applyUnacknowledged {
				steps[1].err = core.ErrCoreUnavailable
				steps = steps[:3]
			}
			next := 0
			call := func(action string, w io.Writer) error {
				if next == len(steps) || steps[next].action != action {
					t.Fatalf("unexpected %q at step %d", action, next)
				}
				step := steps[next]
				next++
				if step.err != nil {
					return step.err
				}
				return json.NewEncoder(w).Encode(core.ApplicationUpdateView{Operation: step.op})
			}
			var out bytes.Buffer
			err := runUpdateWizard(call, &out, io.Discard, func() {}, 10)
			if tc.wantError == "" {
				if err != nil || out.String() != "Updated\n" {
					t.Fatalf("original update did not complete: %q, %v", out.String(), err)
				}
			} else if err == nil || err.Error() != tc.wantError || out.Len() != 0 {
				t.Fatalf("unexpected completion after a refusal: %q, %v", out.String(), err)
			}
			if next != len(steps) {
				t.Fatalf("used %d of %d expected commands", next, len(steps))
			}
		})
	}
}

func TestOfflineRecoveryRequiresExactPreviouslyAdmittedMetadata(t *testing.T) {
	v := core.ApplicationOwnerView{}
	v.View.InstanceID = "original"
	v.View.Operation = core.ApplicationUpdateOperation{ID: strings.Repeat("a", 32), Phase: "recovery", Release: &core.ApplicationRelease{Sequence: 7, ManifestSHA256: strings.Repeat("b", 64)}}
	if !recoveryMetadataMatches(v, v.View.Operation.ID, 7, strings.Repeat("b", 64)) {
		t.Fatal("valid original refused")
	}
	for _, tc := range []struct {
		id   string
		seq  uint64
		hash string
	}{{strings.Repeat("c", 32), 7, strings.Repeat("b", 64)}, {v.View.Operation.ID, 8, strings.Repeat("b", 64)}, {v.View.Operation.ID, 7, strings.Repeat("c", 64)}} {
		if recoveryMetadataMatches(v, tc.id, tc.seq, tc.hash) {
			t.Fatal("recovery retargeted")
		}
	}
	v.View.Operation.Phase = "available"
	if recoveryMetadataMatches(v, v.View.Operation.ID, 7, strings.Repeat("b", 64)) {
		t.Fatal("new apply authorized offline")
	}
}

func TestUpdateWizardSignsInOnlyAtEntry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid-session", true: "expired-session"}[expired], func(t *testing.T) {
			calls, logins := 0, 0
			var out bytes.Buffer
			err := authenticatedUpdateWizard(func(action string, w io.Writer) error {
				calls++
				if expired && calls == 1 {
					return core.ErrUnauthorized
				}
				return json.NewEncoder(w).Encode(core.ApplicationUpdateView{Operation: core.ApplicationUpdateOperation{Phase: "current"}})
			}, func() error { logins++; return nil }, &out, io.Discard)
			expected := 0
			if expired {
				expected = 1
			}
			if err != nil || logins != expected || !strings.Contains(out.String(), "Already current") {
				t.Fatal(logins, out.String(), err)
			}
		})
	}
}
func TestUpdateWizardDoesNotReauthorizeAfterStart(t *testing.T) {
	calls, logins := 0, 0
	err := authenticatedUpdateWizard(func(action string, w io.Writer) error {
		calls++
		if calls == 1 {
			return json.NewEncoder(w).Encode(core.ApplicationUpdateView{Operation: core.ApplicationUpdateOperation{Phase: "available"}})
		}
		return core.ErrUnauthorized
	}, func() error { logins++; return nil }, io.Discard, io.Discard)
	if !errors.Is(err, core.ErrUnauthorized) || logins != 0 {
		t.Fatal(logins, err)
	}
	cancelled := errors.New("owner cancelled sign-in")
	err = authenticatedUpdateWizard(func(string, io.Writer) error { return core.ErrUnauthorized }, func() error { return cancelled }, io.Discard, io.Discard)
	if !errors.Is(err, cancelled) {
		t.Fatal(err)
	}
}

func TestRunUpdateWizardHandlesDesktopReleaseFallback(t *testing.T) {
	t.Parallel()

	t.Run("current", func(t *testing.T) {
		var out, errOut bytes.Buffer
		err := runUpdateWizard(func(action string, dst io.Writer) error {
			if action != "check" {
				t.Fatalf("unexpected action %q", action)
			}
			return json.NewEncoder(dst).Encode(core.DesktopReleaseCheckView{
				InstalledVersion: "0.2.4",
				LatestVersion:    "0.2.4",
				UpdateAvailable:  false,
			})
		}, &out, &errOut, func() {}, 5)
		if err != nil {
			t.Fatalf("runUpdateWizard(current) failed: %v", err)
		}
		if !strings.Contains(out.String(), "Already current (0.2.4)") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})

	t.Run("available", func(t *testing.T) {
		var out, errOut bytes.Buffer
		err := runUpdateWizard(func(action string, dst io.Writer) error {
			return json.NewEncoder(dst).Encode(core.DesktopReleaseCheckView{
				InstalledVersion: "0.2.4",
				LatestVersion:    "0.2.5",
				ReleaseTag:       "v0.2.5-desktop",
				ReleaseURL:       "https://github.com/fased-ai/fased-app/releases/tag/v0.2.5-desktop",
				UpdateAvailable:  true,
			})
		}, &out, &errOut, func() {}, 5)
		if err != nil {
			t.Fatalf("runUpdateWizard(available) failed: %v", err)
		}
		if !strings.Contains(out.String(), "Update available: 0.2.4 -> 0.2.5") || !strings.Contains(out.String(), "v0.2.5-desktop") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})
}
