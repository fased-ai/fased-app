//go:build linux

package hostprep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/packaging/linux/apparmor"
)

// PrepareProtected is called only by the registered helper's root-bootstrap
// entry. There is no command, package, policy, URL or pathname in its request.
func PrepareProtected(ctx context.Context, owner int, out io.Writer) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	p, err := hostplatform.Current()
	if err != nil {
		return err
	}
	packages := append(p.Packages(a.Profile == "local"), "polkitd")
	if a.Profile == "remote" {
		packages = append(packages, "nftables", "openssh-server")
	}
	var missing []string
	for _, name := range packages {
		b, err := protectedOS(ctx, nil, out, "/usr/bin/dpkg-query", "-W", "-f=${Status}", name)
		if err != nil || string(b) != "install ok installed" {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		if _, err = protectedOS(ctx, nil, out, "/usr/bin/apt-get", "update"); err != nil {
			return err
		}
		args := append([]string{"/usr/bin/apt-get", "install", "--yes", "--no-remove", "--no-install-recommends"}, missing...)
		if _, err = protectedOS(ctx, nil, out, args...); err != nil {
			return err
		}
	}
	if p.RestrictedNamespaces {
		policy := apparmor.Policy()
		if policyDigest(policy) != PolicySHA256 {
			return errors.New("reviewed sandbox policy differs")
		}
		if err = ValidateTarget(); err != nil {
			return err
		}
		loaded, err := protectedOS(ctx, nil, out, "/usr/bin/cat", "/sys/kernel/security/apparmor/profiles")
		if err != nil {
			return err
		}
		if _, err = os.Lstat(policyTarget); errors.Is(err, os.ErrNotExist) {
			for _, line := range strings.Split(string(loaded), "\n") {
				if strings.HasPrefix(line, "bwrap ") || strings.HasPrefix(line, "bwrap//") || strings.HasPrefix(line, "unpriv_bwrap ") {
					return errors.New("foreign loaded sandbox policy; preserving it")
				}
			}
		}
		if err = maintenance.PublishProtected(policyTarget, policy, 0644); err != nil {
			return err
		}
		if _, err = protectedOS(ctx, nil, out, "/usr/sbin/apparmor_parser", "-r", policyTarget); err != nil {
			return err
		}
	}
	if a.Profile != "remote" {
		return nil
	}
	// Existing installations keep their daemon enrollment and operator. Setup
	// of another namespace must never take over an administrator's tailnet.
	for _, path := range []string{fmt.Sprintf("/var/lib/fased-app-custody-%d/install.json", owner), fmt.Sprintf("/var/lib/fased-app-vps-%d/install.json", owner)} {
		if _, e := os.Lstat(path); e == nil {
			return nil
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	u, err := user.LookupId(strconv.Itoa(owner))
	if err != nil {
		return err
	}
	dir := filepath.Join(maintenance.OwnerRoot(owner), "tailscale")
	if err = core.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	run := func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		return protectedOS(ctx, b, out, args...)
	}
	if _, err = readPreparationFile("/usr/bin/tailscale"); errors.Is(err, os.ErrNotExist) {
		if err = prepareTailscalePackage(ctx, dir, owner, readPreparationFile, run, p); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err = run(ctx, nil, "/usr/bin/systemctl", "enable", "--now", "tailscaled.service"); err != nil {
		return err
	}
	b, err := run(ctx, nil, "/usr/bin/tailscale", "debug", "prefs")
	if err != nil || len(b) > 64<<10 {
		return errors.New("Tailscale preferences unavailable; preserve enrollment")
	}
	var prefs struct{ OperatorUser string }
	if json.Unmarshal(b, &prefs) != nil || prefs.OperatorUser != "" && prefs.OperatorUser != u.Username {
		return errors.New("Tailscale has a different operator; preserve existing access")
	}
	if prefs.OperatorUser == "" {
		_, err = run(ctx, nil, "/usr/bin/tailscale", "set", "--operator="+u.Username)
	}
	return err
}

// Only trusted semantic Go code above supplies these arguments. It is not a
// caller-accessible command interface and never executes an application image.
func protectedOS(ctx context.Context, input []byte, out io.Writer, args ...string) ([]byte, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 || len(args) == 0 {
		return nil, errors.New("protected OS preparation required")
	}
	if err := trustedHostTool(args[0]); err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/nonexistent", "LANG=C", "DEBIAN_FRONTEND=noninteractive"}
	c.Stdin = bytes.NewReader(input)
	c.Stderr = out
	if args[0] == "/usr/bin/apt-get" {
		c.Stdout = out
		return nil, c.Run()
	}
	var b boundedPreparationOutput
	c.Stdout = &b
	err := c.Run()
	return b.b, err
}

type boundedPreparationOutput struct{ b []byte }

func (b *boundedPreparationOutput) Write(p []byte) (int, error) {
	if len(b.b)+len(p) > 1<<20 {
		return 0, errors.New("oversized OS response")
	}
	b.b = append(b.b, p...)
	return len(p), nil
}
