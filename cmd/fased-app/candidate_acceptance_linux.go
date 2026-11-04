//go:build linux && fased_candidate_acceptance

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Maintainer composition only, never part of the shipped core/installer. No
// environment, JSON, CLI input or endpoint may choose this binding or trust.
// A reviewed acceptance build pins one candidate, owner baseline and operation.
type candidateAcceptanceBinding struct {
	Directory, Parent string
	Local             localinstall.Paths
	Remote            bool
	Admission         bootstrap.CandidateAdmission
}

var compiledCandidateAcceptance *candidateAcceptanceBinding

func candidateAcceptanceSource(binding *candidateAcceptanceBinding) (func(context.Context, string) (*bootstrap.Prepared, error), error) {
	if binding == nil || compiledApplicationChannel == nil || os.Geteuid() == 0 {
		return nil, errors.New("ordinary maintainer and independently compiled candidate acceptance required")
	}
	b := *binding
	if !filepath.IsAbs(b.Directory) || !filepath.IsAbs(b.Parent) || !update.IDOK(b.Admission.OperationID) {
		return nil, errors.New("fixed private candidate paths and exact operation required")
	}
	if b.Admission.ExpiresAt == 0 {
		// Explicitly invoking this maintainer command is manual admission for one
		// fixed operation. All source reads in this process share one deadline.
		b.Admission.ExpiresAt = time.Now().Add(15 * time.Minute).Unix()
	}
	// Capture an independent copy; later global changes cannot retarget source.
	channel := &applicationChannelBinding{Seed: append([]byte(nil), compiledApplicationChannel.Seed...), SHA256: compiledApplicationChannel.SHA256}
	return func(ctx context.Context, id string) (*bootstrap.Prepared, error) {
		if id != b.Admission.OperationID {
			return nil, errors.New("candidate belongs to another operation")
		}
		if err := core.EnsurePrivateDirectory(b.Parent); err != nil {
			return nil, err
		}
		o, err := channelOptions(channel, filepath.Join(b.Parent, "candidate-"+id+"-"+update.NewProbe()))
		if err != nil {
			return nil, err
		}
		p, err := bootstrap.PrepareAttestedCandidate(ctx, o, b.Directory, b.Admission)
		if err == nil {
			p.RetainAcquisitionEvidence()
		}
		return p, err
	}, nil
}

func candidateAcceptanceCommand(args []string, out, errOut io.Writer) error {
	if len(args) != 0 {
		return errors.New("candidate acceptance has no runtime overrides")
	}
	source, err := candidateAcceptanceSource(compiledCandidateAcceptance)
	if err != nil {
		return err
	}
	b := *compiledCandidateAcceptance
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	// Authenticate the exact held candidate before even preparing an old-reader
	// session bridge. The existing owner/controller independently admits again.
	p, err := source(ctx, b.Admission.OperationID)
	if err != nil {
		return err
	}
	defer p.Close()
	if b.Remote {
		m, err := vpsinstall.Load(os.Getuid())
		if err != nil {
			// A cut between generation swaps is intentionally rejected by Load.
			// Admit only the same retained operation, exactly as normal recovery.
			m, err = vpsinstall.LoadApplicationMaintenance(os.Getuid(), b.Admission.OperationID)
			if err != nil {
				return err
			}
		}
		prior, stateErr := vpsinstall.ApplicationUpdateState(m.OwnerUID)
		if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
			return stateErr
		}
		if err = p.CheckCandidateInstallation(vpsinstall.ManifestSHA256(m), m.Root, m.Data, prior); err != nil {
			return err
		}
		system := &custodyinstall.System{NonInteractive: true, Out: errOut}
		if prior.ID == b.Admission.OperationID && prior.Phase != "completed" {
			return maintainRemoteReaderUpdate(ctx, m, b.Admission.OperationID, false, source, out, system)
		}
		var request core.ApplicationReaderBootstrapRequest
		_, callbacks := readerRemoteCallbacks(m, b.Admission.OperationID, &request, system)
		driver, err := p.RemoteApplicationController(m, callbacks, system)
		if err != nil {
			return err
		}
		current, err := driver.Check(ctx)
		if err != nil {
			return err
		}
		if current == nil {
			fmt.Fprintln(out, "Already current.")
			return nil
		}
		if err = prepareRemoteSourceSession(ctx, m, system); err != nil {
			return err
		}
		return maintainRemoteReaderUpdate(ctx, m, b.Admission.OperationID, false, source, out, system)
	}
	m, err := localinstall.Load(b.Local)
	if err != nil {
		return err
	}
	prior, stateErr := localinstall.ApplicationUpdateState(b.Local)
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return stateErr
	}
	if err = p.CheckCandidateInstallation(localinstall.ManifestSHA256(m), m.Root, m.Data, prior); err != nil {
		return err
	}
	if prior.ID == b.Admission.OperationID && prior.Phase != "completed" {
		return maintainLocalReaderUpdate(ctx, m, b.Admission.OperationID, false, source, out)
	}
	var request core.ApplicationReaderBootstrapRequest
	driver, err := p.LocalApplicationController(b.Local, readerLocalCallbacks(m, &request))
	if err != nil {
		return err
	}
	current, err := driver.Check(ctx)
	if err != nil {
		return err
	}
	if current == nil {
		fmt.Fprintln(out, "Already current.")
		return nil
	}
	return maintainLocalReaderUpdate(ctx, m, b.Admission.OperationID, false, source, out)
}
