//go:build linux

package signer

import (
	"errors"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestSignerResponseClassifiesOnlyOwnerInstallationStatusAbsence(t *testing.T) {
	status := Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status"}}
	for _, tc := range []struct {
		name     string
		owner    bool
		request  Request
		response Response
		absent   bool
	}{
		{"older signer status", true, status, Response{Error: "no installation hold"}, true},
		{"non-owner", false, status, Response{Error: "no installation hold"}, false},
		{"other action", true, Request{Action: "fixture-update-status"}, Response{Error: "no installation hold"}, false},
		{"other install action", true, Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "active"}}, Response{Error: "no installation hold"}, false},
		{"conflicting receipt", true, status, Response{Error: "no installation hold", Installation: &update.Installation{}}, false},
		{"other signer failure", true, status, Response{Error: "signer unavailable"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := signerResponseError(tc.owner, tc.request, tc.response)
			if errors.Is(err, ErrNoInstallationHold) != tc.absent {
				t.Fatalf("incorrect absence classification: %v", err)
			}
		})
	}
}
