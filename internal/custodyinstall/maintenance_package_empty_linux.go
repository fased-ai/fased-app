//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// runEmptyPackageUpdate uses the real app-owned checkpoint when no wallet has
// been configured. The stopped signer and its empty directory are checked by
// the protected helper; an app checkpoint is never treated as a signer hold.
func runEmptyPackageUpdate(ctx context.Context, a maintenance.Authority, r update.MaintenanceRecord, base, target Manifest, boot bool) (result error) {
	// Update only the selected instance under its existing journal and lock;
	// the OS package is installed separately and remains unchanged.
	if !packageUpdateConnected {
		return errors.New("package update recovery is unavailable; preserve the installation")
	}
	if base.Network != "" || target.Network != "" || base.Layout != PackageLayout || !r.RecoveryEligible() ||
		r.PreviousPackage == nil || !packagePairMatches(*r.PreviousPackage, base) || !packagePairMatches(*r.Package, target) ||
		r.Application != nil && r.Application.PreviousEngineSHA256 != base.CoreHash {
		return errors.New("original empty-signer package operation required")
	}
	// The instance lock protects its own pair and journal. The system launcher
	// stays unchanged; another instance may be on a different release.
	if err := verifyPackageRootUnits(ctx, a, base, r.OperationID); err != nil {
		return err
	}
	if err := verifyLoadedPackageServices(ctx, base); err != nil {
		return err
	}
	if err := VerifyUnconfiguredPackageSigner(ctx, base); err != nil {
		return err
	}
	if r.Phase == "completed" {
		if err := VerifyPackageInstance(target); err != nil {
			return err
		}
		if r.Package.NativeDesktop != nil {
			if err := verifyProtectedDesktop(target, r.Package); err != nil {
				return err
			}
		}
		if _, err := os.Lstat(base.Root + "/update-active.json"); err == nil {
			if err = verifyPendingNativeTarget(ctx, target, r.Package); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := maintenance.RemoveProtected(base.Root+"/update-active.json", sum(update.MaintenanceFence(r.OperationID)), 0644); err != nil {
			return err
		}
		return nil
	}
	if r.Phase == "prepared" {
		if err := VerifyPackageInstance(base); err != nil {
			return err
		}
		if boot {
			// The app starts after this recovery unit. Its worker then resumes the
			// same operation; never synchronously wait for that ordered service.
			_, err := protectedSystemctl(ctx, "start", "--no-block", "--", instanceWorkerUnit(base, r.OperationID))
			return err
		}
		if err := verifyInstanceUpdatePackages(ctx, base, r.PreviousPackage, *r.Package); err != nil {
			return err
		}
		if r.OwnerLease == "" {
			var view core.ApplicationOwnerView
			if err := protectedAppPeer(ctx, a, base, maintenance.AppMaintenanceRequest{Action: "operation", OperationID: r.OperationID}, &view); err != nil {
				return err
			}
			if err := matchEmptyPackageOperation(view, r, base); err != nil {
				return err
			}
			r.OwnerLease = r.OperationID
			if !r.RecoveryEligible() {
				return errors.New("invalid package application lease")
			}
			if err := storeProtectedRecord(r); err != nil {
				return err
			}
		}
		var claimed, prepared, offline core.ApplicationOwnerView
		if err := protectedAppPeer(ctx, a, base, maintenance.AppMaintenanceRequest{Action: "application-claim", OperationID: r.OperationID}, &claimed); err != nil {
			return err
		}
		if err := matchEmptyPackageOperation(claimed, r, base); err != nil || claimed.View.Operation.OwnerLease != r.OwnerLease {
			return errors.New("the original application lease is unavailable")
		}
		if err := protectedAppPeer(ctx, a, base, maintenance.AppMaintenanceRequest{Action: "application-prepare", OperationID: r.OperationID}, &prepared); err != nil || prepared.Checkpoint == nil {
			return errors.Join(err, errors.New("the original application checkpoint is unavailable"))
		}
		if err := protectedAppPeer(ctx, a, base, maintenance.AppMaintenanceRequest{Action: "application-offline", OperationID: r.OperationID}, &offline); err != nil || offline.Checkpoint == nil || offline.View.Operation.OwnerLease != r.OwnerLease ||
			update.SnapshotDigest("package-application-checkpoint", *offline.Checkpoint) != update.SnapshotDigest("package-application-checkpoint", *prepared.Checkpoint) {
			return errors.Join(err, errors.New("the original application backup could not be verified"))
		}
		r.Application = prepared.Checkpoint
		r.Phase, r.Start = "held", ""
		if !r.RecoveryEligible() || r.Application.PreviousEngineSHA256 != base.CoreHash {
			return errors.New("the application checkpoint differs from the installed package")
		}
		if err := maintenance.PublishProtected(base.Root+"/update-active.json", update.MaintenanceFence(r.OperationID), 0644); err != nil {
			return err
		}
		if err := storeProtectedRecord(r); err != nil {
			return err
		}
	}
	if r.Phase == "held" || r.Phase == "replacing" {
		if !boot {
			if _, err := protectedSystemctl(ctx, "stop", "--", instanceUnit(base, "app"), instanceUnit(base, "signer")); err != nil {
				return err
			}
		}
		if err := servicesStopped(ctx, base, func(ctx context.Context, _ []byte, args ...string) ([]byte, error) {
			return protectedSystemctl(ctx, args[1:]...)
		}); err != nil {
			return err
		}
		if err := VerifyUnconfiguredPackageSigner(ctx, base); err != nil {
			return err
		}
		var offline core.ApplicationOwnerView
		if err := protectedAppPeer(ctx, a, base, maintenance.AppMaintenanceRequest{Action: "application-offline", OperationID: r.OperationID}, &offline); err != nil || offline.Checkpoint == nil || offline.View.Operation.OwnerLease != r.OwnerLease ||
			update.SnapshotDigest("package-application-checkpoint", *offline.Checkpoint) != update.SnapshotDigest("package-application-checkpoint", *r.Application) {
			return errors.Join(err, errors.New("the saved application checkpoint changed; preserve the operation"))
		}
		dir, err := update.PackageMaintenanceDirectory(base.Owner, base.Location, base.ID, r.OperationID)
		if err != nil {
			return err
		}
		for _, component := range r.PreviousPackage.Components {
			name := "previous-" + component.Contract.Component
			hash, size, e := maintenance.ProtectedDigest(filepath.Join(dir, name), 0755, 64<<20)
			if e != nil || hash != component.Artifact.SHA256 || size != component.Artifact.Size {
				return errors.New("the original protected package pair is unavailable")
			}
		}
		if r.PreviousPackage.NativeDesktop != nil {
			hash, size, e := maintenance.ProtectedDigest(filepath.Join(dir, "previous-desktop"), 0755, 64<<20)
			if e != nil || hash != r.PreviousPackage.NativeDesktop.SHA256 || size != r.PreviousPackage.NativeDesktop.Size || r.Package.NativeDesktop == nil {
				return errors.New("the original protected wallet client is unavailable")
			}
		} else if r.Package.NativeDesktop != nil {
			return errors.New("the original protected wallet client is required")
		}
		if r.Phase == "held" {
			if err = verifyInstanceUpdatePackages(ctx, base, r.PreviousPackage, *r.Package); err != nil {
				return err
			}
		}
		r.Phase = "replacing"
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
		for _, component := range r.Package.Components {
			previous := base.CoreHash
			if component.Contract.Component == "signer" {
				previous = base.SignerHash
			}
			if err = maintenance.CopyProtected(filepath.Join(dir, component.Artifact.File), filepath.Join(base.Root, component.Artifact.File), 0755, component.Artifact.SHA256, component.Artifact.Size, previous); err != nil {
				return err
			}
		}
		if r.Package.NativeDesktop != nil {
			if err = maintenance.CopyProtected(filepath.Join(dir, "fased-desktop"), filepath.Join(base.Root, "fased-desktop"), 0755, r.Package.NativeDesktop.SHA256, r.Package.NativeDesktop.Size, r.PreviousPackage.NativeDesktop.SHA256); err != nil {
				return err
			}
			if err = verifyProtectedDesktop(target, r.Package); err != nil {
				return err
			}
		}
		if err = maintenance.ReplaceProtected(base.Root+"/install.json", mustJSON(target), 0644, sum(mustJSON(base))); err != nil {
			return err
		}
		if err = VerifyPackageInstance(target); err != nil {
			return err
		}
		r.Phase, r.Start = "ready", "target"
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
	}
	if r.Phase != "ready" || r.Application == nil || !r.RecoveryEligible() {
		return errors.New("the original application recovery direction is required")
	}
	if err := VerifyPackageInstance(target); err != nil {
		return err
	}
	if r.Package.NativeDesktop != nil {
		if err := verifyProtectedDesktop(target, r.Package); err != nil {
			return err
		}
	}
	if err := verifyPendingNativeTarget(ctx, target, r.Package); err != nil {
		return err
	}
	if boot {
		_, err := protectedSystemctl(ctx, "start", "--no-block", "--", instanceWorkerUnit(base, r.OperationID))
		return err
	}
	if _, err := protectedSystemctl(ctx, "reset-failed", "--", instanceUnit(base, "app")); err != nil {
		return err
	}
	if _, err := protectedSystemctl(ctx, "start", "--", instanceUnit(base, "app")); err != nil {
		return err
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var view core.ApplicationOwnerView
		err := protectedAppPeer(ctx, a, target, maintenance.AppMaintenanceRequest{Action: "operation", OperationID: r.OperationID}, &view)
		if err == nil && view.RunningSHA256 == target.CoreHash && view.BootID != r.Application.Pause.Core.BootID && view.View.Operation.OwnerLease == r.OwnerLease {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("the updated application is not healthy; preserve recovery")
		case <-tick.C:
		}
	}
	var completed core.ApplicationOwnerView
	if err := protectedAppPeer(ctx, a, target, maintenance.AppMaintenanceRequest{Action: "application-finish", OperationID: r.OperationID}, &completed); err != nil {
		return err
	}
	if completed.View.Operation.Phase != "completed" || completed.View.Operation.Receipt == nil || completed.View.Operation.Receipt.EngineSHA256 != target.CoreHash {
		return errors.New("the original application completion is unconfirmed")
	}
	r.Phase = "completed"
	if err := storeProtectedRecord(r); err != nil {
		return err
	}
	return maintenance.RemoveProtected(base.Root+"/update-active.json", sum(update.MaintenanceFence(r.OperationID)), 0644)
}

const packageUpdateConnected = true

// NativePackageUpdateAvailable reports the connected instance controller.
// Each operation must still verify its package, identity and recovery journal.
func NativePackageUpdateAvailable() bool { return packageUpdateConnected }

func matchEmptyPackageOperation(view core.ApplicationOwnerView, r update.MaintenanceRecord, base Manifest) error {
	o := view.View.Operation
	if o.ID != r.OperationID || (o.Phase != "applying" && o.Phase != "recovery") || o.Automatic || o.Release == nil ||
		o.Release.Sequence != r.Package.Sequence || o.Release.ManifestSHA256 != r.Package.ReleaseSHA256 || o.Release.Version != r.Package.Version ||
		o.PolicySHA256 != update.SnapshotDigest("application-update-policy", view.View.Policy) || view.RunningSHA256 != base.CoreHash ||
		(o.OwnerLease != "" && o.OwnerLease != r.OperationID) {
		return errors.New("the original manual application update is required")
	}
	for _, component := range r.Package.Components {
		if component.Contract.Component == "core" && component.Artifact.SHA256 == o.Release.EngineSHA256 {
			return nil
		}
	}
	return errors.New("the approved application differs from the package")
}
