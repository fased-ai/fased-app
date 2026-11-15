//go:build linux

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

var readPackageCandidateForStage = maintenance.ReadPackageCandidateReceipt
var packageCandidateCommandForStage = maintenance.PackageCandidateStreamCommand

func stageAttestedCandidate(ctx context.Context, p *bootstrap.Prepared, packetSHA, scope string, diagnostics io.Writer) error {
	if p == nil || p.Report().LinuxPackageDigestsVerified {
		return errors.New("native packages require the package-aware installation controller")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	reuse := false
	if old, err := maintenance.ReadCandidateReceipt(os.Getuid()); err == nil {
		if old.PacketSHA256 == packetSHA && old.Scope == scope {
			reuse = true
		} else if old.Scope != "fresh" || scope != "update" {
			return errors.New("another candidate is retained; preserve its operation")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	q := maintenance.CandidateRequest{PacketSHA256: packetSHA, Scope: scope}
	cmd, frame, err := maintenance.CandidateStreamCommand(ctx, maintenance.Request{Version: 1, Action: "candidate-stage", Candidate: &q}, diagnostics)
	if err != nil {
		return err
	}
	_, err = streamAttestedCandidate(ctx, p, cmd, frame, reuse, packetSHA, scope)
	return err
}

// stageAttestedPackageCandidate reaches only the original instance's fixed
// helper. Both the caller and the helper verify the same candidate; the
// helper also binds its receipt to the installed predecessor.
func stageAttestedPackageCandidate(ctx context.Context, p *bootstrap.Prepared, packetSHA, location, instance, baseline string, diagnostics io.Writer) error {
	if p == nil {
		return errors.New("verified native package candidate required")
	}
	report := p.Report()
	hash, err := hex.DecodeString(baseline)
	if !report.LinuxPackageDigestsVerified || !report.ProvenanceVerified || report.DescriptorSHA256 != packetSHA || len(hash) != 32 || hex.EncodeToString(hash) != baseline || err != nil {
		return errors.New("verified native package and installed predecessor required")
	}
	if _, err = update.PackageInstanceRoot(os.Getuid(), location, instance); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	reuse := false
	if old, e := readPackageCandidateForStage(os.Getuid(), location, instance); e == nil {
		if old.PacketSHA256 == packetSHA && old.Scope == "update" {
			if old.BaseInstallation != instance || old.BaseReleaseSHA256 != baseline {
				return errors.New("candidate predecessor differs; preserve its operation")
			}
			reuse = true
		} else if old.Scope != "fresh" {
			return errors.New("another candidate is retained; preserve its operation")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	q := maintenance.CandidateRequest{PacketSHA256: packetSHA, Scope: "update"}
	cmd, frame, err := packageCandidateCommandForStage(ctx, location, instance, q, diagnostics)
	if err != nil {
		return err
	}
	receipt, err := streamAttestedCandidate(ctx, p, cmd, frame, reuse, packetSHA, "update")
	if err != nil {
		return err
	}
	if receipt.BaseInstallation != instance || receipt.BaseReleaseSHA256 != baseline || receipt.ReleaseSHA256 != packetSHA || receipt.Sequence != report.Sequence || receipt.Target != report.Target {
		return errors.New("protected package candidate differs from its original instance or release")
	}
	return nil
}

func streamAttestedCandidate(ctx context.Context, p *bootstrap.Prepared, cmd *exec.Cmd, frame []byte, reuse bool, packetSHA, scope string) (maintenance.CandidateReceipt, error) {
	var receipt maintenance.CandidateReceipt
	in, err := cmd.StdinPipe()
	if err != nil {
		return receipt, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return receipt, err
	}
	if err = cmd.Start(); err != nil {
		return receipt, err
	}
	_, err = in.Write(frame)
	if err == nil && !reuse {
		err = p.WriteCandidate(ctx, in)
	}
	err = errors.Join(err, in.Close())
	b, readErr := io.ReadAll(io.LimitReader(out, 65537))
	err = errors.Join(err, readErr, cmd.Wait())
	var reply maintenance.Response
	if err != nil || len(b) > 65536 || update.StrictJSON(b, &reply) != nil || reply.Version != 1 || reply.Action != "candidate-stage" || !reply.Complete || update.StrictJSON(reply.Payload, &receipt) != nil || receipt.Validate() != nil || receipt.Owner != os.Getuid() || receipt.PacketSHA256 != packetSHA || receipt.Scope != scope {
		return maintenance.CandidateReceipt{}, errors.Join(err, errors.New("protected candidate staging was not confirmed; preserve the original operation"))
	}
	return receipt, nil
}

func candidateUpdateBaseline() (string, error) {
	r, err := update.ReadMaintenanceRecord(os.Getuid())
	if err != nil || r.Phase != "idle" && r.Phase != "completed" {
		return "", errors.New("recover the existing custody installation before staging a candidate")
	}
	base, err := r.InstalledPackage()
	if err != nil {
		return "", err
	}
	return base.ReleaseSHA256, nil
}

func candidatePackageUpdateBaseline(location, instance string) (string, error) {
	owner := os.Getuid()
	if owner <= 0 || os.Geteuid() != owner {
		return "", errors.New("ordinary package owner required")
	}
	base, _, err := packageCandidateBaseline(owner, location, instance, nil)
	if err != nil {
		return "", err
	}
	return base.ReleaseSHA256, nil
}
