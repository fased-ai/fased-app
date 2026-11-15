//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// maintenancePackageStageCandidate retains only the exact public candidate
// stream and verifies its original proof under this instance's fixed helper.
func maintenancePackageStageCandidate(ctx context.Context, owner int, location, instance string, q maintenance.CandidateRequest, in io.Reader) (maintenance.CandidateReceipt, error) {
	var empty maintenance.CandidateReceipt
	if in == nil {
		return empty, errors.New("one candidate stream required")
	}
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil {
		return empty, err
	}
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return empty, err
	}
	old, err := maintenance.ReadPackageCandidateReceipt(owner, location, instance)
	if err == nil {
		if old.PacketSHA256 != q.PacketSHA256 || old.Scope != q.Scope {
			return empty, errors.New("another candidate is retained; preserve its original operation")
		}
		if q.Scope == "update" && time.Now().Unix() >= old.ExpiresAt {
			return empty, errors.New("candidate review expired; preserve its original operation")
		}
		identity, e := verifyPackageCandidate(ctx, owner, location, instance, q.PacketSHA256)
		if e != nil || !matchingPackageCandidate(old, identity, a.ImageSHA256) {
			return empty, errors.New("retained candidate differs; preserve it")
		}
		if q.Scope == "update" {
			if _, _, e = packageCandidateBaseline(owner, location, instance, &old); e != nil {
				return empty, e
			}
		}
		return old, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	var installed *update.MaintenancePackage
	var base custodyinstall.Manifest
	if q.Scope == "fresh" {
		if _, e := update.ReadPackageMaintenanceRecord(owner, location, instance); !errors.Is(e, os.ErrNotExist) {
			return empty, errors.New("existing package journal must be preserved")
		}
		instanceRoot, _ := update.PackageInstanceRoot(owner, location, instance)
		if _, e := os.Lstat(instanceRoot); !errors.Is(e, os.ErrNotExist) {
			return empty, errors.New("existing package installation must be preserved")
		}
	} else if q.Scope == "update" {
		installed, base, err = packageCandidateBaseline(owner, location, instance, nil)
		if err != nil {
			return empty, err
		}
	} else {
		return empty, errors.New("fixed package candidate scope required")
	}
	// Refuse before receiving bytes if this helper has no compiled trust.
	if _, err = packageCandidateOptions(owner, location, instance); err != nil {
		return empty, err
	}
	parent := filepath.Join(root, "candidate")
	if err = maintenance.EnsureProtectedDirectory(parent); err != nil {
		return empty, err
	}
	dir := filepath.Join(parent, q.PacketSHA256)
	reservation := filepath.Join(parent, q.PacketSHA256+".reservation.json")
	if err = requirePackageCandidateReservation(dir, reservation); err != nil {
		return empty, err
	}
	if err = maintenance.ReservePackageCandidate(owner, location, instance, q); err != nil {
		return empty, err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return empty, err
	}
	for _, entry := range entries {
		if entry.Name() != q.PacketSHA256 && entry.Name() != q.PacketSHA256+".reservation.json" {
			return empty, errors.New("unclassified candidate input; preserve it")
		}
	}
	if _, err = os.Lstat(dir); err == nil {
		// The matching protected reservation proves this is only an
		// uncommitted public download, not wallet or installation state.
		if err = removeCandidateData(parent, q.PacketSHA256); err != nil {
			return empty, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return empty, err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return empty, err
	}
	if err = bootstrap.ReceiveCandidate(ctx, in, dir, q.PacketSHA256); err != nil {
		return empty, err
	}
	for _, path := range []string{dir, parent} {
		d, e := os.Open(path)
		if e != nil {
			return empty, e
		}
		if e = errors.Join(d.Sync(), d.Close()); e != nil {
			return empty, e
		}
	}
	identity, err := verifyPackageCandidate(ctx, owner, location, instance, q.PacketSHA256)
	if err != nil {
		return empty, err
	}
	if q.Scope == "fresh" && identity.InstallerSHA256 != a.ImageSHA256 {
		return empty, errors.New("fresh candidate differs from the installed helper")
	}
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: owner, Scope: q.Scope, PacketSHA256: q.PacketSHA256, InstallerSHA256: identity.InstallerSHA256, EngineSHA256: identity.Core.SHA256, ProductVersion: identity.Version, Sequence: identity.Sequence, ReleaseSHA256: identity.ReleaseSHA256, Target: identity.Target}
	if installed != nil {
		receipt.BaseSequence, receipt.BaseReleaseSHA256 = installed.Sequence, installed.ReleaseSHA256
		receipt.BaseInstallation = instance
		receipt.BaseManifestSHA = update.SnapshotDigest("candidate-base-manifest:v1", base)
		receipt.ExpiresAt = time.Now().Add(15 * time.Minute).Unix()
	}
	if err = receipt.Validate(); err != nil {
		return empty, err
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return empty, err
	}
	if err = maintenance.PublishProtected(filepath.Join(root, "candidate.json"), append(body, '\n'), 0644); err != nil {
		return empty, err
	}
	return receipt, nil
}

// An already present directory cannot be classified by creating its
// reservation afterwards. ReservePackageCandidate separately verifies the
// pre-existing reservation's exact bytes and protected ownership.
func requirePackageCandidateReservation(directory, reservation string) error {
	_, dirErr := os.Lstat(directory)
	_, reservationErr := os.Lstat(reservation)
	if dirErr != nil && !errors.Is(dirErr, os.ErrNotExist) || reservationErr != nil && !errors.Is(reservationErr, os.ErrNotExist) {
		return errors.New("package candidate reservation cannot be inspected")
	}
	if dirErr == nil && errors.Is(reservationErr, os.ErrNotExist) {
		return errors.New("unclassified package candidate directory; preserve it")
	}
	return nil
}

func packageCandidateOptions(owner int, location, instance string) (bootstrap.Options, error) {
	if compiledMaintenanceOptions == nil && compiledFirstInstallBinding == nil {
		return bootstrap.Options{}, errors.New("trusted package installer release is unavailable")
	}
	if err := maintenance.EnsurePackageAcquisition(owner, location, instance); err != nil {
		return bootstrap.Options{}, err
	}
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return bootstrap.Options{}, err
	}
	if compiledMaintenanceOptions != nil {
		return compiledMaintenanceOptions(root)
	}
	return retailInstallerOptions(compiledFirstInstallBinding, root)
}

func verifyPackageCandidate(ctx context.Context, owner int, location, instance, packet string) (bootstrap.PackageMaintenanceIdentity, error) {
	var empty bootstrap.PackageMaintenanceIdentity
	o, err := packageCandidateOptions(owner, location, instance)
	if err != nil {
		return empty, err
	}
	o.Directory = filepath.Join(filepath.Dir(o.Directory), "candidate-verification")
	if _, err = os.Lstat(o.Directory); err == nil {
		if err = bootstrap.DiscardIncompletePackageMaintenance(owner, location, instance, o); err != nil {
			return empty, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	admission := bootstrap.CandidateAdmission{PacketSHA256: packet, OperationID: update.NewProbe(), BaselineSHA256: packet, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	p, err := bootstrap.PreparePackageMaintenanceCandidate(ctx, owner, location, instance, o, admission)
	if err != nil {
		return empty, err
	}
	identity, identityErr := p.PackageIdentity()
	discardErr := p.DiscardMaintenance()
	return identity, errors.Join(identityErr, discardErr)
}

func matchingPackageCandidate(r maintenance.CandidateReceipt, i bootstrap.PackageMaintenanceIdentity, helper string) bool {
	return r.Validate() == nil && r.ReleaseSHA256 == i.ReleaseSHA256 && r.PacketSHA256 == i.ReleaseSHA256 && r.Sequence == i.Sequence && r.ProductVersion == i.Version && r.EngineSHA256 == i.Core.SHA256 && r.InstallerSHA256 == i.InstallerSHA256 && r.Target == i.Target && (r.Scope != "fresh" || i.InstallerSHA256 == helper)
}

func packageCandidateBaseline(owner int, location, instance string, receipt *maintenance.CandidateReceipt) (*update.MaintenancePackage, custodyinstall.Manifest, error) {
	var empty custodyinstall.Manifest
	r, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err != nil || r.Phase != "idle" && r.Phase != "completed" {
		return nil, empty, errors.New("recover the current package operation before staging another")
	}
	selected, err := custodyinstall.SelectProtectedPackageStart(r, owner, location, instance)
	root, rootErr := custodyinstall.PackageRoot(owner, location, instance)
	actual, loadErr := custodyinstall.LoadPackageInstance(owner, root)
	if err != nil || rootErr != nil || loadErr != nil || actual != selected || custodyinstall.VerifyPackageInstance(actual) != nil {
		return nil, empty, errors.New("installed package identity differs; preserve it")
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, e := os.Lstat(filepath.Join(root, name)); !errors.Is(e, os.ErrNotExist) {
			return nil, empty, errors.New("the existing update fence must be preserved")
		}
	}
	p, err := r.InstalledPackage()
	if err != nil {
		return nil, empty, err
	}
	if receipt != nil && !receipt.MatchesBase(owner, p.Sequence, p.ReleaseSHA256, instance, update.SnapshotDigest("candidate-base-manifest:v1", actual)) {
		return nil, empty, errors.New("candidate predecessor changed; preserve it")
	}
	return p, actual, nil
}

// Initialization consumes no caller-selected release: the original retained
// candidate is independently verified again before the journal is published.
func maintenancePackageInitialize(ctx context.Context, owner int, location, instance string) error {
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil {
		return err
	}
	receipt, err := maintenance.ReadPackageCandidateReceipt(owner, location, instance)
	if err != nil || receipt.Scope != "fresh" || receipt.InstallerSHA256 != a.ImageSHA256 {
		return errors.New("the original fresh package candidate is required")
	}
	identity, err := verifyPackageCandidate(ctx, owner, location, instance, receipt.PacketSHA256)
	if err != nil || !matchingPackageCandidate(receipt, identity, a.ImageSHA256) {
		return errors.New("original package proof differs; preserve the installation")
	}
	root, err := custodyinstall.PackageRoot(owner, location, instance)
	if err != nil {
		return err
	}
	m, err := custodyinstall.LoadPackageInstance(owner, root)
	if err != nil || m.ID != instance || m.Location != location || m.CoreHash != identity.Core.SHA256 {
		return errors.New("installed package differs from the verified core")
	}
	components, err := custodyinstall.DescribeProtectedPackagePair(ctx, owner, location, instance, identity.Version, identity.Source)
	if err != nil {
		return err
	}
	p := update.MaintenancePackage{Version: identity.Version, Source: identity.Source, Sequence: identity.Sequence, ReleaseSHA256: identity.ReleaseSHA256, ProvenanceSHA256: identity.ProvenanceSHA256, Components: components}
	return custodyinstall.InitializeProtectedPackageJournal(ctx, owner, location, instance, p)
}
