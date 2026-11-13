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
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
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
			o := v.View.Operation
			if o.ID != id || o.Phase != "applying" || o.Release == nil || o.PolicySHA256 != update.SnapshotDigest("application-update-policy", v.View.Policy) || o.Automatic && (v.View.Policy.Mode != "automatic" || v.View.Policy.Window != nil && !v.View.Policy.Window.Contains(time.Now())) {
				return errors.New("current owner-approved application operation required")
			}
			q := maintenance.CustodyUpdateRequest{OperationID: id, Sequence: o.Release.Sequence, ReleaseSHA256: o.Release.ManifestSHA256}
			if err = maintenanceCustodyAcquire(ctx, a, q); err != nil {
				return err
			}
		} else if e != nil {
			return e
		}
	}
	return custodyinstall.RunProtectedUpdate(ctx, owner, id, action == "maintenance-recover")
}

func maintenanceCustodyAcquire(ctx context.Context, a maintenance.Authority, q maintenance.CustodyUpdateRequest) error {
	if !update.IDOK(q.OperationID) || q.Sequence == 0 || len(q.ReleaseSHA256) != 64 {
		return errors.New("one authenticated custody release required")
	}
	if r, err := update.ReadMaintenanceRecord(a.Owner); err == nil {
		if r.OperationID == q.OperationID {
			if r.Package == nil || r.Package.Sequence != q.Sequence || r.Package.ReleaseSHA256 != q.ReleaseSHA256 || r.Bootstrap != q.Bootstrap {
				return errors.New("pending custody target cannot change")
			}
			return nil
		}
		if r.Phase != "completed" && r.Phase != "refused" {
			return errors.New("recover the existing custody transaction first")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	o, err := maintenanceOptions(a)
	if err != nil {
		return err
	}
	p, err := bootstrap.PrepareMaintenance(ctx, a.Owner, o)
	if err != nil {
		return err
	}
	defer p.Close()
	release, err := p.ApplicationRelease()
	if err != nil {
		return err
	}
	release.ManifestSHA256 = q.ReleaseSHA256
	if err = p.BindApplicationRelease(*release); err != nil {
		return err
	}
	report := p.Report()
	if report.Sequence != q.Sequence || report.DescriptorSHA256 != q.ReleaseSHA256 || !report.ProvenanceVerified && !report.DevelopmentOnly {
		return errors.New("independently verified custody target differs")
	}
	id, err := p.MaintenanceIdentity()
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
	if err = maintenanceAcquireSigner(ctx, p, a, dir, id); err != nil {
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

func maintenanceAcquireSigner(ctx context.Context, p *bootstrap.Prepared, a maintenance.Authority, dir string, id bootstrap.MaintenanceIdentity) error {
	if id.InstallerSHA256 == a.ImageSHA256 || p.Report().DevelopmentOnly {
		payload := walletPayload()
		if payload == nil || payload.Version != id.Version || payload.Source != id.Source {
			return errors.New("authenticated installer has no matching signer")
		}
		if err := bootstrap.WriteWalletPayload(io.Discard, payload); err != nil {
			return err
		}
		// These bytes are compiled into the already authenticated helper image.
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
	if err = maintenance.CopyProtected(path, image, 0555, artifact.SHA256, artifact.Size, ""); err != nil {
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
	tmp, err := os.CreateTemp(dir, ".signer-")
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
	return maintenance.CopyProtected(tmp.Name(), dir+"/fased-signer", 0755, identity.SHA256, identity.Bytes, "")
}
