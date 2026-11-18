//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// VerifyPackageStart is the unprivileged ExecStartPre entry for an installed
// instance. A journal is not a signing grant: it selects only an authenticated
// executable pair and never unlocks, changes or clears signer state.
func VerifyPackageStart(owner int, location, instance, role string) error {
	if role != "app" && role != "signer" {
		return errors.New("fixed package service role required")
	}
	a, err := maintenance.ReadPackageAuthority(owner, location, instance)
	if err != nil || maintenance.ValidateOwnerIdentity(a) != nil || maintenance.VerifyImage(a) != nil {
		return errors.New("protected package authority is unavailable")
	}
	root, err := PackageRoot(owner, location, instance)
	if err != nil {
		return err
	}
	// The per-instance copy is installed from the verified, pinned helper. It
	// remains stable across payload updates and cannot be supplied by the app.
	self, err := os.Executable()
	if err != nil || self != root+"/fased-install" {
		return errors.New("the instance's protected maintenance gate is required")
	}
	hash, _, err := maintenance.ProtectedDigest(self, 0755, 128<<20)
	if err != nil || hash != a.ImageSHA256 {
		return errors.New("package maintenance gate differs from its authority")
	}
	r, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err != nil {
		return err
	}
	selected, err := selectPackageStart(r, a, location, instance)
	if err != nil {
		return err
	}
	identity := selected.App
	if role == "signer" {
		identity = selected.Signer
		if selected.Network == "" {
			return errors.New("the signer is not configured for a wallet")
		}
	}
	if os.Getuid() != identity.UID || os.Geteuid() != identity.UID || os.Getgid() != identity.GID || os.Getegid() != identity.GID {
		return errors.New("package service identity differs")
	}
	actual, err := LoadPackageInstance(owner, root)
	if err != nil || actual != selected || VerifyPackageInstance(actual) != nil {
		return errors.New("installed package pair differs from the selected recovery state")
	}
	if err = verifyPackageWalletMarker(actual); err != nil {
		return err
	}
	if role == "app" && r.Application != nil && r.Phase != "completed" {
		view, e := core.ReadApplicationOwnerOffline(context.Background(), root+"/app-state", r.OperationID)
		if e != nil || view.Checkpoint == nil || view.View.Operation.OwnerLease != r.OwnerLease ||
			update.SnapshotDigest("package-application-checkpoint", *view.Checkpoint) != update.SnapshotDigest("package-application-checkpoint", *r.Application) {
			return errors.New("the original application checkpoint is unavailable; preserve recovery")
		}
	}
	return verifyPackageStartFences(selected, r, readRoot)
}

// SelectProtectedPackageStart checks the journal's exact recorded direction;
// it does not start a service or replace the physical installed-pair check.
func SelectProtectedPackageStart(r update.MaintenanceRecord, owner int, location, instance string) (Manifest, error) {
	return packageStartSelection(r, owner, location, instance, func(operation string) (update.MaintenanceApproval, error) {
		a, err := maintenance.ReadPackageAuthority(owner, location, instance)
		if err != nil {
			return update.MaintenanceApproval{}, err
		}
		return readPackageStartApproval(r, a, location, instance, operation)
	})
}

func selectPackageStart(r update.MaintenanceRecord, a maintenance.Authority, location, instance string) (Manifest, error) {
	return packageStartSelection(r, a.Owner, location, instance, func(operation string) (update.MaintenanceApproval, error) {
		return readPackageStartApproval(r, a, location, instance, operation)
	})
}

func readPackageStartApproval(r update.MaintenanceRecord, a maintenance.Authority, location, instance, operation string) (update.MaintenanceApproval, error) {
	if location == "local" && r.Phase == "completed" && r.Approval != nil && r.Approval.HelperSHA256 != a.ImageSHA256 {
		return update.ReadInheritedPackageApproval(a.Owner, instance, a.ImageSHA256, r)
	}
	return update.ReadPackageMaintenanceApproval(a.Owner, location, instance, operation)
}

// PackageRecoveryOperation selects only the operation already recorded for the
// exact Local installation. The protected worker still verifies all files,
// signer holds and recovery decisions before changing anything.
func PackageRecoveryOperation(m Manifest, r update.MaintenanceRecord) (string, error) {
	if m.validatePackage() != nil || m.Location != "local" || r.Owner != m.Owner || r.OperationID == m.ID || r.Bootstrap || !r.RecoveryEligible() ||
		r.PreviousPackage == nil || r.Package == nil || !update.SameLinuxDistribution(*r.PreviousPackage, *r.Package) || r.Package.Sequence <= r.PreviousPackage.Sequence {
		return "", errors.New("the original Local package recovery is required")
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil || m != base && m != target || r.Start == "base" && m != base || r.Start == "target" && m != target ||
		!packagePairMatches(*r.PreviousPackage, base) || !packagePairMatches(*r.Package, target) {
		return "", errors.New("package recovery belongs to another installation")
	}
	if r.Installation != nil {
		binding := r.Installation.Binding
		if binding.InstallationID != m.ID || binding.PreviousCore != base.CoreHash || binding.PreviousSigner != base.SignerHash || binding.PreviousManifest != sum(mustJSON(base)) || binding.TargetCore != target.CoreHash || binding.TargetSigner != target.SignerHash {
			return "", errors.New("the original signer update hold differs")
		}
	}
	return r.OperationID, nil
}

// packageStartSelection accepts only a fully bound initial pair or the exact
// recorded base/target of an authenticated operation. A stopped phase is
// never treated as a start instruction.
func packageStartSelection(r update.MaintenanceRecord, owner int, location, instance string, readApproval func(string) (update.MaintenanceApproval, error)) (Manifest, error) {
	var base, target Manifest
	if r.Version != 1 || r.Owner != owner || !update.IDOK(r.OperationID) || strict(r.Base, &base) != nil || strict(r.Target, &target) != nil ||
		base.validatePackage() != nil || target.validatePackage() != nil || base.Owner != owner || base.Location != location || base.ID != instance {
		return Manifest{}, errors.New("exact protected package journal required")
	}
	comparison := target
	comparison.CoreHash, comparison.SignerHash = base.CoreHash, base.SignerHash
	if comparison != base {
		return Manifest{}, errors.New("package update cannot change its instance identity")
	}
	if r.Phase == "idle" {
		m, err := ValidateIdlePackageMaintenanceRecord(r, owner, location, instance)
		return m, err
	}
	if r.OwnerLease != "" || r.Application != nil {
		if base.Network != "" || !r.RecoveryEligible() || r.PreviousPackage == nil || !packagePairMatches(*r.PreviousPackage, base) || !packagePairMatches(*r.Package, target) ||
			r.Application != nil && r.Application.PreviousEngineSHA256 != base.CoreHash {
			return Manifest{}, errors.New("the original app-only package recovery is required")
		}
		if r.Start == "base" && r.Phase == "prepared" {
			return base, nil
		}
		if r.Start == "target" && (r.Phase == "ready" || r.Phase == "completed") && r.Application != nil {
			return target, nil
		}
		return Manifest{}, errors.New("package recovery must finish before starting the service")
	}
	if base.Network == "" && (r.Staged != nil || r.Recovery != nil || r.Installation != nil || r.Start == "target") {
		return Manifest{}, errors.New("an unconfigured signer cannot supply a held package update")
	}
	if !r.RecoveryEligible() || r.Bootstrap || r.PreviousPackage == nil || !packagePairMatches(*r.PreviousPackage, base) || !packagePairMatches(*r.Package, target) || r.Package.Sequence <= r.PreviousPackage.Sequence {
		return Manifest{}, errors.New("recover the original package operation before service start")
	}
	if r.Start == "base" {
		if r.Phase == "prepared" && (r.Approval != nil || r.Staged != nil || r.Recovery != nil || r.Installation != nil) {
			return Manifest{}, errors.New("prepared base has an unclassified signer hold")
		}
		if r.Phase == "base-recovery" && (r.Staged != nil || r.Recovery != nil || r.Installation != nil) {
			if err := verifyPackageApproval(r, base, readApproval); err != nil {
				return Manifest{}, err
			}
			if r.Installation != nil && (r.Installation.InstalledSequenceCommitted || r.Installation.Binding.InstallationID != instance || r.Installation.Binding.PreviousCore != base.CoreHash || r.Installation.Binding.PreviousSigner != base.SignerHash || r.Installation.Binding.TargetCore != target.CoreHash || r.Installation.Binding.TargetSigner != target.SignerHash) {
				return Manifest{}, errors.New("committed or foreign signer cannot start the base")
			}
		}
		return base, nil
	}
	if r.Start != "target" || r.Approval == nil || r.Staged == nil || r.Recovery == nil || r.Installation == nil || readApproval == nil {
		return Manifest{}, errors.New("original signer hold and approval required before target start")
	}
	if err := verifyPackageApproval(r, base, readApproval); err != nil {
		return Manifest{}, err
	}
	approval := *r.Approval
	i := r.Installation
	pause := i.Binding.Index.Binding.Pause.Request
	stagedArtifacts := make([]update.Artifact, 0, len(r.Recovery.Binding.Files))
	for _, file := range r.Recovery.Binding.Files {
		stagedArtifacts = append(stagedArtifacts, file.Artifact)
	}
	if !update.ValidInstallation(*i) || !packageHeldPairMatches(*r.Package, *r.Staged, *i) || i.Binding.InstallationID != instance || i.Binding.PreviousCore != base.CoreHash || i.Binding.PreviousSigner != base.SignerHash || i.Binding.PreviousManifest != approval.PreviousManifest || i.Binding.TargetCore != target.CoreHash || i.Binding.TargetSigner != target.SignerHash ||
		pause == nil || pause.IntentID != r.OperationID || pause.Sequence != r.Package.Sequence || pause.ManifestSHA256 != approval.Digest() || !update.SameCoreMetadata(pause.Expected, approval.Core) ||
		update.SnapshotDigest("package-recovery-index", *r.Recovery) != update.SnapshotDigest("package-recovery-index", i.Binding.Index) ||
		r.Recovery.Binding.Signer.ActiveIntentID != r.OperationID || !r.Recovery.Binding.Signer.RecoveryHold || r.Recovery.Binding.Signer.SequenceFloor != r.Package.Sequence ||
		r.Staged.ID != r.OperationID || r.Staged.Sequence != r.Package.Sequence || r.Staged.ManifestSHA256 != approval.Digest() || r.Staged.State != "staged" || !update.Stageable(*r.Staged) || r.Staged.StageSHA256 != update.StageDigest(*r.Staged) || update.SnapshotDigest("package-staged-artifacts", r.Staged.Artifacts) != update.SnapshotDigest("package-staged-artifacts", stagedArtifacts) {
		return Manifest{}, errors.New("original signer hold or backup differs")
	}
	if i.Phase == "rolled-back-held" || r.Phase == "completed" && i.Phase != "completed" || r.Phase == "signer-completed" && i.Phase != "completed" || r.Phase == "signer-committed-held" && i.Phase != "committed-held" && i.Phase != "completed" || r.Phase == "signer-healthy-held" && i.Phase == "active" {
		return Manifest{}, errors.New("signer completion does not match the recorded phase")
	}
	return target, nil
}

func verifyPackageApproval(r update.MaintenanceRecord, base Manifest, readApproval func(string) (update.MaintenanceApproval, error)) error {
	if r.Approval == nil || r.Package == nil || readApproval == nil {
		return errors.New("original protected release approval required")
	}
	approval, err := readApproval(r.OperationID)
	if err != nil || approval.Validate() != nil || approval.Digest() != r.Approval.Digest() || approval.Owner != base.Owner || approval.InstallationID != base.ID || approval.OperationID != r.OperationID ||
		approval.Source != r.Package.Source || approval.ProductVersion != r.Package.Version || approval.ReleaseSHA256 != r.Package.ReleaseSHA256 || approval.ProvenanceSHA256 != r.Package.ProvenanceSHA256 || approval.DevelopmentOnly != r.Package.DevelopmentOnly ||
		approval.PreviousCore != base.CoreHash || approval.PreviousSigner != base.SignerHash || approval.PreviousManifest != sum(mustJSON(base)) || approval.Plan.Sequence != r.Package.Sequence || update.SnapshotDigest("package-components", approval.Plan.Components) != update.SnapshotDigest("package-components", r.Package.Components) {
		return errors.New("original protected release approval differs")
	}
	return nil
}

func packagePairMatches(p update.MaintenancePackage, m Manifest) bool {
	if p.Validate() != nil || p.NativeDesktop != nil && (m.Layout != PackageLayout || m.Location != "local") || p.DevelopmentOnly && m.Provenance == signer.OwnerKeyProvenance {
		return false
	}
	for _, component := range p.Components {
		if component.Contract.Component == "core" && component.Artifact.SHA256 != m.CoreHash || component.Contract.Component == "signer" && component.Artifact.SHA256 != m.SignerHash {
			return false
		}
	}
	return true
}

// A structurally valid signer hold can still name different compatibility
// contracts or artifact sizes. Bind both to the independently approved pair.
func packageHeldPairMatches(p update.MaintenancePackage, staged update.Intent, held update.Installation) bool {
	if len(p.Components) != len(staged.Artifacts) || len(p.Components) != len(held.Binding.Contracts) {
		return false
	}
	for _, component := range p.Components {
		artifacts, contracts := 0, 0
		for _, artifact := range staged.Artifacts {
			if artifact == component.Artifact {
				artifacts++
			}
		}
		for _, contract := range held.Binding.Contracts {
			if update.SnapshotDigest("package-contract", contract) == update.SnapshotDigest("package-contract", component.Contract) {
				contracts++
			}
		}
		if artifacts != 1 || contracts != 1 {
			return false
		}
	}
	return true
}

func verifyPackageStartFences(m Manifest, r update.MaintenanceRecord, read readHost) error {
	if read == nil {
		return errors.New("protected package fence reader required")
	}
	if _, err := read(m.Root + "/update-start-blocked.json"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("a package recovery fence is active; preserve it")
	}
	fence, err := read(m.Root + "/update-active.json")
	if errors.Is(err, os.ErrNotExist) {
		if r.Phase == "idle" || r.Phase == "prepared" || r.Phase == "completed" {
			return nil
		}
		return errors.New("original package recovery fence is unavailable")
	}
	if err != nil || r.Phase == "idle" || !known(fence, update.MaintenanceFence(r.OperationID), 0644) {
		return errors.New("a different package recovery fence is active")
	}
	return nil
}
