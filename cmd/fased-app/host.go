package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostprep"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"golang.org/x/term"
)

func hostCommand(args []string, out, errOut io.Writer) error {
	paths, err := localinstall.DefaultPaths()
	if err != nil {
		return err
	}
	command := args[0]
	remaining := args[1:]
	action := ""
	if command == "service" {
		if len(remaining) == 0 {
			return errors.New("usage: fased-app service start|stop|status [options]")
		}
		action = remaining[0]
		remaining = remaining[1:]
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(errOut)
	root := f.String("prefix", paths.Root, "private application installation directory")
	data := f.String("data-dir", paths.Data, "private instance state; preserved by uninstall")
	listen := f.String("listen", "127.0.0.1:7331", "loopback instance listener")
	origin := f.String("public-origin", "", "exact private HTTPS reverse-proxy origin")
	desktop := f.String("desktop-binary", "", "optional matching native desktop binary")
	start := f.Bool("start", true, "start the user service after installation")
	autostart := f.Bool("autostart", false, "explicitly enable the user service at login")
	printOnly := f.Bool("print", false, "print the verified local UI URL without opening a window")
	interactive := f.Bool("interactive", false, "guide installation in this terminal")
	profile := f.String("profile", "", "preselected Local/Remote/Connect installation profile")
	access := f.String("access", "local", "local, ssh or tailscale private access")
	vps := f.Bool("vps", false, "require always-on Linux VPS readiness")
	managedRemote := f.Bool("managed-remote", false, "install Ubuntu Remote using a dedicated locked application account")
	packageDir := f.String("bundle", "", "verify and install this platform package inventory")
	verificationOutput := f.String("verification-output", "", "restrict installation/state to paths separate from verified acquisition output")
	bootstrapSequence := f.String("bootstrap-sequence", "", "verified bootstrap release sequence; no version inference")
	bootstrapRelease := f.String("bootstrap-release-sha256", "", "verified bootstrap descriptor identity")
	packageSHA256 := f.String("bundle-sha256", "", "independently obtained SHA-256 of exact bundle.json bytes")
	installRuntime := f.Bool("install-runtime", false, "authorize Ubuntu 24.04's OS package manager to install missing desktop runtime libraries")
	prepareHost := f.Bool("prepare-host", false, "authorize Ubuntu OS prerequisites and the verified restrictive sandbox policy")
	if err = f.Parse(remaining); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *packageSHA256 != "" && (command != "install" || *packageDir == "") {
		return errors.New("--bundle-sha256 requires install --bundle")
	}
	if command != "install" && (*bootstrapSequence != "" || *bootstrapRelease != "") {
		return errors.New("bootstrap release metadata is an installation option")
	}
	if *verificationOutput != "" && command != "install" {
		return errors.New("verification output guard is an installation option")
	}
	if *profile != "" && (*profile != "remote" && *profile != "local" && *profile != "connect" || command != "install" || !*interactive) {
		return errors.New("profile requires interactive Remote installation")
	}
	paths = localinstall.Paths{Root: *root, Data: *data}
	if *managedRemote && command != "install" {
		return errors.New("--managed-remote is an installation option")
	}
	if *managedRemote && !*interactive {
		*vps = true
	}
	budget := 30 * time.Second
	if command == "install" {
		budget = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	if command == "service" || command == "open" || command == "uninstall" || command == "install" {
		target, found, e := enrolledTarget(paths.Data)
		if e != nil {
			return e
		}
		if found {
			if command == "install" {
				return errors.New("this instance is enrolled; use open or service management; original installation is preserved")
			}
			operation := action
			if command == "open" {
				operation = "open"
			}
			if command == "uninstall" {
				operation = "remove"
			}
			return enrolledLifecycle(ctx, target, operation, *prepareHost, *printOnly, out, enrolledSystem(&custodyinstall.System{In: os.Stdin, Out: errOut}))
		}
	}
	switch command {
	case "install":
		var wizard *bufio.Reader
		if *interactive {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("interactive installation requires a terminal")
			}
			r := bufio.NewReader(os.Stdin)
			wizard = r
			choice := *profile
			if choice == "" {
				var e error
				choice, e = installationChoice(r, errOut)
				if e != nil {
					return e
				}
			} else {
				fmt.Fprintln(errOut, "Fased", strings.Title(choice), "setup")
			}
			if choice == "update" {
				return ordinaryUpdateCommand(nil, os.Stdin, out, errOut)
			}
			if choice == "connect" {
				address, e := installationAnswer(r, errOut, "Existing instance URL: ")
				if e != nil {
					return e
				}
				return connectCommand([]string{"--url", address}, out, errOut)
			}
			*vps = choice == "remote"
			if *vps {
				value, e := installationPrivateAccessChoice(r, errOut)
				if e != nil {
					return e
				}
				*access = value
			}
			*managedRemote = false
			if *vps {
				dedicated, e := installationManagedRemote(r, errOut)
				if e != nil {
					return e
				}
				*managedRemote = dedicated
			}
			autostartChoice, e := installationAutostart(r, errOut, *vps)
			if e != nil {
				return e
			}
			*autostart = autostartChoice
		}
		if *managedRemote {
			explicitPaths := false
			f.Visit(func(value *flag.Flag) {
				if value.Name == "prefix" || value.Name == "data-dir" {
					explicitPaths = true
				}
			})
			if err := validateManagedRemote(*packageDir, *desktop != "", *autostart, explicitPaths); err != nil {
				return err
			}
		}
		selectedPaths := paths
		if *managedRemote {
			selectedPaths.Root = vpsinstall.Root(os.Getuid())
			selectedPaths.Data = selectedPaths.Root + "/app-state"
		}
		if err = installationOutputSeparation(*verificationOutput, selectedPaths); err != nil {
			return err
		}
		engine, err := os.Executable()
		if err != nil {
			return err
		}
		var inventory bundle.Manifest
		if *packageDir != "" {
			var canonical string
			inventory, canonical, err = verifiedInstallationBundle(*packageDir, engine, *desktop, *packageSHA256)
			if err != nil {
				return err
			}
			*packageDir = canonical
			if inventory.Desktop {
				*desktop = filepath.Join(canonical, localinstall.DesktopName())
			}
		}
		sequence, release, e := installationReleaseAdmission(*bootstrapSequence, *bootstrapRelease, *verificationOutput, *packageSHA256)
		if e != nil {
			return e
		}
		if *managedRemote && inventory.Desktop {
			return errors.New("managed Remote requires a verified headless bundle")
		}
		if *vps {
			if *access != "ssh" && *access != "tailscale" {
				return errors.New("Remote installation requires --access ssh or tailscale")
			}
			if runtime.GOOS != "linux" || !*autostart {
				return errors.New("VPS installation requires Linux and explicit --autostart")
			}
			if !*managedRemote {
				if err = checkLinger(ctx); err != nil {
					return err
				}
			}
		}
		var accessWizard *bufio.Reader
		if *vps {
			accessWizard = wizard
		}
		plan, err := installationPrivateAccess(ctx, accessWizard, errOut, *access, *listen, privateaccess.System().Plan, hostprep.PrepareTailscale)
		if err != nil {
			return err
		}
		plan = installationSSHPlan(plan, os.Getenv("SSH_CONNECTION"))
		*access = plan.Mode
		if *access == "tailscale" {
			if *origin != "" && *origin != plan.Origin {
				return errors.New("configured origin differs from this Tailscale device")
			}
			*origin = plan.Origin
		}
		if *managedRemote && plan.Mode != "tailscale" && *origin != "" {
			return errors.New("managed Remote HTTPS origin is set by the Tailscale access choice")
		}
		if err = localinstall.Validate(paths, localinstall.Options{Listen: *listen, PublicOrigin: *origin}); err != nil {
			return err
		}
		if *interactive && !*prepareHost && (needsHostPreparation(*desktop) || *managedRemote) {
			prompt := "Authorize required Ubuntu preparation with sudo? [y/N]: "
			if *managedRemote {
				prompt = "Authorize Ubuntu preparation, isolated service and bounded update maintenance with sudo? [y/N]: "
			}
			fmt.Fprint(errOut, prompt)
			choice, _ := wizard.ReadString('\n')
			*prepareHost = strings.EqualFold(strings.TrimSpace(choice), "y")
		}
		if *managedRemote {
			return managedRemoteInstall(ctx, engine, inventory.Files[localinstall.EngineName()].SHA256, *listen, plan, *prepareHost, *start, out, errOut, installationAdmission{sequence: sequence, release: release, prepareUpdates: *interactive && *prepareHost})
		}
		if err = desktopRuntime(ctx, *desktop, *packageDir, *data, *installRuntime, *prepareHost, os.Stdin, errOut); err != nil {
			return err
		}
		m, err := localinstall.Install(ctx, paths, localinstall.Options{ApplicationSequence: sequence, ApplicationReleaseSHA256: release, Engine: engine, EngineSHA256: inventory.Files[localinstall.EngineName()].SHA256, Desktop: *desktop, DesktopSHA256: inventory.Files[localinstall.DesktopName()].SHA256, Listen: *listen, PublicOrigin: *origin, Start: *start, Autostart: *autostart, Location: installLocation(*vps)})
		if err != nil {
			return err
		}
		defaultPaths, e := localinstall.DefaultPaths()
		if e != nil {
			return e
		}
		if m.Root == defaultPaths.Root && m.Data == defaultPaths.Data {
			if e := installCommand(filepath.Join(m.Root, localinstall.EngineName()), m.Root, m.Data, false, errOut); e != nil {
				return e
			}
		}
		url := ""
		if *start {
			url, err = waitForCore(ctx, *data)
			if err != nil {
				return err
			}
			if *access == "tailscale" {
				c, e := core.NewClient(ctx, *data)
				if e != nil {
					return e
				}
				id := c.Endpoint.InstanceID
				c.Close()
				if e = privateaccess.System().Enable(ctx, m.Root, plan, id); e != nil {
					return installationAccessPending(m, e)
				}
				url = plan.Origin
			}
		}
		maintenance := "manual-only"
		if !*vps {
			maintenance = localinstall.PrepareInstallationMaintenance(ctx, m, *start, errOut)
		}
		if *interactive {
			setupURL := ""
			if url != "" {
				setupURL, err = localOwnerSetupLink(ctx, m.Data, url)
				if err != nil {
					return err
				}
			}
			installationHandoff(errOut, m, plan, setupURL)
			if !*vps && *start && setupURL != "" {
				openSetupBrowser(setupURL, errOut)
			}
		}
		return json.NewEncoder(out).Encode(map[string]any{"maintenance": maintenance, "location": installLocation(*vps), "installed": true, "application": filepath.Join(m.Root, localinstall.EngineName()), "data": m.Data, "autostart": m.Autostart, "url": url, "access": plan, "walletsConnected": false})
	case "uninstall":
		manifest, manifestErr := localinstall.Load(paths)
		if err = localinstall.Uninstall(ctx, paths); err != nil {
			return err
		}
		if manifestErr == nil {
			if e := removeCommand(filepath.Join(manifest.Root, localinstall.EngineName()), manifest.Root, manifest.Data, false); e != nil {
				return e
			}
		}
		fmt.Fprintln(out, "Application removed. Instance data was preserved:", *data)
		return nil
	case "service":
		switch action {
		case "start":
			if err = localinstall.Start(ctx, paths); err != nil {
				return err
			}
			url, err := waitForCore(ctx, *data)
			if err != nil {
				return err
			}
			if m, e := localinstall.Load(paths); e == nil && m.Location != "remote" {
				localinstall.PrepareInstallationMaintenance(ctx, m, true, errOut)
			}
			setupURL, e := localOwnerSetupLink(ctx, *data, url)
			if e != nil {
				return e
			}
			fmt.Fprintln(errOut, setupURL)
			fmt.Fprintln(out, url)
			return nil
		case "stop":
			return localinstall.Stop(ctx, paths)
		case "status":
			state, err := localinstall.Status(ctx, paths)
			if err != nil {
				return err
			}
			if state.Active {
				state.URL, _ = waitForCore(ctx, *data)
			}
			return json.NewEncoder(out).Encode(state)
		default:
			return errors.New("service action must be start, stop or status")
		}
	case "open":
		if err = localinstall.Start(ctx, paths); err != nil {
			return err
		}
		url, err := waitForCore(ctx, *data)
		if err != nil {
			return err
		}
		url, err = localOwnerSetupLink(ctx, *data, url)
		if err != nil {
			return err
		}
		if *printOnly {
			fmt.Fprintln(out, url)
			return nil
		}
		m, err := localinstall.Load(paths)
		if err != nil {
			return err
		}
		var process *exec.Cmd
		if m.Files[localinstall.DesktopName()] != "" {
			process = exec.Command(filepath.Join(m.Root, localinstall.DesktopName()), "--data-dir", m.Data)
		} else {
			switch runtime.GOOS {
			case "linux":
				process = exec.Command("xdg-open", url)
			case "darwin":
				process = exec.Command("open", url)
			case "windows":
				process = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
			default:
				return errors.New("no browser opener on this platform")
			}
		}
		if err = process.Start(); err != nil {
			return errors.New("cannot open the window; use open --print for the private URL")
		}
		_ = process.Process.Release()
		fmt.Fprintln(out, url)
		return nil
	}
	return errors.New("unknown installation command")
}

// Bind installation to the running core and the exact verified inventory.
// An optional external digest pins bundle.json, not a publisher/release channel.
func verifiedInstallationBundle(directory, engine, desktop, pin string) (bundle.Manifest, string, error) {
	var empty bundle.Manifest
	directory, err := filepath.Abs(directory)
	if err != nil {
		return empty, "", err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return empty, "", err
	}
	m, err := bundle.VerifyPinned(directory, pin)
	if err != nil {
		return empty, "", err
	}
	if m.Version != core.Version {
		return empty, "", errors.New("bundle version differs from the running core")
	}
	actual, err := filepath.EvalSymlinks(engine)
	if err != nil {
		return empty, "", err
	}
	actual, err = filepath.Abs(actual)
	if err != nil || actual != filepath.Join(directory, localinstall.EngineName()) {
		return empty, "", errors.New("run the core from the selected verified package")
	}
	if desktop != "" {
		actual, err = filepath.EvalSymlinks(desktop)
		if err == nil {
			actual, err = filepath.Abs(actual)
		}
		if !m.Desktop || err != nil || actual != filepath.Join(directory, localinstall.DesktopName()) {
			return empty, "", errors.New("desktop binary must belong to the selected verified package")
		}
	}
	return m, directory, nil
}

// Keep the terminal handoff on the installed engine and its existing state.
// Access instructions are produced by the same planner that configured it.
func installationHandoff(out io.Writer, m localinstall.Manifest, plan privateaccess.Plan, readyURL string) {
	engine := filepath.Join(m.Root, localinstall.EngineName())
	service := func(action string) string {
		return installationCommand(engine, "service", action, "--prefix", m.Root, "--data-dir", m.Data)
	}
	fmt.Fprintln(out, "Fased is installed.")
	if readyURL == "" {
		fmt.Fprintln(out, "Start the installed instance:")
		fmt.Fprintln(out, service("start"))
		if plan.Mode == "tailscale" {
			fmt.Fprintln(out, "Then enable private access:")
			fmt.Fprintln(out, installationCommand(engine, "access", "enable", "--prefix", m.Root, "--data-dir", m.Data))
		}
	} else {
		for _, instruction := range plan.Instructions {
			fmt.Fprintln(out, instruction)
		}
		fmt.Fprintln(out, "Open Fased and choose your password:")
		fmt.Fprintln(out, readyURL)
		fmt.Fprintln(out, "Already set up? Sign in with your existing owner password.")
	}
	fmt.Fprintln(out, "Check this instance:")
	fmt.Fprintln(out, service("status"))
}

// Quote every argument for the platform's normal terminal; paths may contain
// spaces, quotes or shell syntax. This only renders commands, never runs them.
func installationCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if runtime.GOOS == "windows" {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	command := strings.Join(quoted, " ")
	if runtime.GOOS == "windows" {
		return "& " + command
	}
	return command
}
func waitForCore(ctx context.Context, dir string) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := core.NewClient(deadline, dir)
		if err == nil {
			url := client.Endpoint.URL
			client.Close()
			return url, nil
		}
		select {
		case <-deadline.Done():
			return "", errors.New("core did not become ready; inspect the user service journal")
		case <-ticker.C:
		}
	}
}

func installLocation(remote bool) string {
	if remote {
		return "remote"
	}
	return "local"
}
func installationAnswer(r *bufio.Reader, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	s, e := r.ReadString('\n')
	if e != nil {
		return "", errors.New("installation cancelled before saving")
	}
	return strings.TrimSpace(s), nil
}
func installationChoice(r *bufio.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Fased\n  Local   — install on this computer\n  Remote  — install on this server\n  Connect — use an existing Remote instance\n  Update  — update this installation")
	for {
		s, e := installationAnswer(r, out, "Choose [local/remote/connect/update, default local]: ")
		if e != nil {
			return "", e
		}
		switch strings.ToLower(s) {
		case "", "local":
			return "local", nil
		case "remote", "vps":
			return "remote", nil
		case "connect":
			return "connect", nil
		case "update":
			return "update", nil
		}
		fmt.Fprintln(out, "Choose Local, Remote, Connect or Update.")
	}
}

func installationAutostart(r *bufio.Reader, out io.Writer, remote bool) (bool, error) {
	prompt := "Start Fased at login? [y/N]: "
	if remote {
		prompt = "Keep Fased running after logout and reboot? [Y/n]: "
	}
	for {
		answer, err := installationAnswer(r, out, prompt)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return remote, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(out, "Choose yes or no.")
	}
}

// Private access is planned before host preparation or installation. A changed
// transport requires another explicit wizard choice, never an automatic fallback.
func installationPrivateAccess(ctx context.Context, in *bufio.Reader, out io.Writer, mode, listen string, planner func(context.Context, string, string) (privateaccess.Plan, error), preparer ...func(context.Context, *bufio.Reader, io.Writer) error) (privateaccess.Plan, error) {
	// Changing access cannot repair an invalid listener. Reuse the pure local
	// planner's target validation before any readiness call or retry prompt.
	if _, err := (privateaccess.Manager{}).Plan(ctx, "local", listen); err != nil {
		return privateaccess.Plan{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return privateaccess.Plan{}, err
		}
		var plan privateaccess.Plan
		var err error
		if mode == "tailscale" && in != nil && len(preparer) > 0 {
			err = preparer[0](ctx, in, out)
		}
		if err == nil {
			plan, err = planner(ctx, mode, listen)
		}
		if err == nil {
			return plan, nil
		}
		if in == nil {
			return privateaccess.Plan{}, err
		}
		if cancelErr := ctx.Err(); cancelErr != nil {
			return privateaccess.Plan{}, cancelErr
		}
		fmt.Fprintln(out, "Private access is not ready:", err)
		fmt.Fprintln(out, "Prepare Tailscale and retry, or choose SSH. Installation has not started.")
		mode, err = installationPrivateAccessChoice(in, out)
		if err != nil {
			return privateaccess.Plan{}, err
		}
	}
}

func installationPrivateAccessChoice(in *bufio.Reader, out io.Writer) (string, error) {
	for {
		mode, err := installationAnswer(in, out, "Private access [tailscale/ssh, default tailscale]: ")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(mode) {
		case "", "tailscale":
			return "tailscale", nil
		case "ssh":
			return "ssh", nil
		}
		fmt.Fprintln(out, "Choose tailscale or ssh.")
	}
}

func installationAccessPending(m localinstall.Manifest, cause error) error {
	retry := installationCommand(filepath.Join(m.Root, localinstall.EngineName()), "access", "enable", "--prefix", m.Root, "--data-dir", m.Data)
	return fmt.Errorf("Fased is installed; private access is pending: %w; retry with %s", cause, retry)
}

// The verified bootstrap supplies only a restriction, never a target/authority.
// This runs after profile choice and before package/host/access mutation.
func installationOutputSeparation(output string, selected localinstall.Paths) error {
	if output == "" {
		return nil
	}
	if !filepath.IsAbs(output) || filepath.Clean(output) != output || output == string(filepath.Separator) {
		return errors.New("invalid verified acquisition output guard")
	}
	for _, path := range []string{selected.Root, selected.Data} {
		absolute, e := filepath.Abs(path)
		if e != nil {
			return e
		}
		for _, pair := range [][2]string{{output, absolute}, {absolute, output}} {
			rel, e := filepath.Rel(pair[0], pair[1])
			if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return errors.New("selected installation/state must be separate from the verified acquisition output")
			}
		}
	}
	return nil
}

type installationAdmission struct {
	sequence       uint64
	release        string
	prepareUpdates bool
}

// These are restricted verifier handoff data, not proof that an executable
// downloaded independently is trusted. Full pinned bundle admission precedes
// this function; initial executable trust still belongs to the verifier.
func installationReleaseAdmission(sequence, release, output, pinnedBundle string) (uint64, string, error) {
	if sequence == "" && release == "" {
		return 0, "", nil
	}
	n, e := strconv.ParseUint(sequence, 10, 64)
	b, he := hex.DecodeString(release)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != sequence || he != nil || len(b) != 32 || hex.EncodeToString(b) != release || output == "" || pinnedBundle == "" {
		return 0, "", errors.New("verified bootstrap release metadata requires the exact pinned bundle and output guard")
	}
	return n, release, nil
}
