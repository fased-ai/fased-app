//go:build linux

package core

import (
	"context"
	"errors"

	"github.com/fased-ai/fased-app/internal/update"
)

// SettleArchivedWalletCompletion retires a completed owner lease after the
// caller has verified its protected wallet predecessor. The original outcome,
// release, pause and all other workspace rows remain unchanged.
func SettleArchivedWalletCompletion(ctx context.Context, dir, instance, version string, proof ApplicationUpdateReceipt, bound update.CorePauseReceipt, commit bool) (bool, error) {
	s, lock, err := openMaintenanceStore(ctx, dir)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	defer s.db.Close()
	if instance == "" || s.instanceID != instance || proof.Phase != "completed" || !update.IDOK(proof.ID) {
		return false, errors.New("the original completed wallet workspace is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	o, revision, err := readApplicationUpdate(ctx, tx)
	if err != nil || o.Phase != "completed" || o.ID != proof.ID || o.Release == nil ||
		o.Release.Version != version || o.Release.Sequence != proof.Sequence || o.Release.ManifestSHA256 != proof.ManifestSHA256 ||
		o.Release.EngineSHA256 != proof.EngineSHA256 || o.Receipt == nil || *o.Receipt != proof ||
		o.OwnerLease != "" && o.OwnerLease != proof.ID {
		return false, errors.New("workspace update differs from the original protected completion")
	}
	phase, pause, err := readPause(ctx, tx)
	if err != nil || phase != "released" {
		return false, errors.New("the original app pause must already be released")
	}
	snapshot, err := s.snapshotTx(ctx, tx, update.NewProbe())
	actual := update.CorePauseReceipt{Version: 1, State: phase, PauseID: pause.PauseID, Request: &pause.Request, Core: snapshot}
	if err != nil || actual.Core.InstanceID != instance || !update.SameReleasedPause(proof.ID, bound, actual) {
		return false, errors.New("the live app pause differs from the original completed update")
	}
	if o.OwnerLease == "" || !commit {
		return o.OwnerLease != "", nil
	}
	o.OwnerLease = ""
	if err = saveApplicationUpdate(ctx, tx, o, revision); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
