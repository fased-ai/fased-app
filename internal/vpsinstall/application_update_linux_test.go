//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type remoteUpdateFixture struct {
	h                                  *memoryHost
	o                                  hostOps
	old                                Manifest
	c                                  ApplicationUpdate
	cb                                 ApplicationUpdateCallbacks
	calls                              []string
	badHealth, denyRollback, denyFence bool
}

func remoteApplicationFixture(t *testing.T) *remoteUpdateFixture {
	t.Helper()
	h, o, m := installFixture(t)
	data := []byte("authenticated inert next Remote core")
	h.files["/verified/next-fased-app"] = hostFile{UID: 1000, GID: 1000, Mode: 0700, Regular: true, Data: data}
	f := &remoteUpdateFixture{h: h, o: o, old: m, c: ApplicationUpdate{Sequence: 7, ReleaseSHA256: strings.Repeat("1", 64), ID: strings.Repeat("a", 32), Version: "0.0.0-next", ExpectedManifestSHA256: ManifestSHA256(m), Engine: "/verified/next-fased-app", EngineSHA256: hash(data)}}
	run := o.run
	f.o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/mv" && contains(args, "--force") {
			h.calls = append(h.calls, append([]string{}, args...))
			from, to := args[len(args)-2], args[len(args)-1]
			value, ok := h.files[from]
			if !ok {
				return nil, os.ErrNotExist
			}
			h.files[to] = value
			delete(h.files, from)
			if h.fail == len(h.calls) {
				return nil, errors.New("interrupted after root-owned rename")
			}
			return nil, nil
		}
		return run(ctx, b, args...)
	}
	f.cb = ApplicationUpdateCallbacks{
		BeforeStop: func(context.Context, Manifest, Manifest) error {
			f.calls = append(f.calls, "fence")
			if f.denyFence {
				return errors.New("metadata/custody fence refused")
			}
			return nil
		},
		Health: func(_ context.Context, m Manifest) error {
			f.calls = append(f.calls, "health")
			if f.badHealth && m.CoreSHA256 == f.c.EngineSHA256 {
				return errors.New("target health failed")
			}
			return nil
		},
		RollbackAllowed: func(context.Context, Manifest, Manifest) error {
			f.calls = append(f.calls, "rollback-check")
			if f.denyRollback {
				return errors.New("forward-only schema")
			}
			return nil
		},
		Complete: func(_ context.Context, _ Manifest, s ApplicationUpdateStatus) error {
			f.calls = append(f.calls, s.Phase)
			return nil
		},
	}
	return f
}
func (f *remoteUpdateFixture) apply(t *testing.T, rollback bool) (Manifest, error) {
	t.Helper()
	return replaceApplication(t.Context(), f.o, f.old, f.c, f.cb, rollback)
}
func TestRemoteApplicationUpdateCommitsExactRootGeneration(t *testing.T) {
	f := remoteApplicationFixture(t)
	data := hostFile{UID: f.old.AppUID, GID: f.old.AppGID, Mode: 0600, Regular: true, Data: []byte("original secret owner state")}
	f.h.files[f.old.Data+"/original"] = data
	initial := len(f.h.calls)
	m, e := f.apply(t, false)
	if e != nil {
		t.Fatal(e)
	}
	if m.CoreSHA256 != f.c.EngineSHA256 || m.ID != f.old.ID || m.Data != f.old.Data || m.AppUID != f.old.AppUID || m.ApplicationSequence != 7 {
		t.Fatal(m)
	}
	if e = verify(t.Context(), f.o, m); e != nil {
		t.Fatal(e)
	}
	j, e := readApplicationJournal(f.o)
	if e != nil || j.Status.Phase != "completed" || j.Status.ReleaseSHA256 != f.c.ReleaseSHA256 {
		t.Fatal(j, e)
	}
	before := len(f.h.calls)
	callbacks := len(f.calls)
	if _, e = f.apply(t, false); e != nil || len(f.calls) != callbacks {
		t.Fatal("completed transaction replayed", e)
	}
	for _, a := range f.h.calls[before:] {
		if a[0] != "/usr/bin/passwd" {
			t.Fatal("completed transaction mutated installation", a)
		}
	}
	if string(f.h.files[f.old.Data+"/original"].Data) != string(data.Data) {
		t.Fatal("owner state changed")
	}
	for _, a := range f.h.calls[initial:] {
		if a[0] == "/usr/bin/mv" && !strings.Contains(a[len(a)-2], "-update-"+f.c.ID+"/") {
			t.Fatal("rename source escaped fixed update directory", a)
		}
	}
}
func TestRemoteApplicationUpdateRootInterruptionResumes(t *testing.T) {
	base := remoteApplicationFixture(t)
	initial := len(base.h.calls)
	if _, e := base.apply(t, false); e != nil {
		t.Fatal(e)
	}
	operations := len(base.h.calls) - initial
	for n := 1; n <= operations; n++ {
		// Read-only health/account probes may retry once within their existing
		// bounded readiness loop; inject interruption only at mutating OS steps.
		operation := base.h.calls[initial+n-1]
		if operation[0] == "/usr/bin/passwd" || operation[0] == "/usr/bin/setpriv" {
			continue
		}
		t.Run(string(rune('A'+n-1)), func(t *testing.T) {
			f := remoteApplicationFixture(t)
			f.h.fail = len(f.h.calls) + n
			if _, e := f.apply(t, false); e == nil {
				t.Fatal("injected interruption ignored", n)
			}
			f.h.fail = 0
			if _, e := f.apply(t, false); e != nil {
				t.Fatal("same root transaction cannot resume", n, e)
			}
			j, e := readApplicationJournal(f.o)
			if e != nil || (j.Status.Phase != "completed" && j.Status.Phase != "rolled-back") {
				t.Fatal(j, e)
			}
		})
	}
}
func TestRemoteApplicationUpdateHealthRollbackAndConsumedFloor(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatible", true: "held"}[deny], func(t *testing.T) {
			f := remoteApplicationFixture(t)
			f.badHealth = true
			f.denyRollback = deny
			if _, e := f.apply(t, false); e == nil {
				t.Fatal("target failure ignored")
			}
			j, e := readApplicationJournal(f.o)
			want := "rolled-back"
			if deny {
				want = "recovery"
			}
			if e != nil || j.Status.Phase != want || j.Status.Sequence != 7 {
				t.Fatal(j, e)
			}
			if deny {
				f.denyRollback = false
				if _, e = f.apply(t, true); e != nil {
					t.Fatal("compatible explicit rollback failed", e)
				}
			}
			if e = verify(t.Context(), f.o, f.old); e != nil {
				t.Fatal("previous compatible generation not restored", e)
			}
			f.c.ID = strings.Repeat("b", 32)
			f.c.Sequence = 7
			if _, e = f.apply(t, false); e == nil {
				t.Fatal("rollback erased consumed floor")
			}
			f.badHealth = false
			f.c.Sequence = 8
			if _, e = f.apply(t, false); e != nil {
				t.Fatal("new authenticated sequence refused", e)
			}
		})
	}
}
func TestRemoteApplicationUpdateRefusesForeignOrCustodyState(t *testing.T) {
	for _, kind := range []string{"source", "manifest", "unit", "custody", "desktop", "callback"} {
		t.Run(kind, func(t *testing.T) {
			f := remoteApplicationFixture(t)
			switch kind {
			case "source":
				v := f.h.files[f.c.Engine]
				v.Data = []byte("changed")
				f.h.files[f.c.Engine] = v
			case "manifest":
				f.c.ExpectedManifestSHA256 = strings.Repeat("0", 64)
			case "unit":
				v := f.h.files[f.old.UnitPath]
				v.Data = []byte("foreign unit")
				f.h.files[f.old.UnitPath] = v
			case "custody":
				f.denyFence = true
			case "desktop":
				f.c.Desktop = f.c.Engine
				f.c.DesktopSHA256 = f.c.EngineSHA256
			case "callback":
				f.cb.RollbackAllowed = nil
			}
			first := len(f.h.calls)
			if _, e := f.apply(t, false); e == nil {
				t.Fatal("invalid generation accepted", kind)
			}
			for _, a := range f.h.calls[first:] {
				if a[0] == "/usr/bin/systemctl" && contains(a, "stop") {
					t.Fatal("invalid operation stopped existing instance", a)
				}
			}
			if hash(f.h.files[f.old.Core].Data) != f.old.CoreSHA256 {
				t.Fatal("invalid operation changed core")
			}
		})
	}
}

func TestRemoteApplicationUpdateOwnerAndLeaseBoundary(t *testing.T) {
	for _, kind := range []string{"actor", "lease", "enrollment"} {
		t.Run(kind, func(t *testing.T) {
			f := remoteApplicationFixture(t)
			switch kind {
			case "actor":
				f.o.uid = f.old.AppUID
				f.o.euid = f.old.AppUID
			case "lease":
				f.o.lock = func() (io.Closer, error) { return nil, errors.New("owner transaction active") }
			case "enrollment":
				f.o.enrollmentJournal = func() ([]byte, error) {
					return encoded(enrollmentJournalBinding{enrollmentFenceBinding: enrollmentFenceBinding{Source: enrollmentSourceBinding{Data: f.old.Data}}}), nil
				}
			}
			before := len(f.h.calls)
			if _, e := f.apply(t, false); e == nil {
				t.Fatal("protected actor boundary bypassed", kind)
			}
			for _, a := range f.h.calls[before:] {
				if a[0] != "/usr/bin/passwd" {
					t.Fatal("rejected actor reached root mutation", a)
				}
			}
		})
	}
}

func TestRemoteApplicationHeldCompletionReplaysOnlyExactCallback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "rolled-back"}[rollback], func(t *testing.T) {
			f := remoteApplicationFixture(t)
			f.badHealth = rollback
			completions := 0
			f.cb.Complete = func(context.Context, Manifest, ApplicationUpdateStatus) error {
				completions++
				if completions == 1 {
					return errors.New("actor died after durable owner outcome")
				}
				return nil
			}
			if _, e := f.apply(t, false); e == nil {
				t.Fatal("lost completion reply ignored")
			}
			j, e := readApplicationJournal(f.o)
			want := "healthy-held"
			if rollback {
				want = "rolled-back-held"
			}
			if e != nil || j.Status.Phase != want {
				t.Fatal(j, e)
			}
			before := len(f.h.calls)
			callbacks := strings.Join(f.calls, " ")
			delete(f.h.files, f.c.Engine)
			if _, e = resumeRemoteApplication(t.Context(), f.o, f.old, f.c.ID, f.cb, false); e != nil {
				t.Fatal("held completion required new payload", e)
			}
			if strings.Join(f.calls, " ") != callbacks || completions != 2 {
				t.Fatal("held completion repeated callbacks", f.calls, completions)
			}
			for _, a := range f.h.calls[before:] {
				if a[0] != "/usr/bin/passwd" {
					t.Fatal("held completion repeated root mutation", a)
				}
			}
			j, e = readApplicationJournal(f.o)
			want = "completed"
			if rollback {
				want = "rolled-back"
			}
			if e != nil || j.Status.Phase != want {
				t.Fatal(j, e)
			}
		})
	}
}
func TestRemoteApplicationRecoveryUsesOnlyRetainedTransaction(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "rollback"}[rollback], func(t *testing.T) {
			f := remoteApplicationFixture(t)
			run := f.o.run
			hit := false
			f.o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
				out, e := run(ctx, b, args...)
				if e == nil && !hit && args[0] == "/usr/bin/mv" {
					hit = true
					return out, errors.New("reboot after core replacement")
				}
				return out, e
			}
			if _, e := f.apply(t, false); e == nil || !hit {
				t.Fatal("fault missed", e)
			}
			delete(f.h.files, f.c.Engine)
			f.o.run = run
			if _, e := resumeRemoteApplication(t.Context(), f.o, f.old, strings.Repeat("b", 32), f.cb, rollback); e == nil {
				t.Fatal("foreign recovery identity accepted")
			}
			if _, e := resumeRemoteApplication(t.Context(), f.o, f.old, f.c.ID, f.cb, rollback); e != nil {
				t.Fatal("retained transaction required new release", e)
			}
			j, e := readApplicationJournal(f.o)
			want := "completed"
			if rollback {
				want = "rolled-back"
			}
			if e != nil || j.Status.Phase != want || j.Status.Sequence != 7 {
				t.Fatal(j, e)
			}
		})
	}
}

func TestRemoteAuthenticatedFirstInstallAndUpdatedRepairFloor(t *testing.T) {
	h, o, opt := fixtureHost()
	opt.ApplicationSequence = 7
	opt.ApplicationReleaseSHA256 = strings.Repeat("1", 64)
	m, e := install(t.Context(), opt, o)
	if e != nil || m.ApplicationSequence != 7 || m.ApplicationReleaseSHA256 != opt.ApplicationReleaseSHA256 {
		t.Fatal(m, e)
	}
	unsigned := opt
	unsigned.ApplicationSequence = 0
	unsigned.ApplicationReleaseSHA256 = ""
	same, e := install(t.Context(), unsigned, o)
	if e != nil || same != m {
		t.Fatal("unsigned retry erased authenticated baseline", e)
	}
	opt.ApplicationSequence = 8
	if _, e = install(t.Context(), opt, o); e == nil {
		t.Fatal("installer relabeled existing admitted release")
	}
	_ = h
	f := remoteApplicationFixture(t)
	target, e := f.apply(t, false)
	if e != nil {
		t.Fatal(e)
	}
	retry := Options{Core: f.c.Engine, CoreSHA256: f.c.EngineSHA256, Listen: target.Listen, PublicOrigin: target.PublicOrigin, PrepareHost: true, ApplicationSequence: f.c.Sequence, ApplicationReleaseSHA256: f.c.ReleaseSHA256}
	same, e = install(t.Context(), retry, f.o)
	if e != nil || same != target {
		t.Fatal("current admitted release could not repair/retry after update", e)
	}
}

func TestRemoteInitialAdmissionRefusesDamagedPriorFloorBeforeOSMutation(t *testing.T) {
	h, o, opt := fixtureHost()
	h.journals["intent.json"] = encoded(intent{Version: 1, ID: strings.Repeat("a", 32), OwnerUID: 1000, OwnerGID: 1000, CoreSHA256: opt.CoreSHA256, Listen: opt.Listen, ApplicationSequence: 7})
	if _, e := install(t.Context(), opt, o); e == nil || len(h.calls) > 0 {
		t.Fatal("damaged admission reached OS mutation", e, h.calls)
	}
}
