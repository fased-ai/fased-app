package releaseops

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func frozenRelease() release {
	r := release{FixtureOnly: true, Purpose: purpose, RepositoryID: "7", ReleaseID: "9", Sequence: 10, UnitSHA256: sha([]byte("unit")), PreserveSHA256: sha([]byte("financial preservation")), InstalledFloor: 3, ReservedFloor: 4}
	for i, name := range []string{"core.fixture", "signer.fixture", "owner-helper.fixture", "installer.fixture", "evidence.json", "root.json", "targets.json", "snapshot.json", "timestamp.json", "descriptor.json"} {
		o := object{Name: name, Size: 1, SHA256: sha([]byte(name))}
		if i < 4 {
			o.AssetID = string(rune('1' + i))
		}
		r.Objects = append(r.Objects, o)
	}
	return r
}
func testLedger(t *testing.T) (*ledger, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	l, e := openLedger(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = l.close() })
	return l, dir
}
func TestReleaseLedgerReservationAndRetry(t *testing.T) {
	l, dir := testLedger(t)
	r := frozenRelease()
	if e := l.transaction(r, false); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "10.reserved.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = l.transaction(r, false); e != nil {
		t.Fatal(e)
	}
	changed := r
	changed.ReleaseID = "11"
	if e = l.transaction(changed, false); e == nil {
		t.Fatal("sequence rebound")
	}
	changed = r
	changed.Sequence = 9
	if e = l.transaction(changed, false); e == nil {
		t.Fatal("lower sequence")
	}
	if e = l.transaction(r, true); e != nil {
		t.Fatal(e)
	}
	if e = l.transaction(r, true); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(dir, "10.reserved.json"))
	if e != nil || string(after) != string(b) {
		t.Fatal("reservation rewritten")
	}
	changed = r
	changed.Sequence = 11
	if e = l.transaction(changed, false); e != nil {
		t.Fatal(e)
	}
}
func TestReleaseLedgerFaults(t *testing.T) {
	for _, phase := range []string{"created", "file-synced", "synced"} {
		t.Run(phase, func(t *testing.T) {
			l, dir := testLedger(t)
			r := frozenRelease()
			l.fault = func(p string) error {
				if p == phase {
					return errors.New("injected")
				}
				return nil
			}
			if e := l.transaction(r, false); e == nil {
				t.Fatal("fault missed")
			}
			l.fault = nil
			e := l.transaction(r, false)
			if phase == "created" && e == nil || phase != "created" && e != nil {
				t.Fatal("incorrect uncertain retry", e)
			}
			if _, e := os.Stat(filepath.Join(dir, "10.readback.json")); !os.IsNotExist(e) {
				t.Fatal("failed reservation claims readback")
			}
			if phase == "created" {
				r.Sequence++
				if e := l.transaction(r, false); e == nil {
					t.Fatal("torn reservation reset")
				}
			}
		})
	}
	for _, name := range []string{"foreign", "corrupt", "missing-reservation", "record-mode", "symlink", "anchor-change", "different-repository"} {
		t.Run(name, func(t *testing.T) {
			l, dir := testLedger(t)
			r := frozenRelease()
			if e := l.transaction(r, false); e != nil {
				t.Fatal(e)
			}
			p := filepath.Join(dir, "10.reserved.json")
			switch name {
			case "foreign":
				_ = os.WriteFile(filepath.Join(dir, "owner-data"), []byte("preserve"), 0600)
			case "corrupt":
				_ = os.WriteFile(p, []byte("{"), 0600)
			case "missing-reservation":
				if e := l.transaction(r, true); e != nil {
					t.Fatal(e)
				}
				_ = os.Remove(p)
			case "record-mode":
				_ = os.Chmod(p, 0644)
			case "symlink":
				_ = os.Rename(p, p+".saved")
				_ = os.Symlink(p+".saved", p)
			case "anchor-change":
				r.PreserveSHA256 = sha([]byte("changed"))
			case "different-repository":
				r.RepositoryID = "8"
			}
			if e := l.transaction(r, false); e == nil {
				t.Fatal("corruption accepted", name)
			}
			if name == "foreign" {
				b, e := os.ReadFile(filepath.Join(dir, "owner-data"))
				if e != nil || string(b) != "preserve" {
					t.Fatal("foreign work changed")
				}
			}
		})
	}
}
func TestReleaseLedgerConcurrencyAndBounds(t *testing.T) {
	l, dir := testLedger(t)
	r := frozenRelease()
	var wg sync.WaitGroup
	var successes atomic.Int64
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.transaction(r, false) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() < 1 {
		t.Fatal("no reservation")
	}
	if e := l.transaction(r, false); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 2 {
		t.Fatal("duplicate reservation", len(entries), e)
	}
	b, _ := json.Marshal(r)
	if e := os.WriteFile(filepath.Join(dir, "10.readback.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 256; i++ {
		p := filepath.Join(dir, "extra-"+string(rune(0x100+i)))
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := l.transaction(r, false); e == nil {
		t.Fatal("unbounded ledger")
	}
}

func TestReleaseLedgerReadbackAcknowledgementCuts(t *testing.T) {
	for _, phase := range []string{"created", "file-synced", "synced"} {
		t.Run(phase, func(t *testing.T) {
			l, dir := testLedger(t)
			r := frozenRelease()
			if e := l.transaction(r, false); e != nil {
				t.Fatal(e)
			}
			l.fault = func(p string) error {
				if p == phase {
					return errors.New("readback interruption")
				}
				return nil
			}
			// Skip the reservation's retry fsync hook; inject only in the new
			// readback record. The existing reservation remains immutable.
			b, _ := json.Marshal(r)
			if e := l.put("10.readback.json", b); e == nil {
				t.Fatal("cut missed")
			}
			l.fault = nil
			e := l.transaction(r, true)
			if phase == "created" && e == nil || phase != "created" && e != nil {
				t.Fatal("uncertain acknowledgement handling", e)
			}
			got, e := os.ReadFile(filepath.Join(dir, "10.reserved.json"))
			if e != nil || string(got) != string(b) {
				t.Fatal("reservation changed")
			}
		})
	}
}
func TestReleaseLedgerRejectsAuthorityAndPaths(t *testing.T) {
	l, _ := testLedger(t)
	for _, name := range []string{"production", "purpose", "floor", "unit", "helper", "hook", "asset", "size"} {
		t.Run(name, func(t *testing.T) {
			r := frozenRelease()
			switch name {
			case "production":
				r.FixtureOnly = false
			case "purpose":
				r.Purpose = "production"
			case "floor":
				r.Sequence = r.ReservedFloor
			case "unit":
				r.UnitSHA256 = "bad"
			case "helper":
				r.Objects = r.Objects[:9]
			case "hook":
				r.Objects[0].Name = "../../hook"
			case "asset":
				r.Objects[1].AssetID = r.Objects[0].AssetID
			case "size":
				r.Objects[0].Size = 64<<10 + 1
			}
			if e := l.transaction(r, false); e == nil {
				t.Fatal("unsafe release")
			}
		})
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0755)
	if l, e := openLedger(dir); e == nil {
		_ = l.close()
		t.Fatal("public ledger accepted")
	}
}
