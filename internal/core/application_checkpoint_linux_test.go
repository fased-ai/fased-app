//go:build linux

package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type checkpointController struct {
	*updateControllerFixture
	s      *Store
	fail   bool
	backup ApplicationCheckpoint
}

func (d *checkpointController) Apply(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	d.applies++
	c, e := d.s.PrepareApplicationCheckpoint(ctx, o.ID)
	if e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	d.backup = c
	setup, e := d.s.Setup(ctx)
	if e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	setup.Settings.Host = "vps"
	if _, e = d.s.UpdateSetup(ctx, setup.Revision, setup.Settings); !errors.Is(e, ErrCorePaused) {
		return ApplicationUpdateReceipt{}, errors.New("metadata fence failed")
	}
	d.receipt = ApplicationUpdateReceipt{EngineSHA256: o.Release.EngineSHA256, ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, Phase: "completed"}
	if d.fail {
		return ApplicationUpdateReceipt{}, errors.New("disposable controller interruption")
	}
	if e = d.s.CompleteApplicationCheckpoint(ctx, o.ID, d.receipt); e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	return d.receipt, nil
}
func (d *checkpointController) Status(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	d.statuses++
	if e := d.s.CompleteApplicationCheckpoint(ctx, o.ID, d.receipt); e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	return d.receipt, nil
}
func TestApplicationCheckpointFencesBacksUpAndRecoversWithoutWallet(t *testing.T) {
	s := fixtureStore(t)
	d := &checkpointController{updateControllerFixture: fixtureUpdateController(), s: s, fail: true}
	if e := s.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	marker := []byte("disposable encrypted provider bytes; no keys or funds")
	if e := os.WriteFile(filepath.Join(s.dir, "retained-ciphertext"), marker, 0600); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, e := s.ApplicationUpdates(t.Context())
	if e != nil || v.Operation.Phase != "recovery" || d.applies != 1 {
		t.Fatal(v, e)
	}
	p, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	if e != nil || p.State != "paused" {
		t.Fatal(p, e)
	}
	if !strings.HasPrefix(d.backup.Snapshot.File, "application-") || d.backup.Snapshot.Size <= 0 {
		t.Fatal(d.backup)
	}
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	d.s = other
	if e = other.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	updateTickFixture(t, other)
	if d.statuses != 0 {
		t.Fatal("held transaction retried")
	}
	updateCommandFixture(t, other, "recover")
	updateTickFixture(t, other)
	v, e = other.ApplicationUpdates(t.Context())
	if e != nil || v.Operation.Phase != "completed" || d.applies != 1 || d.statuses != 1 {
		t.Fatal(v, e)
	}
	if e = other.CompleteApplicationCheckpoint(t.Context(), v.Operation.ID, d.receipt); e != nil {
		t.Fatal("idempotent release", e)
	}
	got, e := os.ReadFile(filepath.Join(s.dir, "retained-ciphertext"))
	if e != nil || string(got) != string(marker) {
		t.Fatal("ciphertext changed", e)
	}
	setup, e := other.Setup(t.Context())
	if e != nil || setup.Settings.Host != "local" {
		t.Fatal("owner records changed", setup, e)
	}
}
func TestApplicationCheckpointRefusesCustodyAndTamperedIndex(t *testing.T) {
	s := fixtureStore(t)
	d := fixtureUpdateController()
	if e := s.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	v, _ := s.ApplicationUpdates(t.Context())
	o := v.Operation
	o.Phase = "applying"
	if e := s.publishApplicationUpdate(t.Context(), o, v.Revision); e != nil {
		t.Fatal(e)
	}
	s.custodyNetwork = "disposable"
	if _, e := s.PrepareApplicationCheckpoint(t.Context(), o.ID); e == nil {
		t.Fatal("custody accepted")
	}
	s.custodyNetwork = ""
	c, e := s.PrepareApplicationCheckpoint(t.Context(), o.ID)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := recoveryDirectory(s.dir, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer closeCheckpointDir(dir)
	if e = corruptCheckpointFile(dir, c.Snapshot.File); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PrepareApplicationCheckpoint(t.Context(), o.ID); e == nil {
		t.Fatal("tampered backup accepted")
	}
}
func closeCheckpointDir(d int) { _ = unix.Close(d) }
func corruptCheckpointFile(d int, name string) error {
	fd, e := unix.Openat(d, name, unix.O_WRONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	_, e = unix.Write(fd, []byte("invalid"))
	return e
}
