//go:build linux

package custodyinstall

import (
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageHelperTransitionRecovery(t *testing.T) {
	t.Parallel()
	owner := 22000
	instance := strings.Repeat("d", 32)
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: strings.Repeat("e", 32), Phase: "completed", Start: "target"}
	previous, next := strings.Repeat("a", 64), strings.Repeat("b", 64)
	wanted := update.PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance, PreviousHelper: previous, NextHelper: next,
		JournalSHA256: update.SnapshotDigest("package-helper-journal:v1", r),
	}
	fence := sum(mustJSON(wanted))
	newJournal := wanted
	newJournal.JournalSHA256 = strings.Repeat("c", 64)
	otherTarget := wanted
	otherTarget.NextHelper = strings.Repeat("f", 64)
	for _, tc := range []struct {
		name          string
		wanted        update.PackageHelperTransition
		receipt       bool
		helper, fence string
		phase         string
	}{
		{"unchanged", wanted, false, previous, "", "start"},
		{"interrupted after fencing", wanted, false, previous, fence, "resume"},
		{"interrupted after receipt", wanted, true, previous, fence, "resume"},
		{"interrupted after pin switch", wanted, true, next, fence, "resume"},
		{"interrupted before restart", wanted, true, next, "", "committed"},
		{"next Update owns journal", newJournal, true, next, "", "superseded"},
		{"different approval while interrupted", newJournal, true, next, fence, ""},
		{"different successor while interrupted", otherTarget, true, previous, fence, ""},
		{"foreign fence", wanted, false, previous, strings.Repeat("f", 64), ""},
		{"fence removed prematurely", wanted, true, previous, "", ""},
		{"switched without receipt", wanted, false, next, fence, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyPackageHelperTransition(tc.wanted, wanted, tc.receipt, tc.helper, tc.fence, next)
			if got != tc.phase || (err != nil) != (tc.phase == "") {
				t.Fatalf("got phase %q, error %v; want %q", got, err, tc.phase)
			}
		})
	}

	for _, installed := range []string{previous, ""} {
		if phase, err := classifyPackageHelperTransition(wanted, update.PackageHelperTransition{}, false, previous, "", installed); err == nil {
			t.Fatalf("installed predecessor %q was admitted to %q before the paired app Update", installed, phase)
		}
	}

	if !wanted.Valid(owner, instance) || !wanted.Inherits(r) {
		t.Fatal("the original completed journal must be retained")
	}
	r.Phase = "signer-committed-held"
	if wanted.Inherits(r) {
		t.Fatal("a held journal must never authorize a transition")
	}
	r.Phase = "completed"
	r.Approval = &update.MaintenanceApproval{HelperSHA256: next}
	if wanted.Inherits(r) {
		t.Fatal("the previous approval must not be rewritten to name the successor")
	}
}
