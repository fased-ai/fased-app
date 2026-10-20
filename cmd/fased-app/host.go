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
	listen := f.String("listen", "127.0.0.1:7331", "private core listener")
	origin := f.String("public-origin", "", "exact private HTTPS reverse-proxy origin")
	desktop := f.String("desktop-binary", "", "optional matching native desktop binary")
	start := f.Bool("start", true, "start the user service after installation")
	autostart := f.Bool("autostart", false, "explicitly enable the user service at login")
	printOnly := f.Bool("print", false, "print the verified local UI URL without opening a window")
	interactive := f.Bool("interactive", false, "guide installation in this terminal")
	access := f.String("access", "local", "local, ssh or tailscale private access")
	vps := f.Bool("vps", false, "require always-on Linux VPS readiness")
	packageDir := f.String("bundle", "", "verify and install this platform package inventory")
	installRuntime := f.Bool("install-runtime", false, "authorize Ubuntu 24.04's OS package manager to install missing desktop runtime libraries")
	prepareHost := f.Bool("prepare-host", false, "authorize Ubuntu OS prerequisites and the verified restrictive sandbox policy")
	if err = f.Parse(remaining); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
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
			fmt.Fprint(errOut, "Install on this computer or a VPS? [local/vps, default local]: ")
			choice, _ := r.ReadString('\n')
			choice = strings.TrimSpace(choice)
			if choice != "" && choice != "local" && choice != "vps" {
				return errors.New("choose local or vps")
			}
			*vps = choice == "vps"
			if *vps {
				fmt.Fprint(errOut, "Private access [tailscale/ssh, default tailscale]: ")
				choice, _ = r.ReadString('\n')
				*access = strings.TrimSpace(choice)
				if *access == "" {
					*access = "tailscale"
				}
			}
			fmt.Fprint(errOut, "Enable background execution at login (VPS requires it)? [y/N]: ")
			choice, _ = r.ReadString('\n')
			*autostart = strings.EqualFold(strings.TrimSpace(choice), "y")
		}
		if *vps {
			if runtime.GOOS != "linux" || !*autostart {
				return errors.New("VPS installation requires Linux and explicit --autostart")
			}
			if err = checkLinger(ctx); err != nil {
				return err
			}
		}
		engine, err := os.Executable()
		if err != nil {
			return err
		}
		if *packageDir != "" {
			inventory, e := bundle.Verify(*packageDir)
			if e != nil {
				return e
			}
			expected, _ := filepath.Abs(filepath.Join(*packageDir, localinstall.EngineName()))
			actual, _ := filepath.EvalSymlinks(engine)
			if actual != expected {
				return errors.New("run the core from the selected verified package")
			}
			if inventory.Desktop {
				*desktop = filepath.Join(*packageDir, localinstall.DesktopName())
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
		m, err := localinstall.Install(ctx, paths, localinstall.Options{Engine: engine, Desktop: *desktop, Listen: *listen, PublicOrigin: *origin, Start: *start, Autostart: *autostart})
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
		return json.NewEncoder(out).Encode(map[string]any{"installed": true, "application": filepath.Join(m.Root, localinstall.EngineName()), "data": m.Data, "autostart": m.Autostart, "url": url, "access": plan, "walletsConnected": false})
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
