//go:build linux

package signer

import (
	"bytes"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func TestInstalledSequenceCompletion(t *testing.T) {
	dir := fixtureDir(t)
	v, _, _ := configuredFixture(t, dir)
	i, b := installationFixture(t, v)
	vault, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	mock, _ := os.ReadFile(filepath.Join(dir, "mock-receipts.log"))
	for _, phase := range []string{"active", "healthy-held", "committed-held"} {
		got, e := v.Installation(installCommand(t, v, i, b, phase))
		if e != nil {
			t.Fatalf("%s: %v", phase, e)
		}
		if !update.ValidInstallation(*got) {
			t.Fatal(got)
		}
	}
	if _, e := v.AbortUpdate(i.ID); e == nil {
		t.Fatal("committed abort permitted")
	}
	if _, e := v.Installation(installCommand(t, v, i, b, "rolled-back-held")); e == nil {
		t.Fatal("committed rollback permitted")
	}
	v.Close()
	v = openFixture(t, dir)
	if got, e := v.Installation(installCommand(t, v, i, b, "completed")); e != nil || got.Phase != "completed" {
		t.Fatal(got, e)
	}
	v.Close()
	v = openFixture(t, dir)
	defer v.Close()
	seq := v.ledger.main.seq
	for n := 0; n < 2; n++ {
		got, e := v.Installation(update.InstallRequest{Action: "completed", IntentID: i.ID, Binding: &b})
		if e != nil || got.Phase != "completed" {
			t.Fatal(got, e)
		}
	}
	s, e := v.UpdateInventory(update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	if s.ActiveIntentID != "" || s.SequenceFloor != 1 || s.InstalledSequence != 1 || !v.Status().Locked || v.ledger.main.seq != seq {
		t.Fatal(s)
	}
	for name, want := range map[string][]byte{"vault.json": vault, "mock-receipts.log": mock} {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if !bytes.Equal(got, want) {
			t.Fatal("financial bytes changed", name)
		}
	}
	for _, field := range []string{"floor", "authority", "active"} {
		saved := v.ledger.ref
		switch field {
		case "floor":
			v.ledger.updateFloor = 2
		case "authority":
			v.ledger.ref.Epoch = update.NewProbe()
		case "active":
			v.ledger.activeUpdate = update.NewProbe()
		}
		if _, e := v.Installation(update.InstallRequest{Action: "status", IntentID: i.ID}); e == nil {
			t.Fatal("stale completion accepted", field)
		}
		v.ledger.updateFloor = 1
		v.ledger.ref = saved
		v.ledger.activeUpdate = ""
	}
}
func TestInstalledSequencePersistenceFaultAndConcurrentRetry(t *testing.T) {
	for _, phase := range []string{"committed-held", "completed"} {
		for _, step := range []string{"before", "written", "durable"} {
			t.Run(phase+"/"+step, func(t *testing.T) {
				dir := fixtureDir(t)
				v, _, _ := configuredFixture(t, dir)
				i, b := installationFixture(t, v)
				for _, p := range []string{"active", "healthy-held"} {
					if _, e := v.Installation(installCommand(t, v, i, b, p)); e != nil {
						t.Fatal(e)
					}
				}
				if phase == "completed" {
					if _, e := v.Installation(installCommand(t, v, i, b, "committed-held")); e != nil {
						t.Fatal(e)
					}
				}
				request := installCommand(t, v, i, b, phase)
				v.store.crash = func(at string) error {
					if at == "journal:update-install:"+step {
						return syscall.ENOSPC
					}
					return nil
				}
				if _, e := v.Installation(request); e == nil {
					t.Fatal("fault missed")
				}
				v.Close()
				v = openFixture(t, dir)
				defer v.Close()
				if got, e := v.Installation(update.InstallRequest{Action: "status", IntentID: i.ID}); e == nil && got.Phase == "completed" {
					request = update.InstallRequest{Action: phase, IntentID: i.ID, Binding: &b}
				} else {
					request = installCommand(t, v, i, b, phase)
				}
				var wg sync.WaitGroup
				var mu sync.Mutex
				success := 0
				for n := 0; n < 3; n++ {
					wg.Go(func() {
						if _, e := v.Installation(request); e == nil {
							mu.Lock()
							success++
							mu.Unlock()
						}
					})
				}
				wg.Wait()
				if success == 0 {
					t.Fatal("no successful retry")
				}
				s, e := v.UpdateInventory(update.NewProbe())
				if e != nil || s.InstalledSequence != 1 || s.SequenceFloor != 1 || !v.Status().Locked {
					t.Fatal(s, e)
				}
				if phase == "committed-held" && s.ActiveIntentID != i.ID {
					t.Fatal("commit released hold")
				}
			})
		}
	}
}
