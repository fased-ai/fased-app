//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageWalletWindowPreservesPendingUpdate(t *testing.T) {
	root := t.TempDir()
	if err := packageWalletWindowIdle(root, update.MaintenanceRecord{Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := packageWalletWindowIdle(root, update.MaintenanceRecord{Phase: "completed", Start: "target"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []update.MaintenanceRecord{{Phase: "prepared", Start: "base"}, {Phase: "held"}, {Phase: "completed", Start: "base"}} {
		if err := packageWalletWindowIdle(root, r); err == nil {
			t.Fatal("an unfinished update allowed Wallets", r.Phase, r.Start)
		}
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("preserve this hold"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := packageWalletWindowIdle(root, update.MaintenanceRecord{Phase: "completed", Start: "target"}); err == nil {
			t.Fatal("a retained update fence allowed Wallets", name)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != "preserve this hold" {
			t.Fatal("the original fence changed", name, err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
