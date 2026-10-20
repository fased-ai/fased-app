package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"golang.org/x/term"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fased-app:", err)
		os.Exit(1)
	}
}
func run(args []string, in *os.File, out, errOut io.Writer) error {
	if len(args) > 0 && args[0] == "update-recovery" {
		return updateRecoveryCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "update-backup" {
		return updateBackupCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "update-pause" {
		return updatePauseCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "update-inventory" {
		return updateInventoryCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "owner-updates" {
		return ownerUpdatesCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "updates" {
		return updatesCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "accounts" {
		return accountsCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "custody" {
		return custodyCommand(args[1:], in, out, errOut)
	}
	if len(args) == 0 {
		return errors.New("usage: fased-app updates|accounts|install|custody|service|open|access|recover|uninstall|serve|status|claim-code|claim|login|logout|setup|version [options]")
	}
	if args[0] == "install" || args[0] == "uninstall" || args[0] == "service" || args[0] == "open" {
		return hostCommand(args, out, errOut)
	}
	if args[0] == "access" {
		return accessCommand(args[1:], out, errOut)
	}
	if args[0] == "recover" {
		return recoverCommand(args[1:], in, out, errOut)
	}
	if args[0] == "version" {
		fmt.Fprintln(out, core.Version)
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	dirDefault, err := defaultDir()
	if err != nil {
		return err
	}
	dir := flags.String("data-dir", dirDefault, "private instance state directory")
	listen := flags.String("listen", "127.0.0.1:7331", "loopback listener")
	publicOrigin := flags.String("public-origin", "", "exact private HTTPS origin behind a trusted local proxy")
	requireSandbox := flags.Bool("require-sandbox", false, "require installed Linux mount isolation before starting")
	installationRoot := flags.String("installation-root", "", "immutable installed application directory")
	custodyConfig := flags.String("custody-config", "", "root-owned managed fixture identity; never enables real wallets")
	passwordStdin := flags.Bool("password-stdin", false, "read password from stdin instead of a terminal")
	interactive := flags.Bool("interactive", false, "walk through setup in the terminal")
	host := flags.String("host", "", "setup deployment preference: local or vps")
	wallet := flags.String("wallet-mode", "", "setup wallet preference: skip, watch, external, automation")
	provider := flags.String("provider", "", "setup AI preference: none, openai, anthropic, xai")
	if err = flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *interactive && (*host != "" || *wallet != "" || *provider != "") {
		return errors.New("choose interactive setup or explicit preference flags so every saved choice is reviewed")
	}
	// Interactive owner input has no expiry. Each API request has its own bounded timeout.
	ctx := context.Background()
	switch args[0] {
	case "serve":
		if *custodyConfig != "" {
			if !*requireSandbox {
				return errors.New("managed custody core requires its installed sandbox")
			}
			if err = signer.CheckApplication(*custodyConfig, *installationRoot, *dir); err != nil {
				return err
			}
		}
		if *requireSandbox {
			if *custodyConfig != "" {
				err = localinstall.CheckManagedSandbox(*installationRoot, *dir)
			} else {
				err = localinstall.CheckSandbox(*installationRoot, *dir)
			}
			if err != nil {
				return err
			}
		}
		return serve(*dir, *listen, *publicOrigin, out, *custodyConfig)
	case "claim-code":
		store, err := core.OpenStore(*dir)
		if err != nil {
			return err
		}
		defer store.Close()
		code, err := store.IssueClaimCode(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, code.Code)
		fmt.Fprintln(errOut, "One-use owner claim code; expires in 15 minutes. Enter it only in this instance's private UI.")
		return nil
	case "status", "claim", "login", "logout", "setup":
	default:
		return errors.New("unknown command")
	}
	client, err := core.NewClient(ctx, *dir)
	if err != nil {
		return err
	}
	defer client.Close()
	switch args[0] {
	case "status":
		var health core.Health
		if err = client.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(health)
	case "claim", "login":
		code := ""
		if args[0] == "claim" {
			if *passwordStdin || !term.IsTerminal(int(in.Fd())) {
				return errors.New("owner claim requires a terminal; use the private UI for browser setup")
			}
			fmt.Fprint(errOut, "One-use claim code: ")
			secret, err := term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(errOut)
			if err != nil {
				return errors.New("cannot read claim code")
			}
			code = strings.TrimSpace(string(secret))
		}
		var password string
		if *passwordStdin {
			line, err := bufio.NewReader(io.LimitReader(in, 1026)).ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return errors.New("cannot read password")
			}
			password = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		} else {
			if !term.IsTerminal(int(in.Fd())) {
				return errors.New("use a terminal or explicit --password-stdin; passwords are never accepted as arguments")
			}
			fmt.Fprint(errOut, "Owner password: ")
			data, err := term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(errOut)
			if err != nil {
				return errors.New("cannot read terminal password")
			}
			password = string(data)
		}
		if args[0] == "claim" {
			fmt.Fprint(errOut, "Confirm owner password: ")
			confirmation, err := term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(errOut)
			if err != nil || string(confirmation) != password {
				return errors.New("password confirmation does not match")
			}
			err = client.Claim(ctx, code, password)
		} else {
			err = client.Login(ctx, password)
		}
		if err != nil {
			return err
		}
		if err = client.SaveSession(*dir); err != nil {
			return err
		}
		fmt.Fprintln(out, "CLI authenticated. No wallet authority was granted.")
		return nil
	}
	if err = client.LoadSession(ctx, *dir); err != nil {
		return err
	}
	if args[0] == "logout" {
		if err = client.Do(ctx, http.MethodPost, "/api/v1/auth/logout", struct{}{}, nil); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(*dir, "cli-session.json"))
		fmt.Fprintln(out, "CLI logged out.")
		return nil
	}
	var setup core.SetupRecord
	if err = client.Do(ctx, http.MethodGet, "/api/v1/setup", nil, &setup); err != nil {
		return err
	}
	if *interactive {
		if !term.IsTerminal(int(in.Fd())) {
			return errors.New("interactive setup requires a terminal")
		}
		if err = setupWizard(bufio.NewReader(in), errOut, &setup.Settings); err != nil {
			return err
		}
	}
	if *host != "" {
		setup.Settings.Host = *host
	}
	if *wallet != "" {
		setup.Settings.WalletMode = *wallet
	}
	if *provider != "" {
		setup.Settings.Provider = *provider
	}
	if *interactive || *host != "" || *wallet != "" || *provider != "" {
		if err = setup.Settings.Validate(); err != nil {
			return err
		}
		var updated core.SetupRecord
		if err = client.Do(ctx, http.MethodPut, "/api/v1/setup", setup, &updated); err != nil {
			return err
		}
		setup = updated
	}
	return json.NewEncoder(out).Encode(setup)
}
func defaultDir() (string, error) {
	return core.DefaultDataDir()
}
func serve(dir, listen, publicOrigin string, out io.Writer, custodyConfig ...string) error {
	server, err := core.Start(context.Background(), dir, listen, publicOrigin, fasedapp.UI(), custodyConfig...)
	if err != nil {
		return err
	}
	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Closing a client window does not reach this process or its shutdown signal.
	failures := make(chan error, 1)
	go func() { failures <- server.Serve() }()
	_ = json.NewEncoder(out).Encode(map[string]string{"event": "listening", "url": server.URL, "version": core.Version})
	var result error
	select {
	case <-stopCtx.Done():
	case result = <-failures:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(result, server.Close(ctx))
}
