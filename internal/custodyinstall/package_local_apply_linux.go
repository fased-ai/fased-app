//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

type legacyLocalApplyRecord struct {
	Version      int    `json:"version"`
	Owner        int    `json:"owner"`
	Instance     string `json:"instance"`
	OperationID  string `json:"operationId"`
	BridgeSHA256 string `json:"bridgeSha256"`
	ProofSHA256  string `json:"proofSha256"`
}

type LegacyLocalApplyResult struct {
	OperationID string `json:"operationId"`
	InstanceID  string `json:"instanceId"`
	Completed   bool   `json:"completed"`
}

// ApplyLegacyLocalAdoption commits one sealed import. The caller holds the
// bridge Apply lock and PackageLock, and keeps the stopped source leased until
// both the retained service and its completion record have been verified.
func ApplyLegacyLocalAdoption(ctx context.Context, owner int, instance, listen string, recover bool, lock *os.File, source PackageLocalAdoption, verifyImage func(Manifest) error, verifySource func() error) (LegacyLocalApplyResult, error) {
	var result LegacyLocalApplyResult
	a, bridge, err := maintenance.ValidateLocalAdoptionBridge(owner, instance)
	if err != nil || lock == nil || verifyImage == nil || verifySource == nil {
		return result, errors.New("protected Local adoption authority and source lease required")
	}
	if err = checkStoppedLegacyOwner(ctx, a); err != nil {
		return result, err
	}
	root, err := PackageRoot(owner, "local", instance)
	if err != nil {
		return result, err
	}
	m, err := LoadPackageInstance(owner, root)
	if err != nil || m.ID != instance || m.Location != "local" || m.Listen != listen || m.Network != "" || VerifyPackageInstance(m) != nil {
		return result, errors.New("the exact adopted empty-signer installation is required")
	}
	journal, err := update.ReadPackageMaintenanceRecord(owner, "local", instance)
	if err != nil || journal.Package == nil {
		return result, errors.New("the original format-4 adoption journal is required")
	}
	commit, proof, err := bindLegacyLocalImport(m, *journal.Package, journal, source)
	if err != nil {
		return result, err
	}
	proofBytes := mustJSON(proof)
	proofFile, err := readRoot(root + "/legacy-local-adoption.json")
	recordedOwner, ownerErr := ReadEnrollmentCommit(m)
	if err != nil || !known(proofFile, proofBytes, 0644) || ownerErr != nil || recordedOwner != commit ||
		verifyPackageWalletMarker(m) != nil {
		return result, errors.New("the recorded Local adoption proof and workspace must match")
	}
	if err = verifyLoadedPackageServices(ctx, m); err != nil {
		return result, err
	}
	if err = verifyPackageRootUnits(ctx, a, m, instance); err != nil {
		return result, err
	}
	if err = VerifyUnconfiguredPackageSigner(ctx, m); err != nil {
		return result, err
	}
	held, err := readLegacyLocalApplyFence(m, source)
	if err != nil {
		return result, err
	}
	state, err := update.PackageMaintenanceRoot(owner, "local", instance)
	if err != nil {
		return result, err
	}
	intentPath := state + "/legacy-local-apply.json"
	completePath := state + "/legacy-local-apply-complete.json"
	record := legacyLocalApplyRecord{Version: 1, Owner: owner, Instance: instance, BridgeSHA256: filepath.Base(filepath.Dir(bridge)), ProofSHA256: sum(proofBytes)}
	record, pending, complete, err := readLegacyLocalApply(intentPath, completePath, record)
	if err != nil {
		return result, err
	}
	if err = checkLegacyLocalApplyAdmission(recover, pending, complete, held); err != nil {
		return result, err
	}
	if held {
		if err = servicesStopped(ctx, m, func(c context.Context, _ []byte, args ...string) ([]byte, error) {
			return protectedSystemctl(c, args[1:]...)
		}); err != nil {
			return result, err
		}
		if err = verifyImage(m); err != nil {
			return result, err
		}
	}
	if err = verifySource(); err != nil {
		return result, err
	}
	if complete {
		if err = maintenance.PublishProtected(completePath, mustJSON(record), 0644); err != nil {
			return result, err
		}
		return LegacyLocalApplyResult{record.OperationID, commit.InstanceID, true}, nil
	}
	if !pending {
		record.OperationID = update.NewProbe()
	}
	// Re-publishing the same record on recovery fsyncs it even if the first
	// process stopped immediately after its rename.
	if err = maintenance.PublishProtected(intentPath, mustJSON(record), 0644); err != nil {
		return result, err
	}
	result = LegacyLocalApplyResult{OperationID: record.OperationID, InstanceID: commit.InstanceID}
	// RemoveProtected also syncs the containing directory for an already
	// absent fence. Neither enable nor start is reached until that succeeds.
	if err = maintenance.RemoveProtected(root+"/update-start-blocked.json", sum(source.fence()), 0644); err != nil {
		return result, err
	}
	// Existing package boot recovery now sees its exact unfenced idle journal.
	// The empty signer is intentionally left disabled.
	if _, err = protectedSystemctl(ctx, "enable", "--", packageRecoveryUnit(m), instanceUnit(m, "app")); err != nil {
		return result, err
	}
	ready := func(c context.Context, m Manifest) error {
		return waitPackageHealth(c, m, func(h core.Health) bool { return installedPackageOwnerHealthy(m, commit, h) })
	}
	finish := func(c context.Context, m Manifest) error {
		again, err := maintenance.WaitPackageLock(c, m.Owner, m.Location, m.ID)
		if err != nil {
			return err
		}
		defer again.Close()
		actual, err := update.ReadPackageMaintenanceRecord(m.Owner, m.Location, m.ID)
		if err != nil || update.SnapshotDigest("legacy-local-adoption-journal:v1", actual) != proof.JournalSHA256 || checkPackageInitialFences(m, nil) != nil {
			return errors.New("the adopted journal changed during startup; preserve it")
		}
		return maintenance.PublishProtected(completePath, mustJSON(record), 0644)
	}
	if err := startProvisionedPackage(ctx, m, lock, protectedSystemctl, ready, finish); err != nil {
		return result, err
	}
	result.Completed = true
	return result, nil
}

func checkStoppedLegacyOwner(ctx context.Context, a maintenance.Authority) error {
	raw, err := exec.CommandContext(ctx, "/usr/bin/getent", "passwd", strconv.Itoa(a.Owner)).Output()
	fields := strings.Split(strings.TrimSpace(string(raw)), ":")
	if err != nil || len(fields) != 7 || fields[0] == "" || fields[0] != filepath.Base(fields[0]) ||
		fields[2] != strconv.Itoa(a.Owner) || fields[5] != a.Home || fields[6] != "/usr/sbin/nologin" ||
		requireLockedPackageAccount(ctx, fields[0]) != nil {
		return errors.New("Local adoption Apply requires the isolated locked legacy owner")
	}
	if _, err = os.Lstat(filepath.Join("/var/lib/systemd/linger", fields[0])); !errors.Is(err, os.ErrNotExist) {
		return errors.New("the legacy user manager can restart; preserve it")
	}
	manager, err := protectedSystemctl(ctx, "show", "user@"+strconv.Itoa(a.Owner)+".service", "--property=ActiveState,MainPID")
	if err != nil || (string(manager) != "ActiveState=inactive\nMainPID=0\n" && string(manager) != "MainPID=0\nActiveState=inactive\n") {
		return errors.New("the legacy user manager must stay stopped")
	}
	return nil
}

func readLegacyLocalApplyFence(m Manifest, source PackageLocalAdoption) (bool, error) {
	if _, err := os.Lstat(m.Root + "/update-active.json"); !errors.Is(err, os.ErrNotExist) {
		return false, errors.New("an existing package update must be preserved")
	}
	f, err := readRoot(m.Root + "/update-start-blocked.json")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !known(f, source.fence(), 0644) {
		return false, errors.New("the original Local adoption fence differs; preserve it")
	}
	return true, nil
}

func readLegacyLocalApply(intentPath, completePath string, want legacyLocalApplyRecord) (legacyLocalApplyRecord, bool, bool, error) {
	f, err := readRoot(intentPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Lstat(completePath); !errors.Is(e, os.ErrNotExist) {
			return want, false, false, errors.New("the original Local adoption Apply is missing")
		}
		return want, false, false, nil
	}
	var got legacyLocalApplyRecord
	if err != nil || f.Mode != 0644 || strict(f.Data, &got) != nil || !update.IDOK(got.OperationID) {
		return want, false, false, errors.New("invalid recorded Local adoption Apply")
	}
	want.OperationID = got.OperationID
	if got != want {
		return want, false, false, errors.New("the recorded Local adoption Apply belongs to different inputs")
	}
	done, err := readRoot(completePath)
	if errors.Is(err, os.ErrNotExist) {
		return got, true, false, nil
	}
	if err != nil || !known(done, mustJSON(got), 0644) {
		return want, false, false, errors.New("the Local adoption completion record differs")
	}
	return got, true, true, nil
}

func checkLegacyLocalApplyAdmission(recover, pending, complete, held bool) error {
	if complete && (!pending || held) {
		return errors.New("the completed Local adoption has inconsistent startup authority")
	}
	if recover && !pending {
		return errors.New("no recorded Local adoption Apply to recover")
	}
	if !recover && pending {
		return errors.New("recover the original Local adoption Apply")
	}
	if !pending && !held {
		return errors.New("the original Local adoption fence is missing")
	}
	return nil
}
