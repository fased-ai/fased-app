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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

const remoteOperationTimeout = 2 * time.Minute

// Called only after CheckApplication binds the immutable Remote config/root.
// Keep the public handoff numeric so the UI never accepts executable shell text.
func managedRemoteOwner(root string) (int, error) {
	uid, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(root), "fased-app-vps-"))
	if err != nil || uid <= 0 || int64(uid) > 4294967294 || root != vpsinstall.Root(uid) {
		return 0, errors.New("invalid managed Remote owner directory")
	}
	return uid, nil
}

func installationManagedRemote(in *bufio.Reader, out io.Writer) (bool, error) {
	for {
		value, err := installationAnswer(in, out, "Use a dedicated app account with no login or sudo? [Y/n]: ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(value) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(out, "Choose yes or no.")
	}
}

func validateManagedRemote(bundleDir string, desktop, autostart bool, explicitPaths bool) error {
	if bundleDir == "" || desktop {
		return errors.New("managed Remote requires a verified headless bundle")
	}
	if runtime.GOOS != "linux" {
		return errors.New("managed Remote requires Ubuntu Linux")
	}
	if !autostart {
		return errors.New("managed Remote requires explicit --autostart")
	}
	if explicitPaths {
		return errors.New("managed Remote uses dedicated installation directories; omit --prefix and --data-dir")
	}
	return nil
}

func remoteSystem(in io.Reader, out io.Writer) *custodyinstall.System {
	return &custodyinstall.System{In: in, Out: out}
}

func remoteAccessDirectory() (string, error) {
	root, err := core.DefaultDataDir()
	if err != nil {
		return "", err
	}
	if err = core.EnsurePrivateDirectory(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, "vps-access")
	if err = core.EnsurePrivateDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func enableRemoteAccess(ctx context.Context, m vpsinstall.Manifest, system *custodyinstall.System) (string, error) {
	status, err := vpsinstall.Status(ctx, m, system)
	if err != nil {
		return "", err
	}
	if !status.Active || status.InstanceID == "" {
		return "", errors.New("Remote instance is not ready; inspect vps status")
	}
	if m.PublicOrigin == "" {
		return status.URL, nil
	}
	manager := privateaccess.System()
	plan, err := manager.Plan(ctx, "tailscale", m.Listen)
	if err != nil {
		return "", err
	}
	if plan.Origin != m.PublicOrigin {
		return "", errors.New("Tailscale identity changed; installed Remote origin was preserved")
	}
	dir, err := remoteAccessDirectory()
	if err != nil {
		return "", err
	}
	if err = manager.Enable(ctx, dir, plan, status.InstanceID); err != nil {
		return "", err
	}
	return plan.Origin, nil
}

func remotePublicOrigin(plan privateaccess.Plan) string {
	if plan.Mode == "tailscale" {
		return plan.Origin
	}
	return ""
}

func managedRemoteInstall(ctx context.Context, corePath, hash, listen string, plan privateaccess.Plan, prepare, start bool, out, errOut io.Writer) error {
	system := remoteSystem(os.Stdin, errOut)
	m, err := vpsinstall.Install(ctx, vpsinstall.Options{Core: corePath, CoreSHA256: hash, Listen: listen, PublicOrigin: remotePublicOrigin(plan), PrepareHost: prepare, Start: start}, system)
	if err != nil {
		return err
	}
	url := ""
	if start {
		url, err = enableRemoteAccess(ctx, m, system)
		if err != nil {
			return remoteAccessPending(m, err)
		}
	}
	remoteHandoff(errOut, m, plan, start)
	return json.NewEncoder(out).Encode(map[string]any{"installed": true, "location": "remote", "managed": true, "application": m.Core, "data": m.Data, "url": url, "autostart": start, "walletsConnected": false})
}

func remoteHandoff(out io.Writer, m vpsinstall.Manifest, plan privateaccess.Plan, started bool) {
	command := func(action string) string { return installationCommand(m.Core, "vps", action) }
	fmt.Fprintln(out, "Fased Remote is installed with a separate application account.")
	if !started {
		fmt.Fprintln(out, "Start the instance and prepare private access:")
		fmt.Fprintln(out, command("start"))
	} else {
		for _, line := range plan.Instructions {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out, "First visit: get the owner setup code here, then set your password in Fased:")
		fmt.Fprintln(out, command("claim-code"))
		fmt.Fprintln(out, "Already set up? Sign in with your owner password.")
	}
	fmt.Fprintln(out, "Check the instance:")
	fmt.Fprintln(out, command("status"))
	fmt.Fprintln(out, "Keep administrative SSH or provider-console access. SSH and firewall policy have not been changed.")
	if plan.Mode == "tailscale" {
		fmt.Fprintln(out, "After private sign-in and console recovery work, open SSH through Tailscale to review network lockdown:")
		fmt.Fprintln(out, command("harden"))
	}
}

func networkConsent(in io.Reader, out io.Writer) (vpsinstall.NetworkApproval, error) {
	var approval vpsinstall.NetworkApproval
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
			return vpsinstall.NetworkApproval{}, err
		}
		if answer != "y" && answer != "yes" {
			return vpsinstall.NetworkApproval{}, errors.New("network lockdown cancelled; existing access is preserved")
		}
		*question.value = true
	}
	return approval, nil
}

func networkHandoff(out io.Writer, m vpsinstall.Manifest, status vpsinstall.NetworkStatus) {
	if status.Phase == "pending" {
		fmt.Fprintln(out, "Keep this connection open. Open a second SSH TCP connection through Tailscale (no ControlMaster reuse), verify private sign-in, then run before", status.Deadline.UTC().Format(time.RFC3339)+":")
		fmt.Fprintln(out, installationCommand(m.Core, "vps", "confirm", status.AttemptID))
		fmt.Fprintln(out, "Without confirmation, rollback restores the previous SSH policy and removes only Fased's firewall table.")
	}
}

func vpsCommand(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: fased-app vps status|start|stop|claim-code|recover|access|remove|harden|confirm|rollback|network")
	}
	action := args[0]
	switch action {
	case "status", "start", "stop", "claim-code", "recover", "access", "remove", "harden", "confirm", "rollback", "network":
	default:
		return errors.New("unknown vps action")
	}
	f := flag.NewFlagSet("vps "+action, flag.ContinueOnError)
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
	m, err := vpsinstall.Load(os.Getuid())
	if err != nil {
		return err
	}
	system := remoteSystem(in, errOut)
	ctx := context.Background()
	cancel := func() {}
	if action != "recover" {
		ctx, cancel = context.WithTimeout(ctx, remoteOperationTimeout)
	}
	defer cancel()
	target, enrolled, e := vpsEnrollmentTarget(action, m.Data, enrolledTarget)
	if e != nil {
		return e
	}
	if enrolled {
		switch action {
		case "status", "start", "stop", "claim-code", "recover", "access", "remove":
			if action == "remove" {
				dir, e := remoteAccessDirectory()
				if e != nil {
					return e
				}
				if e = privateaccess.System().Remove(ctx, dir); e != nil {
					return e
				}
			}
			if action == "start" || action == "access" {
				// Preserve the original private HTTPS mapping and its exact instance ID.
				var sink io.Writer = io.Discard
				if e = enrolledLifecycle(ctx, target, action, true, true, sink, enrolledSystem(system)); e != nil {
					return e
				}
				url, e := enableEnrolledRemoteAccess(ctx, m, target)
				if e != nil {
					return e
				}
				fmt.Fprintln(out, url)
				return nil
			}
			return enrolledLifecycle(ctx, target, action, true, true, out, enrolledSystem(system))
		}
	}
	switch action {
	case "harden", "confirm", "rollback", "network":
		var status vpsinstall.NetworkStatus
		switch action {
		case "harden":
			approval, e := networkConsent(in, errOut)
			if e != nil {
				return e
			}
			status, err = vpsinstall.BeginNetwork(ctx, m, system, approval)
		case "confirm":
			status, err = vpsinstall.ConfirmNetwork(ctx, m, system, f.Arg(0))
		case "rollback":
			status, err = vpsinstall.RollbackNetwork(ctx, m, system)
		case "network":
			status, err = vpsinstall.InspectNetwork(ctx, m, system)
		}
		if err != nil {
			return err
		}
		networkHandoff(errOut, m, status)
		return json.NewEncoder(out).Encode(status)
	case "status":
		status, err := vpsinstall.Status(ctx, m, system)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(status)
	case "start":
		if err = vpsinstall.Start(ctx, m, system); err != nil {
			return err
		}
		url, err := enableRemoteAccess(ctx, m, system)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, url)
		return nil
	case "stop":
		return vpsinstall.Stop(ctx, m, system)
	case "claim-code":
		return vpsinstall.Claim(ctx, m, system, false, out)
	case "recover":
		return vpsinstall.Claim(ctx, m, system, true, out)
	case "access":
		url, err := enableRemoteAccess(ctx, m, system)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, url)
		return nil
	case "remove":
		dir, err := remoteAccessDirectory()
		if err != nil {
			return err
		}
		if err = privateaccess.System().Remove(ctx, dir); err != nil {
			return err
		}
		if err = vpsinstall.Remove(ctx, m, system); err != nil {
			return err
		}
		fmt.Fprintln(out, "Remote service removed. Instance data, the recovery helper and locked application identity were preserved:", m.Data)
		return nil
	}
	return errors.New("unknown vps action")
}

func validateRuntimeUID(uid, euid int) error {
	if uid <= 0 || euid != uid {
		return errors.New("run Fased as an ordinary OS user; use the installer for privileged OS preparation")
	}
	return nil
}

func enableEnrolledRemoteAccess(ctx context.Context, source vpsinstall.Manifest, target custodyinstall.Manifest) (string, error) {
	if target.Location != "remote" || target.Listen != source.Listen || target.PublicOrigin != source.PublicOrigin || target.Owner != source.OwnerUID {
		return "", errors.New("Remote enrollment binding changed")
	}
	commit, e := custodyinstall.ReadEnrollmentCommit(target)
	if e != nil {
		return "", e
	}
	if target.PublicOrigin == "" {
		return enrolledURL(target), nil
	}
	manager := privateaccess.System()
	plan, e := manager.Plan(ctx, "tailscale", target.Listen)
	if e != nil {
		return "", e
	}
	if plan.Origin != target.PublicOrigin {
		return "", errors.New("Tailscale identity changed; original private origin is preserved")
	}
	dir, e := remoteAccessDirectory()
	if e != nil {
		return "", e
	}
	if e = manager.Enable(ctx, dir, plan, commit.InstanceID); e != nil {
		return "", e
	}
	return plan.Origin, nil
}

// Network recovery owns its pending-fence guard. A lifecycle redirect must not
// prevent that recovery path from reaching its original policy controller.
func vpsEnrollmentTarget(action, data string, lookup func(string) (custodyinstall.Manifest, bool, error)) (custodyinstall.Manifest, bool, error) {
	switch action {
	case "harden", "confirm", "rollback", "network":
		return custodyinstall.Manifest{}, false, nil
	}
	return lookup(data)
}

func remoteAccessPending(m vpsinstall.Manifest, cause error) error {
	return fmt.Errorf("Remote is installed; private access is pending: %w; retry with %s", cause, installationCommand(m.Core, "vps", "access"))
}
