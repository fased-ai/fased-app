//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// Only the explicit trusted installer request reconciles interrupted initial
// OS setup. The normal installed worker has a read-only /etc namespace.
func ReconcileProtectedSetup(ctx context.Context, owner int) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return err
	}
	if r.Phase != "prepared" || r.Start != "base" {
		return nil
	}
	base, _, err := protectedRecordManifests(r)
	if err != nil {
		return err
	}
	if err = verifyProtectedPair(base); err != nil {
		return err
	}
	if err = installProtectedMaintenanceUnits(ctx, a, base); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err = verifyProtectedRootUnits(ctx, a); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "enable", "--", protectedRecoveryUnit(owner)); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "start", "--", protectedRecoveryUnit(owner)); err != nil {
		return err
	}
	if err = verifyProtectedLoadedUnits(ctx, a, base); err != nil {
		return errors.Join(errors.New("complete the protected setup before Update"), err)
	}
	return nil
}
