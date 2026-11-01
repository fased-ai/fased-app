package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"strings"
	"testing"
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
		if e := runUpdateWizard(call, io.Discard, io.Discard, func() {}, 2); e == nil || calls > 3 {
			t.Fatal(phase, calls, e)
		}
	}
}

func TestOrdinaryUpdateReconnectsOnlyAvailability(t *testing.T) {
	for _, failure := range []error{core.ErrCoreUnavailable, core.ErrUnauthorized} {
		calls, applies := 0, 0
		call := func(action string, w io.Writer) error {
			calls++
			if action == "apply" {
				applies++
			}
			if calls == 3 {
				return failure
			}
			v := core.ApplicationUpdateView{}
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
		e := runUpdateWizard(call, io.Discard, io.Discard, func() {}, 6)
		if failure == core.ErrCoreUnavailable && e != nil {
			t.Fatal(e)
		}
		if failure == core.ErrUnauthorized && e == nil {
			t.Fatal("authentication failure retried")
		}
		if applies != 1 {
			t.Fatal("apply was repeated", applies)
		}
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
