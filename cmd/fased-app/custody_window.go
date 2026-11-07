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
)

// The native owner client calls this immutable installed entry, never the core
// HTTP service. The parent binds the enrollment before forwarding a private pipe
// to the existing independently UID-authenticated signer helper.
func custodyWindowCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("custody window", flag.ContinueOnError)
	f.SetOutput(errOut)
	expected := f.String("expected-instance", "", "exact enrolled instance")
	network := f.String("network", "", "enrolled wallet network")
	inspect := f.Bool("inspect", false, "verify public owner target without secret input")
	disposable := f.Bool("disposable-fixture", false, "unfunded disposable keys only")
	if f.Parse(args) != nil || f.NArg() != 0 || !*disposable || *expected == "" || (*network != "devnet" && *network != "mainnet-beta") || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("protected wallet requires an enrolled non-root owner and disposable keys")
	}
	m, err := custodyinstall.Load(os.Getuid())
	if err != nil {
		return errors.New("protected wallet enrollment is required")
	}
	if m.Network != *network || custodyinstall.Verify(m) != nil {
		return errors.New("protected wallet enrollment does not match this instance/network")
	}
	commit, commitErr := custodyinstall.ReadEnrollmentCommit(m)
	committed := commitErr == nil && commit.InstanceID == *expected
	if !committed {
		// Only an absent commit with the exact private parent/child intent is
		// resumable. Existing malformed/foreign commit bytes remain a hard refusal.
		_, missing := os.Lstat(filepath.Join(m.Root, "enrollment-ready.json"))
		if !*inspect || !errors.Is(missing, os.ErrNotExist) {
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
		ready := committed && custodyinstall.Runtime(m) == nil
		return json.NewEncoder(out).Encode(struct {
			Version    int    `json:"version"`
			Instance   string `json:"instanceId"`
			Signer     string `json:"signerId"`
			Network    string `json:"network"`
			Owner      int    `json:"ownerUid"`
			CoreHash   string `json:"coreSha256"`
			SignerHash string `json:"signerSha256"`
			Ready      bool   `json:"ready"`
		}{1, *expected, m.ID, m.Network, m.Owner, m.CoreHash, m.SignerHash, ready})
	}
	lock, err := custodyinstall.OwnerLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	flag, err := custodyOwnerFlag(m, "setup")
	if err != nil {
		return err
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
