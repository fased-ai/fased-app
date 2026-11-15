//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
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
		if old.Scope == "fresh" && q.Scope == "update" {
			if err = retireFreshPackageCandidate(ctx, a, location, instance, old); err != nil {
				return empty, err
			}
		} else if old.Scope == "update" && q.Scope == "update" && old.PacketSHA256 != q.PacketSHA256 {
			if err = retireCompletedPackageCandidate(ctx, a, location, instance, old); err != nil {
				return empty, err
			}
		} else if old.PacketSHA256 != q.PacketSHA256 || old.Scope != q.Scope {
			return empty, errors.New("another candidate is retained; preserve its original operation")
		} else if q.Scope == "update" && time.Now().Unix() >= old.ExpiresAt {
			return empty, errors.New("candidate review expired; preserve its original operation")
		} else {
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
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
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
	if err = verifyRetainedPackageInputs(root, parent, a, location, instance, q.PacketSHA256, entries); err != nil {
		return empty, err
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

// A completed fresh enrollment may release only its public current-candidate
// slot. The signed bytes, reservation and original journal stay on disk.
func retireFreshPackageCandidate(ctx context.Context, a maintenance.Authority, location, instance string, receipt maintenance.CandidateReceipt) error {
	installed, actual, err := packageCandidateBaseline(a.Owner, location, instance, nil)
	if err != nil {
		return err
	}
	r, err := update.ReadPackageMaintenanceRecord(a.Owner, location, instance)
	if err != nil || freshPackageCandidateMatches(a, location, instance, receipt, r) != nil {
		return errors.New("fresh package is not the original completed installation; preserve it")
	}
	m, err := custodyinstall.ValidateInitialPackageMaintenanceRecord(r, a.Owner, location, instance)
	if err != nil || m != actual || installed.Sequence != receipt.Sequence || installed.ReleaseSHA256 != receipt.ReleaseSHA256 {
		return errors.New("fresh installed pair differs; preserve its candidate")
	}
	if entries, e := os.ReadDir(filepath.Join(actual.Root, "signer-state")); e != nil || len(entries) != 0 {
		return errors.New("the original signer is not empty; preserve its candidate")
	}
	identity, err := verifyPackageCandidate(ctx, a.Owner, location, instance, receipt.PacketSHA256)
	if err != nil || !matchingPackageCandidate(receipt, identity, a.ImageSHA256) || identity.Source != installed.Source || identity.ProvenanceSHA256 != installed.ProvenanceSHA256 {
		return errors.New("fresh signed candidate differs; preserve it")
	}
	return archivePackageCandidate(a, location, instance, receipt, r)
}

// A completed update can release its candidate slot only after the exact
// installed target and original protected record are independently checked.
func retireCompletedPackageCandidate(ctx context.Context, a maintenance.Authority, location, instance string, receipt maintenance.CandidateReceipt) error {
	installed, actual, err := packageCandidateBaseline(a.Owner, location, instance, nil)
	if err != nil {
		return err
	}
	r, err := update.ReadPackageMaintenanceRecord(a.Owner, location, instance)
	if err != nil || completedPackageCandidateMatches(a, location, instance, receipt, r) != nil || actual.CoreHash != receipt.EngineSHA256 || installed.Sequence != receipt.Sequence || installed.ReleaseSHA256 != receipt.ReleaseSHA256 {
		return errors.New("the original completed package must be preserved")
	}
	identity, err := verifyPackageCandidate(ctx, a.Owner, location, instance, receipt.PacketSHA256)
	if err != nil || !matchingPackageCandidate(receipt, identity, a.ImageSHA256) || identity.Source != installed.Source || identity.ProvenanceSHA256 != installed.ProvenanceSHA256 {
		return errors.New("completed candidate differs from its signed package")
	}
	return archivePackageCandidate(a, location, instance, receipt, r)
}

func archivePackageCandidate(a maintenance.Authority, location, instance string, receipt maintenance.CandidateReceipt, r update.MaintenanceRecord) error {
	root, err := update.PackageMaintenanceRoot(a.Owner, location, instance)
	if err != nil {
		return err
	}
	archive := filepath.Join(root, "candidate-archive")
	if err = maintenance.EnsureProtectedDirectory(archive); err != nil {
		return err
	}
	receiptBytes, receiptHash := candidateDocument(receipt)
	journalBytes, err := json.Marshal(r)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		name string
		data []byte
	}{{receipt.PacketSHA256 + ".journal.json", append(journalBytes, '\n')}, {receipt.PacketSHA256 + ".receipt.json", receiptBytes}} {
		if err = maintenance.PublishProtected(filepath.Join(archive, item.name), item.data, 0644); err != nil {
			return err
		}
	}
	return maintenance.RemoveProtected(filepath.Join(root, "candidate.json"), receiptHash, 0644)
}

func completedPackageCandidateMatches(a maintenance.Authority, location, instance string, receipt maintenance.CandidateReceipt, r update.MaintenanceRecord) error {
	var base, target custodyinstall.Manifest
	if a.Owner != receipt.Owner || a.Profile != location || receipt.Validate() != nil || receipt.Scope != "update" || r.Owner != a.Owner || r.Phase != "completed" || r.Start != "target" || !r.RecoveryEligible() || r.Package == nil || r.PreviousPackage == nil ||
		update.StrictJSON(r.Base, &base) != nil || update.StrictJSON(r.Target, &target) != nil || base.ID != instance || target.ID != instance || base.Location != location || target.Location != location ||
		!receipt.MatchesBase(a.Owner, r.PreviousPackage.Sequence, r.PreviousPackage.ReleaseSHA256, instance, update.SnapshotDigest("candidate-base-manifest:v1", base)) ||
		receipt.PacketSHA256 != receipt.ReleaseSHA256 || receipt.ReleaseSHA256 != r.Package.ReleaseSHA256 || receipt.Sequence != r.Package.Sequence || receipt.ProductVersion != r.Package.Version || receipt.EngineSHA256 != target.CoreHash {
		return errors.New("completed candidate differs from the protected update")
	}
	if r.Application == nil && (r.Installation == nil || r.Installation.Phase != "completed" || !update.ValidInstallation(*r.Installation)) {
		return errors.New("completed candidate has no original completion")
	}
	if r.Package.NativePackage == nil || r.Package.NativePackage.InstallerSHA256 != receipt.InstallerSHA256 {
		return errors.New("completed package installer identity differs")
	}
	selected, err := custodyinstall.SelectProtectedPackageStart(r, a.Owner, location, instance)
	if err != nil || selected != target {
		return errors.New("completed package target is not the recorded deployment")
	}
	return nil
}

func freshPackageCandidateMatches(a maintenance.Authority, location, instance string, receipt maintenance.CandidateReceipt, r update.MaintenanceRecord) error {
	m, err := custodyinstall.ValidateInitialPackageMaintenanceRecord(r, a.Owner, location, instance)
	if err != nil || a.Owner != receipt.Owner || a.Profile != location || receipt.Validate() != nil || receipt.Scope != "fresh" || m.Network != "" ||
		receipt.PacketSHA256 != receipt.ReleaseSHA256 || receipt.ReleaseSHA256 != r.Package.ReleaseSHA256 || receipt.Sequence != r.Package.Sequence ||
		receipt.ProductVersion != r.Package.Version || receipt.EngineSHA256 != m.CoreHash || receipt.InstallerSHA256 != a.ImageSHA256 {
		return errors.New("fresh candidate differs from the protected initial journal")
	}
	return nil
}

// Previously archived candidate inputs are allowed beside the new candidate
// only when both their protected receipt and exact original reservation exist.
func verifyRetainedPackageInputs(root, parent string, a maintenance.Authority, location, instance, current string, entries []os.DirEntry) error {
	verified := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if name == current || name == current+".reservation.json" {
			continue
		}
		packet := strings.TrimSuffix(name, ".reservation.json")
		raw, err := hex.DecodeString(packet)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != packet {
			return errors.New("unclassified candidate input; preserve it")
		}
		if verified[packet] {
			continue
		}
		archive := filepath.Join(root, "candidate-archive")
		read := func(path string, max int64) ([]byte, error) {
			f, err := update.OpenProtected(path, max, 0644)
			if err != nil {
				return nil, err
			}
			b, readErr := io.ReadAll(io.LimitReader(f, max+1))
			return b, errors.Join(readErr, f.Close())
		}
		receiptBytes, err := read(filepath.Join(archive, packet+".receipt.json"), 8192)
		var receipt maintenance.CandidateReceipt
		if err != nil || update.StrictJSON(receiptBytes, &receipt) != nil || receipt.Validate() != nil || receipt.PacketSHA256 != packet {
			return errors.New("unclassified candidate receipt; preserve it")
		}
		journalBytes, err := read(filepath.Join(archive, packet+".journal.json"), 256<<10)
		var journal update.MaintenanceRecord
		if err != nil || update.StrictJSONBounded(journalBytes, &journal, 256<<10) != nil || (receipt.Scope == "fresh" && freshPackageCandidateMatches(a, location, instance, receipt, journal) != nil) || (receipt.Scope == "update" && completedPackageCandidateMatches(a, location, instance, receipt, journal) != nil) {
			return errors.New("unclassified candidate history; preserve it")
		}
		reservation, err := read(filepath.Join(parent, packet+".reservation.json"), 8192)
		expected, _ := candidateDocument(maintenance.CandidateRequest{PacketSHA256: packet, Scope: receipt.Scope})
		info, statErr := os.Lstat(filepath.Join(parent, packet))
		if err != nil || !bytes.Equal(reservation, expected) || statErr != nil || !protectedCandidateDirectory(info) {
			return errors.New("unclassified retained candidate; preserve it")
		}
		verified[packet] = true
	}
	return nil
}

func protectedCandidateDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm() != 0700 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 0 && st.Gid == 0
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
	p, err := preparePackageCandidate(ctx, owner, location, instance, packet)
	if err != nil {
		return empty, err
	}
	identity, identityErr := p.PackageIdentity()
	discardErr := p.DiscardMaintenance()
	return identity, errors.Join(identityErr, discardErr)
}

// The caller must discard this root-private verification copy after consuming
// the independently verified candidate. The retained candidate is unchanged.
func preparePackageCandidate(ctx context.Context, owner int, location, instance, packet string) (*bootstrap.Prepared, error) {
	o, err := packageCandidateOptions(owner, location, instance)
	if err != nil {
		return nil, err
	}
	o.Directory = filepath.Join(filepath.Dir(o.Directory), "candidate-verification")
	if _, err = os.Lstat(o.Directory); err == nil {
		if err = bootstrap.DiscardIncompletePackageMaintenance(owner, location, instance, o); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	admission := bootstrap.CandidateAdmission{PacketSHA256: packet, OperationID: update.NewProbe(), BaselineSHA256: packet, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	return bootstrap.PreparePackageMaintenanceCandidate(ctx, owner, location, instance, o, admission)
}

func matchingPackageCandidate(r maintenance.CandidateReceipt, i bootstrap.PackageMaintenanceIdentity, helper string) bool {
	return r.Validate() == nil && r.ReleaseSHA256 == i.ReleaseSHA256 && r.PacketSHA256 == i.ReleaseSHA256 && r.Sequence == i.Sequence && r.ProductVersion == i.Version && r.EngineSHA256 == i.Core.SHA256 && r.InstallerSHA256 == i.InstallerSHA256 && r.Target == i.Target && (r.Scope != "fresh" || i.InstallerSHA256 == helper)
}

func nativePackageForHost(identity bootstrap.PackageMaintenanceIdentity) (*update.NativePackage, error) {
	p, err := hostplatform.Current()
	if err != nil {
		return nil, err
	}
	var artifact update.Artifact
	switch p.Family {
	case "apt":
		artifact = identity.Deb
	case "dnf":
		artifact = identity.RPM
	default:
		return nil, errors.New("unsupported Linux package family")
	}
	return &update.NativePackage{Family: p.Family, Artifact: artifact, InstallerSHA256: identity.InstallerSHA256}, nil
}

// A protected worker may only acquire the release selected by the original
// manual core operation and the candidate admitted for this exact instance.
func approvedPackageCandidate(view core.ApplicationOwnerView, operation string, installed update.MaintenancePackage, manifest custodyinstall.Manifest, candidate maintenance.CandidateReceipt, now time.Time) (maintenance.CustodyUpdateRequest, error) {
	q, release, manual, err := approvedCustodyOperation(view, operation, now)
	if err != nil || !manual || view.View.Operation.Automatic || release == nil || now.Unix() >= candidate.ExpiresAt ||
		!candidate.MatchesBase(manifest.Owner, installed.Sequence, installed.ReleaseSHA256, manifest.ID, update.SnapshotDigest("candidate-base-manifest:v1", manifest)) ||
		candidate.Sequence != release.Sequence || candidate.ReleaseSHA256 != release.ManifestSHA256 || candidate.EngineSHA256 != release.EngineSHA256 || candidate.ProductVersion != release.Version || candidate.Target != release.Target {
		return maintenance.CustodyUpdateRequest{}, errors.New("the original manual package operation and candidate must match")
	}
	return q, nil
}

// maintenancePackageAcquire is called under the instance lock by the fixed
// worker. It re-verifies the retained candidate before copying either member of
// the pair, then stages the existing durable update journal.
func maintenancePackageAcquire(ctx context.Context, a maintenance.Authority, location, instance, operation string) (result error) {
	installed, manifest, err := packageCandidateBaseline(a.Owner, location, instance, nil)
	if err != nil || installed == nil {
		return errors.Join(err, errors.New("the original installed package is required"))
	}
	view, err := custodyinstall.ReadProtectedPackageApplicationOperation(ctx, a.Owner, location, instance, operation)
	if err != nil {
		return err
	}
	_, expected, manual, err := approvedCustodyOperation(view, operation, time.Now())
	if err != nil || !manual || view.View.Operation.Automatic || expected == nil {
		return errors.New("the original manual application operation is required")
	}
	receipt, err := maintenance.ReadPackageCandidateReceipt(a.Owner, location, instance)
	if errors.Is(err, os.ErrNotExist) || err == nil && packageNeedsAcquisition(receipt, *expected) {
		receipt, err = acquirePublicPackageCandidate(ctx, a, location, instance, *expected)
	}
	if err != nil {
		return errors.Join(err, errors.New("the approved package candidate is unavailable"))
	}
	q, err := approvedPackageCandidate(view, operation, *installed, manifest, receipt, time.Now())
	if err != nil {
		return err
	}
	if compiledFixtureSourceAllowed != nil && manifest.Provenance != signer.ControlledCustodyProfile {
		return errors.New("the real-host fixture cannot update owner-key custody")
	}
	p, err := preparePackageCandidate(ctx, a.Owner, location, instance, receipt.PacketSHA256)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, p.DiscardMaintenance()) }()
	identity, err := p.PackageIdentity()
	if err != nil || !matchingPackageCandidate(receipt, identity, a.ImageSHA256) || identity.Sequence != q.Sequence || identity.ReleaseSHA256 != q.ReleaseSHA256 {
		return errors.New("the independently verified package differs from the original operation")
	}
	dir, err := update.PackageMaintenanceDirectory(a.Owner, location, instance, operation)
	if err != nil {
		return err
	}
	if err = maintenance.EnsureProtectedDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	if err = maintenance.EnsureProtectedDirectory(dir); err != nil {
		return err
	}
	engine, _ := bundle.Names()
	corePath := filepath.Join(p.Report().BundleDirectory, engine)
	f, err := update.OpenProtected(corePath, identity.Core.Bytes, 0700)
	if err != nil {
		return err
	}
	if err = errors.Join(f.Chmod(0755), f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = maintenance.CopyProtectedBounded(corePath, filepath.Join(dir, "fased-app"), 0755, identity.Core.SHA256, identity.Core.Bytes); err != nil {
		return err
	}
	if err = maintenanceAcquireSigner(ctx, p, a, dir, identity.MaintenanceIdentity, true); err != nil {
		return err
	}
	components, err := custodyinstall.DescribeProtectedPackageUpdatePair(ctx, a.Owner, location, instance, operation, identity.Version, identity.Source)
	if err != nil {
		return err
	}
	native, err := nativePackageForHost(identity)
	if err != nil {
		return err
	}
	pack := update.MaintenancePackage{Version: identity.Version, Source: identity.Source, Sequence: identity.Sequence, ReleaseSHA256: identity.ReleaseSHA256, ProvenanceSHA256: identity.ProvenanceSHA256, Components: components, NativePackage: native}
	if err = pack.Validate(); err != nil {
		return err
	}
	return custodyinstall.StageProtectedPackageInstance(ctx, a.Owner, location, instance, operation, pack)
}

// A retained earlier candidate cannot select the next release. The receiver
// still verifies that the previous candidate's exact operation completed
// before it archives that receipt and admits a new one.
func packageNeedsAcquisition(receipt maintenance.CandidateReceipt, expected core.ApplicationRelease) bool {
	return receipt.Scope == "fresh" || receipt.Scope == "update" && receipt.ReleaseSHA256 != expected.ManifestSHA256
}

// A normal Update fetches and retains the independently authenticated public
// package selected by the core operation. A changed latest release is refused.
func acquirePublicPackageCandidate(ctx context.Context, a maintenance.Authority, location, instance string, expected core.ApplicationRelease) (receipt maintenance.CandidateReceipt, result error) {
	o, err := packageCandidateOptions(a.Owner, location, instance)
	if err != nil {
		return receipt, err
	}
	p, err := bootstrap.PreparePackageMaintenanceNative(ctx, a.Owner, location, instance, o)
	if err != nil {
		return receipt, err
	}
	defer func() { result = errors.Join(result, p.DiscardMaintenance()) }()
	actual, _, err := p.PackageApplicationRelease()
	if err != nil || actual == nil || *actual != expected {
		return receipt, errors.New("public release changed after approval; check again")
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		e := p.WriteCandidate(ctx, writer)
		_ = writer.CloseWithError(e)
		done <- e
	}()
	q := maintenance.CandidateRequest{PacketSHA256: expected.ManifestSHA256, Scope: "update"}
	receipt, err = maintenancePackageStageCandidate(ctx, a.Owner, location, instance, q, reader)
	_ = reader.CloseWithError(err)
	writeErr := <-done
	if err != nil {
		return maintenance.CandidateReceipt{}, errors.Join(err, writeErr)
	}
	return receipt, nil
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
	native, err := nativePackageForHost(identity)
	if err != nil {
		return err
	}
	p := update.MaintenancePackage{Version: identity.Version, Source: identity.Source, Sequence: identity.Sequence, ReleaseSHA256: identity.ReleaseSHA256, ProvenanceSHA256: identity.ProvenanceSHA256, Components: components, NativePackage: native}
	return custodyinstall.InitializeProtectedPackageJournal(ctx, owner, location, instance, p)
}

func maintenancePackageProvision(ctx context.Context, owner int, location, instance, listen string) (result error) {
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil || location != "local" || !custodyinstall.NativePackageUpdateAvailable() {
		return errors.New("native package provisioning and recovery are unavailable")
	}
	receipt, err := maintenance.ReadPackageCandidateReceipt(owner, location, instance)
	if err != nil || receipt.Scope != "fresh" || receipt.InstallerSHA256 != a.ImageSHA256 {
		return errors.New("the original fresh package candidate is required")
	}
	p, err := preparePackageCandidate(ctx, owner, location, instance, receipt.PacketSHA256)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, p.DiscardMaintenance()) }()
	identity, err := p.PackageIdentity()
	if err != nil || !matchingPackageCandidate(receipt, identity, a.ImageSHA256) {
		return errors.New("the original fresh package proof differs")
	}
	state, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return err
	}
	dir := filepath.Join(state, "provision")
	if err = maintenance.EnsureProtectedDirectory(dir); err != nil {
		return err
	}
	engine, _ := bundle.Names()
	corePath := filepath.Join(p.Report().BundleDirectory, engine)
	f, err := update.OpenProtected(corePath, identity.Core.Bytes, 0700)
	if err != nil {
		return err
	}
	if err = errors.Join(f.Chmod(0755), f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = maintenance.CopyProtectedBounded(corePath, filepath.Join(dir, "fased-app"), 0755, identity.Core.SHA256, identity.Core.Bytes); err != nil {
		return err
	}
	if err = maintenanceAcquireSigner(ctx, p, a, dir, identity.MaintenanceIdentity, true); err != nil {
		return err
	}
	signerHash, signerSize, err := maintenance.ProtectedDigest(filepath.Join(dir, "fased-signer"), 0755, 64<<20)
	if err != nil {
		return err
	}
	native, err := nativePackageForHost(identity)
	if err != nil {
		return err
	}
	input := custodyinstall.PackageProvisionInput{Version: identity.Version, Source: identity.Source, ReleaseSHA256: identity.ReleaseSHA256, ProvenanceSHA256: identity.ProvenanceSHA256,
		Sequence: identity.Sequence, Listen: listen, Core: update.Artifact{File: "fased-app", Size: identity.Core.Bytes, SHA256: identity.Core.SHA256},
		Signer: update.Artifact{File: "fased-signer", Size: signerSize, SHA256: signerHash}, Native: *native}
	return custodyinstall.ProvisionProtectedPackage(ctx, owner, location, instance, input)
}
