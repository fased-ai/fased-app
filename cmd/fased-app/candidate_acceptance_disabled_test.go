//go:build !linux || !fased_candidate_acceptance

package main

import (
	"io"
	"testing"
)

func TestOrdinaryBuildRejectsInstalledCandidateAdmission(t *testing.T) {
	if err := candidateAcceptanceCommand(nil, io.Discard, io.Discard); err == nil {
		t.Fatal("ordinary build admitted a staged source")
	}
}
