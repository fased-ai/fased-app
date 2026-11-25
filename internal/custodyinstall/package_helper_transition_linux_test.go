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

	// Multi-hop chained transitions retain all previous ancestors, resume cleanly,
	// and fail closed on stale fences, missing/tampered ancestors, or rollbacks.
	third := strings.Repeat("9", 64)
	fourth := strings.Repeat("8", 64)
	chained := update.PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance,
		PreviousHelper: next, NextHelper: third,
		Ancestors:     wanted.ChainPredecessors(),
		JournalSHA256: wanted.JournalSHA256,
	}
	chainedFence := sum(mustJSON(chained))
	chainedSuperseded := chained
	chainedSuperseded.JournalSHA256 = strings.Repeat("7", 64)
	missingAncestors := chained
	missingAncestors.Ancestors = nil
	wrongAncestors := chained
	wrongAncestors.Ancestors = []string{strings.Repeat("5", 64)}
	thirdHop := update.PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance,
		PreviousHelper: third, NextHelper: fourth,
		Ancestors:     chained.ChainPredecessors(),
		JournalSHA256: wanted.JournalSHA256,
	}
	thirdFence := sum(mustJSON(thirdHop))

	for _, tc := range []struct {
		name               string
		wanted, retained   update.PackageHelperTransition
		receipt            bool
		helper, fence, app string
		phase              string
	}{
		{"chained start after H0->H1", chained, wanted, true, next, "", third, "start"},
		{"chained resume before transition file replace", chained, wanted, true, next, chainedFence, third, "resume"},
		{"chained resume after transition file replace pre-pin", chained, chained, true, next, chainedFence, third, "resume"},
		{"chained resume after pin switch", chained, chained, true, third, chainedFence, third, "resume"},
		{"chained committed before restart", chained, chained, true, third, "", third, "committed"},
		{"chained superseded by later Update", chainedSuperseded, chained, true, third, "", third, "superseded"},
		{"chained refused before paired app Update", chained, wanted, true, next, "", next, ""},
		{"chained refused while H0->H1 fence active", chained, wanted, true, next, fence, third, ""},
		{"chained refused with omitted ancestors", missingAncestors, wanted, true, next, "", third, ""},
		{"chained refused with tampered ancestors", wrongAncestors, wanted, true, next, "", third, ""},
		{"chained third hop carries [H0, H1]", thirdHop, chained, true, third, "", fourth, "start"},
		{"chained third hop resume", thirdHop, thirdHop, true, fourth, thirdFence, fourth, "resume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyPackageHelperTransition(tc.wanted, tc.retained, tc.receipt, tc.helper, tc.fence, tc.app)
			if got != tc.phase || (err != nil) != (tc.phase == "") {
				t.Fatalf("got phase %q, error %v; want %q", got, err, tc.phase)
			}
		})
	}

	rollback := update.PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance,
		PreviousHelper: third, NextHelper: previous,
		Ancestors:     chained.ChainPredecessors(),
		JournalSHA256: wanted.JournalSHA256,
	}
	if rollback.Valid(owner, instance) {
		t.Fatal("helper rollback to prior ancestor unexpectedly validated")
	}
}
