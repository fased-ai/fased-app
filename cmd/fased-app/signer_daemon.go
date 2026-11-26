package main

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

	"github.com/fased-ai/fased-app/internal/custody"
)

func signerDaemonCommand(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("signer-daemon", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dirDefault, err := defaultDir()
	if err != nil {
		dirDefault = ""
	}
	dataDir := flags.String("data-dir", dirDefault, "private custody signer vault directory")
	socketPath := flags.String("socket", "", "Unix socket path for typed custody IPC")
	allowedUID := flags.Int("allowed-app-uid", -1, "OS peer UID permitted to connect over SO_PEERCRED (default current UID)")
	allowedUser := flags.String("allowed-app-user", "", "OS username permitted to connect over SO_PEERCRED (overrides -1)")
	sockModeStr := flags.String("socket-mode", "0600", "octal Unix socket file permissions (0600 or 0660)")
	requireOSKEK := flags.Bool("require-os-kek", false, "require systemd-creds or OS keychain KEK to unwrap signer master key")
	fixtureRPC := flags.Bool("fixture-rpc", false, "development-only fixture RPC mode for isolated local testing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dataDir) == "" {
		return errors.New("signer-daemon requires --data-dir")
	}

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

	svc, err := custody.NewService(custody.Config{
		DataDir:      strings.TrimSpace(*dataDir),
		RequireOSKEK: *requireOSKEK,
		FixtureMode:  *fixtureRPC,
		FastKDF:      *fixtureRPC,
		SocketPath:   strings.TrimSpace(*socketPath),
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	sockSrv, err := custody.StartSocketServerAt(svc, strings.TrimSpace(*socketPath), peerUID, os.FileMode(parsedMode))
	if err != nil {
		return err
	}

	sec, _ := svc.SecurityStatus(context.Background())
	_ = json.NewEncoder(out).Encode(map[string]any{
		"status":         "listening",
		"socket":         sockSrv.SocketPath(),
		"allowedPeerUid": peerUID,
		"kekProtection":  sec.KEKProtection,
		"pid":            os.Getpid(),
	})

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = sockSrv.Close(shutCtx)
	return svc.Close()
}
