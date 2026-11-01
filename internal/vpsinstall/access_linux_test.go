//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"strings"
	"testing"
)

func activeAccessFixture(t *testing.T) (*memoryHost, hostOps, Manifest) {
	fixture := remoteApplicationFixture(t)
	h, o, m := fixture.h, fixture.o, fixture.old
	if e := start(t.Context(), o, m); e != nil {
		t.Fatal(e)
	}
	return h, o, m
}
func TestRemoteAccessChangePreservesIdentityAndRollsBack(t *testing.T) {
	h, o, m := activeAccessFixture(t)
	data := append([]byte(nil), h.files[m.Core].Data...)
	next, e := changeAccess(t.Context(), o, m, "https://node.owner.ts.net", func(context.Context, Manifest) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	same := next
	same.PublicOrigin = m.PublicOrigin
	if same != m || !bytes.Equal(data, h.files[m.Core].Data) {
		t.Fatal("changed identity or executable")
	}
	if e = verify(t.Context(), o, next); e != nil {
		t.Fatal(e)
	}
	rejected := errors.New("controlled private route failure")
	restored, e := changeAccess(t.Context(), o, next, "", func(_ context.Context, target Manifest) error {
		if target.PublicOrigin == "" {
			return rejected
		}
		return nil
	})
	if !errors.Is(e, rejected) || restored != next {
		t.Fatal(restored, e)
	}
	if e = verify(t.Context(), o, next); e != nil {
		t.Fatal("previous route not restored", e)
	}
}
func TestRemoteAccessInterruptedGenerationRecoversBeforeRetry(t *testing.T) {
	h, o, m := activeAccessFixture(t)
	originalRun := o.run
	cut := errors.New("disposable process cut")
	interrupted := false
	o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		result, e := originalRun(ctx, b, args...)
		if !interrupted && args[0] == "/usr/bin/mv" && args[len(args)-1] == m.Config {
			interrupted = true
			panic(cut)
		}
		return result, e
	}
	func() {
		defer func() {
			if recover() != cut {
				t.Fatal("missing controlled interruption")
			}
		}()
		_, _ = changeAccess(t.Context(), o, m, "https://node.owner.ts.net", nil)
	}()
	o.run = originalRun
	current := h.manifest()
	_, e := changeAccess(t.Context(), o, current, "https://node.owner.ts.net", nil)
	if e == nil || !strings.Contains(e.Error(), "previous access restored") {
		t.Fatal(e)
	}
	if e = verify(t.Context(), o, m); e != nil {
		t.Fatal(e)
	}
	next, e := changeAccess(t.Context(), o, m, "https://node.owner.ts.net", nil)
	if e != nil || next.PublicOrigin == "" {
		t.Fatal(next, e)
	}
}
func TestRemoteAccessRefusesPendingUpdate(t *testing.T) {
	_, o, m := activeAccessFixture(t)
	target := m
	target.ApplicationSequence = 7
	target.ApplicationReleaseSHA256 = strings.Repeat("a", 64)
	j := applicationJournal{ProductVersion: "fixture", Previous: m, Target: target, Status: ApplicationUpdateStatus{Version: 1, ID: update.NewProbe(), Sequence: 7, ReleaseSHA256: target.ApplicationReleaseSHA256, PreviousManifestSHA256: ManifestSHA256(m), TargetManifestSHA256: ManifestSHA256(target), EngineSHA256: target.CoreSHA256, Phase: "applying"}}
	if !applicationJournalValid(j, m.OwnerUID) {
		t.Fatal("invalid test journal")
	}
	if e := o.writeJournal(applicationJournalName, j); e != nil {
		t.Fatal(e)
	}
	if _, e := changeAccess(t.Context(), o, m, "https://node.owner.ts.net", nil); e == nil {
		t.Fatal("changed access during update")
	}
	if e := verify(t.Context(), o, m); e != nil {
		t.Fatal(e)
	}
}

func TestRemoteAccessAndUpdateTransactionsFenceEachOther(t *testing.T) {
	f := remoteApplicationFixture(t)
	m := f.old
	next := m
	next.PublicOrigin = "https://node.owner.ts.net"
	j := accessJournal{Version: 1, ID: update.NewProbe(), Phase: "switched", InstanceID: "fixture", Previous: m, Next: next}
	if e := f.o.writeJournal(accessJournalName, j); e != nil {
		t.Fatal(e)
	}
	if _, e := replaceApplication(t.Context(), f.o, m, f.c, f.cb, false); e == nil || !strings.Contains(e.Error(), "private access recovery") {
		t.Fatal(e)
	}
	if len(f.calls) != 0 {
		t.Fatal("update crossed access recovery")
	}
	if e := beginApplicationMaintenance(t.Context(), f.o, m, update.NewProbe()); e == nil || !strings.Contains(e.Error(), "private access recovery") {
		t.Fatal(e)
	}
	delete(f.h.journals, accessJournalName)
	if e := f.o.writeJournal("application-maintenance.json", applicationMaintenanceRequest{Version: 1, ID: update.NewProbe(), InstallationID: m.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := changeAccess(t.Context(), f.o, m, next.PublicOrigin, nil); e == nil || !strings.Contains(e.Error(), "update maintenance") {
		t.Fatal(e)
	}
	if e := verify(t.Context(), f.o, m); e != nil {
		t.Fatal(e)
	}
}
