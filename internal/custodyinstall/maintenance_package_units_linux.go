//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func packageWorkerTemplate(m Manifest) string {
	return fmt.Sprintf("fased-package-maintenance-%d-%s-%s@.service", m.Owner, m.Location, m.ID)
}

// Fedora ships this system-wide stop-timeout default. Its caller also checks
// the effective crash-dump limit. Never accept arbitrary vendor drop-ins.
func allowedPackageDropIns(value string, read readHost) bool {
	paths := strings.Fields(value)
	if len(paths) == 0 {
		return true
	}
	const vendor = "/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
	if len(paths) != 1 || paths[0] != vendor || read == nil {
		return false
	}
	f, err := read(vendor)
	if err != nil || f.UID != 0 || !f.Regular || f.Mode != 0644 {
		return false
	}
	section, setting := false, false
	for _, line := range strings.Split(string(f.Data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		switch {
		case line == "[Service]" && !section && !setting:
			section = true
		case line == "TimeoutStopFailureMode=abort" && section && !setting:
			setting = true
		default:
			return false
		}
	}
	return section && setting
}

// These are the only root service entries for this exact package instance.
// The application and signer units remain unprivileged.
func packageMaintenanceFiles(a maintenance.Authority, m Manifest) (map[string][]byte, error) {
	if m.validatePackage() != nil || a.Owner != m.Owner || a.Profile != m.Location || !validHex(a.ImageSHA256, 32) {
		return nil, errors.New("exact package maintenance authority required")
	}
	state, err := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
	if err != nil {
		return nil, err
	}
	u, err := user.LookupId(strconv.Itoa(m.Owner))
	if err != nil {
		return nil, err
	}
	image := maintenance.ImagePath(a.ImageSHA256)
	args := fmt.Sprintf("--owner %d --location %s --instance %s", m.Owner, m.Location, m.ID)
	units := protectedInstanceUnits(m)
	ordering := strings.Join(units, " ")
	protect := fmt.Sprintf("TimeoutStartSec=10min\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\nReadWritePaths=%s %s\nRestrictSUIDSGID=yes\nLimitCORE=0\n", m.Root, state)
	recovery := fmt.Sprintf("[Unit]\nDescription=Fased update recovery\nBefore=%s %s\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s maintenance-package-recover %s\n%s[Install]\nWantedBy=multi-user.target\n", instanceUnit(m, "app"), instanceUnit(m, "signer"), image, args, protect)
	worker := fmt.Sprintf("[Unit]\nDescription=Fased update\nWants=%s\nAfter=%s %s\n[Service]\nType=oneshot\nExecStart=%s maintenance-package-worker %s --operation %%i\n%s", ordering, packageRecoveryUnit(m), ordering, image, args, protect)
	policy := fmt.Sprintf("polkit.addRule(function(action, subject) {\n  if (action.id == 'org.freedesktop.systemd1.manage-units' && (subject.user == %s || subject.user == %s) && action.lookup('verb') == 'start' && /^fased-package-maintenance-%d-%s-%s@[0-9a-f]{32}\\.service$/.test(action.lookup('unit'))) return polkit.Result.YES;\n});\n", strconv.Quote(u.Username), strconv.Quote(m.App.Name), m.Owner, m.Location, m.ID)
	return map[string][]byte{
		filepath.Join("/etc/systemd/system", packageRecoveryUnit(m)):                                    []byte(recovery),
		filepath.Join("/etc/systemd/system", packageWorkerTemplate(m)):                                  []byte(worker),
		fmt.Sprintf("/etc/polkit-1/rules.d/60-fased-package-%d-%s-%s.rules", m.Owner, m.Location, m.ID): []byte(policy),
	}, nil
}

func installPackageMaintenanceUnits(ctx context.Context, a maintenance.Authority, m Manifest) error {
	files, err := packageMaintenanceFiles(a, m)
	if err != nil {
		return err
	}
	// Distribution-owned polkit directories can legitimately use 0750 and a
	// service group. Verify their owner/ancestry without changing their mode.
	if err = trustedParents("/etc/polkit-1/rules.d"); err != nil {
		return errors.New("protected polkit rules directory required")
	}
	for path, want := range files {
		actual, e := readRoot(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !known(actual, want, 0644) {
			return errors.New("foreign package maintenance setup; preserve it")
		}
	}
	for path, want := range files {
		if err = maintenance.PublishProtected(path, want, 0644); err != nil {
			return err
		}
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err = verifyPackageRootUnits(ctx, a, m, m.ID); err != nil {
		return err
	}
	_, err = protectedSystemctl(ctx, "enable", "--", packageRecoveryUnit(m))
	return err
}

func verifyPackageRootUnits(ctx context.Context, a maintenance.Authority, m Manifest, operation string) error {
	if !update.IDOK(operation) {
		return errors.New("exact package operation required")
	}
	files, err := packageMaintenanceFiles(a, m)
	if err != nil {
		return err
	}
	for path, wanted := range files {
		actual, e := readRoot(path)
		if e != nil || !known(actual, wanted, 0644) {
			return errors.New("protected package service or policy differs; preserve it")
		}
	}
	image := maintenance.ImagePath(a.ImageSHA256)
	args := []string{"--owner", strconv.Itoa(m.Owner), "--location", m.Location, "--instance", m.ID}
	for _, item := range []struct {
		unit, fragment, command string
		worker                  bool
	}{
		{packageRecoveryUnit(m), packageRecoveryUnit(m), "maintenance-package-recover", false},
		{instanceWorkerUnit(m, operation), packageWorkerTemplate(m), "maintenance-package-worker", true},
	} {
		fields, commands, e := atomicUnitInspection(ctx, item.unit)
		if e != nil {
			return e
		}
		if fields["LoadState"] != "loaded" || fields["FragmentPath"] != filepath.Join("/etc/systemd/system", item.fragment) || !allowedPackageDropIns(fields["DropInPaths"], readRoot) || fields["NeedDaemonReload"] != "no" || fields["Type"] != "oneshot" || fields["User"] != "" || fields["Group"] != "" || fields["NoNewPrivileges"] != "yes" || fields["LimitCORE"] != "0" {
			return errors.New("foreign or stale package maintenance service; preserve it")
		}
		wanted := append([]string{image, item.command}, args...)
		if item.worker {
			wanted = append(wanted, "--operation", operation)
		}
		if e = validateAtomicCommandRows(commands, []atomicPairCommand{{argv: wanted}}); e != nil {
			return e
		}
	}
	return nil
}
