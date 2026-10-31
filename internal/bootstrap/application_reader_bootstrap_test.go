package bootstrap

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"strings"
	"testing"
)

func TestApplicationReaderBootstrapRequestUsesAuthenticatedRelease(t *testing.T) {
	f := newFirstFixture(t)
	p, e := Prepare(t.Context(), f.options())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	id, lease := update.NewProbe(), update.NewProbe()
	snapshot := update.CoreSnapshot{Version: 1, ProbeID: update.NewProbe(), BootID: update.NewProbe(), InstanceID: "existing-instance", State: update.State{Name: "core-db", Version: 33, Features: []string{"accounts-v1"}, Anchor: strings.Repeat("a", 64)}}
	r := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: p.report.Sequence, ManifestSHA256: p.report.DescriptorSHA256, Expected: snapshot}
	paused := update.CorePauseReceipt{Version: 1, State: "paused", PauseID: update.NewProbe(), Request: &r, Core: snapshot}
	got, e := p.ApplicationReaderBootstrapRequest(id, lease, strings.Repeat("b", 64), paused)
	if e != nil || got.Release.EngineSHA256 != p.engine.SHA256 || got.Release.ManifestSHA256 != p.report.DescriptorSHA256 || got.OwnerLease != lease {
		t.Fatal(got, e)
	}
	r.Sequence++
	if _, e = p.ApplicationReaderBootstrapRequest(id, lease, strings.Repeat("b", 64), paused); e == nil {
		t.Fatal("altered predecessor release admitted")
	}
}

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
