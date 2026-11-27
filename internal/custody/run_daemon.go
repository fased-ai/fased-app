package custody

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RunDaemon executes the unified hardened out-of-process custody signer daemon.
// Both `fased-signer daemon` and `fased-app signer-daemon` invoke this single entrypoint,
// eliminating split-brain between the standalone signer binary and app daemon subcommand.
func RunDaemon(args []string, defaultDataDir string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("signer-daemon", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dataDir := flags.String("data-dir", defaultDataDir, "private custody signer vault directory")
	socketPath := flags.String("socket", "", "Unix socket path for typed custody IPC")
	ownerSocketPath := flags.String("owner-socket", "", "optional 0600 owner-only Unix socket path for local/admin CLI")
	allowedUID := flags.Int("allowed-app-uid", -1, "OS peer UID permitted to connect over SO_PEERCRED (default current UID)")
	allowedUser := flags.String("allowed-app-user", "", "OS username permitted to connect over SO_PEERCRED (overrides -1)")
	ownerUID := flags.Int("owner-uid", -1, "OS peer UID permitted on --owner-socket (default current UID)")
	sockModeStr := flags.String("socket-mode", "0600", "octal Unix socket file permissions (0600 or 0660)")
	requireOSKEK := flags.Bool("require-os-kek", false, "require systemd-creds or OS keychain KEK to unwrap signer master key")
	requireOwnerAuth := flags.Bool("require-owner-auth", false, "require in-signer owner authorization proofs for all privileged IPC actions")
	allowLoopbackRPC := flags.Bool("allow-loopback-rpc", false, "allow loopback http://127.0.0.1 RPC endpoints for localnet development")
	fixtureRPC := flags.Bool("fixture-rpc", false, "development-only fixture RPC mode for isolated local testing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dataDir) == "" {
		return errors.New("signer daemon requires --data-dir")
	}

	// Suppress core dumps and same-UID ptrace attachment to protect in-memory keys.
	_ = HardenProcessMemory()

	peerUID := *allowedUID
	if strings.TrimSpace(*allowedUser) != "" && peerUID < 0 {
		u, err := user.Lookup(strings.TrimSpace(*allowedUser))
		if err != nil {
			return err
		}
		parsedUID, err := strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		peerUID = parsedUID
	}
	if peerUID < 0 {
		peerUID = os.Getuid()
	}

	parsedMode, err := strconv.ParseUint(strings.TrimSpace(*sockModeStr), 8, 32)
	if err != nil || (parsedMode != 0o600 && parsedMode != 0o660) {
		return errors.New("--socket-mode must be 0600 or 0660")
	}

	effectiveRequireOwnerAuth := *requireOwnerAuth || peerUID != os.Getuid()
	svc, err := NewService(Config{
		DataDir:          strings.TrimSpace(*dataDir),
		RequireOSKEK:     *requireOSKEK,
		RequireOwnerAuth: effectiveRequireOwnerAuth,
		AllowLoopbackRPC: *allowLoopbackRPC,
		FixtureMode:      *fixtureRPC,
		FastKDF:          *fixtureRPC,
		SocketPath:       strings.TrimSpace(*socketPath),
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	sockSrv, err := StartSocketServerAt(svc, strings.TrimSpace(*socketPath), peerUID, os.FileMode(parsedMode))
	if err != nil {
		return err
	}
	var ownerSockSrv *SocketServer
	if strings.TrimSpace(*ownerSocketPath) != "" {
		oUID := *ownerUID
		if oUID < 0 {
			oUID = os.Getuid()
		}
		ownerSockSrv, err = StartOwnerSocketServerAt(svc, strings.TrimSpace(*ownerSocketPath), oUID, 0o600)
		if err != nil {
			_ = sockSrv.Close(context.Background())
			return err
		}
	}

	sec, _ := svc.SecurityStatus(context.Background())
	_ = json.NewEncoder(out).Encode(map[string]any{
		"status":            "listening",
		"socket":            sockSrv.SocketPath(),
		"ownerSocket":       strings.TrimSpace(*ownerSocketPath),
		"allowedPeerUid":    peerUID,
		"kekProtection":     sec.KEKProtection,
		"ownerAuthEnforced": sec.OwnerAuthEnforced,
		"passkeyCount":      sec.PasskeyCount,
		"pid":               os.Getpid(),
	})

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ownerSockSrv != nil {
		_ = ownerSockSrv.Close(shutCtx)
	}
	_ = sockSrv.Close(shutCtx)
	return svc.Close()
}
