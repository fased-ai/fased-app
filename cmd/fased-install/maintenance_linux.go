//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/hostprep"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/update"
)

func maintenanceRegisterCommand(args []string, out io.Writer) error {
	if (len(args) != 4 && len(args) != 5 && len(args) != 6) || args[0] != "--owner" || args[2] != "--profile" || len(args) == 5 && args[4] != "--resume" || len(args) == 6 && (args[4] != "--operation" || !update.IDOK(args[5])) {
		return errors.New("fixed maintenance registration arguments required")
	}
	owner, err := strconv.Atoi(args[1])
	if err != nil || strconv.Itoa(owner) != args[1] {
		return errors.New("invalid maintenance owner")
	}
	a, err := maintenance.Register(owner, args[3])
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if len(args) > 4 {
		if self == maintenance.ImagePath(a.ImageSHA256) {
			if len(args) == 6 {
				return errors.New("a different protected successor is required")
			}
			_, err = fmt.Fprintln(out, self)
			return err
		}
		lock, err := maintenanceLock(owner)
		if err != nil {
			return err
		}
		defer lock.Close()
		r, recordErr := update.ReadMaintenanceRecord(owner)
		if len(args) == 5 && errors.Is(recordErr, os.ErrNotExist) {
			_, err = fmt.Fprintln(out, maintenance.ImagePath(a.ImageSHA256))
			return err
		}
		if len(args) == 5 && recordErr == nil && r.Phase == "completed" {
			// A retry must report the same completed recovery, not create a
			// second operation. An absent handoff retains the ordinary entry.
			_, successorErr := maintenance.RunningSuccessor(owner)
			if successorErr == nil {
				_, err = fmt.Fprintln(out, self)
				return err
			}
			if errors.Is(successorErr, maintenance.ErrNoSuccessor) {
				_, err = fmt.Fprintln(out, maintenance.ImagePath(a.ImageSHA256))
				return err
			}
			return successorErr
		}
		if recordErr != nil || len(args) == 6 && r.OperationID != args[5] || r.Phase == "completed" {
			return errors.New("original held successor operation required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		h, prepareErr := custodyinstall.PrepareProtectedSuccessor(ctx, owner, r.OperationID)
		cancel()
		if prepareErr != nil {
			return prepareErr
		}
		_, err = fmt.Fprintln(out, maintenance.ImagePath(h.SuccessorImage))
		return err
	}
	_, err = fmt.Fprintln(out, maintenance.ImagePath(a.ImageSHA256))
	return err
}

func maintenanceBootstrapCommand(args []string, out io.Writer) error {
	if len(args) != 2 || args[0] != "--owner" {
		return errors.New("fixed maintenance bootstrap owner required")
	}
	owner, err := strconv.Atoi(args[1])
	if err != nil || owner <= 0 || strconv.Itoa(owner) != args[1] {
		return errors.New("invalid maintenance owner")
	}
	if h, e := maintenance.RunningSuccessor(owner); e == nil {
		a, authorityErr := maintenance.ReadAuthority(owner)
		fenced, fenceErr := maintenance.SuccessorFenced(h)
		if authorityErr != nil || fenceErr != nil {
			return errors.New("successor authority or transition fence is unavailable")
		}
		if a.ImageSHA256 == h.PreviousImage || fenced {
			return maintenanceSuccessorCommand([]string{"--owner", strconv.Itoa(owner), "--action", "worker", "--operation", h.OperationID})
		}
		if a.ImageSHA256 != h.SuccessorImage {
			return errors.New("successor authority changed")
		}
	} else if !errors.Is(e, maintenance.ErrNoSuccessor) {
		// A missing dependency of an existing successor is damaged state,
		// not permission to fall back to ordinary setup.
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return hostprep.PrepareProtected(ctx, owner, out)
}

func maintenanceRequestCommand(args []string, in io.Reader, out io.Writer) error {
	if len(args) != 2 || args[0] != "--owner" {
		return errors.New("fixed installed maintenance owner required")
	}
	owner, err := strconv.Atoi(args[1])
	if err != nil || owner <= 0 || strconv.Itoa(owner) != args[1] {
		return errors.New("invalid maintenance owner")
	}
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, err := readMaintenanceInput(ctx, in)
	if err != nil {
		return err
	}
	r, err := maintenance.DecodeRequest(data)
	if err != nil {
		return err
	}
	// The directory is protected by ValidateProcess. One OS lock serializes
	// mutation across callers; no owner-supplied command or pathname is run.
	lock, err := maintenanceLock(owner)
	if err != nil {
		return err
	}
	defer lock.Close()
	// The helper may have been replaced while the caller supplied input.
	// Recheck the executing image and authority at the mutation boundary.
	a, err = maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	if a.Profile != "remote" && r.Action != "custody-update" && r.Action != "custody-initialize" {
		return errors.New("Remote setup authority required")
	}
	workCtx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stop()
	var payload json.RawMessage
	switch r.Action {
	case "custody-initialize":
		err = maintenanceCustodyInitialize(workCtx, a)
	case "custody-update":
		err = maintenanceCustodyAcquire(workCtx, a, *r.Custody)
		if err == nil {
			err = custodyinstall.ReconcileProtectedSetup(workCtx, a.Owner)
		}
		if err == nil {
			err = custodyinstall.RunProtectedUpdate(workCtx, a.Owner, r.Custody.OperationID, false)
		}
	case "serve":
		err = privateaccess.ApplyOwnedServe(workCtx, a.Owner, a.Home, r.Serve.Configuration, r.Serve.ETag)
	case "remote-install":
		err = maintenanceRemoteInstall(workCtx, a, *r.Remote)
	default:
		payload, err = vpsinstall.RunProtected(workCtx, a.Owner, r.Action, r.Peer, io.Discard)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true, Payload: payload})
}

// Only build composition can supply a controlled transport. Runtime requests
// cannot select trust, a download host, a package path or a fixture mode.
var compiledMaintenanceOptions func(string) (bootstrap.Options, error)

func maintenanceOptions(a maintenance.Authority) (bootstrap.Options, error) {
	if compiledMaintenanceOptions != nil {
		return compiledMaintenanceOptions(maintenance.OwnerRoot(a.Owner))
	}
	o, err := retailInstallerOptions(compiledFirstInstallBinding, maintenance.OwnerRoot(a.Owner))
	if err != nil {
		return o, err
	}
	var seed struct {
		DevelopmentOnly bool `json:"developmentOnly"`
	}
	if json.Unmarshal(compiledFirstInstallBinding.Seed, &seed) != nil {
		return o, errors.New("compiled maintenance trust invalid")
	}
	o.Development = seed.DevelopmentOnly
	return o, nil
}

func maintenanceRemoteInstall(ctx context.Context, a maintenance.Authority, r maintenance.RemoteInstallRequest) error {
	if err := privateaccess.ValidateProtectedRemoteSetup(ctx, a.Owner, a.Home, r.Listen, r.Origin); err != nil {
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
	report := p.Report()
	if report.Sequence != r.Sequence || report.DescriptorSHA256 != r.ReleaseSHA256 || !report.ProvenanceVerified && !report.DevelopmentOnly {
		return errors.New("independently verified maintenance release differs from the wizard")
	}
	engine, _ := bundle.Names()
	manifest, err := bundle.Verify(report.BundleDirectory)
	if err != nil {
		return err
	}
	_, err = vpsinstall.InstallProtected(ctx, a.Owner, vpsinstall.Options{Core: filepath.Join(report.BundleDirectory, engine), CoreSHA256: manifest.Files[engine].SHA256, Listen: r.Listen, PublicOrigin: r.Origin, ApplicationSequence: report.Sequence, ApplicationReleaseSHA256: report.DescriptorSHA256, PrepareHost: true, Start: r.Start}, io.Discard)
	if err == nil {
		p.RetainAcquisitionEvidence()
	}
	return err
}

func readMaintenanceInput(ctx context.Context, in io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		data []byte
		err  error
	}
	read := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(in, (32<<10)+1))
		read <- result{b, err}
	}()
	select {
	case <-ctx.Done():
		// This is a one-request executable: returning exits the helper even
		// when a caller keeps its pipe open. No lock or mutation was acquired.
		return nil, ctx.Err()
	case r := <-read:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return r.data, r.err
	}
}
