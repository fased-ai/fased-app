package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/desktop/internal/window"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/wailsapp/wails/v2"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func main() {
	dir, err := core.DefaultDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot locate private core state")
		os.Exit(1)
	}
	state := flag.String("data-dir", dir, "private instance state directory")
	version := flag.Bool("version", false, "show version")
	flag.Parse()
	if *version {
		fmt.Println(core.Version)
		return
	}
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(1)
	}
	var firstPackage string
	if paths, err := localinstall.DefaultPaths(); err == nil && paths.Data == *state {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if _, loadErr := localinstall.Load(paths); errors.Is(loadErr, os.ErrNotExist) {
			self, e := os.Executable()
			if e == nil {
				packageDir := filepath.Dir(self)
				if m, e := bundle.Verify(packageDir); e == nil && m.Desktop {
					firstPackage = packageDir
				}
			}
		} else {
			_ = localinstall.Start(ctx, paths)
		}
		cancel()
	}
	// This window owns no engine process. Closing it never stops the OS service.
	options := window.Options(*state)
	if firstPackage != "" {
		options.OnStartup = func(ctx context.Context) {
			answer, e := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{Type: wailsruntime.QuestionDialog, Title: "Install Fased App", Message: "Install the private core for this OS user and start it in the background? No wallet, RPC or AI account is required. Closing the window leaves the engine running. Login autostart is off. This is an unsigned engineering package." + hostPreparationNotice(), Buttons: []string{"Install", "Cancel"}, DefaultButton: "Install", CancelButton: "Cancel"})
			if e != nil || answer != "Install" {
				wailsruntime.Quit(ctx)
				return
			}
			paths, e := localinstall.DefaultPaths()
			terminal := false
			if e == nil {
				terminal, e = prepareDesktopHost(ctx, firstPackage, paths)
				if e == nil && !terminal {
					_, e = localinstall.Install(ctx, paths, localinstall.Options{Engine: filepath.Join(firstPackage, localinstall.EngineName()), Desktop: filepath.Join(firstPackage, localinstall.DesktopName()), Listen: "127.0.0.1:7331", Start: true})
				}
			}
			if e != nil {
				_, _ = wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{Type: wailsruntime.ErrorDialog, Title: "Installation needs attention", Message: e.Error()})
				wailsruntime.Quit(ctx)
				return
			}
			ready := false
			wait := 8 * time.Second
			if terminal {
				wait = 10 * time.Minute
			}
			for deadline := time.Now().Add(wait); time.Now().Before(deadline) && ctx.Err() == nil; {
				c, err := core.NewClient(ctx, paths.Data)
				if err == nil {
					c.Close()
					ready = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !ready {
				_, _ = wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{Type: wailsruntime.ErrorDialog, Title: "Core needs attention", Message: "The background service did not become ready. Check fased-app service status from this OS account."})
				wailsruntime.Quit(ctx)
				return
			}
			wailsruntime.WindowReload(ctx)
		}
	}
	err = wails.Run(options)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Desktop window unavailable:", err)
		os.Exit(1)
	}
}
