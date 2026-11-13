//go:build linux

package signer

import (
	"os"
	"path/filepath"
	"testing"
)

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
