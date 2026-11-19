//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// PackageLocalAdoption is the fixed source identity recorded with provisioning.
// No paths, passwords, client sessions or image bytes enter protected metadata.
type PackageLocalAdoption struct {
	LegacyManifestSHA256 string `json:"legacyManifestSha256"`
	InstanceID           string `json:"instanceId"`
	StateSHA256          string `json:"stateSha256"`
}

func (a PackageLocalAdoption) valid(input PackageProvisionInput) bool {
	return input.ReleaseSHA256 == maintenance.LegacyLocalRetainedPacket && input.Bundle != nil &&
		input.Source == maintenance.LegacyLocalRetainedSource && input.Bundle.ManifestSHA256 == maintenance.LegacyLocalRetainedBundle &&
		input.Bundle.InstallerSHA256 == maintenance.LegacyLocalRetainedInstaller &&
		input.Version == "0.1.23" && input.PublicOrigin == "" && validHex(a.LegacyManifestSHA256, 32) &&
		enrollmentInstance(a.InstanceID) && validHex(a.StateSHA256, 32)
}

func (a PackageLocalAdoption) fence() []byte {
	return []byte(update.SnapshotDigest("legacy-local-adoption-fence:v1", a) + "\n")
}

func verifyLegacyLocalAdoptionFence(m Manifest, a PackageLocalAdoption) error {
	f, err := readRoot(filepath.Join(m.Root, "update-start-blocked.json"))
	if err != nil || !known(f, a.fence(), 0644) {
		return errors.New("original Local adoption fence required; preserve the instance")
	}
	return nil
}

func prepareLegacyLocalImport(ctx context.Context, m Manifest, p update.MaintenancePackage, a PackageLocalAdoption, importImage func(Manifest) error) error {
	if err := checkPackageInitialFences(m, &a); err != nil {
		return err
	}
	if err := servicesStopped(ctx, m, func(ctx context.Context, _ []byte, args ...string) ([]byte, error) {
		return protectedSystemctl(ctx, args[1:]...)
	}); err != nil {
		return err
	}
	if err := VerifyUnconfiguredPackageSigner(ctx, m); err != nil {
		return err
	}
	journal := update.MaintenanceRecord{Version: 1, Owner: m.Owner, OperationID: m.ID, Phase: "idle", Start: "base", Base: mustJSON(m), Target: mustJSON(m), Package: &p}
	previous, err := update.ReadPackageMaintenanceRecord(m.Owner, m.Location, m.ID)
	if err == nil {
		if update.SnapshotDigest("initial-package", previous) != update.SnapshotDigest("initial-package", journal) {
			return errors.New("existing package operation differs; preserve it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	commit, proof, err := bindLegacyLocalImport(m, p, journal, a)
	if err != nil {
		return err
	}
	for name, expected := range map[string][]byte{"enrollment-ready.json": mustJSON(commit), "legacy-local-adoption.json": mustJSON(proof)} {
		f, err := readRoot(filepath.Join(m.Root, name))
		if !errors.Is(err, os.ErrNotExist) && (err != nil || !known(f, expected, 0644)) {
			return errors.New("existing Local adoption records differ; preserve them")
		}
	}
	return importImage(m)
}

// Record both the format-4 journal and the existing owner commitment. A crash
// between publications leaves the same fence blocking every service start.
func sealLegacyLocalImport(m Manifest, pack update.MaintenancePackage, a PackageLocalAdoption) error {
	if err := checkPackageInitialFences(m, &a); err != nil {
		return err
	}
	r, err := update.ReadPackageMaintenanceRecord(m.Owner, m.Location, m.ID)
	if err != nil {
		return err
	}
	commit, proof, err := bindLegacyLocalImport(m, pack, r, a)
	if err != nil {
		return err
	}
	if err := maintenance.PublishProtected(filepath.Join(m.Root, "enrollment-ready.json"), mustJSON(commit), 0644); err != nil {
		return err
	}
	return maintenance.PublishProtected(filepath.Join(m.Root, "legacy-local-adoption.json"), mustJSON(proof), 0644)
}

type legacyLocalAdoptionProof struct {
	Version                 int                  `json:"version"`
	PredecessorPacketSHA256 string               `json:"predecessorPacketSha256"`
	TargetPacketSHA256      string               `json:"targetPacketSha256"`
	BundleSHA256            string               `json:"bundleSha256"`
	JournalSHA256           string               `json:"journalSha256"`
	OwnerCommitSHA256       string               `json:"ownerCommitSha256"`
	Source                  PackageLocalAdoption `json:"source"`
	ApplyAllowed            bool                 `json:"applyAllowed"`
}

func bindLegacyLocalImport(m Manifest, p update.MaintenancePackage, r update.MaintenanceRecord, a PackageLocalAdoption) (EnrollmentCommit, legacyLocalAdoptionProof, error) {
	var c EnrollmentCommit
	var proof legacyLocalAdoptionProof
	base, err := ValidateInitialPackageMaintenanceRecord(r, m.Owner, m.Location, m.ID)
	if err != nil || base != m || m.Location != "local" || !a.valid(PackageProvisionInput{ReleaseSHA256: p.ReleaseSHA256, Source: p.Source, Bundle: p.Bundle, Version: p.Version}) ||
		update.SnapshotDigest("local-adoption-release", *r.Package) != update.SnapshotDigest("local-adoption-release", p) {
		return c, proof, errors.New("the installed Local adoption journal differs from the retained package")
	}
	c = EnrollmentCommit{Version: 1, ManifestSHA256: sum(mustJSON(EnrollmentIdentity(m))), InstanceID: a.InstanceID, Digest: packageOwnerDigest(m, a.InstanceID)}
	if !validPackageOwnerCommit(m, c) {
		return EnrollmentCommit{}, proof, errors.New("the imported owner identity cannot bind to the package")
	}
	proof = legacyLocalAdoptionProof{Version: 1, Source: a, PredecessorPacketSHA256: maintenance.LegacyLocalPredecessorPacket,
		TargetPacketSHA256: p.ReleaseSHA256, BundleSHA256: p.Bundle.ManifestSHA256,
		JournalSHA256: update.SnapshotDigest("legacy-local-adoption-journal:v1", r), OwnerCommitSHA256: sum(mustJSON(c))}
	return c, proof, nil
}
