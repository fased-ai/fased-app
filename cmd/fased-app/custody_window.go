package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/signer"
)

// The owner client calls this immutable installed entry, never the core HTTP
// service. The parent binds the enrollment before forwarding a private pipe to
// the existing independently UID-authenticated signer helper.
func custodyWindowCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("custody window", flag.ContinueOnError)
	f.SetOutput(errOut)
	expected := f.String("expected-instance", "", "exact enrolled instance")
	network := f.String("network", "", "enrolled wallet network")
	inspect := f.Bool("inspect", false, "verify public owner target without secret input")
	initialize := f.Bool("initialize", false, "initialize the selected package wallet")
	operation := f.String("operation", "", "original wallet operation")
	disposable := f.Bool("disposable-fixture", false, "unfunded disposable keys only")
	ordinary := f.Bool("ordinary-custody", false, "owner-authorized wallet")
	if f.Parse(args) != nil || f.NArg() != 0 || *disposable && *ordinary || !*inspect && !*disposable && !*ordinary || *expected == "" || (*network != "devnet" && *network != "mainnet-beta") || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("protected wallet requires its enrolled non-root owner")
	}
	if *initialize {
		if *inspect || *operation == "" {
			return errors.New("one original wallet initialization is required")
		}
		return initializePackageWallet(*expected, *network, *operation, *disposable, *ordinary, errOut)
	}
	if *operation != "" {
		return errors.New("wallet operation is only used for initialization")
	}
	if *inspect {
		pending, pendingOperation, err := pendingPackageWalletTarget(os.Getuid(), *expected, *network)
		if err != nil {
			return err
		}
		if pendingOperation != "" {
			if _, err = walletOwnerMode(pending, *disposable, *ordinary, true); err != nil {
				return err
			}
			return writeCustodyWindowInspection(out, pending, *expected, *network, false, pendingOperation)
		}
	}
	m, packaged, err := custodyWindowTarget(os.Getuid())
	if err != nil {
		return errors.New("protected wallet enrollment is required")
	}
	mode, err := walletOwnerMode(m, *disposable, *ordinary, *inspect)
	if err != nil {
		return err
	}
	unconfigured := packaged && m.Network == ""
	if m.Network != *network && !(unconfigured && *inspect) || !packaged && custodyinstall.Verify(m) != nil {
		return errors.New("protected wallet enrollment does not match this instance/network")
	}
	commit, commitErr := custodyinstall.ReadEnrollmentCommit(m)
	committed := commitErr == nil && commit.InstanceID == *expected
	if !committed {
		// Only an absent commit with the exact private parent/child intent is
		// resumable. Existing malformed/foreign commit bytes remain a hard refusal.
		_, missing := os.Lstat(filepath.Join(m.Root, "enrollment-ready.json"))
		if packaged || !*inspect || !errors.Is(missing, os.ErrNotExist) {
			return errors.New("protected wallet enrollment does not match this instance/network")
		}
		resume, err := enrollment.CanResumeNative(*expected, m.Location, *network, m.CoreHash, m.SignerHash, m.NativeOwnerAccess)
		if err != nil || !resume {
			return errors.New("protected wallet enrollment needs owner recovery")
		}
	}
	if *inspect {
		// Public readiness is returned even for exact retained StageOnly or
		// stopped roles, so a reopened native window can reach Continue/Resume.
		ready := committed && !unconfigured && custodyinstall.Runtime(m) == nil
		shownNetwork := m.Network
		if unconfigured {
			shownNetwork = *network
		}
		return writeCustodyWindowInspection(out, m, *expected, shownNetwork, ready, "")
	}
	lock, err := custodyinstall.OwnerLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	flag, err := custodyOwnerFlag(m, "setup")
	if err != nil || flag != mode {
		return signer.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(m.Root, "fased-signer"), "window", "--config", filepath.Join(m.Root, "identity.json"), "--window-instance", *expected, flag)
	cmd.Stdin, cmd.Stdout = in, out
	// Child errors contain no private input. Do not include ssh/child diagnostics
	// in a browser response or a persisted operation record.
	cmd.Stderr = errOut
	return cmd.Run()
}

// An inspection may discover the public profile; a secret-bearing request
// must explicitly match it. Fixture input can never become owner custody.
func walletOwnerMode(m custodyinstall.Manifest, fixture, ordinary, inspect bool) (string, error) {
	if fixture && ordinary || !inspect && !fixture && !ordinary {
		return "", signer.ErrUnavailable
	}
	want := "--disposable-fixture"
	if m.Version == 3 {
		if m.Mode != signer.OrdinaryCustodyMode || m.Profile != signer.OwnerCustodyProfile {
			return "", signer.ErrUnavailable
		}
		switch m.Provenance {
		case signer.ControlledCustodyProfile:
		case signer.OwnerKeyProvenance:
			want = "--ordinary-custody"
		default:
			return "", signer.ErrUnavailable
		}
	}
	if fixture && want != "--disposable-fixture" || ordinary && want != "--ordinary-custody" {
		return "", signer.ErrUnavailable
	}
	return want, nil
}

func writeCustodyWindowInspection(out io.Writer, m custodyinstall.Manifest, instance, network string, ready bool, pendingOperation string) error {
	provenance := ""
	if m.Provenance == signer.OwnerKeyProvenance {
		provenance = m.Provenance
	}
	return json.NewEncoder(out).Encode(struct {
		Provenance       string `json:"provenance,omitempty"`
		Version          int    `json:"version"`
		Instance         string `json:"instanceId"`
		Signer           string `json:"signerId"`
		Network          string `json:"network"`
		Owner            int    `json:"ownerUid"`
		CoreHash         string `json:"coreSha256"`
		SignerHash       string `json:"signerSha256"`
		Ready            bool   `json:"ready"`
		PendingOperation string `json:"pendingOperation,omitempty"`
	}{provenance, 1, instance, m.ID, network, m.Owner, m.CoreHash, m.SignerHash, ready, pendingOperation})
}
