//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func stageAttestedCandidate(ctx context.Context, p *bootstrap.Prepared, packetSHA, scope string, diagnostics io.Writer) error {
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
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	_, err = in.Write(frame)
	if err == nil && !reuse {
		err = p.WriteCandidate(ctx, in)
	}
	err = errors.Join(err, in.Close())
	b, readErr := io.ReadAll(io.LimitReader(out, 65537))
	err = errors.Join(err, readErr, cmd.Wait())
	var reply maintenance.Response
	var receipt maintenance.CandidateReceipt
	if err != nil || len(b) > 65536 || update.StrictJSON(b, &reply) != nil || reply.Version != 1 || reply.Action != "candidate-stage" || !reply.Complete || update.StrictJSON(reply.Payload, &receipt) != nil || receipt.Validate() != nil || receipt.Owner != os.Getuid() || receipt.PacketSHA256 != packetSHA || receipt.Scope != scope {
		return errors.Join(err, errors.New("protected candidate staging was not confirmed; preserve the original operation"))
	}
	return nil
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
