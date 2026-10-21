//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func portableFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := fixtureDir(t)
	v, _, g := configuredFixture(t, dir)
	first := intentFor(g, 5)
	if _, e := v.ReserveFixture(first); e != nil {
		t.Fatal(e)
	}
	if _, e := v.ExecuteFixture(first.OperationID); e != nil {
		t.Fatal(e)
	}
	pending := intentFor(g, 7)
	if _, e := v.ReserveFixture(pending); e != nil {
		t.Fatal(e)
	}
	b, h := original(pending)
	if e := v.ledger.append(v, journalRecord{Kind: "prepare", ID: pending.OperationID, Original: b, OriginalHash: h}); e != nil {
		t.Fatal(e)
	}
	if e := v.ledger.append(v, journalRecord{Kind: "sending", ID: pending.OperationID}); e != nil {
		t.Fatal(e)
	}
	i, binding := installationFixture(t, v)
	for _, phase := range []string{"active", "healthy-held", "committed-held", "completed"} {
		if _, e := v.Installation(installCommand(t, v, i, binding, phase)); e != nil {
			t.Fatal(phase, e)
		}
	}
	request := intentFixture(t, v, 2)
	if _, e := v.PrepareUpdate(request); e != nil {
		t.Fatal(e)
	}
	if _, e := v.AbortUpdate(request.ID); e != nil {
		t.Fatal(e)
	}
	id := v.signerID
	if e := v.Close(); e != nil {
		t.Fatal(e)
	}
	return dir, id
}

func TestPortableSnapshotConcurrentDestinations(t *testing.T) {
	dir, id := portableFixture(t)
	a, ra, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	b, rb, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	parent := fixtureDir(t)
	target := filepath.Join(parent, "quarantine")
	type result struct {
		r PortableReceipt
		e error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for _, v := range []struct {
		b []byte
		r PortableReceipt
	}{{a, ra}, {b, rb}} {
		go func() {
			start.Wait()
			r, e := RestorePortableSnapshot(target, v.b, backupPassword, v.r.ArchiveSHA256)
			results <- result{r, e}
		}()
	}
	start.Done()
	success := 0
	var winner PortableReceipt
	for range 2 {
		v := <-results
		if v.e == nil {
			success++
			winner = v.r
		}
	}
	if success != 1 {
		t.Fatal("conflicting import has no single winner", success)
	}
	got, e := os.ReadFile(filepath.Join(target, "receipt.json"))
	if e != nil {
		t.Fatal(e)
	}
	var receipt PortableReceipt
	if json.Unmarshal(got, &receipt) != nil || receipt != winner {
		t.Fatal("mixed concurrent import")
	}
}
func assertPortable(t *testing.T, r PortableReceipt) {
	t.Helper()
	if !r.FixtureOnly || !r.Historical || !r.ReadOnly || !r.Locked || !r.RecoveryRequired || r.InstallAllowed || r.ResumeAllowed || r.WalletSigner || r.FinancialExecution || r.AutomaticUpdates {
		t.Fatal("recovery conferred authority", r)
	}
}
func TestPortableSnapshotColdRoundTrip(t *testing.T) {
	dir, id := portableFixture(t)
	originals := map[string][]byte{}
	for _, name := range []string{"vault.json", "operations.log", "mock-receipts.log"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		originals[name] = b
	}
	doc, receipt, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	assertPortable(t, receipt)
	if receipt.History.Spent != 5 || receipt.History.Held != 7 || receipt.History.Unknown != 1 || receipt.History.InstalledSequence != 1 || receipt.History.ReservedFloor != 2 {
		t.Fatal("nonzero state missing", receipt.History)
	}
	second, r2, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil || bytes.Equal(second, doc) || r2.History != receipt.History {
		t.Fatal("capture changed history/randomness", e)
	}
	for n, b := range originals {
		got, e := os.ReadFile(filepath.Join(dir, n))
		if e != nil || !bytes.Equal(got, b) {
			t.Fatal("source changed", n)
		}
	}
	offline := dir + ".source-unavailable"
	if e = os.Rename(dir, offline); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(offline); e != nil {
			t.Error(e)
		}
	})
	parent := fixtureDir(t)
	target := filepath.Join(parent, "quarantine")
	got, e := RestorePortableSnapshot(target, doc, backupPassword, receipt.ArchiveSHA256)
	if e != nil || got != receipt {
		t.Fatal("portable recovery", e, got)
	}
	assertPortable(t, got)
	for n, b := range originals {
		copy, e := os.ReadFile(filepath.Join(target, n+".snapshot"))
		if e != nil || !bytes.Equal(copy, b) {
			t.Fatal("original bytes changed", n)
		}
	}
	for _, n := range []string{"vault.json", "operations.log", "mock-receipts.log", "vault.lock"} {
		if _, e = os.Stat(filepath.Join(target, n)); !os.IsNotExist(e) {
			t.Fatal("published live signer filename", n)
		}
	}
	if got, e = RestorePortableSnapshot(target, doc, backupPassword, receipt.ArchiveSHA256); e != nil || got != receipt {
		t.Fatal("historical exact retry", e)
	}
	if _, e = RestorePortableSnapshot(filepath.Join(parent, "stale"), doc, backupPassword, r2.ArchiveSHA256); e == nil {
		t.Fatal("stale snapshot accepted against newer pin")
	}
	b, _ := json.Marshal(receipt)
	t.Logf("public recovery inputs/receipt: %s", b)
}
func TestPortableSnapshotInvalidInputs(t *testing.T) {
	dir, id := portableFixture(t)
	doc, r, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"wrong-password", "missing-pin", "wrong-pin", "truncated", "tampered", "oversize", "production", "header-counter", "header-floor", "header-account", "reader", "torn-history"} {
		t.Run(name, func(t *testing.T) {
			b := append([]byte(nil), doc...)
			pin := r.ArchiveSHA256
			pw := backupPassword
			switch name {
			case "wrong-password":
				pw = fixturePassword
			case "missing-pin":
				pin = ""
			case "wrong-pin":
				pin = portableHash(nil)
			case "truncated":
				b = b[:len(b)-1]
			case "tampered":
				b[len(b)-1] ^= 1
			case "oversize":
				b = make([]byte, MaxPortableSnapshot+1)
			default:
				d, e := unsealPortable(b, pw, pin)
				if e != nil {
					t.Fatal(e)
				}
				switch name {
				case "production":
					d.header.FixtureOnly = false
				case "header-counter":
					d.header.History.Spent = 0
				case "header-floor":
					d.header.History.ReservedFloor = 0
				case "header-account":
					d.header.History.Account.Epoch = update.NewProbe()
				case "reader":
					d.header.ContractSHA256 = portableHash(nil)
				case "torn-history":
					d.files[1] = d.files[1][:len(d.files[1])-1]
				}
				b, e = sealPortable(d, pw)
				if e != nil {
					t.Fatal(e)
				}
				pin = portableHash(b)
			}
			parent := fixtureDir(t)
			target := filepath.Join(parent, "quarantine")
			if _, e = RestorePortableSnapshot(target, b, pw, pin); e == nil {
				t.Fatal("unsafe import", name)
			}
			if entries, e := os.ReadDir(parent); e != nil || len(entries) != 0 {
				t.Fatal("rejected archive left state", e, len(entries))
			}
		})
	}
}
func TestPortableSnapshotColdSourceRefusals(t *testing.T) {
	for _, name := range []string{"running", "missing-lock", "missing-journal", "torn-journal", "symlink-journal", "active-update", "foreign-identity", "wrong-source-password", "damaged-vault"} {
		t.Run(name, func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, _ := configuredFixture(t, dir)
			id := v.signerID
			pw := fixturePassword
			if name == "active-update" {
				request := intentFixture(t, v, 1)
				if _, e := v.PrepareUpdate(request); e != nil {
					t.Fatal(e)
				}
			}
			if name != "running" {
				if e := v.Close(); e != nil {
					t.Fatal(e)
				}
			}
			defer v.Close()
			switch name {
			case "missing-lock":
				_ = os.Remove(filepath.Join(dir, "vault.lock"))
			case "missing-journal":
				_ = os.Remove(filepath.Join(dir, "operations.log"))
			case "torn-journal":
				f, e := os.OpenFile(filepath.Join(dir, "operations.log"), os.O_APPEND|os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				_, _ = f.Write([]byte("torn"))
				_ = f.Close()
			case "symlink-journal":
				_ = os.Rename(filepath.Join(dir, "operations.log"), filepath.Join(dir, "saved"))
				_ = os.Symlink("saved", filepath.Join(dir, "operations.log"))
			case "foreign-identity":
				id = update.NewProbe()
			case "wrong-source-password":
				pw = backupPassword
			case "damaged-vault":
				path := filepath.Join(dir, "vault.json")
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				var record envelope
				if json.Unmarshal(b, &record) != nil {
					t.Fatal("fixture record")
				}
				record.Ciphertext[0] ^= 1
				b, _ = json.Marshal(record)
				if e = os.WriteFile(path, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if _, _, e := PackPortableSnapshot(dir, id, pw, backupPassword); e == nil {
				t.Fatal("unsafe cold capture")
			}
			if name == "missing-lock" {
				if _, e := os.Stat(filepath.Join(dir, "vault.lock")); !os.IsNotExist(e) {
					t.Fatal("cold capture created lock")
				}
			}
		})
	}
}
func TestPortableSnapshotQuarantineCuts(t *testing.T) {
	dir, id := portableFixture(t)
	doc, r, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	for _, phase := range []string{"intent.json", "vault.json.snapshot", "operations.log.snapshot", "mock-receipts.log.snapshot", "receipt.json", "renamed"} {
		t.Run(phase, func(t *testing.T) {
			parent := fixtureDir(t)
			target := filepath.Join(parent, "quarantine")
			cut := errors.New("injected quarantine interruption")
			_, e := restorePortableSnapshot(target, doc, backupPassword, r.ArchiveSHA256, func(p string) error {
				if p == phase {
					return cut
				}
				return nil
			})
			if !errors.Is(e, cut) {
				t.Fatal("cut missed", e)
			}
			got, e := RestorePortableSnapshot(target, doc, backupPassword, r.ArchiveSHA256)
			if e != nil || got != r {
				t.Fatal("exact prefix recovery", phase, e)
			}
			if _, e = os.Stat(target + ".pending"); !os.IsNotExist(e) {
				t.Fatal("pending left after recovered completion")
			}
		})
	}
}
func TestPortableSnapshotQuarantineConflicts(t *testing.T) {
	dir, id := portableFixture(t)
	doc, r, e := PackPortableSnapshot(dir, id, fixturePassword, backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"foreign-target", "empty-pending", "foreign-pending", "partial-known-file", "symlink", "hardlink", "mode", "dual-final-pending"} {
		t.Run(name, func(t *testing.T) {
			parent := fixtureDir(t)
			target := filepath.Join(parent, "quarantine")
			pending := target + ".pending"
			if name == "foreign-target" {
				_ = os.Mkdir(target, 0700)
				_ = os.WriteFile(filepath.Join(target, "owner-data"), []byte("preserve"), 0600)
			} else if name == "empty-pending" {
				_ = os.Mkdir(pending, 0700)
			} else {
				_, e := restorePortableSnapshot(target, doc, backupPassword, r.ArchiveSHA256, func(p string) error {
					if p == "vault.json.snapshot" {
						return errors.New("cut")
					}
					return nil
				})
				if e == nil {
					t.Fatal("cut missed")
				}
				file := filepath.Join(pending, "vault.json.snapshot")
				switch name {
				case "foreign-pending":
					_ = os.WriteFile(filepath.Join(pending, "owner-data"), []byte("preserve"), 0600)
				case "partial-known-file":
					_ = os.WriteFile(file, []byte("torn"), 0600)
				case "symlink":
					_ = os.Remove(file)
					_ = os.Symlink("intent.json", file)
				case "hardlink":
					_ = os.Remove(file)
					_ = os.Link(filepath.Join(pending, "intent.json"), file)
				case "mode":
					_ = os.Chmod(file, 0644)
				case "dual-final-pending":
					if _, e = RestorePortableSnapshot(target, doc, backupPassword, r.ArchiveSHA256); e != nil {
						t.Fatal(e)
					}
					_ = os.Mkdir(pending, 0700)
				}
			}
			if _, e := RestorePortableSnapshot(target, doc, backupPassword, r.ArchiveSHA256); e == nil {
				t.Fatal("foreign/conflicting recovery adopted", name)
			}
			if name == "foreign-target" || name == "foreign-pending" {
				p := target
				if name == "foreign-pending" {
					p = pending
				}
				b, e := os.ReadFile(filepath.Join(p, "owner-data"))
				if e != nil || string(b) != "preserve" {
					t.Fatal("foreign work changed")
				}
			}
		})
	}
}
