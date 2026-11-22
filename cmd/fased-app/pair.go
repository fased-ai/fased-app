package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
)

func pairCommand(args []string, out, errOut io.Writer) error {
	sub := "issue"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		rest = args[1:]
	}
	base, err := defaultDir()
	if err != nil {
		return err
	}
	switch sub {
	case "issue":
		return pairIssueCommand(rest, base, out, errOut)
	case "list":
		return pairListCommand(rest, base, out, errOut)
	case "revoke":
		return pairRevokeCommand(rest, base, out, errOut)
	default:
		return errors.New("usage: fased-app pair [list|revoke] [options]")
	}
}

func sshTunnelCommand(listenURL string) string {
	u, err := url.Parse(listenURL)
	port := "7331"
	if err == nil && u.Port() != "" {
		port = u.Port()
	}
	username := "root"
	if uCur, err := user.Current(); err == nil && uCur.Username != "" {
		username = uCur.Username
	} else if envUser := os.Getenv("USER"); envUser != "" {
		username = envUser
	}
	host := "<server-ip>"
	if h, err := os.Hostname(); err == nil && h != "" {
		host = h
	}
	return fmt.Sprintf("ssh -N -L %s:127.0.0.1:%s %s@%s", port, port, username, host)
}

func pairIssueCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("pair", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", defaultDataDir, "private instance state directory")
	origin := f.String("origin", "", "optional browser origin override")
	label := f.String("label", "", "optional device label")
	ttl := f.Duration("ttl", core.DefaultPairingLifetime, "pairing code lifetime (max 30m)")
	noQR := f.Bool("no-qr", false, "omit terminal QR code")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *ttl <= 0 || *ttl > core.MaxPairingLifetime {
		return errors.New("pairing TTL must be between 1s and 30m")
	}
	if *origin != "" {
		normalized, err := connection.Origin(*origin)
		if err != nil {
			return err
		}
		*origin = normalized
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		codeID          string
		code            string
		resolvedLabel   string
		expiresAt       time.Time
		instanceID      string
		publicOrigin    string
		remoteTransport string
		endpointURL     string
	)

	client, err := core.NewClient(ctx, *dir)
	if err == nil {
		defer client.Close()
		var res struct {
			ID              string    `json:"id"`
			Code            string    `json:"code"`
			Label           string    `json:"label"`
			ExpiresAt       time.Time `json:"expiresAt"`
			InstanceID      string    `json:"instanceId"`
			PublicOrigin    string    `json:"publicOrigin"`
			RemoteTransport string    `json:"remoteTransport"`
		}
		req := struct {
			Label      string `json:"label,omitempty"`
			TTLSeconds int    `json:"ttlSeconds"`
		}{
			Label:      *label,
			TTLSeconds: int(ttl.Seconds()),
		}
		if e := client.Do(ctx, http.MethodPost, "/api/v1/auth/pair/issue", req, &res); e != nil {
			return e
		}
		codeID = res.ID
		code = res.Code
		resolvedLabel = res.Label
		expiresAt = res.ExpiresAt
		instanceID = res.InstanceID
		publicOrigin = res.PublicOrigin
		remoteTransport = res.RemoteTransport
		endpointURL = client.Endpoint.URL
	} else if errors.Is(err, core.ErrCoreUnavailable) {
		setupLock, lockErr := core.LockOwnerSetup(*dir)
		if lockErr != nil {
			return lockErr
		}
		defer setupLock.Close()
		store, storeErr := core.OpenStore(*dir)
		if storeErr != nil {
			return storeErr
		}
		defer store.Close()
		issued, issueErr := store.IssuePairingCode(ctx, *label, *ttl)
		if issueErr != nil {
			return issueErr
		}
		codeID = issued.ID
		code = issued.Code
		resolvedLabel = issued.Label
		expiresAt = issued.ExpiresAt
		instanceID = store.Health().InstanceID
		endpointURL = "http://127.0.0.1:7331"
	} else {
		return err
	}

	useOrigin := *origin
	if useOrigin == "" {
		useOrigin = publicOrigin
	}
	if useOrigin == "" {
		useOrigin = endpointURL
	}
	pairURL, err := connection.OwnerPairURL(useOrigin, instanceID, code)
	if err != nil {
		return err
	}

	sshCmd := ""
	if remoteTransport == "ssh" || (publicOrigin == "" && (strings.HasPrefix(useOrigin, "http://127.0.0.1:") || strings.HasPrefix(useOrigin, "http://localhost:"))) {
		sshCmd = sshTunnelCommand(useOrigin)
	}

	if *jsonOut {
		payload := map[string]any{
			"id":         codeID,
			"code":       code,
			"url":        pairURL,
			"instanceId": instanceID,
			"expiresAt":  expiresAt,
		}
		if resolvedLabel != "" {
			payload["label"] = resolvedLabel
		}
		if remoteTransport != "" {
			payload["remoteTransport"] = remoteTransport
		}
		if sshCmd != "" {
			payload["sshCommand"] = sshCmd
		}
		return json.NewEncoder(out).Encode(payload)
	}

	if !*noQR {
		if qr, qrErr := RenderQR(pairURL); qrErr == nil {
			fmt.Fprintln(out, qr)
		}
	}
	if sshCmd != "" {
		fmt.Fprintf(out, "SSH:   %s\n", sshCmd)
	}
	fmt.Fprintf(out, "Link:  %s\n", pairURL)
	fmt.Fprintf(out, "Code:  %s\n", code)
	fmt.Fprintf(out, "Valid: %s\n", expiresAt.Local().Format(time.RFC3339))
	return nil
}

func pairListCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("pair list", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", defaultDataDir, "private instance state directory")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var devices []core.PairedDevice
	client, err := core.NewClient(ctx, *dir)
	if err == nil {
		defer client.Close()
		var res struct {
			Devices []core.PairedDevice `json:"devices"`
		}
		if e := client.Do(ctx, http.MethodGet, "/api/v1/auth/pair/manage", nil, &res); e != nil {
			return e
		}
		devices = res.Devices
	} else if errors.Is(err, core.ErrCoreUnavailable) {
		setupLock, lockErr := core.LockOwnerSetup(*dir)
		if lockErr != nil {
			return lockErr
		}
		defer setupLock.Close()
		store, storeErr := core.OpenStore(*dir)
		if storeErr != nil {
			return storeErr
		}
		defer store.Close()
		devices, _, err = store.ListPairedDevices(ctx, "")
		if err != nil {
			return err
		}
	} else {
		return err
	}

	if *jsonOut {
		if devices == nil {
			devices = []core.PairedDevice{}
		}
		return json.NewEncoder(out).Encode(map[string]any{"devices": devices})
	}
	if len(devices) == 0 {
		fmt.Fprintln(out, "No active paired devices.")
		return nil
	}
	for _, d := range devices {
		transport := d.Transport
		if transport == "" {
			transport = "remote"
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", d.ID, d.Label, transport, d.CreatedAt.Local().Format(time.RFC3339))
	}
	return nil
}

func pairRevokeCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("pair revoke", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", defaultDataDir, "private instance state directory")
	all := f.Bool("all", false, "revoke every active paired device")
	if err := f.Parse(args); err != nil {
		return err
	}
	deviceID := ""
	if !*all {
		if f.NArg() != 1 {
			return errors.New("usage: fased-app pair revoke [--data-dir DIR] [--all | DEVICE_ID]")
		}
		deviceID = strings.TrimSpace(f.Arg(0))
		if deviceID == "" {
			return errors.New("device ID required")
		}
	} else if f.NArg() != 0 {
		return errors.New("cannot pass a device ID with --all")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var revoked int
	client, err := core.NewClient(ctx, *dir)
	if err == nil {
		defer client.Close()
		action := "revoke"
		if *all {
			action = "revoke-all"
		}
		var res struct {
			Revoked int `json:"revoked"`
		}
		req := struct {
			Action   string `json:"action"`
			DeviceID string `json:"deviceId,omitempty"`
		}{Action: action, DeviceID: deviceID}
		if e := client.Do(ctx, http.MethodPost, "/api/v1/auth/pair/manage", req, &res); e != nil {
			return e
		}
		revoked = res.Revoked
	} else if errors.Is(err, core.ErrCoreUnavailable) {
		setupLock, lockErr := core.LockOwnerSetup(*dir)
		if lockErr != nil {
			return lockErr
		}
		defer setupLock.Close()
		store, storeErr := core.OpenStore(*dir)
		if storeErr != nil {
			return storeErr
		}
		defer store.Close()
		if *all {
			revoked, err = store.RevokeAllPairedDevices(ctx)
			if err != nil {
				return err
			}
		} else {
			if err = store.RevokePairedDevice(ctx, deviceID); err != nil {
				return err
			}
			revoked = 1
		}
	} else {
		return err
	}
	fmt.Fprintf(out, "Revoked %d paired device(s).\n", revoked)
	return nil
}
