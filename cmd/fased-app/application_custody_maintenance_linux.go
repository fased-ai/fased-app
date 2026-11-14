//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// UI and CLI keep the ordinary Update records and policy. Custody completion
// consumes the root-bound signer receipt, never the app-only checkpoint.
type custodyMaintenanceController struct {
	owner         int
	check         func(context.Context) (*core.ApplicationRelease, error)
	readRecord    func(int) (update.MaintenanceRecord, error)
	readCandidate func(int) (maintenance.CandidateReceipt, error)
	startWorker   func(context.Context, string) error
	workerState   func(context.Context, string) (custodyWorkerState, error)
	pauseStatus   func(context.Context) (update.CorePauseReceipt, error)
	verifyTarget  func(custodyinstall.Manifest) error
}

func (c *custodyMaintenanceController) completionReady(ctx context.Context, r update.MaintenanceRecord, root string) (bool, error) {
	if c.pauseStatus == nil || r.Installation == nil {
		return false, errors.New("original core pause status required")
	}
	actual, err := c.pauseStatus(ctx)
	if err != nil {
		return false, err
	}
	bound := r.Installation.Binding.Index.Binding.Pause
	if !update.SameReleasedPause(r.OperationID, bound, actual) {
		return false, errors.New("original core pause is not released; preserve the fence")
	}
	marker := filepath.Join(root, "update-active.json")
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("foreign custody fence; preserve it")
	}
	fd, err := syscall.Open(marker, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false, errors.New("cannot inspect the original fence; preserve it")
	}
	f := os.NewFile(uintptr(fd), "custody-update-fence")
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return false, errors.New("foreign custody fence; preserve it")
	}
	contents, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || !bytes.Equal(contents, update.MaintenanceFence(r.OperationID)) {
		return false, errors.New("foreign custody fence; preserve it")
	}
	return false, nil
}

type custodyWorkerState struct {
	load, active, job string
}

func (c *custodyMaintenanceController) Check(ctx context.Context) (*core.ApplicationRelease, error) {
	readRecord := c.readRecord
	if readRecord == nil {
		readRecord = update.ReadMaintenanceRecord
	}
	before, err := readRecord(c.owner)
	if err != nil {
		return nil, err
	}
	previous, err := custodyInstalledPackage(before)
	if err != nil {
		return nil, err
	}
	release, err := c.check(ctx)
	if err != nil {
		return nil, err
	}
	r, err := readRecord(c.owner)
	if err != nil {
		return nil, err
	}
	installed, err := custodyInstalledPackage(r)
	if err != nil {
		return nil, err
	}
	if previous.Sequence != installed.Sequence || previous.ReleaseSHA256 != installed.ReleaseSHA256 {
		return nil, errors.New("installed release changed during check; check again")
	}
	if release == nil || release.Sequence < installed.Sequence {
		return nil, nil
	}
	if release.Sequence == installed.Sequence {
		if release.ManifestSHA256 != installed.ReleaseSHA256 {
			return nil, errors.New("release identity change refused")
		}
		return nil, nil
	}
	return release, nil
}

func custodyInstalledPackage(r update.MaintenanceRecord) (*update.MaintenancePackage, error) {
	if r.Phase != "idle" && r.PreviousPackage == nil && (r.Phase != "completed" || r.Start != "target") {
		return nil, errors.New("original installed release is unavailable during recovery")
	}
	return r.InstalledPackage()
}
func (c *custodyMaintenanceController) launch(ctx context.Context, id string) error {
	if !update.IDOK(id) {
		return errors.New("fixed custody update required")
	}
	if c.startWorker != nil {
		return c.startWorker(ctx, id)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "start", "--no-block", "--", "fased-maintenance-"+strconv.Itoa(c.owner)+"@"+id+".service")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Dir = "/"
	return cmd.Run()
}
func (c *custodyMaintenanceController) Apply(ctx context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	if o.Release == nil {
		return core.ApplicationUpdateReceipt{}, errors.New("reviewed custody release required")
	}
	if o.Automatic {
		readCandidate := c.readCandidate
		if readCandidate == nil {
			readCandidate = maintenance.ReadCandidateReceipt
		}
		candidate, err := readCandidate(c.owner)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return core.ApplicationUpdateReceipt{}, fmt.Errorf("%w: candidate status is unavailable", core.ErrApplicationUpdateNotStarted)
		}
		if err == nil && candidate.Scope == "update" && candidate.Sequence == o.Release.Sequence && candidate.ReleaseSHA256 == o.Release.ManifestSHA256 && time.Now().Unix() < candidate.ExpiresAt {
			return core.ApplicationUpdateReceipt{}, fmt.Errorf("%w: candidate requires a manual update", core.ErrApplicationUpdateNotStarted)
		}
	}
	if err := c.launch(ctx, o.ID); err != nil {
		return core.ApplicationUpdateReceipt{}, err
	}
	return core.ApplicationUpdateReceipt{ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, Phase: "applying"}, nil
}

// Recover is called only after an explicit owner recovery command is durably
// recorded. The root worker independently validates the original operation,
// approval, target and signer hold under its existing lock.
func (c *custodyMaintenanceController) Recover(ctx context.Context, o core.ApplicationUpdateOperation) error {
	if o.Release == nil || !update.IDOK(o.ID) || o.Phase != "applying" {
		return errors.New("original custody recovery operation required")
	}
	readRecord := c.readRecord
	if readRecord == nil {
		readRecord = update.ReadMaintenanceRecord
	}
	r, err := readRecord(c.owner)
	if err != nil {
		return err
	}
	proof := core.ApplicationUpdateReceipt{ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, EngineSHA256: o.Release.EngineSHA256, Phase: "applying"}
	proof, done, err := c.custodyRecordStatus(ctx, r, o, proof)
	if err != nil {
		return err
	}
	if done && proof.Phase == "completed" {
		return nil
	}
	inspect := c.workerState
	if inspect == nil {
		inspect = c.inspectWorker
	}
	state, err := inspect(ctx, o.ID)
	if err != nil {
		return err
	}
	if state.load != "loaded" {
		return errors.New("custody recovery worker is unavailable; preserve its state")
	}
	if state.active == "active" || state.active == "activating" || state.active == "reloading" || state.job != "" && state.job != "0" {
		return nil
	}
	if state.active != "failed" && state.active != "inactive" {
		return errors.New("custody worker state is uncertain; preserve it")
	}
	return c.launch(ctx, o.ID)
}

func (c *custodyMaintenanceController) Status(ctx context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	readRecord := c.readRecord
	if readRecord == nil {
		readRecord = update.ReadMaintenanceRecord
	}
	r, err := readRecord(c.owner)
	if err != nil {
		return core.ApplicationUpdateReceipt{}, err
	}
	if o.Release == nil {
		return core.ApplicationUpdateReceipt{}, errors.New("reviewed custody release required")
	}
	proof := core.ApplicationUpdateReceipt{ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, EngineSHA256: o.Release.EngineSHA256, Phase: "applying"}
	proof, done, err := c.custodyRecordStatus(ctx, r, o, proof)
	if err != nil || done {
		return proof, err
	}
	inspect := c.workerState
	if inspect == nil {
		inspect = c.inspectWorker
	}
	state, err := inspect(ctx, o.ID)
	if err != nil {
		return proof, err
	}
	if state.load == "loaded" && (state.active == "active" || state.active == "activating" || state.active == "reloading" || state.job != "" && state.job != "0") {
		return proof, nil
	}
	// The worker can finish between the first journal read and systemd's
	// observation. Re-read before deciding the original operation needs recovery.
	r, err = readRecord(c.owner)
	if err != nil {
		return proof, err
	}
	proof, done, err = c.custodyRecordStatus(ctx, r, o, proof)
	if err != nil || done {
		return proof, err
	}
	proof.Phase = "recovery"
	return proof, nil
}

func (c *custodyMaintenanceController) custodyRecordStatus(ctx context.Context, r update.MaintenanceRecord, o core.ApplicationUpdateOperation, proof core.ApplicationUpdateReceipt) (core.ApplicationUpdateReceipt, bool, error) {
	if r.OperationID != o.ID {
		if r.Phase == "idle" {
			if _, err := custodyinstall.ValidateInitialMaintenanceRecord(r); err != nil {
				return proof, true, err
			}
			if o.Release.Sequence <= r.Package.Sequence {
				return proof, true, errors.New("custody update must advance the installed release")
			}
			return proof, false, nil
		}
		if r.Phase == "refused" && r.Start == "base" {
			installed, err := custodyInstalledPackage(r)
			if err != nil || o.Release.Sequence <= installed.Sequence {
				return proof, true, errors.New("custody update must advance the preserved base")
			}
			// This only observes the new worker. The protected worker still
			// checks the old signer, core pause and fences before staging.
			return proof, false, nil
		}
		if r.Phase != "completed" {
			return proof, true, errors.New("another custody transaction owns recovery")
		}
		return proof, false, nil
	}
	if r.Package == nil || r.Package.Sequence != o.Release.Sequence || r.Package.ReleaseSHA256 != o.Release.ManifestSHA256 {
		return proof, true, errors.New("protected custody target differs from the operation")
	}
	if r.Phase == "completed" {
		if r.Installation == nil || r.Installation.Phase != "completed" || !update.ValidInstallation(*r.Installation) {
			return proof, true, errors.New("actual signer completion required")
		}
		var target custodyinstall.Manifest
		verifyTarget := c.verifyTarget
		if verifyTarget == nil {
			verifyTarget = custodyinstall.Verify
		}
		if update.StrictJSON(r.Target, &target) != nil || target.CoreHash != o.Release.EngineSHA256 || verifyTarget(target) != nil {
			return proof, true, errors.New("installed target differs from the completed custody record")
		}
		ready, err := c.completionReady(ctx, r, target.Root)
		if err != nil || !ready {
			return proof, false, err
		}
		proof.Phase = "completed"
		return proof, true, nil
	}
	if r.Phase == "base-recovery" {
		proof.Phase = "recovery"
		return proof, true, nil
	}
	return proof, false, nil
}

func (c *custodyMaintenanceController) inspectWorker(ctx context.Context, id string) (custodyWorkerState, error) {
	if c.owner <= 0 || !update.IDOK(id) {
		return custodyWorkerState{}, errors.New("fixed custody worker required")
	}
	unit := "fased-maintenance-" + strconv.Itoa(c.owner) + "@" + id + ".service"
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--all", "--property=LoadState,ActiveState,Job", "--", unit)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil || len(out) > 4096 {
		return custodyWorkerState{}, errors.New("cannot inspect custody worker")
	}
	return parseCustodyWorkerState(out)
}

func parseCustodyWorkerState(out []byte) (custodyWorkerState, error) {
	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return custodyWorkerState{}, errors.New("invalid custody worker status")
		}
		if _, duplicate := fields[key]; duplicate {
			return custodyWorkerState{}, errors.New("ambiguous custody worker status")
		}
		fields[key] = value
	}
	_, jobPresent := fields["Job"]
	if fields["LoadState"] == "" || fields["ActiveState"] == "" || !jobPresent {
		return custodyWorkerState{}, errors.New("incomplete custody worker status")
	}
	return custodyWorkerState{load: fields["LoadState"], active: fields["ActiveState"], job: fields["Job"]}, nil
}

func bindCompiledCustodyMaintenance(store *core.Store, owner int, dir string) error {
	a, err := maintenance.ReadAuthority(owner)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = maintenance.VerifyImage(a); err != nil {
		return err
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Phase == "idle" {
		if _, err = custodyinstall.ValidateInitialMaintenanceRecord(r); err != nil {
			return err
		}
	}
	if _, err = r.InstalledPackage(); err != nil {
		return err
	}
	check := compiledApplicationUpdateCheck
	if check == nil && compiledApplicationChannel != nil {
		factory := func(path string) (bootstrap.Options, error) { return channelOptions(compiledApplicationChannel, path) }
		check = custodyChannelCheck(owner, factory, filepath.Join(dir, "update-channel"), false)
	}
	if check == nil {
		return nil
	}
	pauseStatus := func(ctx context.Context) (update.CorePauseReceipt, error) {
		return store.UpdatePause(ctx, update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	}
	return store.BindApplicationUpdateController(&custodyMaintenanceController{owner: owner, check: check, pauseStatus: pauseStatus}, true)
}

// The target can start before its protected transaction is marked complete.
// Read the installed floor at each check, not only when that process starts.
func custodyChannelCheck(owner int, factory channelOptionsFactory, parent string, developmentOnly bool) func(context.Context) (*core.ApplicationRelease, error) {
	return func(ctx context.Context) (*core.ApplicationRelease, error) {
		r, err := update.ReadMaintenanceRecord(owner)
		if err != nil {
			return nil, err
		}
		installed, err := custodyInstalledPackage(r)
		if err != nil {
			return nil, err
		}
		if developmentOnly && !installed.DevelopmentOnly {
			return nil, errors.New("controlled custody release required")
		}
		candidate, candidateErr := maintenance.ReadCandidateReceipt(owner)
		if candidateErr != nil && !errors.Is(candidateErr, os.ErrNotExist) {
			return nil, candidateErr
		}
		if candidateErr == nil && !developmentOnly && candidate.Scope == "update" && candidate.BaseSequence == installed.Sequence && candidate.BaseReleaseSHA256 == installed.ReleaseSHA256 && time.Now().Unix() < candidate.ExpiresAt {
			manifest, err := custodyinstall.Load(owner)
			if err != nil {
				return nil, err
			}
			if release := candidateCustodyRelease(candidate, owner, *installed, manifest, time.Now()); release != nil {
				return release, nil
			}
		}
		return channelCheck(factory, parent, installed.Sequence, installed.ReleaseSHA256)(ctx)
	}
}

func candidateCustodyRelease(candidate maintenance.CandidateReceipt, owner int, installed update.MaintenancePackage, manifest custodyinstall.Manifest, now time.Time) *core.ApplicationRelease {
	if now.Unix() >= candidate.ExpiresAt || !candidate.MatchesBase(owner, installed.Sequence, installed.ReleaseSHA256, manifest.ID, update.SnapshotDigest("candidate-base-manifest:v1", manifest)) {
		return nil
	}
	return &core.ApplicationRelease{EngineSHA256: candidate.EngineSHA256, Version: candidate.ProductVersion, Sequence: candidate.Sequence, ManifestSHA256: candidate.ReleaseSHA256, Target: candidate.Target}
}
