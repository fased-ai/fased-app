//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestApplicationReaderDriverPersistsTrustedHookClassification(t *testing.T) {
	for _, reader := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "reader"}[reader], func(t *testing.T) {
			f := newFirstFixture(t)
			p, e := Prepare(t.Context(), f.options())
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			o := upgradeEntryFixture(t, f)
			paths := localinstall.Paths{Root: o.Prefix, Data: o.DataDirectory}
			h := localinstall.ApplicationUpdateCallbacks{BeforeStop: func(context.Context, localinstall.Manifest, localinstall.Manifest) error {
				return errors.New("controlled fence before OS stop")
			}, Health: func(context.Context, localinstall.Manifest) error { return nil }, RollbackAllowed: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return nil }, Complete: func(context.Context, localinstall.Manifest, localinstall.ApplicationUpdateStatus) error { return nil }}
			if reader {
				h.AfterStop = func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return nil }
			}
			d, e := p.LocalApplicationController(paths, h)
			if e != nil {
				t.Fatal(e)
			}
			r, e := d.Check(t.Context())
			if e != nil || r == nil {
				t.Fatal(r, e)
			}
			id := update.NewProbe()
			if _, e = d.Apply(t.Context(), core.ApplicationUpdateOperation{ID: id, Release: r}); e == nil {
				t.Fatal("fixture fence missing")
			}
			s, e := localinstall.ApplicationUpdateState(paths)
			if e != nil || s.ID != id || s.ReaderBootstrap != reader {
				t.Fatal("driver lost trusted hook marker", s, e)
			}
		})
	}
}
