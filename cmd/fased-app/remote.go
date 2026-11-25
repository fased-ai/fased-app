package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsnetwork"
)

const remoteOperationTimeout = 2 * time.Minute

func remoteSystem(in io.Reader, out io.Writer) *custodyinstall.System {
	return &custodyinstall.System{In: in, Out: out}
}

func networkConsent(in io.Reader, out io.Writer) (vpsnetwork.Approval, error) {
	var approval vpsnetwork.Approval
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "This closes public inbound connections, keeps private SSH/HTTPS, and disables root/password SSH login. Existing firewall rules are preserved. Rollback stays armed until a second private SSH session confirms.")
	for _, question := range []struct {
		prompt string
		value  *bool
	}{
		{"Have you signed in to this instance through its private HTTPS address? [y/N]: ", &approval.PrivateUI},
		{"Have you tested your VPS provider console as an independent recovery path? [y/N]: ", &approval.ConsoleRecovery},
	} {
		answer, err := installationAnswer(reader, out, question.prompt)
		if err != nil {
			return vpsnetwork.Approval{}, err
		}
		if answer != "y" && answer != "yes" {
			return vpsnetwork.Approval{}, errors.New("network lockdown cancelled; existing access is preserved")
		}
		*question.value = true
	}
	return approval, nil
}

func networkHandoffParts(out io.Writer, corePath, dataDir, phase, attemptID, statusPhase string, deadline time.Time) {
	if statusPhase == "pending" {
		fmt.Fprintln(out, "Keep this connection open. Open a second SSH TCP connection through Tailscale (no ControlMaster reuse), verify private sign-in, then run before", deadline.UTC().Format(time.RFC3339)+":")
		if phase == "package-remote" {
			fmt.Fprintln(out, installationCommand(corePath, "vps", "confirm", "--data-dir", dataDir, attemptID))
		} else {
			fmt.Fprintln(out, installationCommand(corePath, "vps", "confirm", attemptID))
		}
		fmt.Fprintln(out, "Without confirmation, rollback restores the previous SSH policy and removes only Fased's firewall table.")
	}
}

func packageNetworkHandoff(out io.Writer, m vpsnetwork.Manifest, status vpsnetwork.Status) {
	networkHandoffParts(out, m.Core, m.Data, m.Phase, status.AttemptID, status.Phase, status.Deadline)
}

func runPackageNetworkCommand(ctx context.Context, action, attemptID string, m vpsnetwork.Manifest, in io.Reader, out, errOut io.Writer, system *custodyinstall.System) error {
	var status vpsnetwork.Status
	var err error
	switch action {
	case "harden":
		approval, aErr := networkConsent(in, errOut)
		if aErr != nil {
			return aErr
		}
		status, err = vpsnetwork.Begin(ctx, m, system, approval)
	case "confirm":
		status, err = vpsnetwork.Confirm(ctx, m, system, attemptID)
	case "finish":
		status, err = vpsnetwork.Inspect(ctx, m, system)
		if err == nil && status.Phase == "pending" {
			status, err = vpsnetwork.Confirm(ctx, m, system, status.AttemptID)
		} else if err == nil && status.Phase != "committed" {
			err = errors.New("no pending private setup; existing access is preserved")
		}
	case "rollback":
		status, err = vpsnetwork.Rollback(ctx, m, system)
	case "network":
		status, err = vpsnetwork.Inspect(ctx, m, system)
	default:
		return errors.New("unknown vps action")
	}
	if err != nil {
		return err
	}
	packageNetworkHandoff(errOut, m, status)
	return json.NewEncoder(out).Encode(status)
}

func isVPSNetworkAction(action string) bool {
	switch action {
	case "harden", "confirm", "finish", "rollback", "network":
		return true
	default:
		return false
	}
}

func vpsCommand(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: fased-app vps harden|confirm|finish|rollback|network [--data-dir <directory>]")
	}
	action := args[0]
	if !isVPSNetworkAction(action) {
		return errors.New("unknown or retired vps action; use harden|confirm|finish|rollback|network")
	}
	f := flag.NewFlagSet("vps "+action, flag.ContinueOnError)
	dataDir := f.String("data-dir", "", "Remote package data directory for network commands")
	f.SetOutput(errOut)
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if (action == "confirm" && f.NArg() != 1) || (action != "confirm" && f.NArg() != 0) {
		return errors.New("unexpected vps arguments")
	}
	if runtime.GOOS != "linux" || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return errors.New("VPS management requires an ordinary Linux administrator")
	}
	system := remoteSystem(in, errOut)
	ctx, cancel := context.WithTimeout(context.Background(), remoteOperationTimeout)
	defer cancel()
	attemptID := ""
	if action == "confirm" {
		attemptID = f.Arg(0)
	}
	pm, err := vpsnetwork.Load(os.Getuid(), *dataDir)
	if err != nil {
		return err
	}
	return runPackageNetworkCommand(ctx, action, attemptID, pm, in, out, errOut, system)
}

func validateRuntimeUID(uid, euid int) error {
	if uid <= 0 || euid != uid {
		return errors.New("run Fased as an ordinary OS user; use the installer for privileged OS preparation")
	}
	return nil
}
