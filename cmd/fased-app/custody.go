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
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func custodyCommand(args []string, in *os.File, out, errOut io.Writer) (result error) {
	if len(args) > 0 && args[0] == "updates" {
		return custodyUpdatesCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "bootstrap" {
		return bootstrapCommand(args[1:], in, out, errOut)
	}
	if len(args) == 0 {
		return errors.New("usage: fased-app custody install|status|setup|recover|approve|updates|accounts|login|remove --disposable-fixture [options]")
	}
	if runtime.GOOS != "linux" || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("custody fixture requires an ordinary Linux owner; real wallets remain disabled")
	}
	f := flag.NewFlagSet("custody "+args[0], flag.ContinueOnError)
	f.SetOutput(errOut)
	disposable := f.Bool("disposable-fixture", false, "confirm unfunded disposable keys only")
	prepare := f.Bool("prepare-host", false, "authorize dedicated locked OS identities, immutable files and system services")
	key := f.String("signer-binary", "", "matching engineering fased-signer executable")
	listen := f.String("listen", "127.0.0.1:7332", "managed private loopback listener")
	file := f.String("backup-file", "", "owner-selected private encrypted backup file")
	proposalFile := f.String("proposal-file", "", "owner-selected public proposal JSON")
	approvalStdin := f.Bool("approval-stdin", false, "fixture CI only: approval challenge through stdin")
	requestStdin := f.Bool("request-stdin", false, "public account record JSON from stdin")
	passwordStdin := f.Bool("password-stdin", false, "scripted fixture passwords through private stdin only")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 || !*disposable {
		return errors.New("explicit --disposable-fixture confirmation is required; real wallets unavailable")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	system := &custodyinstall.System{In: in, Out: errOut}
	if args[0] == "install" {
		if *key == "" {
			return errors.New("choose a matching --signer-binary before installation")
		}
		engine, e := os.Executable()
		if e != nil {
			return e
		}
		signerPath, e := filepath.Abs(*key)
		if e != nil {
			return e
		}
		installCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		m, e := custodyinstall.Install(installCtx, custodyinstall.Options{Core: engine, Signer: signerPath, Listen: *listen, PrepareHost: *prepare, Disposable: *disposable}, system)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]any{"installed": true, "manifest": m, "url": "http://" + m.Listen, "fixtureOnly": true, "walletSigner": false, "financialExecution": false, "autostart": false})
	}
	m, e := custodyinstall.Load(os.Getuid())
	if e != nil {
		return e
	}
	if e = custodyinstall.Verify(m); e != nil {
		return e
	}
	if args[0] != "remove" && args[0] != "status" {
		lock, le := custodyinstall.OwnerLock()
		if le != nil {
			return le
		}
		defer lock.Close()
	}
	switch args[0] {
	case "status":
		active := custodyinstall.Runtime(m) == nil
		return json.NewEncoder(out).Encode(map[string]any{"installed": true, "active": active, "manifest": m, "url": "http://" + m.Listen, "walletSigner": false, "financialExecution": false})
	case "remove":
		if !*prepare {
			return errors.New("removing system service definitions requires explicit --prepare-host consent")
		}
		if e = custodyinstall.Remove(ctx, m, system); e != nil {
			return e
		}
		fmt.Fprintln(out, "Managed service definitions removed; disabled identities, instance data, signer keys and recovery state preserved at", m.Root)
		return nil
	case "accounts", "login":
		if !*prepare {
			return errors.New("managed CLI access requires explicit --prepare-host authorization for the OS identity switch")
		}
		return system.AccountsHelper(ctx, m, args[0] == "login", *requestStdin, *passwordStdin, out)
	case "approve":
		if *proposalFile == "" {
			return errors.New("choose --proposal-file containing a public proposal")
		}
		command := []string{"approve", "--config", m.Root + "/identity.json", "--disposable-fixture", "--file", *proposalFile}
		if *approvalStdin {
			command = append(command, "--approval-stdin")
		}
		if *passwordStdin {
			command = append(command, "--password-stdin")
		}
		child := exec.CommandContext(ctx, m.Root+"/fased-signer", command...)
		child.Stdin = in
		child.Stdout = out
		child.Stderr = errOut
		return child.Run()
	case "setup", "recover":
		if *file == "" {
			return errors.New("choose --backup-file in a private owner directory")
		}
		command := []string{args[0], "--config", m.Root + "/identity.json", "--disposable-fixture", "--file", *file}
		if *passwordStdin {
			command = append(command, "--password-stdin")
		}
		// Direct terminal FDs to the immutable owner helper. The app backend never
		// reads, stores, proxies or logs an unlock password or private key.
		child := exec.CommandContext(ctx, m.Root+"/fased-signer", command...)
		child.Stdin = in
		child.Stdout = out
		child.Stderr = errOut
		return child.Run()
	case "claim-code", "reset-app-password":
		if args[0] == "reset-app-password" && *passwordStdin {
			return errors.New("app password recovery requires the owner's terminal; --password-stdin is wallet-fixture-only")
		}
		if !*prepare {
			return errors.New("owner app recovery requires explicit --prepare-host OS authorization")
		}
		if args[0] == "reset-app-password" {
			if e = custodyinstall.Control(ctx, m, "app", "stop", system); e != nil {
				return e
			}
			defer func() {
				restart, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				result = errors.Join(result, custodyinstall.Control(restart, m, "app", "start", system))
			}()
		}
		return system.AppHelper(ctx, m, args[0] == "reset-app-password", *passwordStdin, out)
	default:
		return errors.New("unknown custody command")
	}
}
