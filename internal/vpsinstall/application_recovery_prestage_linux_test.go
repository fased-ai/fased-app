//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestRemoteApplicationPrestageReadmissionRetainsPartial(t *testing.T) {
	f := remoteApplicationFixture(t)
	run := f.o.run
	partial := ""
	f.o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/install" && strings.Contains(args[len(args)-1], "/.next-core-") {
			partial = args[len(args)-1]
			f.h.files[partial] = hostFile{UID: 0, GID: 0, Mode: 0755, Regular: true, Data: append([]byte{}, b[:len(b)/2]...)}
			return nil, errors.New("process died during root OS copy")
		}
		return run(ctx, b, args...)
	}
	if _, e := f.apply(t, false); e == nil || partial == "" {
		t.Fatal("missing interrupted first-copy boundary", e)
	}
	if len(f.calls) != 0 {
		t.Fatal("interrupted copy crossed metadata fence", f.calls)
	}
	original := append([]byte{}, f.h.files[partial].Data...)
	if needed, e := applicationUpdateNeedsSource(f.o, f.old.OwnerUID, f.c.ID); e != nil || !needed {
		t.Fatal(needed, e)
	}
	if _, e := resumeRemoteApplication(t.Context(), f.o, f.old, f.c.ID, f.cb, false); e == nil {
		t.Fatal("missing next code resumed without authenticated source")
	}
	f.o.run = run
	before := f.cb.BeforeStop
	f.cb.BeforeStop = func(ctx context.Context, a, b Manifest) error {
		image, e := applicationOwnerImage(f.o, a, f.c.ID)
		if e != nil || image != f.old.Root+"-update-"+f.c.ID+"/previous-core" {
			t.Fatal("real dropped-helper admission refused retained partial", image, e)
		}
		return before(ctx, a, b)
	}
	if _, e := f.apply(t, false); e != nil {
		t.Fatal("exact authenticated candidate could not restage", e)
	}
	if string(f.h.files[partial].Data) != string(original) {
		t.Fatal("interrupted root scratch was overwritten")
	}
	j, e := readApplicationJournal(f.o)
	if e != nil || j.Status.Sequence != 7 || j.Status.Phase != "completed" {
		t.Fatal(j, e)
	}
	for _, name := range []string{".next-core-not-a-canonical-nonce.pending", ".unknown-" + strings.Repeat("1", 32) + ".pending"} {
		path := f.old.Root + "-update-" + f.c.ID + "/" + name
		f.h.files[path] = hostFile{UID: 0, GID: 0, Mode: 0755, Regular: true, Data: []byte("a")}
		if e = verifyApplicationOwnerRetained(f.o, j, f.old.Root+"-update-"+f.c.ID); e == nil {
			t.Fatal("unknown partial name admitted", name)
		}
		delete(f.h.files, path)
	}
}

func TestRemoteApplicationPrestageSerializesRejectWithPublication(t *testing.T) {
	f := remoteApplicationFixture(t)
	lockPath := filepath.Join(t.TempDir(), "installer.lock")
	f.o.lock = func() (io.Closer, error) { return core.LockInstance(lockPath) }
	if e := rejectUnstagedApplication(t.Context(), f.o, f.old, f.c.ID, f.c.Sequence, func() error {
		if _, e := f.apply(t, false); e == nil {
			t.Fatal("overlapping Remote staging bypassed installer lock")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	f.cb.BeforeStage = func(context.Context) error { return errors.New("original dispatch was rejected") }
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("rejected Remote dispatch staged")
	}
	if _, e := readApplicationJournal(f.o); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("rejected Remote dispatch consumed floor", e)
	}
	f.cb.BeforeStage = func(context.Context) error {
		called := false
		if e := rejectUnstagedApplication(t.Context(), f.o, f.old, f.c.ID, f.c.Sequence, func() error { called = true; return nil }); e == nil || called {
			t.Fatal("overlapping Remote rejection bypassed installer lock", e)
		}
		return nil
	}
	if _, e := f.apply(t, false); e != nil {
		t.Fatal(e)
	}
}

func TestRemoteApplicationPrestagePublishedMismatchHeld(t *testing.T) {
	f := remoteApplicationFixture(t)
	read := f.o.read
	f.o.read = func(path string) (hostFile, error) {
		if path == f.c.Engine {
			return hostFile{}, os.ErrNotExist
		}
		return read(path)
	}
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("missing authenticated artifact accepted")
	}
	f.o.read = read
	dir := f.old.Root + "-update-" + f.c.ID
	f.h.files[dir+"/next-core"] = hostFile{UID: 0, GID: 0, Mode: 0755, Regular: true, Data: []byte("unknown published code")}
	if _, e := applicationUpdateNeedsSource(f.o, f.old.OwnerUID, f.c.ID); e == nil {
		t.Fatal("published mismatch treated as absent")
	}
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("published mismatch overwritten")
	}
	if string(f.h.files[dir+"/next-core"].Data) != "unknown published code" || len(f.calls) != 0 {
		t.Fatal("foreign bytes/fence changed")
	}
}
