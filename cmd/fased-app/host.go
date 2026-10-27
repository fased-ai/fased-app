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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
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
	access := f.String("access", "local", "local, ssh or tailscale private access")
	vps := f.Bool("vps", false, "require always-on Linux VPS readiness")
	packageDir := f.String("bundle", "", "verify and install this platform package inventory")
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
	paths = localinstall.Paths{Root: *root, Data: *data}
	budget := 30 * time.Second
	if command == "install" {
		budget = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	switch command {
	case "install":
		var wizard *bufio.Reader
		if *interactive {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("interactive installation requires a terminal")
			}
			r := bufio.NewReader(os.Stdin)
			wizard = r
			choice, e := installationChoice(r, errOut)
			if e != nil {
				return e
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
				value, e := installationAnswer(r, errOut, "Private access [tailscale/ssh, default tailscale]: ")
				if e != nil {
					return e
				}
				*access = value
				if *access == "" {
					*access = "tailscale"
				}
				if *access != "tailscale" && *access != "ssh" {
					return errors.New("choose tailscale or ssh")
				}
			}
			autostartChoice, e := installationAutostart(r, errOut, *vps)
			if e != nil {
				return e
			}
			*autostart = autostartChoice
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
		if *vps {
			if *access != "ssh" && *access != "tailscale" {
				return errors.New("Remote installation requires --access ssh or tailscale")
			}
			if runtime.GOOS != "linux" || !*autostart {
				return errors.New("VPS installation requires Linux and explicit --autostart")
			}
			if err = checkLinger(ctx); err != nil {
				return err
			}
		}
		plan, err := privateaccess.System().Plan(ctx, *access, *listen)
		if err != nil {
			return err
		}
		if *access == "tailscale" {
			if *origin != "" && *origin != plan.Origin {
				return errors.New("configured origin differs from this Tailscale device")
			}
			*origin = plan.Origin
		}
		if err = localinstall.Validate(paths, localinstall.Options{Listen: *listen, PublicOrigin: *origin}); err != nil {
			return err
		}
		if *interactive && !*prepareHost && needsHostPreparation(*desktop) {
			fmt.Fprint(errOut, "Ubuntu needs OS prerequisites and its restrictive Bubblewrap policy. Authorize automatic preparation with sudo? [y/N]: ")
			choice, _ := wizard.ReadString('\n')
			*prepareHost = strings.EqualFold(strings.TrimSpace(choice), "y")
		}
		if err = desktopRuntime(ctx, *desktop, *packageDir, *data, *installRuntime, *prepareHost, os.Stdin, errOut); err != nil {
			return err
		}
		m, err := localinstall.Install(ctx, paths, localinstall.Options{Engine: engine, EngineSHA256: inventory.Files[localinstall.EngineName()].SHA256, Desktop: *desktop, DesktopSHA256: inventory.Files[localinstall.DesktopName()].SHA256, Listen: *listen, PublicOrigin: *origin, Start: *start, Autostart: *autostart, Location: installLocation(*vps)})
		if err != nil {
			return err
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
					return e
				}
				url = plan.Origin
			}
		}
		if *interactive {
			installationHandoff(errOut, m, plan, url)
		}
		return json.NewEncoder(out).Encode(map[string]any{"location": installLocation(*vps), "installed": true, "application": filepath.Join(m.Root, localinstall.EngineName()), "data": m.Data, "autostart": m.Autostart, "url": url, "access": plan, "walletsConnected": false})
	case "uninstall":
		if err = localinstall.Uninstall(ctx, paths); err != nil {
			return err
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
		fmt.Fprintln(out, "First visit: run this on the installed host for an owner setup code, then choose your password in the private UI:")
		fmt.Fprintln(out, installationCommand(engine, "claim-code", "--data-dir", m.Data))
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
	fmt.Fprintln(out, "Fased\n  Local   — install on this computer\n  Remote  — install on this server\n  Connect — use an existing Remote instance")
	for {
		s, e := installationAnswer(r, out, "Choose [local/remote/connect, default local]: ")
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
		}
		fmt.Fprintln(out, "Choose local, remote or connect.")
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
