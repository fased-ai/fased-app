package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/providersecrets"
)

type doctorReport struct {
	Version        string                    `json:"version"`
	OS             string                    `json:"os"`
	Arch           string                    `json:"arch"`
	DataDir        string                    `json:"dataDir"`
	DataDirExists  bool                      `json:"dataDirExists"`
	DataDirPrivate bool                      `json:"dataDirPrivate"`
	DaemonRunning  bool                      `json:"daemonRunning"`
	DaemonURL      string                    `json:"daemonUrl,omitempty"`
	OSVaultKind    string                    `json:"osVaultKind"`
	OSVaultReady   bool                      `json:"osVaultReady"`
	InstallFound   bool                      `json:"installFound"`
	ServerConfig   bool                      `json:"serverConfig"`
	Databases      []core.SQLiteHealthReport `json:"databases"`
	Tools          map[string]bool           `json:"tools"`
	Healthy        bool                      `json:"healthy"`
	Warnings       []string                  `json:"warnings,omitempty"`
}

func probeOSVaultReadOnly() (string, bool) {
	switch runtime.GOOS {
	case "linux":
		if os.Getenv("CREDENTIALS_DIRECTORY") != "" {
			if _, err := os.Stat(filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), providersecrets.CredentialName)); err == nil {
				return "systemd-creds", true
			}
		}
		if _, err := exec.LookPath("secret-tool"); err == nil {
			return "secret-service", true
		}
		if _, err := exec.LookPath("systemd-creds"); err == nil {
			return "systemd-creds-available", true
		}
		return "none", false
	case "darwin":
		if _, err := os.Stat("/usr/bin/security"); err == nil {
			return "macos-keychain", true
		}
		return "none", false
	case "windows":
		return "windows-dpapi", true
	default:
		return "none", false
	}
}

func doctorCommand(args []string, out, errOut io.Writer) error {
	defaultData, err := defaultDir()
	if err != nil {
		return err
	}
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	f.SetOutput(errOut)
	dataDir := f.String("data-dir", defaultData, "private instance state directory")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	vaultKind, vaultReady := probeOSVaultReadOnly()
	rep := doctorReport{
		Version:      core.Version,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		DataDir:      absData,
		OSVaultKind:  vaultKind,
		OSVaultReady: vaultReady,
		Tools:        map[string]bool{},
		Healthy:      true,
	}

	if info, statErr := os.Lstat(absData); statErr == nil && info.IsDir() {
		rep.DataDirExists = true
		if runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0 {
			rep.DataDirPrivate = true
		} else {
			rep.Warnings = append(rep.Warnings, "data directory permissions are wider than 0700")
			rep.Healthy = false
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		rep.Warnings = append(rep.Warnings, "data directory is not accessible")
		rep.Healthy = false
	}

	coreDB := core.InspectSQLiteHealth(ctx, filepath.Join(absData, "core.sqlite"))
	agentsDB := core.InspectSQLiteHealth(ctx, filepath.Join(absData, "agents.sqlite"))
	rep.Databases = []core.SQLiteHealthReport{coreDB, agentsDB}
	for _, db := range rep.Databases {
		if db.Exists && !db.Healthy {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s integrity check failed (%s)", db.Name, db.QuickCheck))
			rep.Healthy = false
		}
	}

	if paths, pErr := localinstall.DefaultPaths(); pErr == nil {
		if _, lErr := localinstall.Load(paths); lErr == nil {
			rep.InstallFound = true
		}
	}
	if _, sErr := os.Stat(filepath.Join(absData, serverConfigFile)); sErr == nil {
		rep.ServerConfig = true
	}

	if c, cErr := core.NewClient(ctx, absData); cErr == nil {
		var health map[string]any
		if hErr := c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); hErr == nil {
			rep.DaemonRunning = true
			rep.DaemonURL = c.Endpoint.URL
		}
		c.Close()
	}

	for _, bin := range []string{"codex", "claude", "gemini", "grok", "antigravity", "tailscale", "bwrap"} {
		_, lookErr := exec.LookPath(bin)
		rep.Tools[bin] = lookErr == nil
	}
	if !rep.OSVaultReady {
		rep.Warnings = append(rep.Warnings, "native OS secret vault unavailable; background tasks require passphrase unlock")
	}

	if *jsonOut {
		return json.NewEncoder(out).Encode(rep)
	}
	fmt.Fprintf(out, "Fased Doctor (%s %s/%s)\n", rep.Version, rep.OS, rep.Arch)
	fmt.Fprintf(out, "  Data dir:     %s (exists=%v private=%v)\n", rep.DataDir, rep.DataDirExists, rep.DataDirPrivate)
	fmt.Fprintf(out, "  Daemon:       running=%v", rep.DaemonRunning)
	if rep.DaemonURL != "" {
		fmt.Fprintf(out, " (%s)", rep.DaemonURL)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  OS vault:     %s (ready=%v)\n", rep.OSVaultKind, rep.OSVaultReady)
	for _, db := range rep.Databases {
		fmt.Fprintf(out, "  Database:     %s exists=%v bytes=%d wal=%d check=%s\n", db.Name, db.Exists, db.Bytes, db.WALBytes, db.QuickCheck)
	}
	for _, w := range rep.Warnings {
		fmt.Fprintf(out, "  Warning:      %s\n", w)
	}
	return nil
}
