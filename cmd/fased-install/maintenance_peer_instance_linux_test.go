//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestPackageMaintenancePeerRequiresCommittedInstance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "core")
	first, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	committed := first.Health().InstanceID
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	other, err := core.OpenStore(filepath.Join(t.TempDir(), "core"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if committed == "" || reopened.Health().InstanceID != committed || other.Health().InstanceID == committed {
		t.Fatal("fixture did not retain a distinct core identity after reopening")
	}
	for _, tc := range []struct {
		name, location, committed, actual string
		wantErr                           bool
	}{
		{"reopened instance", "local", committed, reopened.Health().InstanceID, false},
		{"different instance", "local", committed, other.Health().InstanceID, true},
		{"missing commitment", "local", "", committed, true},
		{"missing live identity", "local", committed, "", true},
		{"legacy path", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyPackagePeerInstance(tc.location, tc.committed, tc.actual)
			if (err != nil) != tc.wantErr {
				t.Fatalf("identity check returned %v", err)
			}
		})
	}
}
