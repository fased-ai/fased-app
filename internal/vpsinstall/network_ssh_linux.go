//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"os"
	"strings"
)

func (n networkController) readSSHConfig(ctx context.Context, path string) ([]byte, error) {
	if e := parents(n.ops, filepathParent(path)); e != nil {
		return nil, e
	}
	f, e := n.ops.read(path)
	if e != nil && !errors.Is(e, os.ErrPermission) {
		return nil, e
	}
	if !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm()&0022 != 0 || f.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, errors.New("SSH configuration must be a root-owned immutable regular file")
	}
	if errors.Is(e, os.ErrPermission) {
		f.Data, e = n.ops.run(ctx, nil, "/usr/bin/cat", "--", path)
		if e != nil {
			return nil, e
		}
	}
	if len(f.Data) > 64<<10 {
		return nil, errors.New("bounded SSH configuration required")
	}
	return f.Data, nil
}

// The first supported profile permits only Ubuntu's one literal snippet include.
// Conditional or recursive configuration needs a separately reviewed policy;
// it is preserved and refused before any network change, never rewritten.
func (n networkController) checkSSHProfile(ctx context.Context) error {
	if e := n.checkSSHInvocation(ctx); e != nil {
		return e
	}
	read := func(path string) ([]byte, error) { return n.readSSHConfig(ctx, path) }

	scan := func(b []byte, main bool) (bool, error) {
		included := false
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.SplitN(line, "#", 2)[0]
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			switch strings.ToLower(f[0]) {
			case "match":
				return false, errors.New("conditional SSH Match policy is unsupported; preserve it and use console-reviewed administration")
			case "include":
				if !main || len(f) != 2 || f[1] != "/etc/ssh/sshd_config.d/*.conf" {
					return false, errors.New("only Ubuntu's literal sshd_config.d include is supported; preserve other includes")
				}
				included = true
			}
		}
		return included, nil
	}
	main, e := read("/etc/ssh/sshd_config")
	if e != nil {
		return e
	}
	included, e := scan(main, true)
	if e != nil {
		return e
	}
	if !included {
		return errors.New("Ubuntu's literal sshd_config.d include is required; no SSH file is rewritten")
	}
	names, e := n.ops.list("/etc/ssh/sshd_config.d")
	if e != nil {
		return e
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		if strings.ContainsAny(name, "/\x00\r\n") {
			return errors.New("invalid SSH snippet name")
		}
		b, e := read("/etc/ssh/sshd_config.d/" + name)
		if e != nil {
			return e
		}
		if _, e = scan(b, false); e != nil {
			return e
		}
	}
	return nil
}

// A bare sshd -T must describe the daemon whose policy will actually be reloaded.
// Only the Ubuntu vendor invocation/default environment is supported here.
func (n networkController) checkSSHInvocation(ctx context.Context) error {
	data, e := n.ops.command(ctx, "/usr/bin/systemctl", "show", "ssh.service", "--property=LoadState,FragmentPath,DropInPaths,ExecStart,EnvironmentFiles,Environment,PassEnvironment")
	if e != nil {
		return e
	}
	p := fields(data)
	if p["LoadState"] != "loaded" || p["DropInPaths"] != "" || p["Environment"] != "" || p["PassEnvironment"] != "" || p["EnvironmentFiles"] != "/etc/default/ssh (ignore_errors=yes)" {
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
				return errors.New("untrusted Ubuntu SSH vendor path alias")
			}
			vendor = "/usr/lib/systemd/system/ssh.service"
		}
	}
	if vendor != "/usr/lib/systemd/system/ssh.service" && vendor != "/lib/systemd/system/ssh.service" {
		return errors.New("only the Ubuntu vendor ssh.service is supported; preserve custom units")
	}
	source, e := n.readSSHConfig(ctx, vendor)
	if e != nil {
		return e
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
		return errors.New("unrecognized Ubuntu SSH vendor invocation")
	}
	exec := p["ExecStart"]
	_, argv, ok := strings.Cut(exec, "argv[]=")
	if !ok || !strings.Contains(exec, "path=/usr/sbin/sshd ;") {
		return errors.New("loaded SSH executable is unsupported")
	}
	argv = strings.TrimSpace(strings.SplitN(argv, " ;", 2)[0])
	if argv != "/usr/sbin/sshd -D $SSHD_OPTS" && argv != "/usr/sbin/sshd -D" {
		return errors.New("loaded SSH -f/-o or other daemon options are unsupported")
	}
	defaults, e := n.readSSHConfig(ctx, "/etc/default/ssh")
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	for _, line := range strings.Split(string(defaults), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		value = strings.TrimSpace(value)
		if !ok || strings.TrimSpace(key) != "SSHD_OPTS" || (value != "" && value != "\"\"" && value != "''") {
			return errors.New("SSHD_OPTS must be empty; preserve custom daemon options and use console-reviewed administration")
		}
	}
	return nil
}
