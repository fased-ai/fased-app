//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// ValidateInitialMaintenanceRecord recognizes an idle, independently verified
// installed pair. It never converts a pending update or signer hold to idle.
func ValidateInitialMaintenanceRecord(r update.MaintenanceRecord) (Manifest, error) {
	var empty Manifest
	if err := r.ValidateIdle(); err != nil {
		return empty, err
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil || base != target || base.Version != 3 || !base.Enrollment || r.OperationID != base.ID {
		return empty, errors.New("initial custody installation identity differs")
	}
	if base.Provenance == signer.OwnerKeyProvenance && r.Package.DevelopmentOnly {
		return empty, errors.New("development release cannot initialize owner custody")
	}
	for _, c := range r.Package.Components {
		switch c.Contract.Component {
		case "core":
			if c.Artifact.SHA256 != base.CoreHash {
				return empty, errors.New("initial core differs from the installed pair")
			}
		case "signer":
			if c.Artifact.SHA256 != base.SignerHash {
				return empty, errors.New("initial signer differs from the installed pair")
			}
		}
	}
	return base, nil
}

// VerifyInitialProtectedDeployment is read-only. A foreign fence remains a
// refusal even when an otherwise valid initial journal exists.
func VerifyInitialProtectedDeployment(owner int) error {
	a, err := maintenance.ReadAuthority(owner)
	if err != nil {
		return err
	}
	if err = maintenance.VerifyImage(a); err != nil {
		return err
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return err
	}
	m, err := ValidateInitialMaintenanceRecord(r)
	if err != nil || m.Location != a.Profile {
		return errors.New("initial journal does not identify the installed profile")
	}
	if err = verifyProtectedPair(m); err != nil {
		return err
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return err
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, err = os.Lstat(m.Root + "/" + name); !errors.Is(err, os.ErrNotExist) {
			return errors.New("existing custody update fence must be preserved")
		}
	}
	return nil
}

// InitializeProtectedJournal is called only by the protected helper while its
// owner operation lock is held, with a package independently verified by that
// helper. It never replaces an existing transaction or an occupied signer.
func InitializeProtectedJournal(ctx context.Context, owner int, p update.MaintenancePackage) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	m, err := Load(owner)
	if err != nil || m.Location != a.Profile || m.Version != 3 || !m.Enrollment {
		return errors.New("exact enrolled custody profile required")
	}
	if err = Verify(m); err != nil {
		return err
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return err
	}
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: m.ID, Phase: "idle", Start: "base", Base: mustJSON(m), Target: mustJSON(m), Package: &p}
	if _, err = ValidateInitialMaintenanceRecord(r); err != nil {
		return err
	}
	old, err := update.ReadMaintenanceRecord(owner)
	if err == nil {
		if _, err = ValidateInitialMaintenanceRecord(old); err != nil || update.SnapshotDigest("initial-maintenance", old) != update.SnapshotDigest("initial-maintenance", r) {
			return errors.New("existing custody journal differs; preserve it")
		}
		if err = VerifyInitialProtectedDeployment(owner); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		for _, role := range []string{"app", "signer"} {
			state, e := protectedSystemctl(ctx, "show", "--property=ActiveState", "--value", "--", Unit(owner, role))
			if e != nil || strings.TrimSpace(string(state)) != "inactive" {
				return errors.New("custody services must be inactive before first journal initialization")
			}
		}
		entries, e := os.ReadDir(m.Root + "/signer-state")
		if e != nil || len(entries) != 0 {
			return errors.New("existing signer state must be preserved")
		}
		for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
			if _, e = os.Lstat(m.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
				return errors.New("existing custody update fence must be preserved")
			}
		}
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
	} else {
		return err
	}
	if err = installProtectedMaintenanceUnits(ctx, a, m); err != nil {
		return err
	}
	return verifyProtectedLoadedUnits(ctx, a, m)
}
