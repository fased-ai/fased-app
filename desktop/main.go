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
	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/wailsapp/wails/v2"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func reportDesktopStartupError(dataDir, message string, err error) {
	full := message
	if err != nil {
		full = fmt.Sprintf("%s: %v", message, err)
	}
	fmt.Fprintln(os.Stderr, full)
	if dataDir != "" {
		if mkErr := os.MkdirAll(dataDir, 0700); mkErr == nil {
			line := fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), full)
			_ = os.WriteFile(filepath.Join(dataDir, "desktop-last-error.log"), []byte(line), 0600)
		}
	}
}

func main() {
	dir, err := core.DefaultDataDir()
	if err != nil {
		reportDesktopStartupError("", "Cannot locate Fased instance data", err)
		os.Exit(1)
	}
	state := flag.String("data-dir", dir, "private instance state directory")
	remote := flag.String("connect", "", "open an existing Remote Fased instance without installing locally")
	ownerLink := flag.String("wallet-owner", "", "open a separate protected wallet owner window from a public Fased link")
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
	if *ownerLink != "" {
		if *remote != "" {
			fmt.Fprintln(os.Stderr, "wallet target must come from its public Fased link")
			os.Exit(1)
		}
		if err := ownerWindow(*ownerLink); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *remote != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		target, e := connection.Probe(ctx, *remote, "")
		if e == nil {
			e = connection.OpenBrowser(target.URL, target.InstanceID)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	var firstPackage string
	var firstPackageUnsigned bool
	if paths, err := localinstall.DefaultPaths(); err == nil && paths.Data == *state {
		allowSource, fenceErr := desktop.OriginalLocalStartAllowed(paths.Data, os.Getuid())
		if fenceErr != nil {
			fmt.Fprintln(os.Stderr, "Local installation needs attention:", fenceErr)
		}
		if allowSource {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			installed, loadErr := localinstall.Load(paths)
			if errors.Is(loadErr, os.ErrNotExist) {
				self, e := os.Executable()
				if e == nil {
					packageDir := filepath.Dir(self)
					if m, e := bundle.Verify(packageDir); e == nil && m.Desktop {
						firstPackage = packageDir
						firstPackageUnsigned = m.UnsignedEngineering
					}
				}
			} else if loadErr == nil {
				startedUpgraded := false
				if self, e := os.Executable(); e == nil {
					packageDir := filepath.Dir(self)
					if m, e := bundle.Verify(packageDir); e == nil && m.Desktop && !m.UnsignedEngineering {
						pkgEngine := m.Files[localinstall.EngineName()].SHA256
						if pkgEngine != "" && installed.Files[localinstall.EngineName()] != pkgEngine {
							signerPath := ""
							signerSHA := m.Files["fased-signer"].SHA256
							if signerSHA != "" {
								signerPath = filepath.Join(packageDir, "fased-signer")
							}
							if _, upErr := localinstall.Install(ctx, paths, localinstall.Options{
								Engine:       filepath.Join(packageDir, localinstall.EngineName()),
								Desktop:      filepath.Join(packageDir, localinstall.DesktopName()),
								Signer:       signerPath,
								SignerSHA256: signerSHA,
								Listen:       installed.Listen,
								Autostart:    installed.Autostart,
								Start:        true,
								Upgrade:      true,
								Location:     installed.Location,
							}); upErr != nil {
								fmt.Fprintln(os.Stderr, "Local desktop core upgrade failed:", upErr)
							} else {
								startedUpgraded = true
							}
						}
					}
				}
				if !startedUpgraded {
					_ = localinstall.Start(ctx, paths)
				}
			} else {
				_ = localinstall.Start(ctx, paths)
			}
			cancel()
		}
	}
	// This window owns no engine process. Closing it never stops the OS service.
	options := window.Options(*state)
	if firstPackage != "" {
		options.OnStartup = func(ctx context.Context) {
			// Linux and Windows question dialogs return Yes/No and ignore
			// custom button labels; use those supported responses on all OSes.
			unsignedNotice := ""
			if firstPackageUnsigned {
				unsignedNotice = " This is an unsigned engineering package."
			}
			answer, e := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{Type: wailsruntime.QuestionDialog, Title: "Set up Fased", Message: "Install Fased on this computer? Choose Yes for Local installation or No to connect to an existing Remote instance. Local installation starts Fased in the background. Login autostart is off." + unsignedNotice + hostPreparationNotice(), Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No"})
			if e != nil {
				wailsruntime.Quit(ctx)
				return
			}
			if answer != "Yes" {
				return
			}
			paths, e := localinstall.DefaultPaths()
			terminal := false
			if e == nil {
				terminal, e = prepareDesktopHost(ctx, firstPackage, paths)
				if e == nil && !terminal {
					verified, verifyErr := bundle.Verify(firstPackage)
					if verifyErr != nil {
						e = verifyErr
					} else {
						signerPath := ""
						if verified.Files["fased-signer"].SHA256 != "" {
							signerPath = filepath.Join(firstPackage, "fased-signer")
						}
						var installed localinstall.Manifest
						installed, e = localinstall.Install(ctx, paths, localinstall.Options{Engine: filepath.Join(firstPackage, localinstall.EngineName()), Desktop: filepath.Join(firstPackage, localinstall.DesktopName()), Signer: signerPath, SignerSHA256: verified.Files["fased-signer"].SHA256, Listen: "127.0.0.1:7331", Start: true, Location: "local"})
						if e == nil {
							localinstall.PrepareInstallationMaintenance(ctx, installed, true, os.Stderr)
						}
					}
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
		reportDesktopStartupError(*state, "Desktop window unavailable", err)
		os.Exit(1)
	}
}
