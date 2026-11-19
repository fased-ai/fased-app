//go:build linux

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestSettleArchivedWalletCompletion(t *testing.T) {
	ctx := t.Context()
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	release := ApplicationRelease{Version: "0.1.22", Sequence: 23, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64), Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	id := update.NewProbe()
	proof := ApplicationUpdateReceipt{ID: id, Phase: "completed", Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, EngineSHA256: release.EngineSHA256}
	op := ApplicationUpdateOperation{ID: id, OwnerLease: id, Phase: "completed", Release: &release, Receipt: &proof}
	instance := s.instanceID
	snapshot, err := s.UpdateSnapshot(ctx, update.NewProbe())
	if err != nil {
		t.Fatal(err)
	}
	req := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, Expected: snapshot}
	bound := update.CorePauseReceipt{Version: 1, State: "paused", PauseID: update.NewProbe(), Request: &req, Core: snapshot}
	pause, err := json.Marshal(pauseRecord{PauseID: bound.PauseID, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE application_updates SET document=?, revision=40", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE update_pause SET phase='released', document=?", string(pause)); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "instance.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	wrong := bound
	wrong.PauseID = update.NewProbe()
	if _, err := SettleArchivedWalletCompletion(ctx, dir, instance, release.Version, proof, wrong, true); err == nil {
		t.Fatal("a foreign pause settled the operation")
	}
	if pending, err := SettleArchivedWalletCompletion(ctx, dir, instance, release.Version, proof, bound, false); err != nil || !pending {
		t.Fatalf("the original lease did not survive refusal and dry run: %v %v", pending, err)
	}
	if changed, err := SettleArchivedWalletCompletion(ctx, dir, instance, release.Version, proof, bound, true); err != nil || !changed {
		t.Fatalf("the matching completion did not settle: %v %v", changed, err)
	}
	if changed, err := SettleArchivedWalletCompletion(ctx, dir, instance, release.Version, proof, bound, true); err != nil || changed {
		t.Fatalf("repeated completion was not a no-op: %v %v", changed, err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, revision, err := readApplicationUpdate(ctx, s.db)
	op.OwnerLease = ""
	if err != nil || revision != 41 || update.SnapshotDigest("update", got) != update.SnapshotDigest("update", op) {
		t.Fatalf("more than the lease changed: revision %d, %v", revision, err)
	}
	var after string
	if err := s.db.QueryRow("SELECT document FROM update_pause").Scan(&after); err != nil || after != string(pause) {
		t.Fatalf("released pause changed: %v", err)
	}
}
