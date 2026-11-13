//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestSuccessorOwnerRequestIsBoundToOriginalUpdate(t *testing.T) {
	id, other, probe := strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)
	binding := update.InstallBinding{InstallationID: strings.Repeat("4", 32)}
	h := maintenance.Successor{OperationID: id, InstallationDigest: update.InstallDigest(binding)}
	preflight := &update.PreflightRequest{IntentID: id, ProbeID: probe}
	allowed := []signer.Request{
		{Action: "fixture-update-inventory", ProbeID: probe},
		{Action: "fixture-update-status", IntentID: id},
		{Action: "fixture-update-preflight", Preflight: preflight},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: id}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "healthy-held", IntentID: id, Binding: &binding, Preflight: preflight}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "committed-held", IntentID: id, Binding: &binding, Preflight: preflight}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "completed", IntentID: id, Binding: &binding, Preflight: preflight}},
	}
	for _, request := range allowed {
		if err := validateSuccessorOwnerRequest(h, request); err != nil {
			t.Fatalf("original operation %s refused: %v", request.Action, err)
		}
	}
	wrongBinding := binding
	wrongBinding.InstallationID = other
	refused := []signer.Request{
		{Action: "fixture-update-status", IntentID: other},
		{Action: "fixture-update-preflight", Preflight: &update.PreflightRequest{IntentID: other}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: other}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "completed", IntentID: id, Binding: &wrongBinding, Preflight: preflight}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "completed", IntentID: id, Binding: &binding, Preflight: &update.PreflightRequest{IntentID: other}}},
		{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "rolled-back-held", IntentID: id, Binding: &binding, Preflight: preflight}},
		{Action: "fixture-update-inventory", ProbeID: probe, Password: []byte("unexpected")},
		{Action: "fixture-update-abort", IntentID: id},
	}
	for _, request := range refused {
		if err := validateSuccessorOwnerRequest(h, request); err == nil {
			t.Fatalf("accepted foreign or unsupported request: %s", request.Action)
		}
	}
}
