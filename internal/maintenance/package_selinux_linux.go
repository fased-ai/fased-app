//go:build linux

package maintenance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type packageSELinuxRule struct {
	pattern  string
	examples []string
}

// Only immutable executables receive an executable label. Application state,
// signer data, approvals and foreign package instances retain their labels.
func packageSELinuxRules(a Authority, location, instance string) ([]packageSELinuxRule, error) {
	if err := validatePackageAuthority(a, a.Owner, location, instance); err != nil {
		return nil, err
	}
	root, err := update.PackageInstanceRoot(a.Owner, location, instance)
	if err != nil {
		return nil, err
	}
	state, err := update.PackageMaintenanceRoot(a.Owner, location, instance)
	if err != nil {
		return nil, err
	}
	parent := regexp.QuoteMeta(filepath.Dir(root))
	id := regexp.QuoteMeta(instance)
	image := ImagePath(a.ImageSHA256)
	stage := filepath.Join(filepath.Dir(root), "."+instance+".pending")
	updateDir := state + "/updates/" + strings.Repeat("0", 32)
	return []packageSELinuxRule{
		{regexp.QuoteMeta(image), []string{image}},
		{parent + "/(" + id + "|[.]" + id + "[.]pending)/fased-(app|install|signer)", []string{root + "/fased-app", root + "/fased-install", root + "/fased-signer", stage + "/fased-app", stage + "/fased-install", stage + "/fased-signer"}},
		{regexp.QuoteMeta(state) + "/(provision|updates/[0-9a-f]{32})/fased-(app|signer)", []string{state + "/provision/fased-app", state + "/provision/fased-signer", updateDir + "/fased-app", updateDir + "/fased-signer"}},
	}, nil
}

var packageExecutablePattern = regexp.MustCompile(`^(/var/lib/fased-maintenance/images/[0-9a-f]{64}/fased-install|/var/lib/fased-app-package/[0-9]+/(local|remote)/([0-9a-f]{32}|[.][0-9a-f]{32}[.]pending)/fased-(app|install|signer)|/var/lib/fased-maintenance/instances/[0-9]+/(local|remote)/[0-9a-f]{32}/(provision|updates/[0-9a-f]{32})/fased-(app|signer))$`)

func packageExecutablePath(path string) bool {
	return filepath.Clean(path) == path && packageExecutablePattern.MatchString(path)
}

func selinuxEnabled() (bool, error) {
	_, err := os.Stat("/sys/fs/selinux/enforce")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func selinuxCommand(name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	return c.CombinedOutput()
}

func selinuxBinContext(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ":")
	return len(parts) >= 4 && parts[2] == "bin_t"
}

// Register the exact Fased files with the standard executable type. The rules
// persist across filesystem relabelling and do not grant an operator OS tools.
func configurePackageSELinux(a Authority, location, instance string) error {
	enabled, err := selinuxEnabled()
	if err != nil || !enabled {
		return err
	}
	rules, err := packageSELinuxRules(a, location, instance)
	if err != nil {
		return err
	}
	query := func(path string) (bool, error) {
		context, e := selinuxCommand("/usr/sbin/matchpathcon", "-n", "-m", "file", path)
		if e != nil {
			return false, errors.New("cannot inspect SELinux executable policy")
		}
		return selinuxBinContext(string(context)), nil
	}
	add := func(pattern string) error {
		// Only file-context mappings change; do not reload the host's kernel
		// policy or affect unrelated services during package registration.
		_, e := selinuxCommand("/usr/sbin/semanage", "fcontext", "-a", "-N", "-f", "f", "-t", "bin_t", pattern)
		return e
	}
	if err = applyPackageSELinuxRules(rules, query, add); err != nil {
		return err
	}
	return restorePackageExecutable(ImagePath(a.ImageSHA256), 0555)
}

func applyPackageSELinuxRules(rules []packageSELinuxRule, query func(string) (bool, error), add func(string) error) error {
	if query == nil || add == nil {
		return errors.New("SELinux policy inspection and registration are required")
	}
	for _, rule := range rules {
		matches := func() (bool, error) {
			for _, example := range rule.examples {
				match, e := query(example)
				if e != nil || !match {
					return false, e
				}
			}
			return true, nil
		}
		matched, err := matches()
		if err != nil {
			return err
		}
		if !matched {
			addErr := add(rule.pattern)
			// A second protected registration can add the same image rule.
			// An existing conflicting rule is never modified or deleted.
			matched, err = matches()
			if err != nil {
				return err
			}
			if !matched {
				if addErr != nil {
					return fmt.Errorf("cannot register the fixed Fased SELinux executable policy; preserve existing policy: %w", addErr)
				}
				return errors.New("Fased SELinux executable policy did not take effect")
			}
		}
	}
	return nil
}

// Called only by fixed protected copy and recovery operations, after the file
// has been checked as root-owned, regular and at the expected fixed path.
func restorePackageExecutable(path string, mode os.FileMode) error {
	if !packageExecutablePath(path) {
		return nil
	}
	enabled, err := selinuxEnabled()
	if err != nil || !enabled {
		return err
	}
	f, err := openProtected(path, 128<<20, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	// Fedora's restricted systemd domain cannot execute restorecon with
	// NoNewPrivileges. chcon is an ordinary bin_t executable and changes only
	// the type of this already-verified root-owned file. The matching fcontext
	// rule above keeps the type across a later filesystem relabel.
	if _, err = selinuxCommand("/usr/bin/chcon", "-t", "bin_t", "--", path); err != nil {
		return fmt.Errorf("cannot label the protected Fased executable: %w", err)
	}
	size, err := unix.Fgetxattr(int(f.Fd()), "security.selinux", nil)
	if err != nil || size <= 0 || size > 1024 {
		return errors.New("protected Fased executable SELinux label is unavailable")
	}
	value := make([]byte, size)
	n, err := unix.Fgetxattr(int(f.Fd()), "security.selinux", value)
	if err != nil || !selinuxBinContext(strings.TrimRight(string(value[:n]), "\x00")) {
		return errors.New("protected Fased executable has the wrong SELinux label")
	}
	return nil
}

// Recovery repairs a label interrupted after atomic replacement before either
// service can execute the pair. The existing journal still selects its target.
func RestorePackageServiceExecutables(owner int, location, instance string) error {
	return restorePackageServiceExecutables(owner, location, instance, restorePackageExecutable)
}

func restorePackageServiceExecutables(owner int, location, instance string, restore func(string, os.FileMode) error) error {
	root, err := update.PackageInstanceRoot(owner, location, instance)
	if err != nil || restore == nil {
		return errors.New("exact package executable recovery required")
	}
	for _, name := range []string{"fased-install", "fased-app", "fased-signer"} {
		if err = restore(filepath.Join(root, name), 0755); err != nil {
			return err
		}
	}
	return nil
}
