//go:build linux

package vpsnetwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/hostplatform"
)

const fedoraCryptoPolicyInclude = "/etc/crypto-policies/back-ends/opensshserver.config"
const vendorAbortDropIn = "/usr/lib/systemd/system/service.d/10-timeout-abort.conf"

func sshServiceForPlatform(p hostplatform.Platform) string {
	if p.Family == "dnf" {
		return "sshd.service"
	}
	return "ssh.service"
}

// allowedVendorDropIns mirrors custodyinstall's reviewed vendor drop-in guard:
// only empty DropInPaths or Fedora's exact root-owned 10-timeout-abort.conf is accepted.
func allowedVendorDropIns(o hostOps, value string) bool {
	paths := strings.Fields(value)
	if len(paths) == 0 {
		return true
	}
	if len(paths) != 1 || paths[0] != vendorAbortDropIn {
		return false
	}
	f, err := rootFile(o, vendorAbortDropIn)
	if err != nil || !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm() != 0644 {
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

func (n networkController) readSSHConfig(ctx context.Context, path string) ([]byte, error) {
	if filepath.Dir(path) == sshConfigDir {
		f, err := readRootFileCtx(ctx, n.ops, path)
		if err != nil {
			return nil, err
		}
		return f.Data, nil
	}
	if err := parents(n.ops, filepathParent(path)); err != nil {
		return nil, err
	}
	f, err := n.ops.read(path)
	if err != nil && !errors.Is(err, os.ErrPermission) {
		return nil, err
	}
	if !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm()&0022 != 0 ||
		f.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, errors.New("SSH configuration must be a root-owned immutable regular file")
	}
	if errors.Is(err, os.ErrPermission) {
		if n.ops.run == nil {
			return nil, os.ErrPermission
		}
		f.Data, err = n.ops.run(ctx, nil, "/usr/bin/cat", "--", path)
		if err != nil {
			return nil, err
		}
	}
	if len(f.Data) > 64<<10 {
		return nil, errors.New("bounded SSH configuration required")
	}
	return f.Data, nil
}

func (n networkController) readCryptoPolicyInclude(ctx context.Context) ([]byte, error) {
	if err := parents(n.ops, filepath.Dir(fedoraCryptoPolicyInclude)); err != nil {
		return nil, err
	}
	cf, err := n.ops.read(fedoraCryptoPolicyInclude)
	if err != nil {
		return nil, err
	}
	if cf.Mode&os.ModeSymlink != 0 {
		if cf.UID != 0 || cf.GID != 0 || cf.Link == "" || strings.Contains(cf.Link, "..") {
			return nil, errors.New("untrusted SSH crypto-policies symlink; preserve it")
		}
		resolved := cf.Link
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(fedoraCryptoPolicyInclude), resolved)
		}
		resolved = filepath.Clean(resolved)
		if !strings.HasPrefix(resolved, "/usr/share/crypto-policies/") {
			return nil, errors.New("untrusted SSH crypto-policies target; preserve it")
		}
		return n.readSSHConfig(ctx, resolved)
	}
	return n.readSSHConfig(ctx, fedoraCryptoPolicyInclude)
}

func scanSSHConfig(b []byte, allowDropInInclude, allowCryptoInclude bool) (sawDropIn, sawCrypto bool, err error) {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.SplitN(line, "#", 2)[0]
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "match":
			return false, false, errors.New("conditional SSH Match policy is unsupported; preserve it and use console-reviewed administration")
		case "include":
			if allowDropInInclude && len(f) == 2 && f[1] == "/etc/ssh/sshd_config.d/*.conf" {
				sawDropIn = true
				continue
			}
			if allowCryptoInclude && len(f) == 2 && f[1] == fedoraCryptoPolicyInclude {
				sawCrypto = true
				continue
			}
			return false, false, errors.New("only reviewed sshd_config.d and vendor crypto-policy includes are supported; preserve other includes")
		}
	}
	return sawDropIn, sawCrypto, nil
}

// checkSSHProfile validates that bare `sshd -t` / `sshd -T` accurately models the
// running OpenSSH daemon on Debian/Ubuntu (ssh.service) or Fedora (sshd.service).
func (n networkController) checkSSHProfile(ctx context.Context, p hostplatform.Platform) error {
	if err := n.checkSSHInvocation(ctx, p); err != nil {
		return err
	}
	mainBytes, err := n.readSSHConfig(ctx, "/etc/ssh/sshd_config")
	if err != nil {
		return err
	}
	sawDropIn, _, err := scanSSHConfig(mainBytes, true, false)
	if err != nil {
		return err
	}
	if !sawDropIn {
		return errors.New("literal sshd_config.d include is required; no SSH file is rewritten")
	}
	names, err := listSSHDir(ctx, n.ops)
	if err != nil {
		return err
	}
	needCryptoCheck := false
	for _, name := range names {
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		if strings.ContainsAny(name, "/\x00\r\n") {
			return errors.New("invalid SSH snippet name")
		}
		b, rErr := n.readSSHConfig(ctx, sshConfigDir+"/"+name)
		if rErr != nil {
			return rErr
		}
		_, sawCrypto, sErr := scanSSHConfig(b, false, p.Family == "dnf")
		if sErr != nil {
			return sErr
		}
		if sawCrypto {
			needCryptoCheck = true
		}
	}
	if needCryptoCheck {
		policyBytes, pErr := n.readCryptoPolicyInclude(ctx)
		if pErr != nil {
			return pErr
		}
		if _, _, sErr := scanSSHConfig(policyBytes, false, false); sErr != nil {
			return sErr
		}
	}
	return nil
}

func (n networkController) checkSSHInvocation(ctx context.Context, plat hostplatform.Platform) error {
	if plat.Family == "dnf" {
		return n.checkFedoraSSHInvocation(ctx)
	}
	return n.checkDebianUbuntuSSHInvocation(ctx)
}

func (n networkController) checkDebianUbuntuSSHInvocation(ctx context.Context) error {
	data, err := n.ops.command(ctx, "/usr/bin/systemctl", "show", "ssh.service", "--property=LoadState,FragmentPath,DropInPaths,ExecStart,EnvironmentFiles,Environment,PassEnvironment")
	if err != nil {
		return err
	}
	p := fields(data)
	if p["LoadState"] != "loaded" || !allowedVendorDropIns(n.ops, p["DropInPaths"]) ||
		p["Environment"] != "" || p["PassEnvironment"] != "" || p["EnvironmentFiles"] != "/etc/default/ssh (ignore_errors=yes)" {
		return errors.New("custom SSH service overrides or environment are unsupported; preserve them and use console-reviewed administration")
	}
	vendor := p["FragmentPath"]
	if vendor == "/lib/systemd/system/ssh.service" {
		lib, le := n.ops.read("/lib")
		if le != nil {
			return le
		}
		if lib.Mode&os.ModeSymlink != 0 {
			if lib.UID != 0 || lib.GID != 0 || (lib.Link != "usr/lib" && lib.Link != "/usr/lib") {
				return errors.New("untrusted SSH vendor path alias")
			}
			vendor = "/usr/lib/systemd/system/ssh.service"
		}
	}
	if vendor != "/usr/lib/systemd/system/ssh.service" && vendor != "/lib/systemd/system/ssh.service" {
		return errors.New("only the vendor ssh.service unit is supported; preserve custom units")
	}
	source, err := n.readSSHConfig(ctx, vendor)
	if err != nil {
		return err
	}
	section := ""
	starts, envFiles := 0, 0
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Service]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "ExecStart":
			starts++
			if value != "/usr/sbin/sshd -D $SSHD_OPTS" {
				return errors.New("custom SSH -f/-o or executable invocation is unsupported")
			}
		case "EnvironmentFile":
			envFiles++
			if value != "-/etc/default/ssh" {
				return errors.New("custom SSH environment file is unsupported")
			}
		case "Environment", "PassEnvironment":
			if value != "" {
				return errors.New("custom SSH service environment is unsupported")
			}
		}
	}
	if starts != 1 || envFiles != 1 {
		return errors.New("unrecognized SSH vendor invocation")
	}
	execLine := p["ExecStart"]
	_, argv, ok := strings.Cut(execLine, "argv[]=")
	if !ok || !strings.Contains(execLine, "path=/usr/sbin/sshd ;") {
		return errors.New("loaded SSH executable is unsupported")
	}
	argv = strings.TrimSpace(strings.SplitN(argv, " ;", 2)[0])
	if argv != "/usr/sbin/sshd -D $SSHD_OPTS" && argv != "/usr/sbin/sshd -D" {
		return errors.New("loaded SSH -f/-o or other daemon options are unsupported")
	}
	return n.checkEmptySSHDefaults(ctx, "/etc/default/ssh", "SSHD_OPTS")
}

func (n networkController) checkFedoraSSHInvocation(ctx context.Context) error {
	data, err := n.ops.command(ctx, "/usr/bin/systemctl", "show", "sshd.service", "--property=LoadState,FragmentPath,DropInPaths,ExecStart,EnvironmentFiles,Environment,PassEnvironment")
	if err != nil {
		return err
	}
	p := fields(data)
	envFiles := p["EnvironmentFiles"]
	if p["LoadState"] != "loaded" || !allowedVendorDropIns(n.ops, p["DropInPaths"]) ||
		(p["Environment"] != "" && p["Environment"] != "OPTIONS=") ||
		p["PassEnvironment"] != "" ||
		(envFiles != "/etc/sysconfig/sshd (ignore_errors=yes)" && envFiles != "") {
		return errors.New("custom SSH service overrides or environment are unsupported; preserve them and use console-reviewed administration")
	}
	vendor := p["FragmentPath"]
	if vendor != "/usr/lib/systemd/system/sshd.service" {
		return errors.New("only the vendor sshd.service unit is supported; preserve custom units")
	}
	source, err := n.readSSHConfig(ctx, vendor)
	if err != nil {
		return err
	}
	section := ""
	starts, envFileCount := 0, 0
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Service]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "ExecStart":
			starts++
			if value != "/usr/sbin/sshd -D $OPTIONS" {
				return errors.New("custom SSH -f/-o or executable invocation is unsupported")
			}
		case "EnvironmentFile":
			envFileCount++
			if value != "-/etc/sysconfig/sshd" {
				return errors.New("custom SSH environment file is unsupported")
			}
		case "Environment":
			if value != "" && value != "OPTIONS=" {
				return errors.New("custom SSH service environment is unsupported")
			}
		case "PassEnvironment":
			if value != "" {
				return errors.New("custom SSH service environment is unsupported")
			}
		}
	}
	if starts != 1 || envFileCount != 1 {
		return errors.New("unrecognized Fedora SSH vendor invocation")
	}
	execLine := p["ExecStart"]
	_, argv, ok := strings.Cut(execLine, "argv[]=")
	if !ok || !strings.Contains(execLine, "path=/usr/sbin/sshd ;") {
		return errors.New("loaded SSH executable is unsupported")
	}
	argv = strings.TrimSpace(strings.SplitN(argv, " ;", 2)[0])
	if argv != "/usr/sbin/sshd -D $OPTIONS" && argv != "/usr/sbin/sshd -D" {
		return errors.New("loaded SSH -f/-o or other daemon options are unsupported")
	}
	return n.checkEmptySSHDefaults(ctx, "/etc/sysconfig/sshd", "OPTIONS")
}

func (n networkController) checkEmptySSHDefaults(ctx context.Context, path, varName string) error {
	defaults, err := n.readSSHConfig(ctx, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	for _, line := range strings.Split(string(defaults), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		value = strings.TrimSpace(value)
		if !ok || strings.TrimSpace(key) != varName || (value != "" && value != "\"\"" && value != "''") {
			return errors.New(varName + " must be empty; preserve custom daemon options and use console-reviewed administration")
		}
	}
	return nil
}
