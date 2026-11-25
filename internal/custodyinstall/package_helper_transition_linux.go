//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// TransitionPackageHelper uses a one-time administrator invocation of the
// exact attested successor. Interrupted cutovers resume with the same command.
func TransitionPackageHelper(ctx context.Context, owner int, location, instance, previousHelper string) error {
	lock, err := maintenance.PackageLock(owner, location, instance)
	if err != nil {
		return err
	}
	m, err := transitionPackageHelper(ctx, owner, location, instance, previousHelper)
	if err = errors.Join(err, lock.Close()); err != nil {
		return err
	}
	if m.ID == "" {
		return nil
	}
	original, err := ReadEnrollmentCommit(m)
	if err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, append([]string{"start", "--"}, protectedInstanceUnits(m)...)...); err != nil {
		return err
	}
	return waitPackageHealth(ctx, m, func(health core.Health) bool {
		return installedPackageOwnerHealthy(m, original, health)
	})
}

func transitionPackageHelper(ctx context.Context, owner int, location, instance, previousHelper string) (Manifest, error) {
	var empty Manifest
	next, err := maintenance.PackageTransitionProcess(owner, location, instance)
	if err != nil {
		return empty, err
	}
	active, err := maintenance.ReadPackageAuthority(owner, location, instance)
	if err != nil {
		return empty, err
	}
	previous := next
	previous.ImageSHA256 = previousHelper
	if active != previous && active != next {
		return empty, errors.New("the selected predecessor is not this instance's helper")
	}
	root, _ := PackageRoot(owner, location, instance)
	state, _ := update.PackageMaintenanceRoot(owner, location, instance)
	fence := filepath.Join(root, "update-start-blocked.json")
	journal, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err != nil {
		return empty, err
	}
	retained, err := update.ReadPackageHelperTransition(owner, location, instance)
	existing := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	transitionPath := filepath.Join(state, update.PackageHelperTransitionFile)
	retainedHash := ""
	if existing {
		retainedHash, _, err = maintenance.ProtectedDigest(transitionPath, 0644, 8192)
		if err != nil {
			return empty, err
		}
	}
	var ancestors []string
	if existing && retained.NextHelper == previousHelper {
		ancestors = retained.ChainPredecessors()
	} else if existing && retained.PreviousHelper == previousHelper && retained.NextHelper == next.ImageSHA256 {
		ancestors = slices.Clone(retained.Ancestors)
	}
	wanted := update.PackageHelperTransition{
		Version: 1, Owner: owner, Instance: instance, PreviousHelper: previousHelper,
		NextHelper: next.ImageSHA256, Ancestors: ancestors,
		JournalSHA256: update.SnapshotDigest("package-helper-journal:v1", journal),
	}
	if !wanted.Valid(owner, instance) || !wanted.Inherits(journal) {
		return empty, errors.New("finish the original package recovery before changing its helper")
	}
	fenceBytes := mustJSON(wanted)
	fenceHash, _, fenceErr := maintenance.ProtectedDigest(fence, 0644, 8192)
	if fenceErr != nil && !errors.Is(fenceErr, os.ErrNotExist) {
		return empty, fenceErr
	}
	installedInstaller := ""
	if journal.Package != nil {
		installedInstaller = journal.Package.InstallerDigest()
	}
	phase, err := classifyPackageHelperTransition(wanted, retained, existing, active.ImageSHA256, fenceHash, installedInstaller)
	if err != nil || phase == "superseded" {
		return empty, err
	}
	if _, err = os.Lstat(filepath.Join(root, "update-active.json")); !errors.Is(err, os.ErrNotExist) {
		return empty, errors.New("the original package update must finish first")
	}
	m, err := selectPackageStart(journal, active, location, instance)
	if err != nil || maintenance.VerifyImage(previous) != nil {
		return empty, errors.Join(err, errors.New("the original verified deployment is required"))
	}
	actual, err := LoadPackageInstance(owner, root)
	if err != nil || m != actual || VerifyPackageInstance(m) != nil || verifyPackageWalletMarker(m) != nil {
		return empty, errors.New("the installed pair differs from the original journal")
	}
	if phase == "committed" {
		return m, verifyPackageHelperTransition(ctx, next, m, journal)
	}
	if err = maintenance.VerifyPackageTransitionPolicy(previous, next, instance, phase == "resume"); err != nil {
		return empty, err
	}
	gate := filepath.Join(root, "fased-install")
	gateHash, _, gateErr := maintenance.ProtectedDigest(gate, 0755, 128<<20)
	if gateErr != nil || gateHash != previousHelper && (phase != "resume" || gateHash != next.ImageSHA256) {
		return empty, errors.New("existing package gate differs; preserve it")
	}
	if phase == "start" {
		if err = preflightPackageHelperTransition(ctx, previous, m, journal); err != nil {
			return empty, err
		}
	}
	if err = maintenance.PublishProtected(fence, fenceBytes, 0644); err != nil {
		return empty, err
	}
	if err = maintenance.ReplaceProtected(transitionPath, fenceBytes, 0644, retainedHash); err != nil {
		return empty, err
	}
	return m, finishPackageHelperTransition(ctx, previous, next, m, journal, fence, fenceBytes)
}

// The fence is durable before the receipt or any authority change. Every
// interrupted prefix then either resumes the same target or fails closed.
// Completed transitions chain forward while preserving prior helper lineage.
func classifyPackageHelperTransition(wanted, retained update.PackageHelperTransition, hasReceipt bool, active, fenceHash, installedInstaller string) (string, error) {
	chained := false
	if hasReceipt && !retained.Equal(wanted) {
		if active == wanted.NextHelper && retained.NextHelper == wanted.NextHelper &&
			retained.PreviousHelper == wanted.PreviousHelper && slices.Equal(retained.Ancestors, wanted.Ancestors) && fenceHash == "" {
			return "superseded", nil
		}
		if retained.NextHelper == wanted.PreviousHelper && slices.Equal(wanted.Ancestors, retained.ChainPredecessors()) {
			chained = true
		} else {
			return "", errors.New("the original helper transition must be preserved")
		}
	}
	// Older installed apps independently validate completed approvals at
	// startup. The same attested bundle must supply the app and successor
	// helper before the pin changes.
	if installedInstaller != wanted.NextHelper {
		return "", errors.New("complete the ordinary Update to the helper's attested package before changing its pin")
	}
	if fenceHash != "" {
		if fenceHash == sum(mustJSON(wanted)) && (active == wanted.PreviousHelper || hasReceipt && !chained && active == wanted.NextHelper) {
			return "resume", nil
		}
		return "", errors.New("an existing recovery fence must be preserved")
	}
	if hasReceipt && !chained && active == wanted.NextHelper {
		return "committed", nil
	}
	if (!hasReceipt || chained) && active == wanted.PreviousHelper {
		return "start", nil
	}
	return "", errors.New("the original helper transition state is unavailable")
}

func preflightPackageHelperTransition(ctx context.Context, previous maintenance.Authority, m Manifest, journal update.MaintenanceRecord) error {
	if err := preflightTransitionCredential(m); err != nil {
		return err
	}
	if err := verifyLoadedPackageServices(ctx, m); err != nil {
		return err
	}
	if err := verifyPackageRootUnits(ctx, previous, m, journal.OperationID); err != nil {
		return err
	}
	if m.Network == "" {
		return VerifyUnconfiguredPackageSigner(ctx, m)
	}
	result, err := protectedOwnerPeer(ctx, previous, m, signer.Request{Action: "fixture-update-inventory", ProbeID: update.NewProbe()})
	if err != nil || result.UpdateInventory == nil || result.UpdateInventory.ActiveIntentID != "" {
		return errors.Join(err, errors.New("the original signer must be reachable with no active update intent"))
	}
	return nil
}

// Older installs have neither credential file. Newer ones must retain the
// exact drop-in and its existing ciphertext through the helper transition.
func preflightTransitionCredential(m Manifest) error {
	dropIn, dropInErr := readRoot(packageTaskCredentialDropIn(m))
	_, size, credentialErr := maintenance.ProtectedDigest(filepath.Join(m.Root, packageTaskCredential), 0600, packageTaskCredentialLimit)
	if errors.Is(dropInErr, os.ErrNotExist) && errors.Is(credentialErr, os.ErrNotExist) {
		return nil
	}
	if dropInErr != nil || credentialErr != nil || size < 32 || !known(dropIn, packageTaskCredentialUnit(m), 0644) {
		return errors.New("existing provider credential configuration must be preserved")
	}
	return nil
}

func finishPackageHelperTransition(ctx context.Context, previous, next maintenance.Authority, m Manifest, journal update.MaintenanceRecord, fence string, fenceBytes []byte) error {
	oldFiles, err := packageMaintenanceFiles(previous, m)
	if err != nil {
		return err
	}
	newFiles, err := packageMaintenanceFiles(next, m)
	if err != nil {
		return err
	}
	units := []string{packageRecoveryUnit(m), instanceUnit(m, "app"), instanceUnit(m, "signer")}
	for path, body := range newFiles {
		hash, _, e := maintenance.ProtectedDigest(path, 0644, 8192)
		if e != nil || hash != sum(oldFiles[path]) && hash != sum(body) {
			return errors.New("a package service or policy changed; preserve it")
		}
	}
	if _, err = protectedSystemctl(ctx, append([]string{"stop", "--"}, units...)...); err != nil {
		return err
	}
	if err = maintenance.ApplyPackageTransitionAuthority(previous, next, m.ID); err != nil {
		return err
	}
	source := maintenance.ImagePath(next.ImageSHA256)
	_, size, err := maintenance.ProtectedDigest(source, 0555, 128<<20)
	if err != nil {
		return err
	}
	if err = maintenance.ReplaceProtectedPackageGate(source, filepath.Join(m.Root, "fased-install"), next.ImageSHA256, size, previous.ImageSHA256); err != nil {
		return err
	}
	for _, name := range []string{packageRecoveryUnit(m), packageWorkerTemplate(m)} {
		path := filepath.Join("/etc/systemd/system", name)
		if err = maintenance.ReplaceProtected(path, newFiles[path], 0644, sum(oldFiles[path])); err != nil {
			return err
		}
	}
	system := System{protectedOwner: m.Owner, protectedPurpose: "prepare"}
	if err = preparePackageTaskCredential(ctx, m.Root, system.Run, maintenance.ProtectedDigest, maintenance.PublishProtected); err != nil {
		return err
	}
	if err = publishPackageTaskCredentialUnit(m); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err = verifyPackageHelperTransition(ctx, next, m, journal); err != nil {
		return err
	}
	return maintenance.RemoveProtected(fence, sum(fenceBytes), 0644)
}

func verifyPackageHelperTransition(ctx context.Context, next maintenance.Authority, m Manifest, journal update.MaintenanceRecord) error {
	active, err := maintenance.ReadPackageAuthority(m.Owner, m.Location, m.ID)
	if err != nil || active != next {
		return errors.New("the successor authority is unavailable")
	}
	if err = maintenance.VerifyPackageTransitionPolicy(next, next, m.ID, false); err != nil {
		return err
	}
	gate, _, err := maintenance.ProtectedDigest(filepath.Join(m.Root, "fased-install"), 0755, 128<<20)
	if err != nil || gate != next.ImageSHA256 {
		return errors.New("the successor service gate is unavailable")
	}
	if _, size, e := maintenance.ProtectedDigest(filepath.Join(m.Root, packageTaskCredential), 0600, packageTaskCredentialLimit); e != nil || size < 32 {
		return errors.New("the original encrypted provider credential is unavailable")
	}
	if hash, _, e := maintenance.ProtectedDigest(packageTaskCredentialDropIn(m), 0644, 8192); e != nil || hash != sum(packageTaskCredentialUnit(m)) {
		return errors.New("the exact provider credential drop-in is unavailable")
	}
	selected, err := selectPackageStart(journal, next, m.Location, m.ID)
	if err != nil || selected != m {
		return errors.Join(err, errors.New("successor changed the selected deployment"))
	}
	if err = verifyPackageRootUnits(ctx, next, m, journal.OperationID); err != nil {
		return err
	}
	if err = verifyLoadedPackageServices(ctx, m); err != nil {
		return err
	}
	fields, err := loadedUnitPolicyFields(m, "app", definitions(m)["fased-app.service"], m.Root+"/fased-app")
	if err != nil || !slices.Contains(strings.Fields(fields["DropInPaths"]), packageTaskCredentialDropIn(m)) {
		return errors.New("systemd has not loaded the protected provider credential drop-in")
	}
	return nil
}
