//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/hostprep"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/tailnet"
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
	data, stream, err := readMaintenanceFrame(ctx, in)
	if err != nil {
		return err
	}
	r, err := maintenance.DecodeRequest(data)
	if err != nil {
		return err
	}
	if stream != nil && r.Action == "custody-enrollment-image" {
		a, err = maintenance.ValidateProcess(owner)
		if err != nil {
			return err
		}
		streamCtx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
		defer stop()
		return maintenanceCustodyEnrollmentImage(streamCtx, a, *r.Image, stream, out)
	}
	if r.Action == "custody-enrollment-image" {
		return errors.New("enrollment image stream required")
	}
	if (r.Action == "candidate-stage") != (stream != nil) {
		return errors.New("candidate staging requires its fixed private stream")
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
	if a.Profile != "remote" && r.Action != "candidate-stage" && r.Action != "custody-update" && r.Action != "custody-initialize" && r.Action != "custody-enrollment-prepare" && r.Action != "custody-enrollment-empty" && r.Action != "custody-enrollment-stage" && r.Action != "custody-enrollment-cutover" && r.Action != "custody-enrollment-activation" {
		return errors.New("Remote setup authority required")
	}
	workCtx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stop()
	if stream != nil {
		if closer, ok := in.(io.Closer); ok {
			stopClose := context.AfterFunc(workCtx, func() { _ = closer.Close() })
			defer stopClose()
		}
	}
	var payload json.RawMessage
	switch r.Action {
	case "candidate-stage":
		var receipt maintenance.CandidateReceipt
		receipt, err = maintenanceStageCandidate(workCtx, a, *r.Candidate, stream)
		if err == nil {
			payload, err = json.Marshal(receipt)
		}
	case "custody-enrollment-activation":
		var source enrollment.ProtectedSource
		source, err = protectedEnrollmentSource(a)
		if err == nil {
			err = enrollment.WithProtectedActivation(workCtx, a.Owner, r.Enrollment.OperationID, r.Enrollment.Step, source, func(v enrollment.ProtectedActivation) error {
				journal, e := update.ReadMaintenanceRecord(a.Owner)
				initialize := errors.Is(e, os.ErrNotExist) || e == nil && journal.Phase == "idle"
				if e != nil && !errors.Is(e, os.ErrNotExist) {
					return e
				}
				if !initialize {
					var target custodyinstall.Manifest
					if journal.Phase != "completed" || journal.Start != "target" || journal.Installation == nil || journal.Installation.Phase != "completed" || !update.ValidInstallation(*journal.Installation) || update.StrictJSON(journal.Target, &target) != nil || target != v.Target {
						return errors.New("existing custody update must be recovered before activation")
					}
				}
				for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
					if _, e = os.Lstat(v.Target.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
						return errors.New("existing custody update fence must be preserved")
					}
				}
				m, e := custodyinstall.PrepareProtectedEnrollmentActivation(workCtx, a.Owner, v.Target.ID, v.InstanceID, v.Digest)
				if e != nil {
					return e
				}
				if initialize {
					if e = maintenanceCustodyInitialize(workCtx, a); e != nil {
						return e
					}
				}
				return custodyinstall.StartProtectedEnrollmentServices(workCtx, a.Owner, m, v.InstanceID, v.Running, v.Autostart)
			})
		}
	case "custody-enrollment-cutover":
		var source enrollment.ProtectedSource
		source, err = protectedEnrollmentSource(a)
		if err == nil {
			err = enrollment.RunProtectedCutover(workCtx, a.Owner, r.Enrollment.OperationID, r.Enrollment.Step, source)
		}
	case "custody-enrollment-empty":
		err = custodyinstall.CheckProtectedEnrollmentSigner(a.Owner, r.Enrollment.OperationID)
	case "custody-enrollment-stage":
		err = custodyinstall.PrepareProtectedEnrollmentStage(a.Owner, r.Enrollment.OperationID)
	case "custody-enrollment-prepare":
		var m custodyinstall.Manifest
		m, err = maintenanceCustodyPrepare(workCtx, a, *r.Enrollment)
		if err == nil {
			payload, err = json.Marshal(m)
		}
	case "custody-initialize":
		err = maintenanceCustodyInitialize(workCtx, a)
	case "custody-update":
		err = maintenanceCustodyAcquire(workCtx, a, *r.Custody, false)
		if err == nil {
			err = custodyinstall.ReconcileProtectedSetup(workCtx, a.Owner)
		}
		if err == nil {
			err = custodyinstall.RunProtectedUpdate(workCtx, a.Owner, r.Custody.OperationID, false)
		}
	case "serve":
		err = privateaccess.ApplyOwnedServe(workCtx, a.Owner, a.Home, r.Serve.Configuration, r.Serve.ETag)
	case "tailscale-login":
		err = hostprep.ProtectedTailscaleLogin(workCtx, a.Owner, os.Stderr)
	case "remote-preflight-origin":
		var origin string
		origin, err = privateaccess.ProtectedPendingOrigin(a.Owner)
		if err == nil {
			payload, err = json.Marshal(origin)
		}
	case "remote-install":
		var device tailnet.Identity
		device, err = maintenanceRemoteInstall(workCtx, a, *r.Remote)
		if err == nil {
			payload, err = json.Marshal(device)
		}
	default:
		payload, err = vpsinstall.RunProtected(workCtx, a.Owner, r.Action, r.Peer, io.Discard)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true, Payload: payload})
}

func protectedEnrollmentSource(a maintenance.Authority) (enrollment.ProtectedSource, error) {
	if a.Profile == "local" {
		return enrollment.ProtectedSource{}, nil
	}
	if a.Profile != "remote" {
		return enrollment.ProtectedSource{}, errors.New("fixed enrollment profile required")
	}
	m, err := vpsinstall.LoadProtected(a.Owner)
	if err != nil {
		return enrollment.ProtectedSource{}, err
	}
	return enrollment.ProtectedSource{Data: m.Data, Listen: m.Listen, PublicOrigin: m.PublicOrigin, UID: m.AppUID, GID: m.AppGID, ApplicationSequence: m.ApplicationSequence, ApplicationReleaseSHA256: m.ApplicationReleaseSHA256}, nil
}

// Regular requests retain their bounded EOF framing. Only a fixed enrollment
// stream has a bounded header followed by the existing private image/lock pipe.
func readMaintenanceFrame(ctx context.Context, in io.Reader) ([]byte, io.Reader, error) {
	type result struct {
		data []byte
		rest io.Reader
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		reader := bufio.NewReaderSize(in, (32<<10)+1)
		for _, prefix := range []string{maintenance.EnrollmentStreamPrefix, maintenance.CandidateStreamPrefix} {
			peeked, e := reader.Peek(len(prefix))
			if e != nil || string(peeked) != prefix {
				continue
			}
			_, _ = reader.Discard(len(prefix))
			line, err := reader.ReadSlice('\n')
			if err != nil || len(line) > 32<<10 {
				ch <- result{err: errors.New("invalid maintenance stream header")}
				return
			}
			ch <- result{data: append([]byte(nil), line...), rest: reader}
			return
		}
		b, err := io.ReadAll(io.LimitReader(reader, (32<<10)+1))
		ch <- result{data: b, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case r := <-ch:
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return r.data, r.rest, r.err
	}
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

func maintenanceRemoteInstall(ctx context.Context, a maintenance.Authority, r maintenance.RemoteInstallRequest) (_ tailnet.Identity, resultErr error) {
	var empty tailnet.Identity
	_, approvalErr := privateaccess.ReadProtectedInitialApproval(a.Owner)
	if err := requireFreshRemoteApproval(a.Owner, a.Home, approvalErr, os.Lstat); err != nil {
		return empty, err
	}
	o, err := maintenanceOptions(a)
	if err != nil {
		return empty, err
	}
	descriptor, descriptorHash, err := bootstrap.InspectMaintenance(ctx, a.Owner, o)
	if err != nil {
		return empty, err
	}
	if descriptor.Sequence != r.Sequence || descriptorHash != r.ReleaseSHA256 {
		return empty, errors.New("independently verified maintenance release differs from the wizard")
	}
	device, err := privateaccess.ConfirmProtectedRemoteInitial(ctx, a.Owner, r.Origin, r.Listen, descriptor.Sequence, descriptorHash, "", os.Stderr)
	if err != nil {
		return empty, err
	}
	p, err := bootstrap.PrepareMaintenance(ctx, a.Owner, o)
	if err != nil {
		return empty, err
	}
	installStarted := false
	defer func() {
		if !installStarted {
			p.RetainAcquisitionEvidence()
		}
		resultErr = errors.Join(resultErr, p.Close())
	}()
	report := p.Report()
	if report.Sequence != r.Sequence || report.DescriptorSHA256 != r.ReleaseSHA256 || !report.ProvenanceVerified && !report.DevelopmentOnly {
		return empty, errors.New("independently verified maintenance release differs from the wizard")
	}
	engine, _ := bundle.Names()
	manifest, err := bundle.Verify(report.BundleDirectory)
	if err != nil {
		return empty, err
	}
	if err = privateaccess.BindProtectedInitialCore(a.Owner, r.Origin, r.Listen, report.Sequence, report.DescriptorSHA256, manifest.Files[engine].SHA256); err != nil {
		return empty, err
	}
	installStarted = true
	_, err = vpsinstall.InstallProtected(ctx, a.Owner, vpsinstall.Options{Core: filepath.Join(report.BundleDirectory, engine), CoreSHA256: manifest.Files[engine].SHA256, Listen: r.Listen, PublicOrigin: r.Origin, ApplicationSequence: report.Sequence, ApplicationReleaseSHA256: report.DescriptorSHA256, PrepareHost: true, Start: r.Start}, io.Discard)
	if err == nil {
		p.RetainAcquisitionEvidence()
	}
	return device, err
}

func requireFreshRemoteApproval(owner int, home string, approvalErr error, stat func(string) (os.FileInfo, error)) error {
	if approvalErr == nil {
		return nil
	}
	if !errors.Is(approvalErr, os.ErrNotExist) {
		return approvalErr
	}
	// Never replace a prior pending claimant or adopt an installation that was
	// created before this protected approval existed.
	for _, path := range []string{vpsinstall.Root(owner), filepath.Join(maintenance.OwnerRoot(owner), "remote", "intent.json"), filepath.Join(home, ".config", "fased-app", "vps-access", "owner-handoff.json")} {
		if _, err := stat(path); err == nil {
			return errors.New("existing Remote setup lacks protected approval; preserve its original owner")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
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
