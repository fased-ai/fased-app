//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"runtime"
	"strings"
	"testing"
)

func TestRemoteApplicationReaderAfterStopHookAndHeldFailure(t *testing.T) {
	f := remoteApplicationFixture(t)
	f.c.ReaderBootstrap = true
	f.cb.AfterStop = func(_ context.Context, a, b Manifest) error {
		if f.h.active {
			t.Fatal("reader migration with active service")
		}
		m := f.h.manifest()
		if m != a || b.CoreSHA256 != f.c.EngineSHA256 {
			t.Fatal("reader callback did not precede swap")
		}
		return errors.New("held reader bootstrap")
	}
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("reader hook refusal ignored")
	}
	if f.h.manifest() != f.old || f.h.active {
		t.Fatal("code replaced or service started despite held reader")
	}
}

func TestRemoteApplicationReaderSourceInventoryUsesOriginalFixedCommand(t *testing.T) {
	_, o, m, c := ownerCallFixture(t)
	got, e := applicationReaderInventory(t.Context(), m, c.ID, o, func(_ context.Context, args []string) ([]byte, error) {
		base := applicationOwnerHelperArgs(m, m.Core)
		want := append(append([]string{}, base[:len(base)-4]...), m.Core, "update-inventory", "--data-dir", m.Data, "--probe-id", c.ID)
		if len(args) != len(want) {
			t.Fatal(args)
		}
		for i := range args {
			if args[i] != want[i] {
				t.Fatal(args)
			}
		}
		return encoded(update.CoreSnapshot{Version: 1, ProbeID: c.ID, InstanceID: "source", State: update.State{Name: "core-db", Version: 33}}), nil
	})
	if e != nil || got.State.Version != 33 {
		t.Fatal(got, e)
	}
	if _, e = applicationReaderInventory(t.Context(), m, "../foreign", o, func(context.Context, []string) ([]byte, error) { t.Fatal("foreign probe dispatched"); return nil, nil }); e == nil {
		t.Fatal("foreign source probe accepted")
	}
}

func TestRemoteApplicationReaderAdmitsOnlyExactStagedTarget(t *testing.T) {
	f := remoteApplicationFixture(t)
	f.c.ReaderBootstrap = true
	f.cb.AfterStop = func(context.Context, Manifest, Manifest) error { return nil }
	f.denyFence = true
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("fixture fence missing")
	}
	j, e := readApplicationJournal(f.o)
	if e != nil {
		t.Fatal(e)
	}
	oldImage := f.old.Root + "-update-" + f.c.ID + "/previous-core"
	image := strings.TrimSuffix(oldImage, "/previous-core") + "/next-core"
	o := appOwnerOps(f, image)
	if _, e := validateApplicationOwnerHelper(f.old.Config, image, f.old.AppUID, o); e != nil {
		t.Fatal("admitted staged target identity refused", e)
	}
	r := core.ApplicationReaderBootstrapRequest{Version: 1, OperationID: j.Status.ID, OwnerLease: update.NewProbe(), PreviousEngineSHA256: j.Previous.CoreSHA256, Release: core.ApplicationRelease{Version: j.ProductVersion, Sequence: j.Status.Sequence, ManifestSHA256: j.Status.ReleaseSHA256, EngineSHA256: j.Target.CoreSHA256, Target: update.Target{OS: "linux", Arch: runtime.GOARCH}}}
	if e := validateApplicationReaderOwnerRequest(o, f.old, image, r); e != nil {
		t.Fatal(e)
	}
	if e := validateApplicationReaderOwnerRequest(o, f.old, oldImage, r); e == nil {
		t.Fatal("original reader image admitted as migration target")
	}
	r.Release.Sequence++
	if e := validateApplicationReaderOwnerRequest(o, f.old, image, r); e == nil {
		t.Fatal("unadmitted release accepted")
	}
}

func TestRemoteApplicationReaderFlagBoundAcrossInterruptedResume(t *testing.T) {
	f := remoteApplicationFixture(t)
	f.c.ReaderBootstrap = true
	hook := func(context.Context, Manifest, Manifest) error { return nil }
	f.cb.AfterStop = hook
	f.denyFence = true
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("fixture fence absent")
	}
	j, e := readApplicationJournal(f.o)
	if e != nil || !j.Status.ReaderBootstrap {
		t.Fatal(j, e)
	}
	f.c.ReaderBootstrap = false
	f.cb.AfterStop = nil
	before := len(f.h.calls)
	if _, e = f.apply(t, false); e == nil || len(f.h.calls) != before {
		t.Fatal("changed reader flag admitted", e)
	}
	f.cb.AfterStop = hook
	f.denyFence = false
	if _, e = resumeRemoteApplication(t.Context(), f.o, f.old, f.c.ID, f.cb, false); e != nil {
		t.Fatal("reader flag lost on retained resume", e)
	}
	j, e = readApplicationJournal(f.o)
	if e != nil || !j.Status.ReaderBootstrap || j.Status.Phase != "completed" {
		t.Fatal(j, e)
	}
}

func TestRemoteApplicationReaderFlagPreventsOldCodeRollback(t *testing.T) {
	f := remoteApplicationFixture(t)
	f.c.ReaderBootstrap = true
	f.cb.AfterStop = func(context.Context, Manifest, Manifest) error { return nil }
	f.badHealth = true
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("bad target accepted")
	}
	if f.h.manifest().CoreSHA256 != f.c.EngineSHA256 {
		t.Fatal("old reader restored after reader migration")
	}
	for _, call := range f.calls {
		if call == "rollback-check" {
			t.Fatal("reader entered same-schema rollback")
		}
	}
}
