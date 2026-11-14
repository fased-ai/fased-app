//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func maintenanceOwnerArgument(args []string) (int, error) {
	if len(args) < 2 || args[0] != "--owner" {
		return 0, errors.New("fixed maintenance owner required")
	}
	owner, err := strconv.Atoi(args[1])
	if err != nil || owner <= 0 || strconv.Itoa(owner) != args[1] {
		return 0, errors.New("invalid maintenance owner")
	}
	return owner, nil
}

func maintenanceLock(owner int) (*os.File, error) {
	fd, err := syscall.Open(maintenance.OwnerRoot(owner)+"/operation.lock", syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "maintenance-operation")
	info, err := f.Stat()
	if err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || !info.Mode().IsRegular() || info.Mode() != 0600 {
			err = errors.New("foreign maintenance lock; preserve it")
		}
	}
	if err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("maintenance already running or unsafe lock: %w", err)
	}
	return f, nil
}

// The successor is admitted only to the fixed original operation. It takes the
// same root lock as the original worker before inspecting or changing state.
func maintenanceSuccessorCommand(args []string) error {
	owner, err := maintenanceOwnerArgument(args)
	if err != nil || len(args) != 6 || args[2] != "--action" || (args[3] != "prepare" && args[3] != "recover" && args[3] != "worker") || args[4] != "--operation" || !update.IDOK(args[5]) {
		return errors.New("fixed successor action required")
	}
	boot := args[3] == "recover"
	prepare := args[3] == "prepare"
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected successor execution required")
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != strconv.Itoa(owner) && caller != "0" {
		return errors.New("successor caller does not own this installation")
	}
	lock, err := maintenanceLock(owner)
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil || r.OperationID != args[5] {
		return errors.New("successor cannot start another operation")
	}
	if prepare {
		_, err := custodyinstall.PrepareProtectedSuccessor(ctx, owner, args[5])
		return err
	}
	h, err := maintenance.RunningSuccessor(owner)
	if errors.Is(err, maintenance.ErrNoSuccessor) && boot {
		h, err = custodyinstall.PrepareProtectedSuccessor(ctx, owner, args[5])
	}
	if err != nil {
		return err
	}
	if args[5] != h.OperationID {
		return errors.New("successor cannot start another operation")
	}
	return custodyinstall.RunProtectedSuccessor(ctx, owner, h.OperationID, boot, lock)
}

// The successor may inspect the original completed receipt only under the
// existing transaction lock and as the original signer UID.
func maintenanceTransitionStatusCommand(args []string, out io.Writer) error {
	owner, err := maintenanceOwnerArgument(args)
	if err != nil || len(args) != 4 || args[2] != "--operation" || !update.IDOK(args[3]) {
		return errors.New("fixed historical operation required")
	}
	h, err := maintenance.RunningSuccessor(owner)
	if err != nil || h.OperationID != args[3] {
		return errors.New("protected original successor required")
	}
	lock, err := maintenance.OpenSuccessorLock(owner)
	if err != nil {
		return err
	}
	defer lock.Close()
	fenced, err := maintenance.SuccessorFenced(h)
	if err != nil || !fenced {
		return errors.New("exact successor restart fence required")
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil || custodyinstall.VerifyOriginalCompletedSignerRecord(r, h) != nil {
		return errors.New("completed original signer receipt required")
	}
	c, err := signer.MaintenanceConfig(owner)
	if err != nil || os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID || custodyinstall.VerifyOriginalSignerConfig(r, c) != nil {
		return errors.New("original unprivileged signer identity required")
	}
	actual, err := signer.InspectInstallationReadOnly(c, h.OperationID)
	if err != nil || actual == nil || !update.ValidInstallation(*actual) || update.SnapshotDigest("completion", *actual) != update.SnapshotDigest("completion", *r.Installation) {
		return errors.Join(err, errors.New("original completed signer receipt differs"))
	}
	return json.NewEncoder(out).Encode(signer.Response{Installation: actual})
}

func maintenanceCustodyCommand(action string, args []string, out io.Writer) error {
	owner, err := maintenanceOwnerArgument(args)
	if err != nil {
		return err
	}
	if action == "maintenance-gate" {
		if len(args) != 4 || args[2] != "--role" {
			return errors.New("fixed service role required")
		}
		return custodyinstall.VerifyProtectedStart(owner, args[3])
	}
	if action == "maintenance-owner-record" {
		if len(args) != 4 || args[2] != "--operation" || !update.IDOK(args[3]) {
			return errors.New("fixed owner completion required")
		}
		if err = custodyinstall.CompleteProtectedOwnerRecord(owner, args[3]); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Complete bool `json:"complete"`
		}{true})
	}
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var id string
	if action == "maintenance-recover" {
		if len(args) != 2 {
			return errors.New("fixed recovery arguments required")
		}
		r, err := update.ReadMaintenanceRecord(owner)
		if err != nil {
			return err
		}
		id = r.OperationID
		if r.Phase == "idle" {
			return custodyinstall.VerifyInitialProtectedDeployment(owner)
		}
		// Initial installation of the start gate does not enter an update.
		// It is started while the installing process holds the same lock.
		if r.Phase == "prepared" && r.Start == "base" {
			return nil
		}
	} else if action == "maintenance-worker" {
		if len(args) != 4 || args[2] != "--operation" || !update.IDOK(args[3]) {
			return errors.New("fixed update operation required")
		}
		id = args[3]
	} else {
		return errors.New("unsupported custody maintenance entry")
	}
	lock, err := maintenanceLock(owner)
	if err != nil {
		return err
	}
	defer lock.Close()
	if action == "maintenance-worker" {
		r, e := update.ReadMaintenanceRecord(owner)
		if errors.Is(e, os.ErrNotExist) || e == nil && r.OperationID != id {
			v, e := custodyinstall.ReadProtectedApplicationOperation(ctx, owner, id)
			if e != nil {
				return e
			}
			q, _, manual, e := approvedCustodyOperation(v, id, time.Now())
			if e != nil {
				return e
			}
			if err = maintenanceCustodyAcquire(ctx, a, q, manual, &v); err != nil {
				return err
			}
		} else if e != nil {
			return e
		}
	}
	return custodyinstall.RunProtectedUpdate(ctx, owner, id, action == "maintenance-recover")
}

// custodyAcquisition is written only after the existing bootstrap verifier has
// authenticated both exact binaries. Its root-protected receipt lets a retry
// reuse the same bytes instead of following a newer release.
type custodyAcquisition struct {
	Version          int                                  `json:"version"`
	Request          maintenance.CustodyEnrollmentRequest `json:"request"`
	Identity         bootstrap.MaintenanceIdentity        `json:"identity"`
	Sequence         uint64                               `json:"sequence"`
	ReleaseSHA256    string                               `json:"releaseSha256"`
	ProvenanceSHA256 string                               `json:"provenanceSha256,omitempty"`
	DevelopmentOnly  bool                                 `json:"developmentOnly"`
	SignerBytes      int64                                `json:"signerBytes"`
}

func protectedAcquisition(path string, q maintenance.CustodyEnrollmentRequest, development bool) (custodyAcquisition, error) {
	var r custodyAcquisition
	f, err := update.OpenProtected(path, 32<<10, 0644)
	if err != nil {
		return r, err
	}
	b, err := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	err = errors.Join(err, f.Close())
	if err != nil || update.StrictJSON(b, &r) != nil || r.Version != 1 || r.Request != q || r.Identity.Core.SHA256 != q.CoreSHA256 || r.Identity.Core.Bytes <= 0 || r.Identity.Core.Bytes > 64<<20 || r.SignerBytes <= 0 || r.SignerBytes > 64<<20 || r.Sequence == 0 || q.Sequence != 0 && r.Sequence != q.Sequence || len(r.ReleaseSHA256) != 64 || (!r.DevelopmentOnly && len(r.ProvenanceSHA256) != 64) || r.DevelopmentOnly != development {
		return custodyAcquisition{}, errors.New("original verified acquisition differs; preserve it")
	}
	return r, nil
}

func openEnrollmentCore(path string, size int64, open func(string, int64, os.FileMode) (*os.File, error)) (*os.File, error) {
	f, err := open(path, size, 0700)
	if err == nil {
		return f, nil
	}
	// A previous attempt may have synced the executable mode before its
	// protected acquisition receipt was published.
	return open(path, size, 0755)
}

// Initialize only from the protected helper's independently verified release;
// the IPC request cannot choose bytes, paths, an owner or a release floor.
func maintenanceCustodyPrepare(ctx context.Context, a maintenance.Authority, q maintenance.CustodyEnrollmentRequest) (_ custodyinstall.Manifest, resultErr error) {
	var empty custodyinstall.Manifest
	if a.Profile == "local" {
		if q.PublicOrigin != "" {
			return empty, errors.New("Local custody cannot select a public origin")
		}
	} else if a.Profile == "remote" {
		m, err := vpsinstall.LoadProtected(a.Owner)
		if err != nil || q.NativeOwnerAccess || q.Listen != m.Listen || q.PublicOrigin != m.PublicOrigin || q.Sequence != m.ApplicationSequence || q.ReleaseSHA256 != m.ApplicationReleaseSHA256 {
			return empty, errors.New("Remote custody must retain its existing private instance")
		}
		if err = privateaccess.ValidateProtectedRemoteSetup(ctx, a.Owner, a.Home, m.Listen, m.PublicOrigin); err != nil {
			return empty, err
		}
	} else {
		return empty, errors.New("installation profile required")
	}
	if _, err := update.ReadMaintenanceRecord(a.Owner); err == nil {
		return empty, errors.New("existing custody maintenance must be preserved")
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	o, err := maintenanceOptions(a)
	if err != nil {
		return empty, err
	}
	controlled, err := enrollment.ValidateProtectedPrepare(a, q)
	if err != nil {
		return empty, err
	}
	// A fixed protected path bounds retained acquisitions to this operation.
	o.Directory = filepath.Join(filepath.Dir(o.Directory), "custody-"+q.OperationID)
	dir := update.MaintenanceDirectory(a.Owner, q.OperationID)
	provenance := signer.OwnerKeyProvenance
	if controlled {
		provenance = signer.ControlledCustodyProfile
	}
	options := custodyinstall.Options{NativeOwnerAccess: q.NativeOwnerAccess, Core: dir + "/fased-app", Signer: dir + "/fased-signer", Network: q.Network, Listen: q.Listen, Location: a.Profile, PublicOrigin: q.PublicOrigin, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: provenance, PrepareHost: true, Disposable: true, StageOnly: true}
	if err = custodyinstall.ReserveProtectedEnrollment(a.Owner, q.OperationID, q.CoreSHA256, q.SignerSHA256, options, controlled); err != nil {
		return empty, err
	}
	if err = maintenance.EnsureProtectedDirectory(filepath.Dir(dir)); err != nil {
		return empty, err
	}
	if err = maintenance.EnsureProtectedDirectory(dir); err != nil {
		return empty, err
	}
	reservation, _ := json.Marshal(q)
	reservationPath := dir + "/enrollment-acquisition-reservation.json"
	if f, e := update.OpenProtected(reservationPath, 32<<10, 0644); e == nil {
		old, readErr := io.ReadAll(io.LimitReader(f, (32<<10)+1))
		readErr = errors.Join(readErr, f.Close())
		if readErr != nil || string(old) != string(reservation) {
			return empty, errors.New("acquisition reservation differs; preserve it")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		if _, e = os.Lstat(o.Directory); !errors.Is(e, os.ErrNotExist) {
			return empty, errors.New("unclassified acquisition exists; preserve it")
		}
		if err = maintenance.PublishEnrollmentRecord(a.Owner, q.OperationID, "enrollment-acquisition-reservation.json", reservation); err != nil {
			return empty, err
		}
	} else {
		return empty, e
	}

	receiptPath := dir + "/enrollment-acquisition.json"
	acq, err := protectedAcquisition(receiptPath, q, o.Development)
	if errors.Is(err, os.ErrNotExist) {
		var p *bootstrap.Prepared
		var e error
		acquire := func() (*bootstrap.Prepared, error) {
			candidate, selected, e := candidateAcquisition(ctx, a, o, "fresh", q.OperationID, update.SnapshotDigest("candidate-enrollment-request", q), q.Sequence, q.ReleaseSHA256, q.CoreSHA256, true)
			if selected || e != nil {
				return candidate, e
			}
			return bootstrap.PrepareMaintenance(ctx, a.Owner, o)
		}
		if _, e = os.Lstat(o.Directory); errors.Is(e, os.ErrNotExist) {
			p, e = acquire()
		} else if e == nil {
			if _, markerErr := os.Lstat(filepath.Join(o.Directory, "bootstrap.json")); errors.Is(markerErr, os.ErrNotExist) {
				if e = bootstrap.DiscardIncompleteMaintenance(a.Owner, o); e == nil {
					p, e = acquire()
				}
			} else if markerErr != nil {
				e = markerErr
			} else {
				p, e = bootstrap.ResumeMaintenance(ctx, a.Owner, o)
			}
		}
		if e != nil {
			return empty, e
		}
		defer func() { resultErr = errors.Join(resultErr, p.Close()) }()
		report := p.Report()
		if report.Sequence == 0 || !report.ProvenanceVerified && !report.DevelopmentOnly {
			return empty, errors.New("authenticated enrollment release required")
		}
		if q.Sequence != 0 && !p.MatchesMaintenanceRelease(q.Sequence, q.ReleaseSHA256) {
			return empty, errors.Join(errors.New("original installed release is not available from this channel"), p.DiscardMaintenance())
		}
		id, e := p.MaintenanceIdentity()
		if e != nil || id.Core.SHA256 != q.CoreSHA256 {
			if e == nil {
				e = p.DiscardMaintenance()
			}
			return empty, errors.Join(errors.New("verified core differs from the source"), e)
		}
		engine, _ := bundle.Names()
		corePath := filepath.Join(report.BundleDirectory, engine)
		f, e := openEnrollmentCore(corePath, id.Core.Bytes, update.OpenProtected)
		if e != nil {
			return empty, e
		}
		e = errors.Join(f.Chmod(0755), f.Sync(), f.Close())
		if e != nil {
			return empty, e
		}
		// Keep the signer beside the pinned bundle. Adding a directory inside
		// the bundle would make the verifier reject it as an unknown entry.
		signerStage := filepath.Join(o.Directory, "enrollment-signer")
		if e = maintenance.EnsureProtectedDirectory(signerStage); e != nil {
			return empty, e
		}
		if hash, _, e := maintenance.ProtectedDigest(signerStage+"/fased-signer", 0755, 64<<20); e == nil {
			if hash != q.SignerSHA256 {
				return empty, errors.Join(errors.New("verified release signer differs from the owner operation"), p.DiscardMaintenance())
			}
		} else if errors.Is(e, os.ErrNotExist) {
			if e = maintenanceAcquireSigner(ctx, p, a, signerStage, id, true); e != nil {
				return empty, e
			}
		} else {
			return empty, e
		}
		signerHash, signerSize, e := maintenance.ProtectedDigest(signerStage+"/fased-signer", 0755, 64<<20)
		if e != nil || signerHash != q.SignerSHA256 {
			if e == nil {
				e = p.DiscardMaintenance()
			}
			return empty, errors.Join(errors.New("verified signer differs from the source"), e)
		}
		acq = custodyAcquisition{Version: 1, Request: q, Identity: id, Sequence: report.Sequence, ReleaseSHA256: report.DescriptorSHA256, ProvenanceSHA256: report.ProvenanceSHA256, DevelopmentOnly: report.DevelopmentOnly, SignerBytes: signerSize}
		raw, _ := json.Marshal(acq)
		if e = maintenance.PublishEnrollmentRecord(a.Owner, q.OperationID, "enrollment-acquisition.json", raw); e != nil {
			return empty, e
		}
	} else if err != nil {
		return empty, err
	}
	engine, _ := bundle.Names()
	corePath := filepath.Join(o.Directory, "bundle", engine)
	signerPath := filepath.Join(o.Directory, "enrollment-signer", "fased-signer")
	if digest, size, e := maintenance.ProtectedDigest(corePath, 0755, 64<<20); e != nil || digest != q.CoreSHA256 || size != acq.Identity.Core.Bytes {
		return empty, errors.New("original verified core is unavailable; preserve operation")
	}
	if digest, size, e := maintenance.ProtectedDigest(signerPath, 0755, 64<<20); e != nil || digest != q.SignerSHA256 || size != acq.SignerBytes {
		return empty, errors.New("original verified signer is unavailable; preserve operation")
	}
	if err = maintenance.CopyEnrollmentAsset(a.Owner, q.OperationID, "fased-app", q.CoreSHA256, acq.Identity.Core.Bytes); err != nil {
		return empty, err
	}
	if err = maintenance.CopyEnrollmentAsset(a.Owner, q.OperationID, "fased-signer", q.SignerSHA256, acq.SignerBytes); err != nil {
		return empty, err
	}
	m, err := custodyinstall.InstallProtected(ctx, a.Owner, q.OperationID, options, controlled, io.Discard)
	if err != nil {
		return m, err
	}
	components, err := custodyinstall.DescribeProtectedPair(ctx, a.Owner, q.OperationID, acq.Identity.Version)
	if err != nil {
		return m, err
	}
	pack := update.MaintenancePackage{Version: acq.Identity.Version, Source: acq.Identity.Source, Sequence: acq.Sequence, ReleaseSHA256: acq.ReleaseSHA256, ProvenanceSHA256: acq.ProvenanceSHA256, DevelopmentOnly: acq.DevelopmentOnly, Components: components}
	base, _ := json.Marshal(m)
	initial := update.MaintenanceRecord{Version: 1, Owner: a.Owner, OperationID: m.ID, Phase: "idle", Start: "base", Base: base, Target: base, Package: &pack}
	if _, err = custodyinstall.ValidateInitialMaintenanceRecord(initial); err != nil {
		return m, err
	}
	raw, _ := json.Marshal(pack)
	if err = maintenance.PublishEnrollmentRecord(a.Owner, q.OperationID, "enrollment-package.json", raw); err != nil {
		return m, err
	}
	return m, nil
}

func maintenanceCustodyInitialize(ctx context.Context, a maintenance.Authority) error {
	if r, err := update.ReadMaintenanceRecord(a.Owner); err == nil {
		if _, err = custodyinstall.ValidateInitialMaintenanceRecord(r); err != nil {
			return errors.New("existing custody operation must be preserved")
		}
		return custodyinstall.InitializeProtectedJournal(ctx, a.Owner, *r.Package)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	m, err := custodyinstall.Load(a.Owner)
	if err != nil || m.Location != a.Profile || !m.Enrollment || m.Version != 3 {
		return errors.New("committed custody installation required")
	}
	if _, err = custodyinstall.ReadEnrollmentCommit(m); err != nil {
		return err
	}
	dir := update.MaintenanceDirectory(a.Owner, m.ID)
	f, err := update.OpenProtected(dir+"/enrollment-package.json", 256<<10, 0644)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	err = errors.Join(err, f.Close())
	var pack update.MaintenancePackage
	if err != nil || update.StrictJSON(raw, &pack) != nil || pack.Validate() != nil {
		return errors.New("original verified enrollment package is unavailable")
	}
	for _, c := range pack.Components {
		hash, size, e := maintenance.ProtectedDigest(dir+"/"+c.Artifact.File, 0755, 64<<20)
		if e != nil || hash != c.Artifact.SHA256 || size != c.Artifact.Size {
			return errors.New("original verified enrollment bytes changed")
		}
	}
	if err = custodyinstall.InitializeProtectedJournal(ctx, a.Owner, pack); err != nil {
		return err
	}
	return nil
}

// approvedCustodyOperation retains the complete identity selected by the
// owner. The protected acquisition must not substitute another engine,
// version, or target that happens to have the same projected request.
func approvedCustodyOperation(v core.ApplicationOwnerView, id string, now time.Time) (maintenance.CustodyUpdateRequest, *core.ApplicationRelease, bool, error) {
	o := v.View.Operation
	if o.ID != id || o.Phase != "applying" || o.Release == nil || o.PolicySHA256 != update.SnapshotDigest("application-update-policy", v.View.Policy) || o.Automatic && (v.View.Policy.Mode != "automatic" || v.View.Policy.Window != nil && !v.View.Policy.Window.Contains(now)) {
		return maintenance.CustodyUpdateRequest{}, nil, false, errors.New("current owner-approved application operation required")
	}
	r := *o.Release
	return maintenance.CustodyUpdateRequest{OperationID: id, Sequence: r.Sequence, ReleaseSHA256: r.ManifestSHA256}, &r, !o.Automatic, nil
}

type verifiedCustodyAcquisition interface {
	ApplicationRelease() (*core.ApplicationRelease, error)
	BindApplicationRelease(core.ApplicationRelease) error
	Report() bootstrap.Report
	MaintenanceIdentity() (bootstrap.MaintenanceIdentity, error)
}

// The cleanup follows both the original owner operation and the independent
// release verifier. Keeping these checks together prevents a refused request
// from retiring a previous installation's recovery evidence.
func finishCustodyAcquisition(q maintenance.CustodyUpdateRequest, v *core.ApplicationOwnerView, now time.Time, p verifiedCustodyAcquisition, retire func() error) (bootstrap.Report, bootstrap.MaintenanceIdentity, error) {
	var report bootstrap.Report
	var identity bootstrap.MaintenanceIdentity
	var expected *core.ApplicationRelease
	if !q.Bootstrap {
		if v == nil {
			return report, identity, errors.New("current owner operation required")
		}
		approved, release, _, err := approvedCustodyOperation(*v, q.OperationID, now)
		if err != nil || approved != q {
			return report, identity, errors.New("custody request differs from the current owner operation")
		}
		expected = release
	} else if v != nil {
		return report, identity, errors.New("bootstrap cannot replace an owner operation")
	}
	release, err := p.ApplicationRelease()
	if err != nil || release == nil {
		return report, identity, errors.Join(err, errors.New("verified custody release required"))
	}
	if expected != nil {
		release = expected
	} else {
		release.ManifestSHA256 = q.ReleaseSHA256
	}
	if err = p.BindApplicationRelease(*release); err != nil {
		return report, identity, err
	}
	report = p.Report()
	if report.Sequence != q.Sequence || report.DescriptorSHA256 != q.ReleaseSHA256 || !report.ProvenanceVerified && !report.DevelopmentOnly {
		return report, identity, errors.New("independently verified custody target differs")
	}
	identity, err = p.MaintenanceIdentity()
	if err != nil {
		return report, identity, err
	}
	if err = retire(); err != nil {
		return report, identity, err
	}
	return report, identity, nil
}

func maintenanceCustodyAcquire(ctx context.Context, a maintenance.Authority, q maintenance.CustodyUpdateRequest, manualCandidate bool, ownerView *core.ApplicationOwnerView) error {
	if !update.IDOK(q.OperationID) || q.Sequence == 0 || len(q.ReleaseSHA256) != 64 {
		return errors.New("one authenticated custody release required")
	}
	if r, err := update.ReadMaintenanceRecord(a.Owner); err == nil {
		if r.OperationID == q.OperationID {
			if r.Phase == "idle" {
				return errors.New("installation identity cannot be reused as an update")
			}
			if r.Package == nil || r.Package.Sequence != q.Sequence || r.Package.ReleaseSHA256 != q.ReleaseSHA256 || r.Bootstrap != q.Bootstrap {
				return errors.New("pending custody target cannot change")
			}
			return nil
		}
		if r.Phase != "idle" && r.Phase != "completed" && r.Phase != "refused" {
			return errors.New("recover the existing custody transaction first")
		}
		if r.Phase == "idle" || r.PreviousPackage != nil {
			if r.Phase == "idle" {
				if _, err = custodyinstall.ValidateInitialMaintenanceRecord(r); err != nil {
					return err
				}
			}
			installed, err := r.InstalledPackage()
			if err != nil || q.Sequence <= installed.Sequence {
				return errors.New("custody release must advance the authenticated installed release")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The direct helper entry is also used to resume an already journalled
	// update and for the explicit legacy bootstrap above. A new non-bootstrap
	// operation must originate in the same protected core record as the worker.
	if !q.Bootstrap && ownerView == nil {
		v, err := custodyinstall.ReadProtectedApplicationOperation(ctx, a.Owner, q.OperationID)
		if err != nil {
			return err
		}
		ownerView = &v
	}
	now := time.Now()
	var expected *core.ApplicationRelease
	if !q.Bootstrap {
		approved, release, manual, err := approvedCustodyOperation(*ownerView, q.OperationID, now)
		if err != nil || approved != q {
			return errors.New("custody request differs from the current owner operation")
		}
		expected, manualCandidate = release, manual
	}
	o, err := maintenanceOptions(a)
	if err != nil {
		return err
	}
	coreSHA := ""
	if expected != nil {
		coreSHA = expected.EngineSHA256
	}
	p, selected, err := candidateAcquisition(ctx, a, o, "update", q.OperationID, update.SnapshotDigest("candidate-update-request", q), q.Sequence, q.ReleaseSHA256, coreSHA, manualCandidate && !q.Bootstrap)
	if err == nil && !selected {
		p, err = bootstrap.PrepareMaintenance(ctx, a.Owner, o)
	}
	if err != nil {
		return err
	}
	defer p.Close()
	report, id, err := finishCustodyAcquisition(q, ownerView, now, p, func() error {
		// Retire while the initial deployment is still current. Its protected
		// receipts are archived before its public candidate cache is removed.
		if receipt, err := maintenance.ReadCandidateReceipt(a.Owner); err == nil {
			if receipt.Scope == "fresh" {
				return retireFreshCandidate(a, receipt)
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	dir := update.MaintenanceDirectory(a.Owner, q.OperationID)
	if err = maintenance.EnsureProtectedDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	if err = maintenance.EnsureProtectedDirectory(dir); err != nil {
		return err
	}
	engine, _ := bundle.Names()
	corePath := filepath.Join(report.BundleDirectory, engine)
	// The verified extractor deliberately creates private 0700 executables.
	// Normalize only this held, root-owned authenticated acquisition before
	// publishing its independently hashed deployment copy.
	f, e := update.OpenProtected(corePath, id.Core.Bytes, 0700)
	if e != nil {
		return e
	}
	e = errors.Join(f.Chmod(0755), f.Sync(), f.Close())
	if e != nil {
		return e
	}
	if err = maintenance.CopyProtected(corePath, dir+"/fased-app", 0755, id.Core.SHA256, id.Core.Bytes, ""); err != nil {
		return err
	}
	if err = maintenanceAcquireSigner(ctx, p, a, dir, id, false); err != nil {
		return err
	}
	components, err := custodyinstall.DescribeProtectedPair(ctx, a.Owner, q.OperationID, id.Version)
	if err != nil {
		return err
	}
	pack := update.MaintenancePackage{Version: id.Version, Source: id.Source, Sequence: report.Sequence, ReleaseSHA256: report.DescriptorSHA256, ProvenanceSHA256: report.ProvenanceSHA256, DevelopmentOnly: report.DevelopmentOnly, Components: components}
	if err = custodyinstall.StageProtectedPackage(ctx, a.Owner, q.OperationID, pack, q.Bootstrap); err != nil {
		return err
	}
	p.RetainAcquisitionEvidence()
	return nil
}

func selectEmbeddedSigner(id bootstrap.MaintenanceIdentity, development bool, primary, successor *bootstrap.WalletPayload) *bootstrap.WalletPayload {
	if primary != nil && primary.Version == id.Version && primary.Source == id.Source {
		return primary
	}
	if development && successor != nil && successor.Version == id.Version && successor.Source == id.Source {
		return successor
	}
	return nil
}

func maintenanceAcquireSigner(ctx context.Context, p *bootstrap.Prepared, a maintenance.Authority, dir string, id bootstrap.MaintenanceIdentity, enrollment bool) error {
	if id.InstallerSHA256 == a.ImageSHA256 || p.Report().DevelopmentOnly {
		payload := selectEmbeddedSigner(id, p.Report().DevelopmentOnly, walletPayload(), fixtureSuccessorWalletPayload())
		if payload == nil {
			return errors.New("authenticated installer has no matching signer")
		}
		if err := bootstrap.WriteWalletPayload(io.Discard, payload); err != nil {
			return err
		}
		// These bytes are compiled into the already authenticated helper image.
		if enrollment {
			return maintenance.PublishProtectedSigner(dir+"/fased-signer", payload.Data)
		}
		return maintenance.PublishProtected(dir+"/fased-signer", payload.Data, 0755)
	}
	path, artifact, err := p.AcquireMaintenanceInstaller(ctx)
	if err != nil {
		return err
	}
	if err = maintenance.EnsureProtectedDirectory(maintenance.StateRoot + "/images/" + artifact.SHA256); err != nil {
		return err
	}
	image := maintenance.ImagePath(artifact.SHA256)
	if enrollment {
		err = maintenance.CopyProtectedBounded(path, image, 0555, artifact.SHA256, artifact.Size)
	} else {
		err = maintenance.CopyProtected(path, image, 0555, artifact.SHA256, artifact.Size, "")
	}
	if err != nil {
		return err
	}
	// The authenticated successor is executable input, never a root program.
	// A transient locked identity has no access to owner/vault data or network.
	c := exec.CommandContext(ctx, "/usr/bin/systemd-run", "--quiet", "--pipe", "--wait", "--collect", "--property=DynamicUser=yes", "--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateNetwork=yes", "--property=PrivateTmp=yes", "--", image, "--emit-embedded-signer")
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	c.Dir = "/"
	r, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err = c.Start(); err != nil {
		return err
	}
	var tmp *os.File
	if enrollment {
		// The reserved acquisition is root-private and contains no wallet
		// secrets. Retire only its fixed, incomplete extraction before retry;
		// random crash leftovers otherwise accumulate without a bound.
		pending := filepath.Join(dir, ".signer-extraction.pending")
		if i, statErr := os.Lstat(pending); statErr == nil {
			st, ok := i.Sys().(*syscall.Stat_t)
			if !ok || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || !i.Mode().IsRegular() || (i.Mode().Perm() != 0755 && i.Mode().Perm()&^0600 != 0) || i.Size() < 0 || i.Size() > 64<<20 {
				c.Process.Kill()
				c.Wait()
				return errors.New("foreign signer extraction; preserve it")
			}
			if err = os.Remove(pending); err != nil {
				c.Process.Kill()
				c.Wait()
				return err
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			c.Process.Kill()
			c.Wait()
			return statErr
		}
		tmp, err = os.OpenFile(pending, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	} else {
		tmp, err = os.CreateTemp(dir, ".signer-")
	}
	if err != nil {
		c.Process.Kill()
		c.Wait()
		return err
	}
	defer os.Remove(tmp.Name())
	identity, readErr := bootstrap.ReadWalletPayload(r, tmp, id.Version, id.Source)
	if readErr != nil {
		c.Process.Kill()
	}
	err = errors.Join(readErr, c.Wait(), tmp.Chmod(0755), tmp.Sync(), tmp.Close())
	if err != nil {
		return err
	}
	if enrollment {
		return maintenance.CopyProtectedBounded(tmp.Name(), dir+"/fased-signer", 0755, identity.SHA256, identity.Bytes)
	}
	return maintenance.CopyProtected(tmp.Name(), dir+"/fased-signer", 0755, identity.SHA256, identity.Bytes, "")
}
