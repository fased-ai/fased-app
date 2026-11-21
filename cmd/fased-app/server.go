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
	"os/exec"
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

type serverConfig struct {
	Version       int    `json:"version"`
	Transport     string `json:"transport"`
	Listen        string `json:"listen"`
	PublicOrigin  string `json:"publicOrigin,omitempty"`
	DataDir       string `json:"dataDir"`
	BinaryPath    string `json:"binaryPath"`
	UnitPath      string `json:"unitPath"`
	CompanionPath string `json:"companionPath,omitempty"`
}

const serverConfigFile = "server-config.json"

var (
	serverSystemctl       = runSystemctlUser
	serverBinaryInstaller = installServerBinary
	serverReadyTimeout    = 6 * time.Second
)

func runSystemctlUser(ctx context.Context, args ...string) error {
	full := append([]string{"--user"}, args...)
	cmd := exec.CommandContext(ctx, "systemctl", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("systemctl --user %s: %s: %w", strings.Join(args, " "), msg, err)
		}
		return fmt.Errorf("systemctl --user %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func defaultUserSystemdDir() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot determine user systemd directory")
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func defaultServerPrefix() (string, error) {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "fased-app"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot determine server installation prefix")
	}
	return filepath.Join(home, ".local", "share", "fased-app"), nil
}

func installServerBinary(targetPath string) (string, error) {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	exePath, exeErr := os.Executable()
	if exeErr == nil && exePath != "" {
		if targetStat, tErr := os.Stat(absTarget); tErr == nil {
			if exeStat, eErr := os.Stat(exePath); eErr == nil && os.SameFile(targetStat, exeStat) &&
				!strings.HasPrefix(absTarget, "/tmp/") && !strings.Contains(absTarget, "/.cache/fased-install/") {
				return absTarget, nil
			}
		}
	}
	if err = os.MkdirAll(filepath.Dir(absTarget), 0700); err != nil {
		return "", err
	}
	srcPath := "/proc/self/exe"
	src, err := os.Open(srcPath)
	if err != nil {
		if exePath == "" {
			return "", err
		}
		src, err = os.Open(exePath)
		if err != nil {
			return "", err
		}
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(absTarget), ".fased-app-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err = io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err = tmp.Chmod(0700); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmpPath, absTarget); err != nil {
		return "", err
	}
	cleanup = false

	// Offer ~/.local/bin/fased convenience symlink if unoccupied.
	if home, hErr := os.UserHomeDir(); hErr == nil && home != "" {
		binDir := filepath.Join(home, ".local", "bin")
		if os.MkdirAll(binDir, 0755) == nil {
			linkPath := filepath.Join(binDir, "fased")
			if _, lErr := os.Lstat(linkPath); errors.Is(lErr, os.ErrNotExist) {
				_ = os.Symlink(absTarget, linkPath)
			}
		}
	}
	return absTarget, nil
}

func serverCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: fased-app server init|install|uninstall|status [options]")
	}
	base, err := defaultDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		return serverInitCommand(args[1:], base, out, errOut)
	case "install":
		return serverInstallCommand(args[1:], base, out, errOut)
	case "uninstall":
		return serverUninstallCommand(args[1:], base, out, errOut)
	case "status":
		return serverStatusCommand(args[1:], base, out, errOut)
	default:
		return errors.New("usage: fased-app server init|install|uninstall|status [options]")
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
			if _, _, tErr := tailnet.Origin("https://" + domain); tErr != nil {
				return serverInitPlan{}, errors.New("Tailscale --domain must be your MagicDNS hostname (e.g. box.tailnet.ts.net)")
			}
			publicOrigin = "https://" + domain
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
	serviceUnit := formatServerUnit(transport, strings.Join(execArgs, " "), absData, true, "multi-user.target")

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

func formatServerUnit(transport, execLine, absData string, protectHome bool, wantedBy string) string {
	homeDirective := ""
	if protectHome {
		homeDirective = "ProtectHome=read-only\n"
	}
	return fmt.Sprintf(`[Unit]
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
%sReadWritePaths=%s

[Install]
WantedBy=%s
`, transport, execLine, homeDirective, absData, wantedBy)
}

func buildUserServiceUnit(plan serverInitPlan, binPath string) string {
	execArgs := []string{
		binPath,
		"serve",
		"--instance-location", "remote",
		"--remote-transport", plan.Transport,
		"--listen", plan.Listen,
		"--data-dir", plan.DataDir,
	}
	if plan.PublicOrigin != "" {
		execArgs = append(execArgs, "--public-origin", plan.PublicOrigin)
	}
	home, _ := os.UserHomeDir()
	inHome := home != "" && (strings.HasPrefix(plan.DataDir, home+string(os.PathSeparator)) || strings.HasPrefix(binPath, home+string(os.PathSeparator)))
	return formatServerUnit(plan.Transport, strings.Join(execArgs, " "), plan.DataDir, !inHome, "default.target")
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

func serverInstallCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	prefixDefault, prefixErr := defaultServerPrefix()
	if prefixErr != nil {
		prefixDefault = filepath.Join(defaultDataDir, "bin")
	}
	systemdDefault, systemdErr := defaultUserSystemdDir()

	f := flag.NewFlagSet("server install", flag.ContinueOnError)
	f.SetOutput(errOut)
	transport := f.String("transport", "", "remote ingress: tailscale, cloudflare, domain, or ssh (default ssh)")
	domain := f.String("domain", "", "HTTPS domain for cloudflare, domain, or optional tailscale (*.ts.net)")
	listen := f.String("listen", "127.0.0.1:7331", "loopback listener")
	dataDir := f.String("data-dir", defaultDataDir, "private instance state directory")
	prefix := f.String("prefix", prefixDefault, "server binary installation directory")
	systemdDir := f.String("systemd-dir", systemdDefault, "systemd unit directory")
	binPath := f.String("binary", "", "explicit fased-app binary path (skips copying current binary)")
	start := f.Bool("start", true, "enable and start fased-server.service")
	noQR := f.Bool("no-qr", false, "omit terminal QR code when auto-pairing")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *systemdDir == "" {
		if systemdErr != nil {
			return systemdErr
		}
		return errors.New("systemd directory required")
	}

	chosenTransport := strings.ToLower(strings.TrimSpace(*transport))
	if chosenTransport == "" {
		if strings.TrimSpace(*domain) != "" {
			return errors.New("--transport is required when --domain is set")
		}
		chosenTransport = "ssh"
	}

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	absSystemd, err := filepath.Abs(*systemdDir)
	if err != nil {
		return err
	}

	resolvedBin := strings.TrimSpace(*binPath)
	if resolvedBin == "" {
		absPrefix, pErr := filepath.Abs(*prefix)
		if pErr != nil {
			return pErr
		}
		resolvedBin, err = serverBinaryInstaller(filepath.Join(absPrefix, "fased-app"))
		if err != nil {
			return err
		}
	}

	plan, err := buildServerInitPlan(chosenTransport, *domain, *listen, absData, resolvedBin)
	if err != nil {
		return err
	}
	if absSystemd != "/etc/systemd/system" {
		plan.ServiceUnit = buildUserServiceUnit(plan, resolvedBin)
	}

	if err = core.EnsurePrivateDirectory(absData); err != nil {
		return err
	}
	if err = os.MkdirAll(absSystemd, 0700); err != nil {
		return err
	}
	unitPath := filepath.Join(absSystemd, "fased-server.service")
	if err = os.WriteFile(unitPath, []byte(plan.ServiceUnit), 0600); err != nil {
		return err
	}

	companionPath := ""
	if plan.CompanionFile != "" && plan.CompanionBody != "" {
		serverSubdir := filepath.Join(absData, "server")
		if err = core.EnsurePrivateDirectory(serverSubdir); err != nil {
			return err
		}
		companionPath = filepath.Join(serverSubdir, plan.CompanionFile)
		if err = os.WriteFile(companionPath, []byte(plan.CompanionBody), 0600); err != nil {
			return err
		}
		for i, c := range plan.Commands {
			switch plan.CompanionFile {
			case "cloudflared.yml":
				plan.Commands[i] = strings.ReplaceAll(c, "cloudflared.yml", companionPath)
			case "Caddyfile":
				plan.Commands[i] = strings.ReplaceAll(c, "install -m 0644 Caddyfile ", "install -m 0644 "+companionPath+" ")
			}
		}
	}

	cfg := serverConfig{
		Version:       1,
		Transport:     plan.Transport,
		Listen:        plan.Listen,
		PublicOrigin:  plan.PublicOrigin,
		DataDir:       absData,
		BinaryPath:    resolvedBin,
		UnitPath:      unitPath,
		CompanionPath: companionPath,
	}
	if err = core.WritePrivateJSON(filepath.Join(absData, serverConfigFile), cfg); err != nil {
		return err
	}

	var (
		started    bool
		pairURL    string
		pairCode   string
		sshCommand string
	)
	if *start {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err = serverSystemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
		if err = serverSystemctl(ctx, "enable", "--now", "fased-server.service"); err != nil {
			return err
		}
		started = true

		deadline := time.Now().Add(serverReadyTimeout)
		for time.Now().Before(deadline) {
			client, cErr := core.NewClient(ctx, absData)
			if cErr == nil {
				var res struct {
					Code         string `json:"code"`
					InstanceID   string `json:"instanceId"`
					PublicOrigin string `json:"publicOrigin"`
				}
				issueErr := client.Do(ctx, http.MethodPost, "/api/v1/auth/pair/issue", struct {
					Label string `json:"label,omitempty"`
				}{}, &res)
				epURL := client.Endpoint.URL
				client.Close()
				if issueErr == nil && res.Code != "" {
					useOrigin := res.PublicOrigin
					if useOrigin == "" {
						useOrigin = epURL
					}
					if u, uErr := connection.OwnerPairURL(useOrigin, res.InstanceID, res.Code); uErr == nil {
						pairURL = u
						pairCode = res.Code
						if plan.Transport == "ssh" {
							sshCommand = sshTunnelCommand(epURL)
						}
					}
				}
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	if *jsonOut {
		payload := map[string]any{
			"installed":    true,
			"started":      started,
			"transport":    cfg.Transport,
			"listen":       cfg.Listen,
			"dataDir":      cfg.DataDir,
			"binaryPath":   cfg.BinaryPath,
			"unitPath":     cfg.UnitPath,
			"publicOrigin": cfg.PublicOrigin,
			"commands":     plan.Commands,
		}
		if cfg.CompanionPath != "" {
			payload["companionPath"] = cfg.CompanionPath
		}
		if pairURL != "" {
			payload["pairUrl"] = pairURL
			payload["pairCode"] = pairCode
		}
		if sshCommand != "" {
			payload["sshCommand"] = sshCommand
		}
		return json.NewEncoder(out).Encode(payload)
	}

	fmt.Fprintf(out, "Installed fased-server.service (%s on %s)\n", cfg.Transport, cfg.Listen)
	if cfg.PublicOrigin != "" {
		fmt.Fprintf(out, "Origin:  %s\n", cfg.PublicOrigin)
	}
	if cfg.CompanionPath != "" {
		fmt.Fprintf(out, "Config:  %s\n", cfg.CompanionPath)
	}
	if pairURL != "" {
		fmt.Fprintln(out)
		if !*noQR {
			if qr, qrErr := RenderQR(pairURL); qrErr == nil {
				fmt.Fprintln(out, qr)
			}
		}
		if sshCommand != "" {
			fmt.Fprintf(out, "SSH:   %s\n", sshCommand)
		}
		fmt.Fprintf(out, "Pair:  %s\n", pairURL)
	}
	fmt.Fprintln(out, "\nNext:")
	for _, cmd := range plan.Commands {
		fmt.Fprintf(out, "  %s\n", cmd)
	}
	return nil
}

func serverUninstallCommand(args []string, defaultDataDir string, out, errOut io.Writer) error {
	systemdDefault, _ := defaultUserSystemdDir()

	f := flag.NewFlagSet("server uninstall", flag.ContinueOnError)
	f.SetOutput(errOut)
	dataDir := f.String("data-dir", defaultDataDir, "private instance state directory")
	systemdDir := f.String("systemd-dir", systemdDefault, "systemd unit directory")
	purge := f.Bool("purge", false, "delete all instance state including keys and database")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}

	unitPath := ""
	if *systemdDir != "" {
		unitPath = filepath.Join(*systemdDir, "fased-server.service")
	}
	cfgPath := filepath.Join(absData, serverConfigFile)
	if raw, rErr := core.ReadPrivateFile(cfgPath); rErr == nil {
		var cfg serverConfig
		if json.Unmarshal(raw, &cfg) == nil && cfg.UnitPath != "" {
			unitPath = cfg.UnitPath
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = serverSystemctl(ctx, "disable", "--now", "fased-server.service")
	_ = serverSystemctl(ctx, "daemon-reload")

	removedUnit := false
	if unitPath != "" {
		if err = os.Remove(unitPath); err == nil {
			removedUnit = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_ = os.RemoveAll(filepath.Join(absData, "server"))
	_ = os.Remove(cfgPath)

	purged := false
	if *purge {
		if err = os.RemoveAll(absData); err != nil {
			return err
		}
		purged = true
	}

	if *jsonOut {
		return json.NewEncoder(out).Encode(map[string]any{
			"uninstalled": true,
			"unitRemoved": removedUnit,
			"purged":      purged,
			"dataDir":     absData,
		})
	}
	if purged {
		fmt.Fprintf(out, "Uninstalled Fased server and purged %s\n", absData)
	} else {
		fmt.Fprintf(out, "Uninstalled Fased server service. Instance state preserved at %s\n", absData)
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
		if errors.Is(err, core.ErrCoreUnavailable) {
			cfgPath := filepath.Join(*dir, serverConfigFile)
			if raw, rErr := core.ReadPrivateFile(cfgPath); rErr == nil {
				var cfg serverConfig
				if json.Unmarshal(raw, &cfg) == nil {
					_, unitErr := os.Stat(cfg.UnitPath)
					status := map[string]any{
						"running":       false,
						"transport":     cfg.Transport,
						"listen":        cfg.Listen,
						"publicOrigin":  cfg.PublicOrigin,
						"unitPath":      cfg.UnitPath,
						"unitInstalled": unitErr == nil,
						"pairedDevices": 0,
					}
					if *jsonOut {
						return json.NewEncoder(out).Encode(status)
					}
					fmt.Fprintf(out, "Status:    stopped\nTransport: %s\nListen:    %s\n", cfg.Transport, cfg.Listen)
					if cfg.PublicOrigin != "" {
						fmt.Fprintf(out, "Origin:    %s\n", cfg.PublicOrigin)
					}
					fmt.Fprintf(out, "Unit:      %s (present: %v)\n", cfg.UnitPath, unitErr == nil)
					return nil
				}
			}
		}
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
		"running":       true,
		"instanceId":    health.InstanceID,
		"version":       health.Version,
		"location":      location,
		"transport":     transport,
		"listenUrl":     client.Endpoint.URL,
		"publicOrigin":  client.Endpoint.PublicOrigin,
		"pairedDevices": len(managed.Devices),
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
