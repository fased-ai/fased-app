//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
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
)

// UI and CLI keep the ordinary Update records and policy. Custody completion
// consumes the root-bound signer receipt, never the app-only checkpoint.
type custodyMaintenanceController struct {
	owner        int
	check        func(context.Context) (*core.ApplicationRelease, error)
	readRecord   func(int) (update.MaintenanceRecord, error)
	startWorker  func(context.Context, string) error
	workerState  func(context.Context, string) (custodyWorkerState, error)
	pauseStatus  func(context.Context) (update.CorePauseReceipt, error)
	verifyTarget func(custodyinstall.Manifest) error
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
	return c.check(ctx)
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
	if r.Package == nil {
		return errors.New("protected custody channel is not initialized")
	}
	check := compiledApplicationUpdateCheck
	if check == nil && compiledApplicationChannel != nil {
		factory := func(path string) (bootstrap.Options, error) { return channelOptions(compiledApplicationChannel, path) }
		check = channelCheck(factory, filepath.Join(dir, "update-channel"), r.Package.Sequence, r.Package.ReleaseSHA256)
	}
	if check == nil {
		return nil
	}
	pauseStatus := func(ctx context.Context) (update.CorePauseReceipt, error) {
		return store.UpdatePause(ctx, update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	}
	return store.BindApplicationUpdateController(&custodyMaintenanceController{owner: owner, check: check, pauseStatus: pauseStatus}, true)
}
