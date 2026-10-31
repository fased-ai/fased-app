//go:build linux

package localinstall

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestApplicationReaderAfterStopHookOrderingAndHeldFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful", true: "held"}[fail], func(t *testing.T) {
			f := applicationFixtureFor(t)
			f.candidate.ReaderBootstrap = true
			f.callbacks.AfterStop = func(_ context.Context, a, b Manifest) error {
				if len(f.calls) == 0 || f.calls[len(f.calls)-1] != "stop-"+f.old.Version {
					t.Fatal("reader hook did not follow stop", f.calls)
				}
				m, e := Load(f.paths)
				if e != nil || ManifestSHA256(m) != ManifestSHA256(a) || b.Files[engineName] != f.candidate.EngineSHA256 {
					t.Fatal("reader hook ran after code/identity change", e)
				}
				f.calls = append(f.calls, "reader-bootstrap")
				if fail {
					return errors.New("held reader migration")
				}
				return nil
			}
			_, e := f.apply(t, nil, false)
			if (e != nil) != fail {
				t.Fatal(e)
			}
			if fail {
				if strings.Contains(strings.Join(f.calls, " "), "start-") {
					t.Fatal("started after reader refusal", f.calls)
				}
				m, e := Load(f.paths)
				if e != nil || ManifestSHA256(m) != ManifestSHA256(f.old) {
					t.Fatal("replaced code after refused bootstrap", e)
				}
			}
			f.preserved(t)
		})
	}
}

func TestApplicationReaderFlagBoundAcrossInterruptedResume(t *testing.T) {
	f := applicationFixtureFor(t)
	f.candidate.ReaderBootstrap = true
	hook := func(context.Context, Manifest, Manifest) error { return nil }
	f.callbacks.AfterStop = hook
	f.denied = true
	if _, e := f.apply(t, nil, false); e == nil {
		t.Fatal("fixture did not pause")
	}
	status, e := ApplicationUpdateState(f.paths)
	if e != nil || !status.ReaderBootstrap {
		t.Fatal(status, e)
	}
	f.candidate.ReaderBootstrap = false
	f.callbacks.AfterStop = nil
	before := len(f.calls)
	if _, e = f.apply(t, nil, false); e == nil || len(f.calls) != before {
		t.Fatal("changed reader flag admitted", e)
	}
	f.callbacks.AfterStop = hook
	f.denied = false
	if _, e = resumeRetainedApplication(t.Context(), f.paths, f.candidate.ID, f.callbacks, f.services, false); e != nil {
		t.Fatal("reader marker lost in recovery reconstruction", e)
	}
	status, e = ApplicationUpdateState(f.paths)
	if e != nil || !status.ReaderBootstrap || status.Phase != "completed" {
		t.Fatal(status, e)
	}
}

func TestApplicationReaderFlagPreventsOldCodeRollback(t *testing.T) {
	f := applicationFixtureFor(t)
	f.candidate.ReaderBootstrap = true
	f.callbacks.AfterStop = func(context.Context, Manifest, Manifest) error { return nil }
	f.badHealth = true
	if _, e := f.apply(t, nil, false); e == nil {
		t.Fatal("bad target silently accepted")
	}
	m, e := Load(f.paths)
	if e != nil || m.Files[engineName] != f.candidate.EngineSHA256 {
		t.Fatal("reader rollback restored old code", e)
	}
	for _, call := range f.calls {
		if call == "rollback-check" {
			t.Fatal("reader attempted compatible same-schema rollback")
		}
	}
}
