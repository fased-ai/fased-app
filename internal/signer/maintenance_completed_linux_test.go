//go:build linux

package signer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestInitialMaintenanceJournalPreservesSignerFence(t *testing.T) {
	const owner = 1800
	id := strings.Repeat("a", 32)
	root := "/var/lib/fased-app-custody-1800"
	keyHash := strings.Repeat("b", 64)
	c := Config{Version: 3, OwnerUID: owner, SignerUID: 1802, SignerID: id, StateDir: root + "/signer-state", Provenance: ControlledCustodyProfile}
	base, _ := json.Marshal(map[string]any{"version": 3, "enrollment": true, "owner": owner, "id": id, "root": root, "signerHash": keyHash, "signer": map[string]any{"uid": c.SignerUID}})
	core := update.Contract{Lifecycle: update.CurrentLifecycle(), Version: 1, Component: "core", Protocol: 1, PeerProtocol: update.Range{Min: 3, Max: 3}, Features: []string{"account-registry-v1", "fixture-proposals-v1", update.OrdinaryMaintenanceFeature, update.JournalBasePackageFeature}, RequiresPeer: []string{update.OrdinaryMaintenanceFeature}, State: []update.StateSupport{{Name: "core-db", Versions: update.Range{Min: 36, Max: 36}}}}
	p := &update.MaintenancePackage{Version: "0.1.0", Source: strings.Repeat("c", 40), Sequence: 1, ReleaseSHA256: strings.Repeat("d", 64), DevelopmentOnly: true, Components: []update.Component{
		{Version: "0.1.0", Contract: core, Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: strings.Repeat("e", 64)}},
		{Version: "0.1.0", Contract: OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: keyHash}},
	}}
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: id, Phase: "idle", Start: "base", Base: base, Target: base, Package: p}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if !protectedMaintenanceRecordRPCAllowed(c, Request{Action: "unlock"}, true, r, func() bool { return true }) {
		t.Fatal("a verified idle journal blocked normal signer access")
	}
	if protectedMaintenanceRecordRPCAllowed(c, Request{Action: "unlock"}, true, r, func() bool { return false }) {
		t.Fatal("an existing fence was bypassed")
	}
	r.Installation = &update.Installation{}
	if protectedMaintenanceRecordRPCAllowed(c, Request{Action: "unlock"}, true, r, func() bool { return true }) {
		t.Fatal("a retained installation hold was bypassed")
	}
}

func TestCompletedMaintenanceFenceDeniesMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
	}{
		{"present"}, {"foreign"}, {"unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, action := range []string{"unlock", "create", "swap-sign", "receipt-rpc-save"} {
				if completedMaintenanceRPCAllowed(Request{Action: action}, func() bool { return false }) {
					t.Fatal("mutation allowed with a held or uninspectable fence", action)
				}
			}
			for _, action := range []string{"status", "lock"} {
				if !completedMaintenanceRPCAllowed(Request{Action: action}, func() bool { return false }) {
					t.Fatal("safe recovery status was refused", action)
				}
			}
		})
	}
	if !completedMaintenanceRPCAllowed(Request{Action: "unlock"}, func() bool { return true }) {
		t.Fatal("confirmed absent fence did not restore normal authorization")
	}
}

func TestMaintenanceFenceAbsenceRequiresSameExistingDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "custody")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if !maintenanceFenceAbsentAt(root, dir) {
		t.Fatal("existing anchored directory with no marker was refused")
	}
	marker := filepath.Join(root, "update-active.json")
	if err = os.WriteFile(marker, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if maintenanceFenceAbsentAt(root, dir) {
		t.Fatal("present foreign marker treated as absent")
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(root, root+"-retained"); err != nil {
		t.Fatal(err)
	}
	if maintenanceFenceAbsentAt(root, dir) || completedMaintenanceRPCAllowed(Request{Action: "unlock"}, func() bool { return maintenanceFenceAbsentAt(root, dir) }) {
		t.Fatal("renamed parent restored mutation authority")
	}
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if maintenanceFenceAbsentAt(root, dir) {
		t.Fatal("replacement parent restored mutation authority")
	}
}

func TestMissingCustodyRootDoesNotImplyNoMaintenance(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing-installation")
	c := Config{Version: 3, OwnerUID: 2147483000, StateDir: filepath.Join(missingRoot, "signer-state")}
	if protectedMaintenanceRPCAllowed(c, Request{Action: "unlock"}, true) {
		t.Fatal("missing custody root granted signer authority")
	}
	if !protectedMaintenanceRPCAllowed(c, Request{Action: "status"}, true) {
		t.Fatal("safe status should remain available")
	}
	c.Version = 1
	if !protectedMaintenanceRPCAllowed(c, Request{Action: "unlock"}, true) {
		t.Fatal("legacy non-custody predecessor was blocked")
	}
}
