//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// The immutable predecessor can still admit a later update before detecting
// successor unit overrides. Keep this draft unavailable until a durable
// future-update authority and its interrupted transition have been proven.
const successorFutureUpdatesSafe = false

// The original signer may be inspected after it commits but before the
// root transaction is published completed. The caller must still hold the
// original lock, verify the persistent fence and inspect the actual vault.
func VerifyOriginalCompletedSignerRecord(r update.MaintenanceRecord, h maintenance.Successor) error {
	if r.OperationID != h.OperationID || (r.Phase != "signer-completed" && r.Phase != "completed") || r.Start != "target" || r.Installation == nil || r.Installation.Phase != "completed" || !update.ValidInstallation(*r.Installation) || r.Installation.Digest != h.InstallationDigest {
		return errors.New("completed original signer receipt required")
	}
	return nil
}

// The cold signer reader must use the exact protected installation identities,
// network and state/socket paths from the original transaction.
func VerifyOriginalSignerConfig(r update.MaintenanceRecord, c signer.Config) error {
	base, target, err := protectedRecordManifests(r)
	if err != nil || r.Installation == nil || !update.ValidInstallation(*r.Installation) || c != identityConfig(target) || c.Validate() != nil {
		return errors.New("original protected signer configuration required")
	}
	b := r.Installation.Binding
	if base.ID != b.InstallationID || target.ID != b.InstallationID || base.CoreHash != b.PreviousCore || base.SignerHash != b.PreviousSigner || sum(mustJSON(base)) != b.PreviousManifest || target.CoreHash != b.TargetCore || target.SignerHash != b.TargetSigner {
		return errors.New("original protected signer configuration required")
	}
	return nil
}

// PrepareProtectedSuccessor is called by the verified protected installer
// while the original operation lock is held. It never replaces the authority,
// approval, signer or deployment pair. Partial publication remains held.
func PrepareProtectedSuccessor(ctx context.Context, owner int, id string) (maintenance.Successor, error) {
	var h maintenance.Successor
	if os.Getuid() != 0 || os.Geteuid() != 0 || !update.IDOK(id) {
		return h, errors.New("protected successor setup required")
	}
	if !successorFutureUpdatesSafe {
		return h, errors.New("successor admission held: future update authority is not yet safe")
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil || r.OperationID != id {
		return h, errors.New("original successor operation required")
	}
	self, err := os.Executable()
	if err != nil {
		return h, err
	}
	h, err = maintenance.RunningSuccessor(owner)
	fresh := errors.Is(err, maintenance.ErrNoSuccessor)
	if fresh {
		h, err = maintenance.NewSuccessor(owner)
	}
	if err != nil || h.OperationID != id || self != maintenance.ImagePath(h.SuccessorImage) {
		return h, errors.New("exact protected successor required")
	}
	a, err := maintenance.ReadAuthority(owner)
	if err != nil || a.ImageSHA256 != h.PreviousImage {
		return h, errors.New("original maintenance authority required")
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil {
		return h, err
	}
	if fresh {
		if err = verifyProtectedPair(target); err != nil {
			return h, err
		}
	}
	original, err := protectedMaintenanceFiles(a, base)
	if err != nil {
		return h, err
	}
	for path, want := range original {
		f, e := readRoot(path)
		if e != nil || !known(f, want, 0644) {
			return h, errors.New("original protected maintenance setup differs")
		}
	}
	// Recovery starts before the worker. If interrupted after its override, the
	// protected successor can complete setup under the same lock on next boot.
	for _, worker := range []bool{false, true} {
		path, body := successorOverride(owner, id, worker, self)
		if err = maintenance.EnsureProtectedDirectory(filepath.Dir(path)); err != nil {
			return h, err
		}
		entries, e := os.ReadDir(filepath.Dir(path))
		if e != nil {
			return h, e
		}
		for _, entry := range entries {
			if entry.Name() != filepath.Base(path) {
				return h, errors.New("foreign successor unit override; preserving it")
			}
		}
		if err = maintenance.PublishProtected(path, body, 0644); err != nil {
			return h, err
		}
	}
	if fresh {
		if err = maintenance.PublishSuccessor(h); err != nil {
			return h, err
		}
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return h, err
	}
	return maintenance.RunningSuccessor(owner)
}

// RunProtectedSuccessor accepts only the root-owned successor and the exact
// original operation. The caller holds the existing OS transaction lock.
func RunProtectedSuccessor(ctx context.Context, owner int, id string, boot bool, lock *os.File) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected successor execution required")
	}
	if !successorFutureUpdatesSafe {
		return errors.New("successor execution held: future update authority is not yet safe")
	}
	if lock == nil {
		return errors.New("original successor lock required")
	}
	h, err := maintenance.RunningSuccessor(owner)
	if err != nil || h.OperationID != id {
		return errors.New("original successor operation required")
	}
	a, err := maintenance.ReadAuthority(owner)
	if err != nil || a.ImageSHA256 != h.PreviousImage {
		return errors.New("original maintenance authority required")
	}
	return runProtectedUpdate(ctx, a, owner, id, boot, maintenance.ImagePath(h.SuccessorImage), lock)
}
