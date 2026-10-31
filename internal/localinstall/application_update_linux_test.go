//go:build linux

package localinstall

import (
	"bytes"
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type applicationFixture struct {
	paths     Paths
	old       Manifest
	candidate ApplicationUpdate
	callbacks ApplicationUpdateCallbacks
	services  applicationServices
	calls     []string
	denied    bool
	badHealth bool
}

func applicationFixtureFor(t *testing.T) *applicationFixture {
	t.Helper()
	p, engine := installFixture(t)
	old, e := Install(t.Context(), p, Options{Engine: engine})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p.Data, "owner-record"), []byte("preserve original owner data"), 0600); e != nil {
		t.Fatal(e)
	}
	next := filepath.Join(filepath.Dir(engine), "next-core")
	if e = os.WriteFile(next, []byte("next inert compiled artifact fixture"), 0700); e != nil {
		t.Fatal(e)
	}
	hash, e := updateFileDigest(next)
	if e != nil {
		t.Fatal(e)
	}
	f := &applicationFixture{paths: p, old: old, candidate: ApplicationUpdate{Sequence: 7, ReleaseSHA256: strings.Repeat("1", 64), ID: strings.Repeat("a", 32), Version: "0.0.0-next", ExpectedManifestSHA256: ManifestSHA256(old), Engine: next, EngineSHA256: hash}}
	f.callbacks = ApplicationUpdateCallbacks{
		BeforeStop: func(context.Context, Manifest, Manifest) error {
			f.calls = append(f.calls, "fence")
			if f.denied {
				return errors.New("custody present or metadata unapproved")
			}
			return nil
		},
		Health: func(_ context.Context, m Manifest) error {
			f.calls = append(f.calls, "health-"+m.Version)
			if f.badHealth && m.Version == f.candidate.Version {
				return errors.New("controlled target health failure")
			}
			return nil
		},
		RollbackAllowed: func(context.Context, Manifest, Manifest) error {
			f.calls = append(f.calls, "rollback-check")
			if f.denied {
				return errors.New("metadata incompatible")
			}
			return nil
		},
		Complete: func(_ context.Context, _ Manifest, s ApplicationUpdateStatus) error {
			f.calls = append(f.calls, "complete-"+s.Phase)
			return nil
		},
	}
	f.services = applicationServices{stop: func(_ context.Context, m Manifest) error { f.calls = append(f.calls, "stop-"+m.Version); return nil }, start: func(_ context.Context, m Manifest) error { f.calls = append(f.calls, "start-"+m.Version); return nil }}
	return f
}
func (f *applicationFixture) apply(t *testing.T, fault func(string) error, rollback bool) (Manifest, error) {
	t.Helper()
	return replaceApplication(t.Context(), f.paths, f.candidate, f.callbacks, f.services, fault, rollback)
}
func (f *applicationFixture) preserved(t *testing.T) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(f.paths.Data, "owner-record"))
	if e != nil || string(b) != "preserve original owner data" {
		t.Fatal("owner state changed", e)
	}
}
func TestApplicationUpdateCommitAndIdempotentReadback(t *testing.T) {
	f := applicationFixtureFor(t)
	m, e := f.apply(t, nil, false)
	if e != nil || m.Version != f.candidate.Version {
		t.Fatal(m, e)
	}
	s, e := ApplicationUpdateState(f.paths)
	if e != nil || s.Phase != "completed" || s.ID != f.candidate.ID || s.EngineSHA256 != f.candidate.EngineSHA256 {
		t.Fatal(s, e)
	}
	if got, e := updateFileDigest(filepath.Join(f.paths.Root, engineName)); e != nil || got != s.EngineSHA256 {
		t.Fatal(got, e)
	}
	calls := strings.Join(f.calls, " ")
	if !strings.Contains(calls, "fence stop-") || !strings.Contains(calls, "health-0.0.0-next complete-completed") {
		t.Fatal(calls)
	}
	before := len(f.calls)
	if _, e = f.apply(t, nil, false); e != nil || len(f.calls) != before {
		t.Fatal("completed update replayed", e, f.calls)
	}
	f.preserved(t)
}
func TestApplicationUpdateInterruptedReplacementResumes(t *testing.T) {
	for _, phase := range []string{"before-stop", "replaced-" + engineName, "manifest"} {
		t.Run(phase, func(t *testing.T) {
			f := applicationFixtureFor(t)
			hit := false
			if _, e := f.apply(t, func(p string) error {
				if p == phase {
					hit = true
					return errors.New("process interrupted")
				}
				return nil
			}, false); e == nil || !hit {
				t.Fatal(e, hit)
			}
			s, e := ApplicationUpdateState(f.paths)
			if e != nil || s.Phase != "applying" {
				t.Fatal(s, e)
			}
			if _, e = f.apply(t, nil, false); e != nil {
				t.Fatal("same identity could not resume", e)
			}
			f.preserved(t)
		})
	}
}
func TestApplicationUpdateHealthFailureRollsBackCompatibleCodeOnly(t *testing.T) {
	f := applicationFixtureFor(t)
	f.badHealth = true
	if _, e := f.apply(t, nil, false); e == nil {
		t.Fatal("target health failure suppressed")
	}
	s, e := ApplicationUpdateState(f.paths)
	if e != nil || s.Phase != "rolled-back" {
		t.Fatal(s, e)
	}
	m, e := Load(f.paths)
	if e != nil || ManifestSHA256(m) != ManifestSHA256(f.old) {
		t.Fatal("old manifest not restored", e)
	}
	if e = verify(m); e != nil {
		t.Fatal(e)
	}
	f.preserved(t)
}
func TestApplicationUpdateIncompatibleHealthFailureRemainsHeld(t *testing.T) {
	f := applicationFixtureFor(t)
	f.badHealth = true
	f.callbacks.RollbackAllowed = func(context.Context, Manifest, Manifest) error { return errors.New("forward-only schema") }
	if _, e := f.apply(t, nil, false); e == nil {
		t.Fatal("incompatible failure suppressed")
	}
	s, e := ApplicationUpdateState(f.paths)
	if e != nil || s.Phase != "recovery" {
		t.Fatal(s, e)
	}
	if got, e := updateFileDigest(filepath.Join(f.paths.Root, engineName)); e != nil || got != f.candidate.EngineSHA256 {
		t.Fatal("incompatible target silently rolled back", e)
	}
	if strings.Contains(strings.Join(f.calls, " "), "complete-") {
		t.Fatal("recovery hold was released")
	}
	f.callbacks.RollbackAllowed = func(context.Context, Manifest, Manifest) error { return nil }
	if _, e = f.apply(t, nil, true); e != nil {
		t.Fatal("explicit compatible recovery failed", e)
	}
	f.preserved(t)
}
func TestApplicationUpdateRejectsChangedInputManifestAndCustody(t *testing.T) {
	for _, kind := range []string{"source", "manifest", "custody", "missing callback", "add desktop"} {
		t.Run(kind, func(t *testing.T) {
			f := applicationFixtureFor(t)
			switch kind {
			case "source":
				if e := os.WriteFile(f.candidate.Engine, []byte("changed unverified artifact"), 0700); e != nil {
					t.Fatal(e)
				}
			case "manifest":
				f.candidate.ExpectedManifestSHA256 = strings.Repeat("0", 64)
			case "custody":
				f.denied = true
			case "missing callback":
				f.callbacks.RollbackAllowed = nil
			case "add desktop":
				f.candidate.Desktop = f.candidate.Engine
				f.candidate.DesktopSHA256 = f.candidate.EngineSHA256
			}
			if _, e := f.apply(t, nil, false); e == nil {
				t.Fatal("invalid update accepted", kind)
			}
			if strings.Contains(strings.Join(f.calls, " "), "stop-") {
				t.Fatal("invalid update stopped service", f.calls)
			}
			m, e := Load(f.paths)
			if e != nil || ManifestSHA256(m) != ManifestSHA256(f.old) {
				t.Fatal("old manifest changed", e)
			}
			if e = verify(m); e != nil {
				t.Fatal(e)
			}
			f.preserved(t)
		})
	}
}
func TestApplicationUpdateRejectsTamperedBackupAndDifferentPendingIdentity(t *testing.T) {
	for _, kind := range []string{"backup", "identity", "symlink", "manifest backup"} {
		t.Run(kind, func(t *testing.T) {
			f := applicationFixtureFor(t)
			_, e := f.apply(t, func(p string) error {
				if p == "before-stop" {
					return errors.New("interrupted")
				}
				return nil
			}, false)
			if e == nil {
				t.Fatal("fault missed")
			}
			dir := filepath.Join(applicationUpdateDirectory(f.paths), f.candidate.ID)
			e = nil
			switch kind {
			case "backup":
				e = os.WriteFile(filepath.Join(dir, "previous-"+engineName), []byte("tampered"), 0700)
			case "identity":
				f.candidate.ID = strings.Repeat("b", 32)
			case "symlink":
				p := filepath.Join(dir, "next-"+engineName)
				e = os.Remove(p)
				if e == nil {
					e = os.Symlink(f.candidate.Engine, p)
				}
			case "manifest backup":
				e = os.WriteFile(filepath.Join(dir, "previous-install.json"), []byte("{}"), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			before := len(f.calls)
			if _, e = f.apply(t, nil, false); e == nil {
				t.Fatal("tampered transaction resumed", kind)
			}
			if len(f.calls) != before {
				t.Fatal("tampered transaction reached service/fence")
			}
			f.preserved(t)
		})
	}
}
func TestApplicationUpdateDoesNotTouchDataDirectory(t *testing.T) {
	f := applicationFixtureFor(t)
	original := []byte("original uncertain result and wallet metadata, never restored")
	p := filepath.Join(f.paths.Data, "financial-original")
	if e := os.WriteFile(p, original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := f.apply(t, nil, false); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(p)
	if e != nil || !bytes.Equal(got, original) {
		t.Fatal("data was restored/replaced", e)
	}
}

func TestApplicationUpdateConsumedFloorSurvivesRollback(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "rolled back"}[failed], func(t *testing.T) {
			f := applicationFixtureFor(t)
			f.badHealth = failed
			_, e := f.apply(t, nil, false)
			if (e != nil) != failed {
				t.Fatal(e)
			}
			state, e := ApplicationUpdateState(f.paths)
			if e != nil || state.Sequence != 7 || state.ReleaseSHA256 != f.candidate.ReleaseSHA256 {
				t.Fatal(state, e)
			}
			actual, e := Load(f.paths)
			if e != nil {
				t.Fatal(e)
			}
			f.candidate.ExpectedManifestSHA256 = ManifestSHA256(actual)
			f.candidate.ID = strings.Repeat("b", 32)
			for _, seq := range []uint64{6, 7} {
				f.candidate.Sequence = seq
				if _, e = f.apply(t, nil, false); e == nil {
					t.Fatal("consumed release floor lost", seq)
				}
			}
			f.badHealth = false
			f.candidate.Sequence = 8
			if _, e = f.apply(t, nil, false); e != nil {
				t.Fatal("new authenticated sequence refused", e)
			}
			actual, e = Load(f.paths)
			if e != nil || actual.ApplicationSequence != 8 || actual.ApplicationReleaseSHA256 != f.candidate.ReleaseSHA256 {
				t.Fatal(actual, e)
			}
		})
	}
}

func TestApplicationUpdateOwnerLeaseAndExistingInstallationRequired(t *testing.T) {
	f := applicationFixtureFor(t)
	l, e := core.LockInstance(filepath.Join(f.paths.Root, "install.lock"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.apply(t, nil, false); e == nil || len(f.calls) > 0 {
		t.Fatal("parallel owner installation lease bypassed", e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	absent := f.paths
	absent.Root = filepath.Join(filepath.Dir(f.paths.Root), "absent-installation")
	if _, e = replaceApplication(t.Context(), absent, f.candidate, f.callbacks, f.services, nil, false); e == nil {
		t.Fatal("update became a fresh install")
	}
	if _, e = os.Lstat(absent.Root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("update created an installation", e)
	}
}

func TestApplicationUpdateHeldCompletionReplaysOnlyExactCallback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "rolled-back"}[rollback], func(t *testing.T) {
			f := applicationFixtureFor(t)
			f.badHealth = rollback
			completions := 0
			f.callbacks.Complete = func(context.Context, Manifest, ApplicationUpdateStatus) error {
				completions++
				if completions == 1 {
					return errors.New("controller stopped after durable owner outcome")
				}
				return nil
			}
			if _, e := f.apply(t, nil, false); e == nil {
				t.Fatal("lost completion reply ignored")
			}
			state, e := ApplicationUpdateState(f.paths)
			want := "healthy-held"
			if rollback {
				want = "rolled-back-held"
			}
			if e != nil || state.Phase != want {
				t.Fatal(state, e)
			}
			calls := strings.Join(f.calls, " ")
			if e = os.Remove(f.candidate.Engine); e != nil {
				t.Fatal(e)
			}
			if _, e = resumeRetainedApplication(t.Context(), f.paths, f.candidate.ID, f.callbacks, f.services, false); e != nil {
				t.Fatal("exact held completion did not resume", e)
			}
			if strings.Join(f.calls, " ") != calls || completions != 2 {
				t.Fatal("held completion repeated stop/start/fence/health", f.calls, completions)
			}
			state, e = ApplicationUpdateState(f.paths)
			want = "completed"
			if rollback {
				want = "rolled-back"
			}
			if e != nil || state.Phase != want {
				t.Fatal(state, e)
			}
		})
	}
}
func TestApplicationRecoveryUsesOnlyRetainedOriginalTransaction(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "rollback"}[rollback], func(t *testing.T) {
			f := applicationFixtureFor(t)
			if _, e := f.apply(t, func(p string) error {
				if p == "replaced-"+engineName {
					return errors.New("reboot")
				}
				return nil
			}, false); e == nil {
				t.Fatal("fault missed")
			}
			if e := os.Remove(f.candidate.Engine); e != nil {
				t.Fatal(e)
			}
			if _, e := resumeRetainedApplication(t.Context(), f.paths, strings.Repeat("b", 32), f.callbacks, f.services, rollback); e == nil {
				t.Fatal("foreign recovery id accepted")
			}
			if _, e := resumeRetainedApplication(t.Context(), f.paths, f.candidate.ID, f.callbacks, f.services, rollback); e != nil {
				t.Fatal("retained transaction required a new release", e)
			}
			state, e := ApplicationUpdateState(f.paths)
			want := "completed"
			if rollback {
				want = "rolled-back"
			}
			if e != nil || state.Phase != want || state.Sequence != 7 {
				t.Fatal(state, e)
			}
			f.preserved(t)
		})
	}
}

func TestAuthenticatedFirstInstallPersistsReleaseFloor(t *testing.T) {
	p, engine := installFixture(t)
	release := strings.Repeat("1", 64)
	m, e := Install(t.Context(), p, Options{Engine: engine, ApplicationSequence: 7, ApplicationReleaseSHA256: release})
	if e != nil || m.ApplicationSequence != 7 || m.ApplicationReleaseSHA256 != release {
		t.Fatal(m, e)
	}
	repaired, e := Install(t.Context(), p, Options{Engine: engine})
	if e != nil || ManifestSHA256(repaired) != ManifestSHA256(m) {
		t.Fatal("ordinary retry erased first admission", e)
	}
	if _, e = Install(t.Context(), p, Options{Engine: engine, ApplicationSequence: 8, ApplicationReleaseSHA256: release}); e == nil {
		t.Fatal("install retry relabeled release")
	}
	bad, source := installFixture(t)
	if _, e = Install(t.Context(), bad, Options{Engine: source, ApplicationSequence: 7}); e == nil {
		t.Fatal("unpaired initial release accepted")
	}
	if _, e = os.Lstat(bad.Root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("bad admission mutated target", e)
	}
}
