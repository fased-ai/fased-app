package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

type serverInitPlan struct {
	Transport     string            `json:"transport"`
	Listen        string            `json:"listen"`
	DataDir       string            `json:"dataDir"`
	PublicOrigin  string            `json:"publicOrigin,omitempty"`
	ServiceUnit   string            `json:"serviceUnit"`
	CompanionFile string            `json:"companionFile,omitempty"`
	CompanionBody string            `json:"companionBody,omitempty"`
	Commands      []string          `json:"commands"`
	WrittenFiles  map[string]string `json:"writtenFiles,omitempty"`
}

func serverCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: fased-app server init|status [options]")
	}
	base, err := defaultDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		return serverInitCommand(args[1:], base, out, errOut)
	case "status":
		return serverStatusCommand(args[1:], base, out, errOut)
	default:
		return errors.New("usage: fased-app server init|status [options]")
	}
}

func validateLoopbackListen(listen string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", 0, errors.New("listen must be host:port on loopback")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", 0, errors.New("listen address must stay on loopback (127.0.0.1 or ::1)")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || strconv.Itoa(port) != portStr || port < 1024 || port > 65535 {
		return "", 0, errors.New("listen port must be between 1024 and 65535")
	}
	return net.JoinHostPort(ip.String(), portStr), port, nil
}

func buildServerInitPlan(transport, domain, listen, dataDir, binPath string) (serverInitPlan, error) {
	transport = strings.ToLower(strings.TrimSpace(transport))
	domain = strings.TrimSpace(domain)
	domain = strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	domain = strings.TrimSuffix(domain, "/")

	normListen, port, err := validateLoopbackListen(listen)
	if err != nil {
		return serverInitPlan{}, err
	}
	if dataDir == "" {
		return serverInitPlan{}, errors.New("data-dir required")
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return serverInitPlan{}, err
	}
	if strings.ContainsAny(absData, "\r\n\t\"'") {
		return serverInitPlan{}, errors.New("invalid data-dir path")
	}
	if binPath == "" {
		binPath = "/usr/local/bin/fased-app"
	}

	var publicOrigin string
	var companionFile string
	var companionBody string
	var commands []string

	switch transport {
	case "tailscale":
		if domain != "" {
			origin, _, _, tErr := func() (string, string, string, error) {
				o := "https://" + domain
				h, p, e := tailnet.Origin(o)
				return o, h, p, e
			}()
			if tErr != nil {
				return serverInitPlan{}, errors.New("Tailscale --domain must be your MagicDNS hostname (e.g. box.tailnet.ts.net)")
			}
			publicOrigin = origin
		}
		if publicOrigin != "" {
			commands = append(commands, fmt.Sprintf("sudo tailscale serve --bg --https=443 http://%s", normListen))
		} else {
			commands = append(commands,
				"tailscale status --json   # check your <host>.ts.net DNSName",
				fmt.Sprintf("sudo tailscale serve --bg --https=443 http://%s", normListen),
			)
		}
	case "cloudflare":
		if domain == "" {
			return serverInitPlan{}, errors.New("cloudflare transport requires --domain (e.g. fased.example.com)")
		}
		origin, oErr := connection.RemoteHTTPSOrigin("https://" + domain)
		if oErr != nil {
			return serverInitPlan{}, oErr
		}
		publicOrigin = origin
		companionFile = "cloudflared.yml"
		companionBody = fmt.Sprintf(
			"ingress:\n  - hostname: %s\n    service: http://%s\n  - service: http_status:404\n",
			strings.TrimPrefix(origin, "https://"),
			normListen,
		)
		commands = append(commands,
			"cloudflared tunnel create fased",
			fmt.Sprintf("cloudflared tunnel route dns fased %s", strings.TrimPrefix(origin, "https://")),
			"cloudflared tunnel --config cloudflared.yml run fased",
		)
	case "domain":
		if domain == "" {
			return serverInitPlan{}, errors.New("domain transport requires --domain (e.g. fased.example.com)")
		}
		origin, oErr := connection.RemoteHTTPSOrigin("https://" + domain)
		if oErr != nil {
			return serverInitPlan{}, oErr
		}
		publicOrigin = origin
		companionFile = "Caddyfile"
		companionBody = fmt.Sprintf(
			"%s {\n\treverse_proxy %s\n}\n",
			strings.TrimPrefix(origin, "https://"),
			normListen,
		)
		commands = append(commands,
			"sudo install -m 0644 Caddyfile /etc/caddy/Caddyfile",
			"sudo systemctl reload caddy",
		)
	case "ssh":
		if domain != "" {
			return serverInitPlan{}, errors.New("ssh transport runs on loopback only; omit --domain")
		}
		commands = append(commands, fmt.Sprintf("ssh -N -L %d:%s <user>@<server-ip>", port, normListen))
	default:
		return serverInitPlan{}, errors.New("transport must be tailscale, cloudflare, domain, or ssh")
	}

	execArgs := []string{
		binPath,
		"serve",
		"--instance-location", "remote",
		"--remote-transport", transport,
		"--listen", normListen,
		"--data-dir", absData,
	}
	if publicOrigin != "" {
		execArgs = append(execArgs, "--public-origin", publicOrigin)
	}
	serviceUnit := fmt.Sprintf(`[Unit]
Description=Fased Remote Server (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, transport, strings.Join(execArgs, " "), absData)

	commands = append(commands, fmt.Sprintf("%s pair --data-dir %s", binPath, absData))
	return serverInitPlan{
		Transport:     transport,
		Listen:        normListen,
		DataDir:       absData,
		PublicOrigin:  publicOrigin,
		ServiceUnit:   serviceUnit,
		CompanionFile: companionFile,
		CompanionBody: companionBody,
		Commands:      commands,
	}, nil
}

func serverInitCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("server init", flag.ContinueOnError)
	f.SetOutput(errOut)
	transport := f.String("transport", "tailscale", "remote ingress: tailscale, cloudflare, domain, or ssh")
	domain := f.String("domain", "", "HTTPS domain for cloudflare, domain, or optional tailscale (*.ts.net)")
	listen := f.String("listen", "127.0.0.1:7331", "loopback listener")
	dataDir := f.String("data-dir", defaultDataDir, "private instance state directory")
	outputDir := f.String("output-dir", "", "optional directory to write systemd unit and proxy config")
	binPath := f.String("binary", "", "path to fased-app binary for systemd ExecStart")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	resolvedBin := strings.TrimSpace(*binPath)
	if resolvedBin == "" {
		if exe, err := os.Executable(); err == nil && exe != "" {
			resolvedBin = exe
		} else {
			resolvedBin = "fased-app"
		}
	}

	plan, err := buildServerInitPlan(*transport, *domain, *listen, *dataDir, resolvedBin)
	if err != nil {
		return err
	}

	if *outputDir != "" {
		outAbs, err := filepath.Abs(*outputDir)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(outAbs, 0700); err != nil {
			return err
		}
		written := map[string]string{}
		unitPath := filepath.Join(outAbs, "fased-server.service")
		if err := os.WriteFile(unitPath, []byte(plan.ServiceUnit), 0600); err != nil {
			return err
		}
		written["serviceUnit"] = unitPath
		if plan.CompanionFile != "" && plan.CompanionBody != "" {
			compPath := filepath.Join(outAbs, plan.CompanionFile)
			if err := os.WriteFile(compPath, []byte(plan.CompanionBody), 0600); err != nil {
				return err
			}
			written["companion"] = compPath
		}
		plan.WrittenFiles = written
	}

	if *jsonOut {
		return json.NewEncoder(out).Encode(plan)
	}

	fmt.Fprintf(out, "Transport: %s\nListen:    %s\n", plan.Transport, plan.Listen)
	if plan.PublicOrigin != "" {
		fmt.Fprintf(out, "Origin:    %s\n", plan.PublicOrigin)
	}
	if len(plan.WrittenFiles) > 0 {
		for k, p := range plan.WrittenFiles {
			fmt.Fprintf(out, "Wrote %s: %s\n", k, p)
		}
	} else {
		fmt.Fprintf(out, "\n--- fased-server.service ---\n%s", plan.ServiceUnit)
		if plan.CompanionFile != "" {
			fmt.Fprintf(out, "\n--- %s ---\n%s", plan.CompanionFile, plan.CompanionBody)
		}
	}
	fmt.Fprintln(out, "\nNext:")
	for _, cmd := range plan.Commands {
		fmt.Fprintf(out, "  %s\n", cmd)
	}
	return nil
}

func serverStatusCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("server status", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", defaultDataDir, "private instance state directory")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := core.NewClient(ctx, *dir)
	if err != nil {
		return err
	}
	defer client.Close()

	var health core.Health
	if err = client.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); err != nil {
		return err
	}
	var managed struct {
		Devices []core.PairedDevice `json:"devices"`
	}
	if err = client.Do(ctx, http.MethodGet, "/api/v1/auth/pair/manage", nil, &managed); err != nil {
		return err
	}
	location := ""
	transport := ""
	if health.Connection != nil {
		location = health.Connection.Location
		transport = health.Connection.RemoteTransport
		if transport == "" {
			transport = health.Connection.Transport
		}
	}
	status := map[string]any{
		"running":        true,
		"instanceId":     health.InstanceID,
		"version":        health.Version,
		"location":       location,
		"transport":      transport,
		"listenUrl":      client.Endpoint.URL,
		"publicOrigin":   client.Endpoint.PublicOrigin,
		"pairedDevices":  len(managed.Devices),
	}
	if *jsonOut {
		return json.NewEncoder(out).Encode(status)
	}
	fmt.Fprintf(out, "Status:    running (%s)\n", health.Version)
	fmt.Fprintf(out, "Location:  %s (%s)\n", location, transport)
	fmt.Fprintf(out, "Listen:    %s\n", client.Endpoint.URL)
	if client.Endpoint.PublicOrigin != "" {
		fmt.Fprintf(out, "Origin:    %s\n", client.Endpoint.PublicOrigin)
	}
	fmt.Fprintf(out, "Devices:   %d paired\n", len(managed.Devices))
	return nil
}
