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
	if len(args) > 0 && (args[0] == "custody-maintenance" || args[0] == "custody-maintenance-inventory" || args[0] == "custody-maintenance-status") {
		return signerMaintenanceCommand(args, input, out)
	}
	if len(args) == 2 && args[0] == "fixture-install-guard" && args[1] == "--predecessor-capability" {
		return nil
	}
	if len(args) > 0 && args[0] == "fixture-validate-reusable-setup" {
		return validateReusableSetupCommand(args[1:], input, out, errOut)
	}
	if len(args) > 0 && args[0] == "fixture-validate-stage-recovery" {
		return validateStageRecoveryCommand(args[1:], errOut)
	}
	if len(args) > 0 && args[0] == "fixture-validate-preparation-start" {
		return validatePreparationStartCommand(args[1:], errOut)
	}
	if len(args) > 0 && args[0] == "fixture-validate-preparation-fence" {
		return validatePreparationFenceCommand(args[1:], errOut)
	}
	if len(args) > 0 && args[0] == "fixture-preparation-hold" {
		return preparationHoldCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "fixture-validate-atomic-worker" {
		return validateAtomicWorkerCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "fixture-validate-atomic-provision" {
		return validateAtomicProvisionCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && (args[0] == "fixture-portable-export" || args[0] == "fixture-portable-recover") {
		return portableCommand(args, input, out, errOut)
	}
	if len(args) > 0 && (args[0] == "fixture-inspection-compatibility" || args[0] == "fixture-reader-guard" || args[0] == "fixture-inspection-empty" || args[0] == "fixture-inspection-init" || args[0] == "fixture-inspection-cold") {
		return inspectionFixtureCommand(args, input, out, errOut)
	}
	if len(args) == 1 && args[0] == "ordinary-custody-compatibility" {
		return json.NewEncoder(out).Encode(signer.OrdinaryUpdateContract())
	}
	if len(args) == 1 && args[0] == "custody-compatibility" {
		return json.NewEncoder(out).Encode(signer.ManagedUpdateContract())
	}
	if len(args) == 1 && args[0] == "fixture-compatibility" {
		return json.NewEncoder(out).Encode(signer.UpdateContract())
	}
	if len(args) == 1 && args[0] == "ordinary-custody-version" {
		return json.NewEncoder(out).Encode(map[string]any{"protocol": 3, "ownerFlow": 1, "mode": signer.OrdinaryCustodyMode, "profile": signer.OwnerCustodyProfile, "provenance": signer.OwnerKeyProvenance, "walletSigner": false})
	}
	if len(args) == 1 && args[0] == "custody-version" {
		return json.NewEncoder(out).Encode(map[string]any{"protocol": 2, "ownerFlow": 1, "mode": signer.ManagedFixtureMode, "walletSigner": false})
	}
	if len(args) == 1 && args[0] == "fixture-version" {
		return json.NewEncoder(out).Encode(map[string]any{"protocol": 1, "ownerFlow": 1, "mode": signer.FixtureNetwork, "walletSigner": false})
	}
	if len(args) == 0 {
		return errors.New("usage: fased-signer serve|owner|app|approve --config <identity.json> (--disposable-fixture|--ordinary-custody) [options]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", "", "root-owned immutable signer identity configuration")
	ordinary := flags.Bool("ordinary-custody", false, "owner-authorized custody; normal admission remains held")
	fixture := flags.Bool("disposable-fixture", false, "confirm these are unfunded disposable test keys")
	rpcOrigin := flags.String("rpc-origin", "", "canonical read-only receipt RPC origin; key is entered privately")
	action := flags.String("action", "status", "owner administration or bounded fixture operation; app: status and fixture operations only")
	approvalStdin := flags.Bool("approval-stdin", false, "fixture CI only: approval challenge line from stdin")
	passwordStdin := flags.Bool("password-stdin", false, "fixture automation only: password lines from private stdin")
	snapshotPin := flags.String("snapshot-sha256", "", "independent checksum of the selected encrypted history backup")
	documentPath := flags.String("file", "", "exclusive owner output or encrypted restore input")
	seedPath := flags.String("seed-file", "", "private owner file containing a disposable 32-byte seed")
	lifetime := flags.Duration("unlock-for", 15*time.Minute, "manual unlock lifetime, at most one hour")
	requestStdin := flags.Bool("request-stdin", false, "public typed fixture policy/intent JSON from stdin; no credentials")
	intentID := flags.String("intent-id", "", "exact disposable installation intent for cold inspection")
	guardDigest := flags.String("guard-digest", "", "installation binding digest for a fixed replacement")
	guardDirection := flags.String("guard-direction", "", "fixed replacement direction")
	guardComponent := flags.String("guard-component", "", "fixed replacement component")
	guardSource := flags.String("guard-source-sha256", "", "fixed replacement source digest")
	guardStage := flags.String("guard-stage", "", "pair verification before or after replacement")
	windowInstance := flags.String("window-instance", "", "exact enrolled instance for the isolated owner window")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*fixture == *ordinary) || *configPath == "" {
		return errors.New("select one custody mode and an identity configuration")
	}
	if args[0] != "fixture-install-guard" && args[0] != "fixture-install-guard-status" && args[0] != "fixture-install-start-guard" && (*guardDigest != "" || *guardDirection != "" || *guardComponent != "" || *guardSource != "" || *guardStage != "") {
		return errors.New("replacement guard options require the fixture guard")
	}
	if *snapshotPin != "" && args[0] != "recover" && !(args[0] == "owner" && *action == "restore") {
		return errors.New("snapshot fingerprint requires owner recovery")
	}
	if *windowInstance != "" && args[0] != "window" {
		return errors.New("window instance requires protected window input")
	}
	if *ordinary && !signer.OrdinaryOwnerAdmissionEnabled() {
		return signer.ErrUnavailable
	}
	c, err := signer.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if args[0] == "fixture-install-start-guard" {
		if !*fixture || *ordinary || *passwordStdin || *requestStdin || *approvalStdin || *documentPath != "" || *seedPath != "" || *rpcOrigin != "" || *windowInstance != "" || *guardComponent != "" || *guardSource != "" || *guardStage != "" || *configPath != fmt.Sprintf("/var/lib/fased-app-custody-%d/identity.json", c.OwnerUID) {
			return errors.New("start guard accepts only the fixed disposable installation identity")
		}
		return signer.VerifyInstallationStartGuard(context.Background(), c, *intentID, *guardDigest, *guardDirection)
	}
	if c.Version == 3 && *fixture && (c.Profile != signer.OwnerCustodyProfile || c.Provenance != signer.ControlledCustodyProfile) {
		return signer.ErrUnavailable
	}
	if *ordinary && (c.Version != 3 || c.Mode != signer.OrdinaryCustodyMode) {
		return signer.ErrUnavailable
	}
	if args[0] == "ordinary-reader-handoff" {
		if *passwordStdin || *requestStdin || *approvalStdin || *documentPath != "" {
			return signer.ErrUnavailable
		}
		r, e := signer.CommitOrdinaryReaderHandoff(c, *intentID)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(r)
	}
	if args[0] == "fixture-install-inspect" || args[0] == "fixture-update-inspect" || args[0] == "fixture-install-guard" || args[0] == "fixture-install-guard-status" {
		if *passwordStdin || *requestStdin || *approvalStdin || *documentPath != "" {
			return errors.New("cold inspection accepts no credentials or documents")
		}
		if (args[0] == "fixture-install-guard" || args[0] == "fixture-install-guard-status") && (!*fixture || *ordinary || *seedPath != "" || *rpcOrigin != "" || *windowInstance != "") {
			return errors.New("replacement guard requires disposable fixture custody")
		}
		if args[0] == "fixture-install-guard-status" && (*guardDirection != "" || *guardComponent != "" || *guardSource != "" || *guardStage != "") {
			return errors.New("guard status accepts only the exact installation digest")
		}
		if args[0] == "fixture-update-inspect" {
			r, e := signer.InspectUpdate(c, *intentID)
			if e != nil {
				return e
			}
			return json.NewEncoder(out).Encode(r)
		}
		inspect := signer.InspectInstallation
		if args[0] == "fixture-install-guard" || args[0] == "fixture-install-guard-status" {
			if err := signer.VerifyInstallationGuardBoundary(c, *intentID, *guardDigest); err != nil {
				return err
			}
			inspect = signer.InspectInstallationReadOnly
		}
		i, e := inspect(c, *intentID)
		if e != nil {
			return e
		}
		if args[0] == "fixture-install-guard" {
			if err = signer.ValidateInstallationReplacement(i, *intentID, *guardDigest, *guardDirection, *guardComponent, *guardSource); err != nil {
				return err
			}
			if err = signer.VerifyInstallationGuardInventory(c, i, *intentID, *guardDirection, *guardStage); err != nil {
				return err
			}
			return signer.VerifyInstallationGuardBoundary(c, *intentID, *guardDigest)
		}
		if args[0] == "fixture-install-guard-status" {
			if i == nil || !update.ValidInstallation(*i) || i.Digest != *guardDigest {
				return errors.New("guarded signer installation differs")
			}
			if err = signer.VerifyInstallationGuardBoundary(c, *intentID, *guardDigest); err != nil {
				return err
			}
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
	if args[0] != "owner" && args[0] != "app" && args[0] != "setup" && args[0] != "recover" && args[0] != "approve" && args[0] != "approve-swap" && args[0] != "import" && args[0] != "window" {
		return errors.New("unsupported fixture command")
	}
	owner := args[0] != "app"
	if owner {
		if err = signer.CheckOwner(c); err != nil {
			return err
		}
	}
	if args[0] == "window" {
		if *requestStdin || *passwordStdin || *approvalStdin || *documentPath != "" || *seedPath != "" || *rpcOrigin != "" || *action != "status" {
			return errors.New("protected window accepts only its private input protocol")
		}
		return ownerWindow(ctx, c, *windowInstance, input, out)
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
		} else if c.Version == 3 {
			importAction = "import-ordinary-seed"
		}
		return conductImportAction(ctx, *seedPath, *documentPath, password, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
			return signer.Call(ctx, c, true, r)
		}, importAction)
	}
	if args[0] == "setup" || args[0] == "recover" {
		return ownerFlow(ctx, c, args[0], *documentPath, password, out, *snapshotPin)
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
	case "create", "unlock", "backup", "backup-history":
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
		r.Document, err = readPrivateBoundedFile(*documentPath, signer.MaxRecoveryBackup)
		r.SnapshotSHA256 = *snapshotPin
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
	if (r.Action == "backup" || r.Action == "backup-history" || r.Action == "export-fixture-seed") && *documentPath == "" {
		return errors.New("choose an exclusive owner output file; secret documents never go to stdout")
	}
	response, err := signer.Call(ctx, c, owner, r)
	if err != nil {
		return err
	}
	defer clear(response.Document)
	if r.Action == "backup" || r.Action == "backup-history" || r.Action == "export-fixture-seed" {
		if err = writeOwnerFile(*documentPath, response.Document); err != nil {
			return err
		}
	}
	if len(r.Action) >= 8 && r.Action[:8] == "fixture-" {
		return json.NewEncoder(out).Encode(response)
	}
	return json.NewEncoder(out).Encode(response.Status.PublicView())
}
