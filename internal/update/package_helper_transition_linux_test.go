//go:build linux

package update

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPackageHelperTransitionChainAndValidation(t *testing.T) {
	t.Parallel()
	owner := 22000
	instance := strings.Repeat("d", 32)
	h0 := strings.Repeat("0", 64)
	h1 := strings.Repeat("1", 64)
	h2 := strings.Repeat("2", 64)
	h3 := strings.Repeat("3", 64)
	r := MaintenanceRecord{Version: 1, Owner: owner, OperationID: strings.Repeat("e", 32), Phase: "completed", Start: "target"}
	journalDigest := SnapshotDigest("package-helper-journal:v1", r)

	legacy := PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance,
		PreviousHelper: h0, NextHelper: h1, JournalSHA256: journalDigest,
	}
	if !legacy.Valid(owner, instance) {
		t.Fatal("valid legacy helper transition rejected")
	}
	rawLegacy, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawLegacy), "ancestorHelpersSha256") {
		t.Fatalf("empty ancestors unexpectedly serialized: %s", rawLegacy)
	}
	var decodedLegacy PackageHelperTransition
	if err = StrictJSON(rawLegacy, &decodedLegacy); err != nil || !legacy.Equal(decodedLegacy) {
		t.Fatalf("legacy StrictJSON round-trip failed: err=%v decoded=%+v", err, decodedLegacy)
	}

	chained := PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance,
		PreviousHelper: h1, NextHelper: h2, Ancestors: legacy.ChainPredecessors(),
		JournalSHA256: journalDigest,
	}
	if !chained.Valid(owner, instance) || !chained.IncludesPredecessor(h0) || !chained.IncludesPredecessor(h1) || chained.IncludesPredecessor(h2) || chained.IncludesPredecessor("bad") {
		t.Fatalf("chained predecessor checks failed: %+v", chained)
	}
	if legacy.Equal(chained) {
		t.Fatal("legacy transition unexpectedly equaled chained transition")
	}
	thirdHop := chained.ChainPredecessors()
	if len(thirdHop) != 2 || thirdHop[0] != h0 || thirdHop[1] != h1 {
		t.Fatalf("unexpected 3rd-hop ancestor slice: %v", thirdHop)
	}
	rawChained, err := json.Marshal(chained)
	if err != nil {
		t.Fatal(err)
	}
	var decodedChained PackageHelperTransition
	if err = StrictJSON(rawChained, &decodedChained); err != nil || !chained.Equal(decodedChained) {
		t.Fatalf("chained StrictJSON round-trip failed: err=%v decoded=%+v", err, decodedChained)
	}

	// Reject cycles/rollbacks, duplicates, invalid hashes, and >maxHelperTransitionAncestors.
	invalidCases := []struct {
		name string
		t    PackageHelperTransition
	}{
		{"ancestor matches next (rollback)", PackageHelperTransition{Version: 1, Owner: owner, Instance: instance, PreviousHelper: h1, NextHelper: h0, Ancestors: []string{h0}, JournalSHA256: journalDigest}},
		{"ancestor matches previous", PackageHelperTransition{Version: 1, Owner: owner, Instance: instance, PreviousHelper: h1, NextHelper: h2, Ancestors: []string{h1}, JournalSHA256: journalDigest}},
		{"duplicate ancestor", PackageHelperTransition{Version: 1, Owner: owner, Instance: instance, PreviousHelper: h2, NextHelper: h3, Ancestors: []string{h0, h0}, JournalSHA256: journalDigest}},
		{"non-canonical ancestor hash", PackageHelperTransition{Version: 1, Owner: owner, Instance: instance, PreviousHelper: h1, NextHelper: h2, Ancestors: []string{strings.Repeat("A", 64)}, JournalSHA256: journalDigest}},
	}
	for _, tc := range invalidCases {
		if tc.t.Valid(owner, instance) {
			t.Fatalf("%s unexpectedly passed Valid", tc.name)
		}
	}

	maxAncestors := make([]string, maxHelperTransitionAncestors)
	for i := range maxAncestors {
		b := []byte(strings.Repeat("a", 64))
		b[0] = "0123456789abcdef"[i/16]
		b[1] = "0123456789abcdef"[i%16]
		maxAncestors[i] = string(b)
	}
	atMax := PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance,
		PreviousHelper: h1, NextHelper: h2, Ancestors: maxAncestors, JournalSHA256: journalDigest,
	}
	if !atMax.Valid(owner, instance) {
		t.Fatal("64 distinct ancestors unexpectedly rejected")
	}
	overMax := atMax
	overMax.Ancestors = append(slicesCloneTest(maxAncestors), h3)
	if overMax.Valid(owner, instance) {
		t.Fatal(">64 ancestors unexpectedly accepted")
	}

	// Live selection (allowAncestors=false) must still refuse an ancestor helper,
	// whereas archive selection (allowAncestors=true) admits h0 past helper matching.
	r.Approval = &MaintenanceApproval{HelperSHA256: h0}
	_, liveErr := readTransitionPredecessorApproval(chained, owner, "remote", instance, h2, r, false)
	if liveErr == nil || liveErr.Error() != "the original completed package approval is unavailable" {
		t.Fatalf("unexpected live approval result for ancestor: %v", liveErr)
	}
	_, archiveErr := readTransitionPredecessorApproval(chained, owner, "remote", instance, h2, r, true)
	if archiveErr == nil || archiveErr.Error() == "the original completed package approval is unavailable" {
		t.Fatalf("archive approval did not admit ancestor %s past helper check: %v", h0, archiveErr)
	}
}

func slicesCloneTest(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}
