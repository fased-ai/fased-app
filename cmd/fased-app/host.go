package main

import (
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
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
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
	if err = f.Parse(remaining); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	paths = localinstall.Paths{Root: *root, Data: *data}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch command {
	case "install":
		engine, err := os.Executable()
		if err != nil {
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
		}
		return json.NewEncoder(out).Encode(map[string]any{"installed": true, "application": filepath.Join(m.Root, "fased-app"), "data": m.Data, "autostart": m.Autostart, "url": url, "walletsConnected": false})
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
		if m.Files["fased-desktop"] != "" {
			process = exec.Command(filepath.Join(m.Root, "fased-desktop"), "--data-dir", m.Data)
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
