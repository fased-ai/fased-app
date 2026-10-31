package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This controller models OS acknowledgements only. Installed file replacement
// and trust are tested by their owning installer/bootstrap packages; this is
// controlled orchestration evidence, never public installed acceptance.
type updateControllerFixture struct {
	checks, applies, statuses int
	release                   ApplicationRelease
	receipt                   ApplicationUpdateReceipt
	uncertain                 bool
}

func fixtureUpdateController() *updateControllerFixture {
	return &updateControllerFixture{release: ApplicationRelease{EngineSHA256: strings.Repeat("c", 64), Version: "0.0.0-test.2", Sequence: 7, ManifestSHA256: strings.Repeat("a", 64), Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}}
}
func (d *updateControllerFixture) Check(context.Context) (*ApplicationRelease, error) {
	d.checks++
	r := d.release
	return &r, nil
}
func (d *updateControllerFixture) Apply(_ context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	d.applies++
	d.receipt = ApplicationUpdateReceipt{EngineSHA256: o.Release.EngineSHA256, ID: o.ID, ManifestSHA256: o.Release.ManifestSHA256, Sequence: o.Release.Sequence, Phase: "completed"}
	if d.uncertain {
		return ApplicationUpdateReceipt{}, errors.New("secret endpoint must never be recorded")
	}
	return d.receipt, nil
}
func (d *updateControllerFixture) Status(context.Context, ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	d.statuses++
	return d.receipt, nil
}
func updateCommandFixture(t *testing.T, s *Store, action string) ApplicationUpdateView {
	t.Helper()
	v, e := s.ApplicationUpdates(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	c := ApplicationUpdateCommand{Action: action, Revision: v.Revision, PolicyRevision: v.PolicyRevision}
	if action == "apply" {
		c.ManifestSHA256 = v.Operation.Release.ManifestSHA256
	}
	v, e = s.CommandApplicationUpdate(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func updateTickFixture(t *testing.T, s *Store) {
	t.Helper()
	if e := s.runApplicationUpdates(t.Context()); e != nil {
		t.Fatal(e)
	}
}
func updateModeFixture(t *testing.T, s *Store, p UpdatePolicy) {
	t.Helper()
	v, e := s.UpdatePreferences(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Action: "policy", Revision: v.Revision, Policy: &p})
	if e != nil {
		t.Fatal(e)
	}
}
func TestApplicationUpdateManualReviewOneControllerAndRestart(t *testing.T) {
	s := fixtureStore(t)
	d := fixtureUpdateController()
	if e := s.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	v := updateCommandFixture(t, s, "check")
	if v.Operation.Phase != "check-queued" || v.ActivationEnabled {
		t.Fatal(v)
	}
	updateTickFixture(t, s)
	v, _ = s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "available" || d.checks != 1 {
		t.Fatal(v)
	}
	if _, e := s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "apply", Revision: v.Revision, PolicyRevision: v.PolicyRevision, ManifestSHA256: strings.Repeat("b", 64)}); e == nil {
		t.Fatal("stale release accepted")
	}
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, _ = s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "completed" || d.applies != 1 {
		t.Fatal(v, d)
	}
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	restored, e := other.ApplicationUpdates(t.Context())
	if e != nil || restored.Operation.ID != v.Operation.ID || restored.Operation.Phase != "completed" || restored.ControllerAvailable {
		t.Fatal(restored, e)
	}
	if e = other.runApplicationUpdates(t.Context()); e != nil || d.applies != 1 {
		t.Fatal("restart replayed", e)
	}
}
func TestApplicationUpdateAutomaticWindowPinPolicyAndNoReplay(t *testing.T) {
	s := fixtureStore(t)
	d := fixtureUpdateController()
	if e := s.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC) }
	w := &MaintenanceWindow{TimeZone: "UTC", Days: []int{1}, Start: "11:00", End: "12:00"}
	p := UpdatePolicy{Mode: "automatic", Notify: true, Window: w}
	updateModeFixture(t, s, p)
	updateTickFixture(t, s)
	updateTickFixture(t, s)
	if d.checks != 1 || d.applies != 0 {
		t.Fatal(d)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 26, 11, 10, 0, 0, time.UTC) }
	p.Pin = &UpdatePin{Sequence: "8", ManifestSHA256: strings.Repeat("b", 64)}
	updateModeFixture(t, s, p)
	updateTickFixture(t, s)
	if d.applies != 0 {
		t.Fatal("pin ignored")
	}
	p.Pin = nil
	updateModeFixture(t, s, p)
	updateTickFixture(t, s)
	v, _ := s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "queued" || !v.Operation.Automatic {
		t.Fatal(v)
	}
	updateModeFixture(t, s, UpdatePolicy{Mode: "manual", Notify: true})
	updateTickFixture(t, s)
	v, _ = s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "available" || d.applies != 0 {
		t.Fatal("opt-out ignored", v)
	}
	p.Mode = "automatic"
	updateModeFixture(t, s, p)
	updateTickFixture(t, s)
	d.uncertain = true
	updateTickFixture(t, s)
	v, _ = s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "recovery" || strings.Contains(v.Operation.Failure, "secret") {
		t.Fatal(v)
	}
	updateTickFixture(t, s)
	updateTickFixture(t, s)
	if d.applies != 1 || d.statuses != 0 {
		t.Fatal("uncertain work retried", d)
	}
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if e := other.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	updateTickFixture(t, other)
	if d.applies != 1 {
		t.Fatal("restart replay")
	}
	updateCommandFixture(t, other, "recover")
	updateTickFixture(t, other)
	v, _ = other.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "completed" || d.applies != 1 || d.statuses != 1 {
		t.Fatal(v, d)
	}
}
func TestApplicationUpdateClosedOrdinaryModeAndPreservedState(t *testing.T) {
	s := fixtureStore(t)
	before, e := os.ReadFile(filepath.Join(s.dir, "core.sqlite"))
	if e != nil || len(before) == 0 {
		t.Fatal(e)
	}
	updateModeFixture(t, s, UpdatePolicy{Mode: "automatic", Notify: true})
	v, e := s.ApplicationUpdates(t.Context())
	if e != nil || v.ControllerAvailable || v.ActivationEnabled {
		t.Fatal(v, e)
	}
	if _, e = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "check", Revision: v.Revision, PolicyRevision: v.PolicyRevision}); e == nil {
		t.Fatal("ordinary activation")
	}
	updateTickFixture(t, s)
	v, _ = s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "idle" {
		t.Fatal(v)
	}
	if (UpdatePolicy{Mode: "anything"}).Validate() == nil {
		t.Fatal("unknown mode")
	}
	if s.db.QueryRow("PRAGMA user_version").Scan(new(int)) != nil {
		t.Fatal("database lost")
	}
}
