// fased-signer is an explicitly disposable Linux engineering fixture. It is not
// part of the app package/installer and cannot sign or submit transactions.
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
	"os/signal"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/term"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fased-signer:", err)
		os.Exit(1)
	}
}

func run(args []string, input *os.File, out, errOut io.Writer) error {
	if len(args) > 0 && (args[0] == "fixture-portable-export" || args[0] == "fixture-portable-recover") {
		return portableCommand(args, input, out, errOut)
	}
	if len(args) > 0 && (args[0] == "fixture-inspection-compatibility" || args[0] == "fixture-reader-guard" || args[0] == "fixture-inspection-empty" || args[0] == "fixture-inspection-init" || args[0] == "fixture-inspection-cold") {
		return inspectionFixtureCommand(args, input, out, errOut)
	}
	if len(args) == 1 && args[0] == "custody-compatibility" {
		return json.NewEncoder(out).Encode(signer.ManagedUpdateContract())
	}
	if len(args) == 1 && args[0] == "fixture-compatibility" {
		return json.NewEncoder(out).Encode(signer.UpdateContract())
	}
	if len(args) == 1 && args[0] == "custody-version" {
		return json.NewEncoder(out).Encode(map[string]any{"protocol": 2, "ownerFlow": 1, "mode": signer.ManagedFixtureMode, "walletSigner": false})
	}
	if len(args) == 1 && args[0] == "fixture-version" {
		return json.NewEncoder(out).Encode(map[string]any{"protocol": 1, "ownerFlow": 1, "mode": signer.FixtureNetwork, "walletSigner": false})
	}
	if len(args) == 0 {
		return errors.New("usage: fased-signer serve|owner|app|approve --config <root-owned-fixture.json> --disposable-fixture [options]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", "", "root-owned immutable fixture identity configuration")
	fixture := flags.Bool("disposable-fixture", false, "confirm these are unfunded disposable test keys")
	rpcOrigin := flags.String("rpc-origin", "", "canonical read-only receipt RPC origin; key is entered privately")
	action := flags.String("action", "status", "owner administration or bounded fixture operation; app: status and fixture operations only")
	approvalStdin := flags.Bool("approval-stdin", false, "fixture CI only: approval challenge line from stdin")
	passwordStdin := flags.Bool("password-stdin", false, "fixture automation only: password lines from private stdin")
	documentPath := flags.String("file", "", "exclusive owner output or encrypted restore input")
	seedPath := flags.String("seed-file", "", "private owner file containing a disposable 32-byte seed")
	lifetime := flags.Duration("unlock-for", 15*time.Minute, "manual unlock lifetime, at most one hour")
	requestStdin := flags.Bool("request-stdin", false, "public typed fixture policy/intent JSON from stdin; no credentials")
	intentID := flags.String("intent-id", "", "exact disposable installation intent for cold inspection")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || !*fixture || *configPath == "" {
		return errors.New("explicit disposable fixture confirmation and identity configuration required")
	}
	c, err := signer.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if args[0] == "fixture-install-inspect" || args[0] == "fixture-update-inspect" {
		if *passwordStdin || *requestStdin || *approvalStdin || *documentPath != "" {
			return errors.New("cold inspection accepts no credentials or documents")
		}
		if args[0] == "fixture-update-inspect" {
			r, e := signer.InspectUpdate(c, *intentID)
			if e != nil {
				return e
			}
			return json.NewEncoder(out).Encode(r)
		}
		i, e := signer.InspectInstallation(c, *intentID)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(i)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if args[0] == "serve" {
		server, err := signer.Serve(ctx, c)
		if err != nil {
			return err
		}
		defer server.Close()
		fmt.Fprintf(out, "Disposable signer ready; pid=%d uid=%d; real wallets and signing unavailable.\n", os.Getpid(), os.Geteuid())
		<-ctx.Done()
		return nil
	}
	if args[0] != "owner" && args[0] != "app" && args[0] != "setup" && args[0] != "recover" && args[0] != "approve" && args[0] != "approve-swap" && args[0] != "import" {
		return errors.New("unsupported fixture command")
	}
	owner := args[0] != "app"
	if owner {
		if err = signer.CheckOwner(c); err != nil {
			return err
		}
	}
	if !owner && !signer.AppActionAllowed(*action) {
		return errors.New("owner administration denied")
	}
	if *rpcOrigin != "" && *action != "receipt-rpc-save" {
		return errors.New("RPC origin requires receipt-rpc-save")
	}
	r := signer.Request{Action: *action}
	if *requestStdin {
		if *passwordStdin || args[0] == "setup" || args[0] == "recover" || args[0] == "import" {
			return errors.New("public fixture input cannot carry credentials")
		}
		document, e := io.ReadAll(io.LimitReader(input, 16*1024+1))
		if e != nil {
			return e
		}
		r.PublicInput, err = signer.ParsePublicInput(document)
		if err != nil {
			return errors.New("invalid public fixture request")
		}
	}
	reader := bufio.NewReader(io.LimitReader(input, 4096))
	password := func(label string, confirm bool) ([]byte, error) {
		read := func() ([]byte, error) {
			if *passwordStdin {
				line, err := reader.ReadBytes('\n')
				if err != nil {
					return nil, errors.New("fixture password input requires a newline")
				}
				return line[:len(line)-1], nil
			}
			if !term.IsTerminal(int(input.Fd())) {
				return nil, errors.New("use an interactive terminal or explicit fixture --password-stdin")
			}
			fmt.Fprint(errOut, label+": ")
			p, err := term.ReadPassword(int(input.Fd()))
			fmt.Fprintln(errOut)
			return p, err
		}
		p, err := read()
		if err != nil {
			return nil, err
		}
		if confirm {
			fmt.Fprint(errOut, "Confirm ")
			other, err := read()
			defer clear(other)
			if err != nil || string(p) != string(other) {
				clear(p)
				return nil, errors.New("password confirmation differs")
			}
		}
		return p, nil
	}
	if args[0] == "approve" || args[0] == "approve-swap" {
		if *requestStdin {
			return errors.New("choose an owner proposal file")
		}
		b, e := readPublicProposal(*documentPath)
		if e != nil {
			return e
		}
		confirm := func(expected string) error {
			if !*approvalStdin && !term.IsTerminal(int(input.Fd())) {
				return errors.New("owner approval requires a terminal")
			}
			fmt.Fprintf(errOut, "Type %s to approve, or anything else to cancel: ", expected)
			line, e := reader.ReadString('\n')
			if e != nil || line != expected+"\n" {
				return errors.New("approval cancelled; no permission granted")
			}
			return nil
		}
		if args[0] == "approve-swap" {
			p, e := signer.ParseSwapProposal(b)
			if e != nil {
				return e
			}
			return conductSwapApproval(ctx, p, password, confirm, errOut, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
				return signer.Call(ctx, c, true, r)
			})
		}
		p, e := signer.ParseProposal(b)
		if e != nil {
			return e
		}
		return conductApproval(ctx, p, password, confirm, errOut, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
			return signer.Call(ctx, c, true, r)
		})
	}
	if args[0] == "import" {
		importAction := "import-fixture-seed"
		if c.Version == 2 {
			importAction = "import-managed-seed"
		}
		return conductImportAction(ctx, *seedPath, *documentPath, password, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
			return signer.Call(ctx, c, true, r)
		}, importAction)
	}
	if args[0] == "setup" || args[0] == "recover" {
		return ownerFlow(ctx, c, args[0], *documentPath, password, out)
	}
	switch r.Action {
	case "receipt-rpc-save":
		if !owner || *requestStdin || *rpcOrigin == "" {
			return errors.New("protected owner RPC origin required")
		}
		r.RPCOrigin = *rpcOrigin
		r.RPCKey, err = password("RPC API key", false)
		if err == nil {
			r.Password, err = password("Signer password", false)
		}
	case "receipt-rpc-unlock":
		if !owner || *requestStdin || *lifetime < time.Second || *lifetime > signer.MaxReceiptRPCLease {
			return errors.New("read-only receipt lease must be one second to fifteen minutes")
		}
		r.Password, err = password("Signer password", false)
		r.UnlockSeconds = int(*lifetime / time.Second)
	case "fixture-update-inventory":
		r.ProbeID = update.NewProbe()
	case "status", "lock":
	case "fixture-approvals", "fixture-state", "fixture-policy", "fixture-grant", "fixture-revoke", "fixture-recover-authority", "fixture-reserve", "fixture-execute", "fixture-reconcile", "fixture-operation":
	case "create", "unlock", "backup":
		r.Password, err = password("Fixture password", r.Action != "unlock")
		if r.Action == "unlock" {
			if *lifetime < time.Second || *lifetime > signer.MaxUnlock {
				return errors.New("unlock lifetime must be one second to one hour")
			}
			r.UnlockSeconds = int(*lifetime / time.Second)
		}
	case "import-fixture-seed", "import-managed-seed":
		r.Document, err = readOwnerFile(*documentPath)
		if err == nil {
			r.Password, err = password("New fixture unlock password", true)
		}
	case "restore", "verify-backup":
		r.Document, err = readOwnerFile(*documentPath)
		if err == nil {
			r.Password, err = password("Backup password", false)
		}
		if err == nil && r.Action == "restore" {
			r.NewPassword, err = password("New fixture unlock password", true)
		}
	case "export-fixture-seed":
	default:
		return signer.ErrUnavailable
	}
	defer clear(r.RPCKey)
	defer clear(r.Password)
	defer clear(r.NewPassword)
	defer clear(r.Document)
	if err != nil {
		return err
	}
	if (r.Action == "backup" || r.Action == "export-fixture-seed") && *documentPath == "" {
		return errors.New("choose an exclusive owner output file; secret documents never go to stdout")
	}
	response, err := signer.Call(ctx, c, owner, r)
	if err != nil {
		return err
	}
	defer clear(response.Document)
	if r.Action == "backup" || r.Action == "export-fixture-seed" {
		if err = writeOwnerFile(*documentPath, response.Document); err != nil {
			return err
		}
	}
	if len(r.Action) >= 8 && r.Action[:8] == "fixture-" {
		return json.NewEncoder(out).Encode(response)
	}
	return json.NewEncoder(out).Encode(response.Status.PublicView())
}
