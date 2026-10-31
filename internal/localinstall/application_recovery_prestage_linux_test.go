//go:build linux

package localinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestApplicationPrestageDesktopBoundary(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "next-desktop-missing", true: "all-code-retained-offline"}[complete], func(t *testing.T) {
			f := applicationFixtureFor(t)
			oldDesktop := filepath.Join(f.paths.Root, desktopName)
			if e := os.WriteFile(oldDesktop, []byte("old inert desktop"), 0700); e != nil {
				t.Fatal(e)
			}
			f.old.Files[desktopName], _ = updateFileDigest(oldDesktop)
			if e := core.WritePrivateJSON(filepath.Join(f.paths.Root, "install.json"), f.old); e != nil {
				t.Fatal(e)
			}
			f.candidate.ExpectedManifestSHA256 = ManifestSHA256(f.old)
			f.candidate.Desktop = filepath.Join(filepath.Dir(f.candidate.Engine), "next-desktop-input")
			if e := os.WriteFile(f.candidate.Desktop, []byte("next inert desktop"), 0700); e != nil {
				t.Fatal(e)
			}
			f.candidate.DesktopSHA256, _ = updateFileDigest(f.candidate.Desktop)
			_, e := f.apply(t, func(p string) error {
				if p == "journal-prepared" {
					return errors.New("cut before copy")
				}
				return nil
			}, false)
			if e == nil {
				t.Fatal("missing cut")
			}
			dir := filepath.Join(applicationUpdateDirectory(f.paths), f.candidate.ID)
			if e = os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e = copyUpdateFile(f.candidate.Engine, filepath.Join(dir, "next-"+engineName), f.candidate.EngineSHA256); e != nil {
				t.Fatal(e)
			}
			if complete {
				if e = copyUpdateFile(f.candidate.Desktop, filepath.Join(dir, "next-"+desktopName), f.candidate.DesktopSHA256); e != nil {
					t.Fatal(e)
				}
			}
			needed, e := ApplicationUpdateNeedsSource(f.paths, f.candidate.ID)
			if e != nil || needed == complete {
				t.Fatal(needed, e)
			}
			if complete {
				_, e = resumeRetainedApplication(t.Context(), f.paths, f.candidate.ID, f.callbacks, f.services, false)
			} else {
				_, e = f.apply(t, nil, false)
			}
			if e != nil {
				t.Fatal(e)
			}
			f.preserved(t)
		})
	}
}

func TestApplicationPrestageSerializesRejectWithPublication(t *testing.T) {
	t.Run("staging-first", func(t *testing.T) {
		f := applicationFixtureFor(t)
		rejected := false
		f.callbacks.BeforeStage = func(context.Context) error {
			e := RejectUnstagedApplication(t.Context(), f.paths, f.candidate.ID, f.candidate.Sequence, ManifestSHA256(f.old), func() error { rejected = true; return nil })
			if e == nil || rejected {
				t.Fatal("overlapping rejection bypassed installer lock", e)
			}
			return nil
		}
		if _, e := f.apply(t, nil, false); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("rejection-first", func(t *testing.T) {
		f := applicationFixtureFor(t)
		if e := RejectUnstagedApplication(t.Context(), f.paths, f.candidate.ID, f.candidate.Sequence, ManifestSHA256(f.old), func() error {
			if _, e := f.apply(t, nil, false); e == nil {
				t.Fatal("overlapping staging bypassed installer lock")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		refused := errors.New("owner dispatch was rejected")
		f.callbacks.BeforeStage = func(context.Context) error { return refused }
		if _, e := f.apply(t, nil, false); !errors.Is(e, refused) {
			t.Fatal(e)
		}
		if _, e := ApplicationUpdateState(f.paths); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("rejected dispatch consumed journal/floor", e)
		}
		if len(f.calls) != 0 {
			t.Fatal("rejected dispatch crossed fence", f.calls)
		}
	})
}

func TestApplicationPrestageReadmissionPreservesInterruptedScratch(t *testing.T) {
	f := applicationFixtureFor(t)
	cut := errors.New("crash after durable identity, before code copy")
	if _, e := f.apply(t, func(phase string) error {
		if phase == "journal-prepared" {
			return cut
		}
		return nil
	}, false); !errors.Is(e, cut) {
		t.Fatal(e)
	}
	if len(f.calls) != 0 {
		t.Fatal("stopped or fenced before staging", f.calls)
	}
	if needed, e := ApplicationUpdateNeedsSource(f.paths, f.candidate.ID); e != nil || !needed {
		t.Fatal(needed, e)
	}
	dir := filepath.Join(applicationUpdateDirectory(f.paths), f.candidate.ID)
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	scratch := filepath.Join(dir, ".staging-next-"+engineName)
	if e := os.WriteFile(scratch, []byte("unclassified old partial"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := resumeRetainedApplication(t.Context(), f.paths, f.candidate.ID, f.callbacks, f.services, false); e == nil {
		t.Fatal("missing code resumed without authenticated source")
	}
	if _, e := f.apply(t, nil, false); e != nil {
		t.Fatal("same authenticated candidate could not finish staging", e)
	}
	b, e := os.ReadFile(scratch)
	if e != nil || string(b) != "unclassified old partial" {
		t.Fatal("unknown scratch overwritten", e)
	}
	f.preserved(t)
	if e = RejectUnstagedApplication(t.Context(), f.paths, f.candidate.ID, f.candidate.Sequence, ManifestSHA256(f.old), func() error { return nil }); e == nil {
		t.Fatal("changed installed baseline approved for unsent rejection")
	}
}

func TestApplicationPrestagePublishedMismatchHeld(t *testing.T) {
	f := applicationFixtureFor(t)
	if e := RejectUnstagedApplication(t.Context(), f.paths, f.candidate.ID, f.candidate.Sequence, ManifestSHA256(f.old), func() error { return nil }); e != nil {
		t.Fatal(e)
	}
	_, _ = f.apply(t, func(p string) error {
		if p == "journal-prepared" {
			return errors.New("cut")
		}
		return nil
	}, false)
	dir := filepath.Join(applicationUpdateDirectory(f.paths), f.candidate.ID)
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "next-"+engineName)
	if e := os.WriteFile(path, []byte("unknown final"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := ApplicationUpdateNeedsSource(f.paths, f.candidate.ID); e == nil {
		t.Fatal("unknown published input treated as absent")
	}
	if _, e := f.apply(t, nil, false); e == nil {
		t.Fatal("unknown final overwritten")
	}
	if len(f.calls) != 0 {
		t.Fatal("unknown final crossed fence", f.calls)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "unknown final" {
		t.Fatal("unknown final changed")
	}
	f.preserved(t)
}
