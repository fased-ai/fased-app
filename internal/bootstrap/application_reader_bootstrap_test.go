package bootstrap

import (
	"github.com/fased-ai/fased-app/internal/core"
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
	for _, schema := range []int{35, 36} {
		snapshot.State.Version = schema
		r.Expected = snapshot
		prepared := update.CorePauseReceipt{Version: 1, State: "prepared", Request: &r, Core: snapshot}
		got, err := p.ApplicationReaderBootstrapRequest(id, lease, strings.Repeat("b", 64), prepared)
		expected, _ := core.ApplicationReaderRequestVersion(schema)
		if err != nil || got.Version != expected {
			t.Fatal("prepared authenticated handoff", schema, got, err)
		}
	}
	r.Expected = paused.Core
	r.Sequence++
	if _, e = p.ApplicationReaderBootstrapRequest(id, lease, strings.Repeat("b", 64), paused); e == nil {
		t.Fatal("altered predecessor release admitted")
	}
}
