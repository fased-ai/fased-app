//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// The caller supplies a bounded stream, never a root path. The helper retains
// one candidate and verifies it with its compiled production trust before it
// exposes public metadata or permits the existing custody transaction to use it.
func maintenanceStageCandidate(ctx context.Context, a maintenance.Authority, q maintenance.CandidateRequest, in io.Reader) (maintenance.CandidateReceipt, error) {
	var empty maintenance.CandidateReceipt
	if in == nil {
		return empty, errors.New("candidate stream required")
	}
	if existing, err := maintenance.ReadCandidateReceipt(a.Owner); err == nil {
		if existing.PacketSHA256 == q.PacketSHA256 && existing.Scope == q.Scope {
			if q.Scope == "update" && time.Now().Unix() >= existing.ExpiresAt {
				return renewCandidate(ctx, a, existing)
			}
			if err = verifyRetainedCandidate(ctx, a, existing); err != nil {
				return empty, err
			}
			return existing, nil
		}
		if existing.Scope != "fresh" || q.Scope != "update" {
			return empty, errors.New("another candidate is retained; preserve it")
		}
		if err = retireFreshCandidate(a, existing); err != nil {
			return empty, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if _, err := maintenance.ReadCandidateOperation(a.Owner); !errors.Is(err, os.ErrNotExist) {
		return empty, errors.New("an earlier candidate operation must be preserved")
	}
	var base *update.MaintenancePackage
	var baseManifest custodyinstall.Manifest
	r, err := update.ReadMaintenanceRecord(a.Owner)
	if q.Scope == "fresh" {
		if a.Profile != "local" || !errors.Is(err, os.ErrNotExist) {
			return empty, errors.New("fresh candidate requires a new Local custody installation")
		}
		if _, err = os.Lstat(custodyinstall.Root(a.Owner)); !errors.Is(err, os.ErrNotExist) {
			return empty, errors.New("existing custody state must be preserved")
		}
	} else {
		if err != nil || r.Phase != "idle" && r.Phase != "completed" {
			return empty, errors.New("recover the current custody operation before staging an update")
		}
		base, err = r.InstalledPackage()
		if err != nil {
			return empty, err
		}
		baseManifest, err = custodyinstall.Load(a.Owner)
		if err != nil || baseManifest.Location != a.Profile {
			return empty, errors.New("installed custody identity differs; preserve it")
		}
	}
	parent := filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate")
	if err = maintenance.EnsureProtectedDirectory(parent); err != nil {
		return empty, err
	}
	if err = clearOtherUncommittedCandidate(a.Owner, parent, q); err != nil {
		return empty, err
	}
	reservationName := q.PacketSHA256 + ".reservation.json"
	directory := filepath.Join(parent, q.PacketSHA256)
	reservationPath := filepath.Join(parent, reservationName)
	_, dirErr := os.Lstat(directory)
	_, reservationErr := os.Lstat(reservationPath)
	if dirErr == nil && errors.Is(reservationErr, os.ErrNotExist) {
		return empty, errors.New("unclassified candidate directory; preserve it")
	}
	if dirErr != nil && !errors.Is(dirErr, os.ErrNotExist) || reservationErr != nil && !errors.Is(reservationErr, os.ErrNotExist) {
		return empty, errors.New("candidate reservation cannot be inspected")
	}
	reservation, _ := json.Marshal(q)
	if err = maintenance.PublishProtected(reservationPath, append(reservation, '\n'), 0644); err != nil {
		return empty, err
	}
	if dirErr == nil {
		// This reserved directory contains only uncommitted public candidate
		// bytes. No receipt exists, so no update can refer to it yet.
		if err = removeCandidateData(parent, q.PacketSHA256); err != nil {
			return empty, err
		}
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return empty, err
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return empty, err
	}
	if err = bootstrap.ReceiveCandidate(ctx, in, directory, q.PacketSHA256); err != nil {
		return empty, err
	}
	for _, path := range []string{directory, parent} {
		d, e := os.Open(path)
		if e != nil {
			return empty, e
		}
		if e = errors.Join(d.Sync(), d.Close()); e != nil {
			return empty, e
		}
	}
	release, identity, err := verifyProtectedCandidate(ctx, a, q.PacketSHA256)
	if err != nil {
		return empty, err
	}
	if q.Scope == "fresh" && identity.InstallerSHA256 != a.ImageSHA256 {
		return empty, errors.New("fresh candidate differs from the protected installer")
	}
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: a.Owner, Scope: q.Scope, PacketSHA256: q.PacketSHA256, InstallerSHA256: identity.InstallerSHA256, EngineSHA256: release.EngineSHA256, ProductVersion: release.Version, Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256, Target: release.Target}
	if base != nil {
		receipt.BaseSequence, receipt.BaseReleaseSHA256 = base.Sequence, base.ReleaseSHA256
		receipt.BaseInstallation = baseManifest.ID
		receipt.BaseManifestSHA = update.SnapshotDigest("candidate-base-manifest:v1", baseManifest)
		receipt.ExpiresAt = time.Now().Add(15 * time.Minute).Unix()
	}
	if err = receipt.Validate(); err != nil {
		return empty, err
	}
	body, _ := json.Marshal(receipt)
	if err = maintenance.PublishProtected(filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate.json"), append(body, '\n'), 0644); err != nil {
		return empty, err
	}
	return receipt, nil
}

// Before a receipt exists no update can consume this root-owned public input.
// A classified interrupted attempt can therefore be replaced, while any
// operation record or unknown file is retained for recovery.
func clearOtherUncommittedCandidate(owner int, parent string, requested maintenance.CandidateRequest) error {
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) == 0 {
		return err
	}
	var reservation string
	for _, entry := range entries {
		if len(entry.Name()) == 64+len(".reservation.json") && filepath.Ext(entry.Name()) == ".json" {
			if reservation != "" {
				return errors.New("ambiguous candidate inputs; preserve them")
			}
			reservation = entry.Name()
		}
	}
	if reservation == "" {
		return errors.New("unclassified candidate inputs; preserve them")
	}
	f, err := update.OpenProtected(filepath.Join(parent, reservation), 8192, 0644)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	err = errors.Join(err, f.Close())
	var old maintenance.CandidateRequest
	if err != nil || update.StrictJSON(b, &old) != nil || reservation != old.PacketSHA256+".reservation.json" {
		return errors.New("candidate reservation is invalid; preserve it")
	}
	requestBytes, _ := json.Marshal(maintenance.Request{Version: 1, Action: "candidate-stage", Candidate: &old})
	if _, err = maintenance.DecodeRequest(requestBytes); err != nil {
		return errors.New("candidate reservation is invalid; preserve it")
	}
	for _, entry := range entries {
		if entry.Name() != reservation && entry.Name() != old.PacketSHA256 {
			return errors.New("unclassified candidate inputs; preserve them")
		}
	}
	if old == requested {
		return nil
	}
	if _, err = maintenance.ReadCandidateOperation(owner); !errors.Is(err, os.ErrNotExist) {
		return errors.New("an earlier candidate operation must be preserved")
	}
	if _, err = os.Lstat(filepath.Join(parent, old.PacketSHA256)); err == nil {
		if err = removeCandidateData(parent, old.PacketSHA256); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, hash := candidateDocument(old)
	return maintenance.RemoveProtected(filepath.Join(parent, reservation), hash, 0644)
}

func verifyProtectedCandidate(ctx context.Context, a maintenance.Authority, packet string) (*core.ApplicationRelease, bootstrap.MaintenanceIdentity, error) {
	var empty bootstrap.MaintenanceIdentity
	o, err := maintenanceOptions(a)
	if err != nil {
		return nil, empty, err
	}
	o.Directory = filepath.Join(filepath.Dir(o.Directory), "candidate-verification")
	if _, err = os.Lstat(o.Directory); err == nil {
		if err = bootstrap.DiscardIncompleteMaintenance(a.Owner, o); err != nil {
			return nil, empty, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, empty, err
	}
	admission := bootstrap.CandidateAdmission{PacketSHA256: packet, OperationID: update.NewProbe(), BaselineSHA256: packet, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	p, err := bootstrap.PrepareMaintenanceCandidate(ctx, a.Owner, o, admission)
	if err != nil {
		return nil, empty, err
	}
	release, releaseErr := p.ApplicationRelease()
	identity, identityErr := p.MaintenanceIdentity()
	discardErr := p.DiscardMaintenance()
	if err = errors.Join(releaseErr, identityErr, discardErr); err != nil || release == nil {
		return nil, empty, errors.Join(err, errors.New("candidate verification did not complete"))
	}
	return release, identity, nil
}

func renewCandidate(ctx context.Context, a maintenance.Authority, old maintenance.CandidateReceipt) (maintenance.CandidateReceipt, error) {
	var empty maintenance.CandidateReceipt
	if err := verifyRetainedCandidate(ctx, a, old); err != nil {
		return empty, err
	}
	_, previous := candidateDocument(old)
	renewed := old
	renewed.ExpiresAt = time.Now().Add(15 * time.Minute).Unix()
	body, _ := candidateDocument(renewed)
	if err := maintenance.ReplaceProtected(filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate.json"), body, 0644, previous); err != nil {
		return empty, err
	}
	return renewed, nil
}

func verifyRetainedCandidate(ctx context.Context, a maintenance.Authority, old maintenance.CandidateReceipt) error {
	if old.Scope == "fresh" && old.InstallerSHA256 != a.ImageSHA256 {
		return errors.New("fresh candidate helper changed; preserve it")
	}
	if old.Scope == "update" {
		if _, err := maintenance.ReadCandidateOperation(a.Owner); !errors.Is(err, os.ErrNotExist) {
			return errors.New("recover the original candidate operation before staging again")
		}
		r, err := update.ReadMaintenanceRecord(a.Owner)
		if err != nil || r.Phase != "idle" && r.Phase != "completed" {
			return errors.New("recover the current custody operation before staging again")
		}
		base, err := r.InstalledPackage()
		if err != nil {
			return err
		}
		manifest, err := custodyinstall.Load(a.Owner)
		if err != nil || !old.MatchesBase(a.Owner, base.Sequence, base.ReleaseSHA256, manifest.ID, update.SnapshotDigest("candidate-base-manifest:v1", manifest)) {
			return errors.New("candidate predecessor changed; preserve it")
		}
	}
	release, identity, err := verifyProtectedCandidate(ctx, a, old.PacketSHA256)
	if err != nil || release.Version != old.ProductVersion || release.Sequence != old.Sequence || release.ManifestSHA256 != old.ReleaseSHA256 || release.EngineSHA256 != old.EngineSHA256 || release.Target != old.Target || identity.InstallerSHA256 != old.InstallerSHA256 {
		return errors.Join(err, errors.New("retained candidate differs; preserve it"))
	}
	return nil
}

func removeCandidateData(parent, name string) error {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	i, err := root.Lstat(name)
	if err != nil {
		return err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || !i.IsDir() || i.Mode().Perm()&^0700 != 0 {
		return errors.New("foreign candidate staging directory; preserve it")
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	held, err := dir.Stat(".")
	if err != nil || !os.SameFile(i, held) {
		dir.Close()
		return errors.New("candidate staging directory changed; preserve it")
	}
	// A crash after mkdir under a restrictive umask may precede chmod.
	if i.Mode().Perm() != 0700 {
		if err = dir.Chmod(".", 0700); err != nil {
			dir.Close()
			return err
		}
	}
	if err = dir.Close(); err != nil {
		return err
	}
	if err = root.RemoveAll(name); err != nil {
		return err
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// Retire only the public acceptance cache of a completely installed fresh
// pair. The original enrollment, keys, journal and archived receipts remain.
func retireFreshCandidate(a maintenance.Authority, receipt maintenance.CandidateReceipt) error {
	r, err := update.ReadMaintenanceRecord(a.Owner)
	if err != nil {
		return err
	}
	m, err := custodyinstall.ValidateInitialMaintenanceRecord(r)
	if err != nil || m.Owner != a.Owner || m.Location != a.Profile || m.CoreHash != receipt.EngineSHA256 || r.Package.Sequence != receipt.Sequence || r.Package.ReleaseSHA256 != receipt.ReleaseSHA256 || a.ImageSHA256 != receipt.InstallerSHA256 {
		return errors.New("fresh candidate installation is not complete; preserve it")
	}
	if err = custodyinstall.VerifyInitialProtectedDeployment(a.Owner); err != nil {
		return err
	}
	dir := update.MaintenanceDirectory(a.Owner, m.ID)
	operation, err := maintenance.ReadCandidateOperation(a.Owner)
	if errors.Is(err, os.ErrNotExist) {
		f, e := update.OpenProtected(filepath.Join(dir, "candidate-operation.json"), 8192, 0644)
		if e != nil {
			return errors.New("original candidate operation is unavailable; preserve it")
		}
		b, e := io.ReadAll(io.LimitReader(f, 8193))
		e = errors.Join(e, f.Close())
		if e != nil || update.StrictJSON(b, &operation) != nil {
			return errors.New("archived candidate operation is invalid")
		}
	} else if err != nil {
		return err
	}
	if operation.Validate() != nil || operation.Owner != a.Owner || operation.Scope != "fresh" || operation.PacketSHA256 != receipt.PacketSHA256 || operation.OperationID != m.ID {
		return errors.New("original candidate operation differs; preserve it")
	}
	receiptBytes, receiptHash := candidateDocument(receipt)
	operationBytes, operationHash := candidateDocument(operation)
	for _, item := range []struct {
		name string
		data []byte
	}{{"candidate-receipt.json", receiptBytes}, {"candidate-operation.json", operationBytes}} {
		if err = maintenance.PublishProtected(filepath.Join(dir, item.name), item.data, 0644); err != nil {
			return err
		}
	}
	parent := filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate")
	if _, err = os.Lstat(filepath.Join(parent, receipt.PacketSHA256)); err == nil {
		if err = removeCandidateData(parent, receipt.PacketSHA256); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, reservationHash := candidateDocument(maintenance.CandidateRequest{PacketSHA256: receipt.PacketSHA256, Scope: "fresh"})
	for _, item := range []struct{ path, hash string }{
		{filepath.Join(parent, receipt.PacketSHA256+".reservation.json"), reservationHash},
		{filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate-operation.json"), operationHash},
		{filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate.json"), receiptHash},
	} {
		if err = maintenance.RemoveProtected(item.path, item.hash, 0644); err != nil {
			return err
		}
	}
	return nil
}

func candidateDocument(value any) ([]byte, string) {
	b, _ := json.Marshal(value)
	b = append(b, '\n')
	h := sha256.Sum256(b)
	return b, hex.EncodeToString(h[:])
}

// candidateAcquisition pins the matching candidate to the original request;
// the direct bootstrap caller also reaches it without a core operation. A
// matching retry may finish after admission expires.
func candidateAcquisition(ctx context.Context, a maintenance.Authority, o bootstrap.Options, scope, operation, requestSHA string, sequence uint64, releaseSHA, coreSHA string, manual bool) (*bootstrap.Prepared, bool, error) {
	bound, boundErr := maintenance.ReadCandidateOperation(a.Owner)
	if boundErr != nil && !errors.Is(boundErr, os.ErrNotExist) {
		return nil, true, boundErr
	}
	var previous *maintenance.CandidateOperation
	if boundErr == nil {
		previous = &bound
	}
	receipt, err := maintenance.ReadCandidateReceipt(a.Owner)
	if errors.Is(err, os.ErrNotExist) {
		if err := checkCandidateOriginal(nil, previous, scope, operation, requestSHA, sequence, releaseSHA, coreSHA); err != nil {
			return nil, true, err
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	err = checkCandidateOriginal(&receipt, previous, scope, operation, requestSHA, sequence, releaseSHA, coreSHA)
	if err != nil {
		return nil, true, err
	}
	selected, err := candidateSelectsRelease(receipt, scope, sequence, releaseSHA, coreSHA, time.Now(), func() error {
		return boundErr
	})
	if err != nil || !selected {
		return nil, selected, err
	}
	if scope == "update" && !manual {
		return nil, true, errors.New("candidate admission requires a manual owner update")
	}
	if scope == "update" {
		r, err := update.ReadMaintenanceRecord(a.Owner)
		if err != nil {
			return nil, true, err
		}
		base, err := r.InstalledPackage()
		if err != nil {
			return nil, true, errors.New("candidate predecessor changed; preserve the operation")
		}
		manifest, err := custodyinstall.Load(a.Owner)
		if err != nil || !receipt.MatchesBase(a.Owner, base.Sequence, base.ReleaseSHA256, manifest.ID, update.SnapshotDigest("candidate-base-manifest:v1", manifest)) {
			return nil, true, errors.New("candidate installation identity changed; preserve the operation")
		}
	}
	want := maintenance.CandidateOperation{Version: 1, Owner: a.Owner, Scope: scope, PacketSHA256: receipt.PacketSHA256, OperationID: operation, RequestSHA256: requestSHA}
	bind, err := candidateBindingRequired(receipt, want, previous, time.Now())
	if err != nil {
		return nil, true, err
	}
	if bind {
		body, _ := json.Marshal(want)
		if err = maintenance.PublishProtected(filepath.Join(maintenance.OwnerRoot(a.Owner), "candidate-operation.json"), append(body, '\n'), 0644); err != nil {
			return nil, true, err
		}
	}
	// Each protected attempt is named by the original operation. An incomplete
	// acquisition is disposable only after the original binding was checked.
	if scope == "update" {
		o.Directory = filepath.Join(filepath.Dir(o.Directory), "candidate-"+operation)
		if _, err = os.Lstat(o.Directory); err == nil {
			if err = bootstrap.DiscardIncompleteMaintenance(a.Owner, o); err != nil {
				return nil, true, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, true, err
		}
	}
	baseline := receipt.BaseReleaseSHA256
	if scope == "fresh" {
		baseline = receipt.PacketSHA256
	}
	admission := bootstrap.CandidateAdmission{PacketSHA256: receipt.PacketSHA256, OperationID: operation, BaselineSHA256: baseline, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	p, err := bootstrap.PrepareMaintenanceCandidate(ctx, a.Owner, o, admission)
	return p, true, err
}

// A bound operation must not silently fall back to another release when its
// receipt or request no longer matches. Other operation IDs still require
// pending-binding classification before they can safely be admitted.
func checkCandidateOriginal(receipt *maintenance.CandidateReceipt, bound *maintenance.CandidateOperation, scope, operation, requestSHA string, sequence uint64, releaseSHA, coreSHA string) error {
	if receipt == nil {
		if bound != nil {
			return errors.New("original candidate receipt is unavailable; preserve the operation")
		}
		return nil
	}
	if bound == nil {
		return nil
	}
	if bound.OperationID != operation {
		return nil
	}
	if bound.Validate() != nil || receipt.Validate() != nil || bound.Owner != receipt.Owner || bound.Scope != receipt.Scope || bound.PacketSHA256 != receipt.PacketSHA256 || bound.Scope != scope || bound.RequestSHA256 != requestSHA || receipt.Sequence != sequence || receipt.ReleaseSHA256 != releaseSHA || coreSHA != "" && receipt.EngineSHA256 != coreSHA {
		return errors.New("candidate request differs from the original operation")
	}
	return nil
}

func candidateSelectsRelease(receipt maintenance.CandidateReceipt, scope string, sequence uint64, releaseSHA, coreSHA string, now time.Time, readOperation func() error) (bool, error) {
	if receipt.Scope != scope || receipt.Sequence != sequence {
		return false, nil
	}
	if scope == "update" && now.Unix() >= receipt.ExpiresAt {
		if err := readOperation(); errors.Is(err, os.ErrNotExist) {
			// An unused expired candidate must not shadow a later public release.
			return false, nil
		} else if err != nil {
			return true, err
		}
	}
	if receipt.ReleaseSHA256 != releaseSHA || coreSHA != "" && receipt.EngineSHA256 != coreSHA {
		return true, errors.New("candidate differs from the original operation")
	}
	return true, nil
}

func candidateBindingRequired(receipt maintenance.CandidateReceipt, want maintenance.CandidateOperation, bound *maintenance.CandidateOperation, now time.Time) (bool, error) {
	if receipt.Validate() != nil || want.Validate() != nil || want.Owner != receipt.Owner || want.Scope != receipt.Scope || want.PacketSHA256 != receipt.PacketSHA256 {
		return false, errors.New("candidate operation differs from the protected admission")
	}
	if bound != nil {
		if bound.Validate() != nil || *bound != want {
			return false, errors.New("another candidate operation is retained; preserve it")
		}
		return false, nil
	}
	if receipt.Scope == "update" && now.Unix() >= receipt.ExpiresAt {
		return false, errors.New("candidate admission expired before the update began")
	}
	return true, nil
}
