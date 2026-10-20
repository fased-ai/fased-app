package localinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const unitName = "fased-app.service"
const engineName = "fased-app"
const desktopName = "fased-desktop"
const definitionName = unitName

func defaultRoot(home string) string { return filepath.Join(home, ".local", "share", "fased-app") }

func supported() error {
	if os.Geteuid() == 0 {
		return errors.New("run Fased App as an ordinary OS user")
	}
	return nil
}
func quote(value string) string {
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "$", "$$").Replace(value)
	return "\"" + value + "\""
}
func definition(p Paths, o Options) ([]byte, error) {
	if _, err := validateDefinition(p, o); err != nil {
		return nil, err
	}
	args := []string{quote(filepath.Join(p.Root, "fased-app")), "serve", "--data-dir", quote(p.Data), "--listen", quote(o.Listen), "--require-sandbox", "--installation-root", quote(p.Root)}
	if o.PublicOrigin != "" {
		args = append(args, "--public-origin", quote(o.PublicOrigin))
	}
	unit := "[Unit]\nDescription=Fased App private core\nAfter=network.target\n\n[Service]\nType=simple\nExecStart=" + strings.Join(args, " ") + "\nRestart=on-failure\nRestartSec=3\nTimeoutStopSec=15\nUMask=0077\nNoNewPrivileges=yes\nPrivateUsers=yes\nPrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\nReadOnlyPaths=" + quote(p.Root) + "\nReadWritePaths=" + quote(p.Data) + "\nProtectControlGroups=yes\nProtectKernelTunables=yes\nRestrictSUIDSGID=yes\nLockPersonality=yes\n\n[Install]\nWantedBy=default.target\n"
	return []byte(unit), nil
}
func systemctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("user service manager refused %s: %s", args[0], strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
func properties(ctx context.Context, m Manifest) (map[string]string, error) {
	output, err := systemctl(ctx, "show", unitName, "--property=LoadState,ActiveState,FragmentPath,UnitFileState")
	p := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			p[key] = value
		}
	}
	if p["LoadState"] == "not-found" {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(p["FragmentPath"])
	if err != nil || actual != filepath.Join(m.Root, unitName) {
		return nil, errors.New("another service uses the Fased App name; refusing to modify it")
	}
	return p, nil
}
func register(ctx context.Context, m Manifest) error {
	if err := supported(); err != nil {
		return err
	}
	p, err := properties(ctx, m)
	if err != nil {
		return err
	}
	if p["LoadState"] == "not-found" {
		if _, err = systemctl(ctx, "link", filepath.Join(m.Root, unitName)); err != nil {
			return err
		}
	}
	_, err = systemctl(ctx, "daemon-reload")
	return err
}
func enable(ctx context.Context, m Manifest) error {
	if _, err := properties(ctx, m); err != nil {
		return err
	}
	_, err := systemctl(ctx, "enable", unitName)
	return err
}
func start(ctx context.Context, m Manifest) error {
	if _, err := properties(ctx, m); err != nil {
		return err
	}
	_, err := systemctl(ctx, "start", unitName)
	return err
}
func stop(ctx context.Context, m Manifest) error {
	p, err := properties(ctx, m)
	if err != nil {
		return err
	}
	if p["LoadState"] == "not-found" {
		return nil
	}
	_, err = systemctl(ctx, "stop", unitName)
	return err
}
func status(ctx context.Context, m Manifest) (State, error) {
	p, err := properties(ctx, m)
	if err != nil {
		return State{}, err
	}
	return State{Installed: true, Active: p["ActiveState"] == "active", Autostart: p["UnitFileState"] == "enabled"}, nil
}
func unregister(ctx context.Context, m Manifest) error {
	p, err := properties(ctx, m)
	if err != nil {
		return err
	}
	if p["LoadState"] == "not-found" {
		return nil
	}
	if err = stop(ctx, m); err != nil {
		return err
	}
	if _, err = systemctl(ctx, "disable", unitName); err != nil {
		return err
	}
	_, err = systemctl(ctx, "daemon-reload")
	return err
}
