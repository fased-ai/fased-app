package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestTrustRehearsalStrictExpiry(t *testing.T) {
	for _, role := range []string{"root", "timestamp", "snapshot", "targets"} {
		t.Run(role, func(t *testing.T) {
			f := newTrustFixture(t)
			switch role {
			case "root":
				f.root.Signed.Expires = time.Unix(f.now, 0)
				f.seed.Root = f.rootSign()
			case "timestamp":
				f.ts.Signed.Expires = time.Unix(f.now, 0)
				f.publishTargets(f.bytes(f.tg))
			case "snapshot":
				f.sn.Signed.Expires = time.Unix(f.now, 0)
				f.publishTargets(f.bytes(f.tg))
			case "targets":
				f.tg.Signed.Expires = time.Unix(f.now, 0)
				f.tg.ClearSignatures()
				_, _ = f.tg.Sign(f.keys["targets"][0])
				_, _ = f.tg.Sign(f.keys["targets"][1])
				f.publishTargets(f.bytes(f.tg))
			}
			if _, e := RehearseTrust(context.Background(), f.options(nil)); e == nil {
				t.Fatal("equal-deadline metadata accepted without prior state")
			}
		})
	}
}

func fixtureStore(t *testing.T) (*FixtureTrustStore, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("private store ACL acceptance is unsupported")
	}
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	s, e := OpenFixtureTrustStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestTrustRehearsalStoreInterruption(t *testing.T) {
	for _, point := range []string{"before-snapshot", "partial-snapshot", "after-snapshot", "partial-head", "before-head", "after-head"} {
		t.Run(point, func(t *testing.T) {
			f := newTrustFixture(t)
			first := f.run(nil)
			s, dir := fixtureStore(t)
			s.fault = func(p string) error {
				if p == point {
					return errors.New("injected write interruption")
				}
				return nil
			}
			if _, e := s.Commit(first); e == nil {
				t.Fatal("fault not reached")
			}
			s.fault = nil
			restarted, e := OpenFixtureTrustStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer restarted.Close()
			h, e := restarted.Commit(first)
			if e != nil || h != first.report.NextSHA256 {
				t.Fatal("exact retry failed", e)
			}
			if b, e := restarted.Load(first.seedSHA, h); e != nil || string(b) != string(first.next) {
				t.Fatal("lost durable snapshot", e)
			}
			entries, e := os.ReadDir(dir)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				st, e := entry.Info()
				if e != nil || st.Mode().Perm() != 0600 {
					t.Fatal("nonprivate fixture state")
				}
			}
		})
	}
}

func TestTrustRehearsalStoreCASAndPreservation(t *testing.T) {
	f := newTrustFixture(t)
	first := f.run(nil)
	s, dir := fixtureStore(t)
	if _, e := s.Commit(TrustTransition{}); e == nil {
		t.Fatal("forged transition accepted")
	}
	if _, e := s.Commit(first); e != nil {
		t.Fatal(e)
	}
	f.now++
	f.release(2, 11)
	a := f.run(&first)
	f.now++
	f.release(3, 12)
	b := f.run(&first)
	second, e := OpenFixtureTrustStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, item := range []struct {
		s *FixtureTrustStore
		t TrustTransition
	}{{s, a}, {second, b}} {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := item.s.Commit(item.t); errs <- e }()
	}
	wg.Wait()
	close(errs)
	pass, refuse := 0, 0
	for e := range errs {
		if e == nil {
			pass++
		} else {
			refuse++
		}
	}
	if pass != 1 || refuse != 1 {
		t.Fatal("CAS did not serialize independent file handles", pass, refuse)
	}
	if _, e := s.Load(first.seedSHA, first.report.NextSHA256); e == nil {
		t.Fatal("stale external anchor accepted")
	}
	h, e := s.head()
	if e != nil {
		t.Fatal(e)
	}
	current, e := s.Load(first.seedSHA, h.StateSHA256)
	if e != nil {
		t.Fatal(e)
	}
	var state TrustFixtureSnapshot
	if StrictJSON(current, &state) != nil || state.InstalledFloor != 3 || state.ReservedFloor != 4 || state.PreserveSHA256 != f.seed.PreserveSHA256 {
		t.Fatal("financial fixture anchors changed")
	}
	oldHead := tj(fixtureTrustHead{Version: 1, Purpose: RehearsalPurpose, SeedSHA256: first.seedSHA, StateSHA256: first.report.NextSHA256})
	if e = os.WriteFile(filepath.Join(dir, "head.json"), oldHead, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Load(first.seedSHA, h.StateSHA256); e == nil {
		t.Fatal("restored-old head accepted")
	}
	if e = os.Remove(filepath.Join(dir, "head.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Commit(first); e == nil {
		t.Fatal("missing head reset nonempty store")
	}
}

func TestTrustRehearsalStoreForeignAndCorrupt(t *testing.T) {
	for _, kind := range []string{"foreign", "symlink", "corrupt", "missing-blob"} {
		t.Run(kind, func(t *testing.T) {
			f := newTrustFixture(t)
			first := f.run(nil)
			s, dir := fixtureStore(t)
			if kind == "foreign" || kind == "symlink" {
				keep := filepath.Join(t.TempDir(), "keep")
				if e := os.WriteFile(keep, []byte("owner"), 0600); e != nil {
					t.Fatal(e)
				}
				if kind == "foreign" {
					if e := os.WriteFile(filepath.Join(dir, "keep"), []byte("owner"), 0600); e != nil {
						t.Fatal(e)
					}
				} else if e := os.Symlink(keep, filepath.Join(dir, "lock")); e != nil {
					t.Fatal(e)
				}
				if _, e := s.Commit(first); e == nil {
					t.Fatal("foreign store adopted")
				}
				if b, e := os.ReadFile(keep); e != nil || string(b) != "owner" {
					t.Fatal("foreign bytes touched")
				}
				return
			}
			if _, e := s.Commit(first); e != nil {
				t.Fatal(e)
			}
			name := filepath.Join(dir, first.report.NextSHA256+".json")
			if kind == "corrupt" {
				if e := os.WriteFile(name, []byte("bad"), 0600); e != nil {
					t.Fatal(e)
				}
			} else if e := os.Remove(name); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Load(first.seedSHA, first.report.NextSHA256); e == nil {
				t.Fatal("corrupt/missing snapshot accepted")
			}
		})
	}
}
