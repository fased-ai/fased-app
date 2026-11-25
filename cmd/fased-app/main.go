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
	"runtime"
	"strings"
	"syscall"
	"time"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"golang.org/x/term"
)

func main() {
	err := dispatchPackageInstance(os.Args[1:])
	if err == nil {
		err = run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fased-app:", err)
		if errors.Is(err, core.ErrCoreUnavailable) {
			os.Exit(75)
		}
		if errors.Is(err, core.ErrUnauthorized) {
			os.Exit(77)
		}
		os.Exit(1)
	}
}
func run(args []string, in *os.File, out, errOut io.Writer) error {
	if len(args) > 0 && (args[0] == "private-ingress-capability" || args[0] == "private-ingress-check") {
		return privateIngressCommand(args, out)
	}
	if len(args) == 1 && args[0] == "native-release-binding" {
		if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
			return errors.New("native release identity requires an unprivileged process")
		}
		version := 1
		if compiledNativeDesktopSHA256 == "" {
			version = 2
		}
		b := bootstrap.NativeReleaseBinding{Version: version, ProductVersion: core.Version, Source: compiledNativeSource, DesktopSHA256: compiledNativeDesktopSHA256, SignerSHA256: compiledNativeSignerSHA256}
		if !b.Valid(core.Version, compiledNativeSource) {
			return errors.New("native release identity is unavailable")
		}
		return json.NewEncoder(out).Encode(b)
	}
	if len(args) > 0 && (args[0] == "custody-maintenance" || args[0] == "custody-maintenance-inventory" || args[0] == "custody-maintenance-verify") {
		return metadataMaintenanceCommand(args, in, out)
	}
	if len(args) > 0 && args[0] == "native-client" {
		return nativeAppClient(args[1:], in, out)
	}
	if len(args) > 0 && args[0] == "acceptance-update" {
		return candidateAcceptanceCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "settle-wallet-predecessor" {
		return settleWalletPredecessorCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "reader-session-client" {
		return readerSessionClient(args[1:], out)
	}
	if len(args) > 0 && args[0] == "install-command" {
		return installedCommandCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "update" {
		return ordinaryUpdateCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "maintain-update" {
		return maintainUpdateCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "maintenance-delegate" {
		return maintenanceDelegateCommand(args[1:], errOut)
	}
	if len(args) > 0 && args[0] == "maintenance-recover" {
		return maintenanceRecoveryCommand(args[1:], errOut)
	}
	if len(args) > 0 && args[0] == "enrollment-image" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return enrollmentImageCommand(ctx, args[1:], in, out, errOut)
	}
	if len(args) > 1 && args[0] == "pilot" && args[1] == "prepare" {
		return pilotPrepareCommand(args[2:], out, errOut)
	}
	if len(args) > 0 && args[0] == "vps" {
		return vpsCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "host" {
		return hostSubcommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "connect" {
		return connectCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "pair" {
		return pairCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "server" {
		return serverCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "doctor" {
		return doctorCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && (args[0] == "backup" || args[0] == "restore") {
		return stateBackupCommand(args[0], args[1:], out, errOut)
	}
	if len(args) > 1 && args[0] == "custody" && args[1] == "fixture-inspection" {
		return inspectionFixtureCommand(args[2:], in, out, errOut)
	}
	if len(args) > 0 && (args[0] == "owner-fixture-inspection" || args[0] == "fixture-reader-guard" || args[0] == "fixture-inspection-empty" || args[0] == "fixture-inspection-init" || args[0] == "fixture-inspection-cold" || args[0] == "fixture-update-pause-guard" || args[0] == "fixture-inspection-compatibility") {
		return inspectionFixtureCommand(args, in, out, errOut)
	}
	if len(args) > 1 && args[0] == "updates" && args[1] == "preferences" {
		return updatePreferencesCommand(args[2:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "update-preferences" {
		return updatePreferencesCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "update-status" {
		return updateStatusCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "owner-bootstrap" {
		return ownerBootstrapCommand(args[1:], in, out, errOut)
	}
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
	if len(args) > 0 && args[0] == "updates" && (len(args) == 1 || args[1] == "status" || args[1] == "check" || args[1] == "apply" || args[1] == "recover") {
		return applicationUpdatesCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "updates" {
		return updatesCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "ai-connection" {
		return aiConnectionCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "wen-market-data" {
		return wenMarketDataCommand(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "wen-research" {
		return wenResearchCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "wen-session" {
		return wenSessionCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "agents" {
		return agentsCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "accounts" {
		return accountsCommand(args[1:], in, out, errOut)
	}
	if len(args) > 0 && args[0] == "custody" {
		return custodyCommand(args[1:], in, out, errOut)
	}
	if len(args) == 0 {
		return errors.New("usage: fased-app wen-session|wen-research|wen-market-data|agents|update|updates|accounts|install|connect|pair|server|custody|service|open|access|recover|uninstall|serve|status|claim-code|claim|login|logout|setup|version [options]")
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
		if len(args) == 2 && args[1] == "--entry-json" {
			return json.NewEncoder(out).Encode(bootstrap.CurrentEntryCapabilities())
		}
		if len(args) == 2 && args[1] == "--json" {
			return json.NewEncoder(out).Encode(map[string]any{"version": core.Version, "stateSchema": core.StateSchema, "maintenanceProtocol": 1})
		}
		if len(args) != 1 {
			return errors.New("usage: fased-app version [--json]")
		}
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
	instanceLocation := flags.String("instance-location", "local", "runtime host profile set by installation: local or remote")
	listen := flags.String("listen", "127.0.0.1:7331", "loopback listener")
	researchOrigin := flags.String("wen-research-origin", "", "optional local read-only Kraken research service")
	publicOrigin := flags.String("public-origin", "", "exact private HTTPS origin behind a trusted local proxy")
	remoteTransport := flags.String("remote-transport", "", "remote ingress transport: tailscale, cloudflare, domain, proxy, or ssh")
	requireSandbox := flags.Bool("require-sandbox", false, "require installed Linux mount isolation before starting")
	requirePassword := flags.Bool("require-password", false, "require explicit owner password instead of instant Local browser entry")
	installationRoot := flags.String("installation-root", "", "immutable installed application directory")
	custodyConfig := flags.String("custody-config", "", "root-owned managed fixture identity; never enables real wallets")
	passwordStdin := flags.Bool("password-stdin", false, "read password from stdin instead of a terminal")
	privateProofStdin := flags.Bool("private-proof-stdin", false, "verify a private Tailscale connection through owner IPC")
	aiFixture := flags.Bool("ai-fixture", false, "development-only in-process provider fixtures; test credentials only, no live API")
	sessionFixture := flags.Bool("session-fixture", false, "development-only bounded shadow session fixtures; no model, market or wallet")
	interactive := flags.Bool("interactive", false, "walk through setup in the terminal")
	host := flags.String("host", "", "setup deployment preference: local or vps")
	wallet := flags.String("wallet-mode", "", "setup wallet preference: skip, watch, external, automation")
	provider := flags.String("provider", "", "setup AI preference: none, openai, anthropic, xai")
	executionProfile := flags.String("execution-profile", signer.ExecutionProfileOff, "Stage 4 non-WEN execution profile: off, testnet, or mainnet-capped")
	agentVaultID := flags.String("agent-vault-id", "", "optional typed AgentVault ID for Stage 4 testnet or mainnet-capped execution")
	solanaDevnetRPC := flags.String("solana-devnet-rpc", "", "optional HTTPS Solana Devnet JSON-RPC endpoint for Stage 4 rehearsal")
	solanaMainnetRPC := flags.String("solana-mainnet-rpc", "", "optional HTTPS Solana Mainnet-Beta JSON-RPC endpoint for Stage 4 mainnet-capped execution")
	confirmMainnetCapped := flags.Bool("confirm-mainnet-capped", false, "explicit owner confirmation required for --execution-profile=mainnet-capped")
	mainnetMaxOrderMicroUSD := flags.Uint64("mainnet-max-order-micro-usd", 0, "per-order ceiling in micro-USD (<= 250000000 = $250) for mainnet-capped")
	mainnetMaxDailyMicroUSD := flags.Uint64("mainnet-max-daily-micro-usd", 0, "daily spend ceiling in micro-USD (<= 1000000000 = $1000) for mainnet-capped")
	if err = flags.Parse(args[1:]); err != nil {
		return err
	}
	if *executionProfile != signer.ExecutionProfileOff && *executionProfile != signer.ExecutionProfileTestnet && *executionProfile != signer.ExecutionProfileMainnetCapped {
		return errors.New("execution profile must be off, testnet, or mainnet-capped")
	}
	if *executionProfile != signer.ExecutionProfileMainnetCapped && (*confirmMainnetCapped || *mainnetMaxOrderMicroUSD != 0 || *mainnetMaxDailyMicroUSD != 0) {
		return errors.New("--confirm-mainnet-capped and --mainnet-max-*-micro-usd require --execution-profile=mainnet-capped")
	}
	if *agentVaultID != "" && *executionProfile == signer.ExecutionProfileOff {
		return errors.New("--agent-vault-id requires --execution-profile=testnet or mainnet-capped")
	}
	if *solanaDevnetRPC != "" && *executionProfile != signer.ExecutionProfileTestnet {
		return errors.New("--solana-devnet-rpc requires --execution-profile=testnet")
	}
	if *solanaMainnetRPC != "" && *executionProfile != signer.ExecutionProfileMainnetCapped {
		return errors.New("--solana-mainnet-rpc requires --execution-profile=mainnet-capped")
	}
	if *executionProfile == signer.ExecutionProfileTestnet && (args[0] != "serve" || *custodyConfig == "" || *sessionFixture || *aiFixture) {
		return errors.New("--execution-profile=testnet requires serve with --custody-config and no development fixture modes")
	}
	var mainnetPolicy signer.MainnetCappedPolicy
	if *executionProfile == signer.ExecutionProfileMainnetCapped {
		if args[0] != "serve" || *custodyConfig == "" || *sessionFixture || *aiFixture {
			return errors.New("--execution-profile=mainnet-capped requires serve with --custody-config and no development fixture modes")
		}
		if *agentVaultID == "" {
			return errors.New("--execution-profile=mainnet-capped requires --agent-vault-id")
		}
		mainnetPolicy = signer.MainnetCappedPolicy{
			Confirmed:        *confirmMainnetCapped,
			MaxOrderMicroUSD: *mainnetMaxOrderMicroUSD,
			MaxDailyMicroUSD: *mainnetMaxDailyMicroUSD,
		}
		if err = mainnetPolicy.Validate(); err != nil {
			return err
		}
	}
	if *instanceLocation != "local" && *instanceLocation != "remote" {
		return errors.New("instance location must be local or remote")
	}
	if *remoteTransport != "" {
		if args[0] != "serve" || *instanceLocation != "remote" {
			return errors.New("--remote-transport requires serve --instance-location remote")
		}
		switch *remoteTransport {
		case "tailscale", "cloudflare", "domain", "proxy", "ssh":
		default:
			return errors.New("--remote-transport must be tailscale, cloudflare, domain, proxy, or ssh")
		}
	}
	if *privateProofStdin && args[0] != "status" {
		return errors.New("private access proof requires status")
	}
	installedProviderFixture := *aiFixture && *requireSandbox && *installationRoot != "" && *custodyConfig == "" && *researchOrigin == "" && *instanceLocation == "local" && *publicOrigin == ""
	if (*aiFixture && !installedProviderFixture || *sessionFixture) && *instanceLocation != "local" {
		return errors.New("development fixtures are local-only")
	}
	if *sessionFixture && (args[0] != "serve" || *aiFixture || *custodyConfig != "" || *requireSandbox || *publicOrigin != "" || *researchOrigin != "") {
		return errors.New("session fixture is a standalone serve-only option; no AI, signer or external research")
	}
	if *researchOrigin != "" && (args[0] != "serve" || *aiFixture || *requireSandbox || *custodyConfig != "") {
		return errors.New("research service is a standalone serve-only read option")
	}
	if *aiFixture && args[0] != "serve" {
		return errors.New("AI fixture is a serve-only development option")
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
		ownerUID := 0
		if runtime.GOOS != "windows" {
			if err = validateRuntimeUID(os.Getuid(), os.Geteuid()); err != nil {
				return err
			}
		}
		if *custodyConfig != "" {
			if !*requireSandbox {
				return errors.New("managed custody core requires its installed sandbox")
			}
			if err = signer.CheckApplication(*custodyConfig, *installationRoot, *dir); err != nil {
				return err
			}
			config, e := signer.LoadConfig(*custodyConfig)
			if e != nil {
				return e
			}
			ownerUID = custodyRuntimeOwnerUID(config, *instanceLocation)
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
		if *requireSandbox && *installationRoot != "" && *custodyConfig == "" {
			if err = localinstall.CheckProviderFixture(localinstall.Paths{Root: *installationRoot, Data: *dir}, installedProviderFixture); err != nil {
				return err
			}
		}
		if *sessionFixture {
			server, e := core.StartSessionFixture(context.Background(), *dir, *listen, fasedapp.UI())
			if e != nil {
				return e
			}
			server.ConfigureRequirePassword(*requirePassword)
			return serveStarted(server, out)
		}
		if *aiFixture && !installedProviderFixture {
			if *custodyConfig != "" || *requireSandbox || *publicOrigin != "" {
				return errors.New("AI fixture requires a standalone local development core")
			}
			return serveAgentFixture(*dir, *listen, *requirePassword, out)
		}
		source := ""
		if *requireSandbox && *installationRoot != "" && *custodyConfig == "" {
			source = "local"
		}
		return serveInstalledRuntimeWithStage4(*installationRoot, *dir, *listen, *publicOrigin, *researchOrigin, *instanceLocation, *remoteTransport, source, ownerUID, installedProviderFixture, *requirePassword, out, core.TestnetExecutionConfig{
			Profile:          *executionProfile,
			MainnetPolicy:    mainnetPolicy,
			AgentVaultID:     *agentVaultID,
			SolanaDevnetRPC:  *solanaDevnetRPC,
			SolanaMainnetRPC: *solanaMainnetRPC,
		}, *custodyConfig)
	case "setup-link":
		return ownerSetupLink(ctx, *dir, *publicOrigin, out)
	case "claim-code":
		setupLock, err := core.LockOwnerSetup(*dir)
		if err != nil {
			return err
		}
		defer setupLock.Close()
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
		if *privateProofStdin {
			if in == nil {
				return errors.New("private access proof input required")
			}
			data, readErr := io.ReadAll(io.LimitReader(in, 1025))
			if readErr != nil || len(data) > 1024 {
				return errors.New("bounded private access proof required")
			}
			var request core.PrivateAccessProofRequest
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Validate() != nil {
				return errors.New("one private access proof request required")
			}
			var proof core.PrivateAccessProof
			if err = client.Do(ctx, http.MethodPost, "/api/v1/private-access/control", request, &proof); err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(proof)
		}
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
	return serveWithResearch(dir, listen, publicOrigin, "", out, custodyConfig...)
}
func serveWithResearch(dir, listen, publicOrigin, researchOrigin string, out io.Writer, custodyConfig ...string) error {
	return serveRuntime(dir, listen, publicOrigin, researchOrigin, "local", 0, out, custodyConfig...)
}
func serveRuntime(dir, listen, publicOrigin, researchOrigin, location string, ownerUID int, out io.Writer, custodyConfig ...string) error {
	return serveInstalledRuntime(dir, listen, publicOrigin, researchOrigin, location, "", ownerUID, out, custodyConfig...)
}

// Set only by the explicitly compiled disposable acceptance executable.
// Ordinary builds have no selector, flag or endpoint/CA override for this hook.
var compiledInstalledWalletRead func(*core.Server, string) (func(), error)

func serveInstalledRuntime(dir, listen, publicOrigin, researchOrigin, location, source string, ownerUID int, out io.Writer, custodyConfig ...string) error {
	return serveInstalledRuntimeAt("", dir, listen, publicOrigin, researchOrigin, location, source, ownerUID, out, custodyConfig...)
}
func serveInstalledRuntimeAt(installationRoot, dir, listen, publicOrigin, researchOrigin, location, source string, ownerUID int, out io.Writer, custodyConfig ...string) error {
	return serveInstalledRuntimeWithProviderFixture(installationRoot, dir, listen, publicOrigin, researchOrigin, location, source, ownerUID, false, out, custodyConfig...)
}
func serveInstalledRuntimeWithProviderFixture(installationRoot, dir, listen, publicOrigin, researchOrigin, location, source string, ownerUID int, fixture bool, out io.Writer, custodyConfig ...string) error {
	return serveInstalledRuntimeWithOptions(installationRoot, dir, listen, publicOrigin, researchOrigin, location, source, ownerUID, fixture, false, out, custodyConfig...)
}
func serveInstalledRuntimeWithOptions(installationRoot, dir, listen, publicOrigin, researchOrigin, location, source string, ownerUID int, fixture, requirePassword bool, out io.Writer, custodyConfig ...string) error {
	return serveInstalledRuntimeWithTransport(installationRoot, dir, listen, publicOrigin, researchOrigin, location, "", source, ownerUID, fixture, requirePassword, out, custodyConfig...)
}
func serveInstalledRuntimeWithTransport(installationRoot, dir, listen, publicOrigin, researchOrigin, location, remoteTransport, source string, ownerUID int, fixture, requirePassword bool, out io.Writer, custodyConfig ...string) error {
	return serveInstalledRuntimeWithStage4(installationRoot, dir, listen, publicOrigin, researchOrigin, location, remoteTransport, source, ownerUID, fixture, requirePassword, out, core.TestnetExecutionConfig{}, custodyConfig...)
}

func serveInstalledRuntimeWithStage4(installationRoot, dir, listen, publicOrigin, researchOrigin, location, remoteTransport, source string, ownerUID int, fixture, requirePassword bool, out io.Writer, stage4Cfg core.TestnetExecutionConfig, custodyConfig ...string) error {
	start := core.Start
	if fixture {
		if installationRoot == "" || source != location || (source != "local" && source != "remote") || researchOrigin != "" || len(custodyConfig) > 1 || len(custodyConfig) == 1 && custodyConfig[0] != "" {
			return errors.New("provider fixture requires its validated app-only installation")
		}
		start = core.StartInstalledProviderFixture
		custodyConfig = nil
	}
	server, err := start(context.Background(), dir, listen, publicOrigin, fasedapp.UI(), custodyConfig...)
	if err != nil {
		return err
	}
	if err = server.ConfigureConnectionWithTransport(location, remoteTransport, ownerUID); err == nil {
		server.ConfigureRequirePassword(requirePassword)
		projectionSource := source
		if len(custodyConfig) == 1 && custodyConfig[0] != "" {
			projectionSource, err = verifiedCustodyInstallationSource(custodyConfig[0], installationRoot, dir, location, publicOrigin)
		}
		if err == nil {
			err = server.ConfigureInstallationSource(projectionSource)
		}
	}
	if err == nil {
		err = server.ConfigureWENResearch(researchOrigin)
	}
	if err == nil {
		if len(custodyConfig) == 1 && custodyConfig[0] != "" {
			err = bindCompiledCustodyMaintenance(server.Store, ownerUID, dir)
		} else {
			var check func(context.Context) (*core.ApplicationRelease, error)
			check, err = installedChannelCheck(source, installationRoot, dir, ownerUID)
			if err == nil && check != nil {
				if source == "local" && installationRoot != "" {
					err = bindCompiledLocalMaintenance(server.Store, installationRoot, dir, check)
				}
			}
		}
	}

	if err == nil && (stage4Cfg.Profile == signer.ExecutionProfileTestnet || stage4Cfg.Profile == signer.ExecutionProfileMainnetCapped) {
		if len(custodyConfig) != 1 || custodyConfig[0] == "" {
			err = errors.New("stage 4 execution requires custody config")
		} else {
			var sCfg signer.Config
			sCfg, err = signer.LoadConfig(custodyConfig[0])
			if err == nil && stage4Cfg.Profile == signer.ExecutionProfileTestnet && (sCfg.Network == "mainnet" || sCfg.Network == "mainnet-beta") {
				err = signer.ErrUnavailable
			}
			if err == nil && stage4Cfg.Profile == signer.ExecutionProfileMainnetCapped && sCfg.Network == "devnet" {
				err = signer.ErrUnavailable
			}
			if err == nil {
				stage4Cfg.SignerCall = func(callCtx context.Context, req signer.Request) (signer.Response, error) {
					return signer.Call(callCtx, sCfg, false, req)
				}
				err = server.ConfigureTestnetExecution(context.Background(), stage4Cfg)
			}
		}
	}
	if err == nil && len(custodyConfig) == 1 && compiledInstalledWalletRead != nil {
		var closeFixture func()
		closeFixture, err = compiledInstalledWalletRead(server, custodyConfig[0])
		if closeFixture != nil {
			defer closeFixture()
		}
	}
	if err != nil {
		_ = server.Close(context.Background())
		return err
	}
	return serveStarted(server, out)
}

// Custody keeps its own maintenance controller. Expose its verified installer
// class to Wallets without selecting app-only update authority from that class.
func verifiedCustodyInstallationSource(configPath, root, dir, location, origin string) (string, error) {
	c, err := signer.LoadConfig(configPath)
	if err != nil {
		return "", err
	}
	var m custodyinstall.Manifest
	if root == custodyinstall.Root(c.OwnerUID) {
		m, err = custodyinstall.Load(c.OwnerUID)
		if err == nil {
			err = custodyinstall.Verify(m)
		}
	} else {
		m, err = custodyinstall.LoadPackageInstance(c.OwnerUID, root)
		if err == nil {
			err = custodyinstall.VerifyPackageInstance(m)
		}
	}
	if err != nil {
		return "", errors.New("custody installation source is not verified")
	}
	return custodySourceProjection(m, c, root, dir, location, origin)
}

func custodySourceProjection(m custodyinstall.Manifest, c signer.Config, root, dir, location, origin string) (string, error) {
	if m.Owner != c.OwnerUID || m.Root != root || dir != filepath.Join(root, "app-state") || m.ID != c.SignerID || m.App.UID != c.AppUID || m.Signer.UID != c.SignerUID || m.Network != c.Network {
		return "", errors.New("custody installation source is not verified")
	}
	if !m.Enrollment {
		return "", nil
	}
	if m.Location != location || m.PublicOrigin != origin || (m.Location != "local" && m.Location != "remote") {
		return "", errors.New("custody installation source differs from its owner manifest")
	}
	return m.Location, nil
}
func serveAgentFixture(dir, listen string, requirePassword bool, out io.Writer) error {
	server, err := core.StartAgentFixture(context.Background(), dir, listen, "", fasedapp.UI())
	if err != nil {
		return err
	}
	server.ConfigureRequirePassword(requirePassword)
	return serveStarted(server, out)
}
func serveStarted(server *core.Server, out io.Writer) error {
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

// The installed service authenticates config before deriving its connection
// owner. The app service UID is separate from that owner for enrolled custody.
func custodyRuntimeOwnerUID(config signer.Config, location string) int {
	if config.Version == 2 || config.Version == 3 || location == "remote" {
		return config.OwnerUID
	}
	return 0
}
