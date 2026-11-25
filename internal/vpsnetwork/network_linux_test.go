//go:build linux

package vpsnetwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/hostplatform"
)

type noClose struct{}

func (noClose) Close() error { return nil }

type memoryHost struct {
	files                     map[string]hostFile
	journals                  map[string][]byte
	calls                     [][]string
	tables                    map[string][]byte
	enabled, active           map[string]bool
	proof                     networkAccess
	clock                     time.Time
	beforeCommit, beforeApply func()
	fault                     string
	sshService                string
	sshActive                 bool
	requireSSHDRunDir         bool
	sshExec                   string
	sshDropInPaths            string
	sshEnvFiles               string
	sshEnv                    string
	sshConfigDirRestricted    bool
	kernelJSON                []byte
	nftFail                   bool
	serviceCalls              [][]string
}

func baseFixtureDirs(files map[string]hostFile) {
	for _, p := range []string{
		"/", "/var", "/var/lib", "/run",
		"/etc", "/etc/systemd", "/etc/systemd/system",
		"/etc/systemd/system/multi-user.target.wants",
		"/etc/ssh", "/etc/ssh/sshd_config.d", "/etc/default",
		"/usr", "/usr/lib", "/usr/lib/systemd", "/usr/lib/systemd/system",
	} {
		files[p] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
	}
}

func packageManifestFixture(ownerUID, appUID int, id string) Manifest {
	root := "/var/lib/fased-app-package/" + strconv.Itoa(ownerUID) + "/remote/" + id
	unit := "fased-package-" + strconv.Itoa(ownerUID) + "-remote-" + id + "-app.service"
	return Manifest{
		Version:      1,
		ID:           id,
		Phase:        packageRemotePhase,
		OwnerUID:     ownerUID,
		OwnerGID:     ownerUID,
		AppUID:       appUID,
		AppGID:       appUID,
		AppUser:      "fs-pkg-a",
		Root:         root,
		Data:         root + "/app-state",
		Core:         root + "/fased-app",
		Config:       root + "/install.json",
		Unit:         unit,
		UnitPath:     "/etc/systemd/system/" + unit,
		Listen:       "127.0.0.1:17490",
		PublicOrigin: "https://controlled.owner.ts.net",
		CoreSHA256:   strings.Repeat("a", 64),
	}
}

func newMemoryHost(m Manifest, osRel string, plat hostplatform.Platform) (*memoryHost, networkController) {
	files := map[string]hostFile{}
	baseFixtureDirs(files)
	files["/etc/os-release"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte(osRel)}
	files["/etc/ssh/sshd_config"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("Include /etc/ssh/sshd_config.d/*.conf\nUsePAM yes\n")}
	files["/etc/default/ssh"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("SSHD_OPTS=\n")}
	files["/usr/lib/systemd/system/ssh.service"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("[Service]\nEnvironmentFile=-/etc/default/ssh\nExecStart=/usr/sbin/sshd -D $SSHD_OPTS\n")}

	h := &memoryHost{
		files:       files,
		journals:    map[string][]byte{},
		tables:      map[string][]byte{"foreign": []byte("foreign firewall bytes")},
		enabled:     map[string]bool{},
		active:      map[string]bool{},
		clock:       time.Unix(1800000000, 0).UTC(),
		proof:       networkAccess{SessionID: "100.90.0.1:10001->100.90.0.2:22", PeerIP: "100.90.0.1", ServerIP: "100.90.0.2", SSHPort: 22},
		sshService:  "ssh.service",
		sshActive:   true,
		sshExec:     "/usr/sbin/sshd -D $SSHD_OPTS",
		sshEnvFiles: "/etc/default/ssh (ignore_errors=yes)",
	}

	o := hostOps{
		uid:  m.OwnerUID,
		euid: m.OwnerUID,
		gid:  m.OwnerGID,
		read: h.read,
		list: h.list,
		run:  h.run,
		command: func(ctx context.Context, a ...string) ([]byte, error) {
			if a[0] == "/usr/bin/getent" && a[1] == "passwd" && a[2] == strconv.Itoa(m.OwnerUID) {
				return []byte(fmt.Sprintf("owner:x:%d:%d:Owner:/home/owner:/bin/bash\n", m.OwnerUID, m.OwnerGID)), nil
			}
			if a[0] == "/usr/bin/systemctl" && len(a) > 2 && (a[2] == "ssh.service" || a[2] == "sshd.service") {
				if a[2] != h.sshService {
					return []byte("LoadState=not-found\nFragmentPath=\nDropInPaths=\n"), nil
				}
				return []byte(fmt.Sprintf("LoadState=loaded\nFragmentPath=/usr/lib/systemd/system/%s\nDropInPaths=%s\nEnvironmentFiles=%s\nEnvironment=%s\nPassEnvironment=\nExecStart={ path=/usr/sbin/sshd ; argv[]=%s ; }\n", h.sshService, h.sshDropInPaths, h.sshEnvFiles, h.sshEnv, h.sshExec)), nil
			}
			if a[0] == "/usr/bin/systemctl" && len(a) > 2 && strings.HasPrefix(a[2], "fased-vps-net-") {
				name := a[2]
				path := "/etc/systemd/system/" + name
				if _, ok := h.files[path]; !ok {
					return []byte("LoadState=not-found\nFragmentPath=\nDropInPaths=\n"), nil
				}
				active := "inactive"
				if h.active[name] {
					active = "active"
				}
				state := "disabled"
				if h.enabled[name] {
					state = "enabled"
				}
				return []byte(fmt.Sprintf("LoadState=loaded\nFragmentPath=%s\nDropInPaths=%s\nUser=0\nGroup=0\nActiveState=%s\nUnitFileState=%s\n", path, h.sshDropInPaths, active, state)), nil
			}
			return nil, errors.New("unexpected OS read-only command")
		},
		readJournal: func(name string) ([]byte, error) {
			b, ok := h.journals[name]
			if !ok {
				return nil, os.ErrNotExist
			}
			return bytes.Clone(b), nil
		},
		writeJournal: func(name string, v any) error {
			h.journals[name] = encoded(v)
			return nil
		},
		lock: func() (io.Closer, error) { return noClose{}, nil },
	}
	n := networkController{
		ops:          o,
		now:          func() time.Time { return h.clock },
		proof:        func(context.Context, Manifest) (networkAccess, error) { return h.proof, nil },
		packageCheck: func(Manifest) error { return nil },
		platform:     func([]byte) (hostplatform.Platform, error) { return plat, nil },
	}
	return h, n
}

func ubuntuNetworkFixture(t *testing.T) (*memoryHost, networkController, Manifest) {
	t.Helper()
	m := packageManifestFixture(1000, 1005, strings.Repeat("b", 32))
	plat := hostplatform.Platform{ID: "ubuntu", Version: "24.04", Codename: "noble", Family: "apt", RestrictedNamespaces: true, Remote: true}
	h, n := newMemoryHost(m, "ID=ubuntu\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\n", plat)
	return h, n, m
}

func fedoraNetworkFixture(t *testing.T) (*memoryHost, networkController, Manifest) {
	t.Helper()
	m := packageManifestFixture(1000, 1005, strings.Repeat("f", 32))
	plat := hostplatform.Platform{ID: "fedora", Version: "45", Family: "dnf", RestrictedNamespaces: false, Remote: true}
	h, n := newMemoryHost(m, "ID=fedora\nVERSION_ID=45\n", plat)

	for _, dir := range []string{
		"/etc/sysconfig",
		"/etc/crypto-policies", "/etc/crypto-policies/back-ends",
		"/usr/share", "/usr/share/crypto-policies", "/usr/share/crypto-policies/DEFAULT",
		"/usr/lib/systemd/system/service.d",
	} {
		h.files[dir] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
	}
	delete(h.files, "/etc/os-release")
	h.files["/etc/os-release"] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "../usr/lib/os-release"}
	h.files["/usr/lib/os-release"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("ID=fedora\nVERSION_ID=45\n")}
	h.files["/etc/ssh/sshd_config.d"] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0700}
	h.files["/etc/ssh/sshd_config"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0600, Data: []byte("Include /etc/ssh/sshd_config.d/*.conf\nUsePAM yes\n")}
	h.files["/etc/ssh/sshd_config.d/50-redhat.conf"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0600, Data: []byte("Include /etc/crypto-policies/back-ends/opensshserver.config\n")}
	h.files["/etc/crypto-policies/back-ends/opensshserver.config"] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/usr/share/crypto-policies/DEFAULT/opensshserver.txt"}
	h.files["/usr/share/crypto-policies/DEFAULT/opensshserver.txt"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("Ciphers aes256-gcm@openssh.com\nRequiredRSASize 2048\n")}
	h.files["/etc/sysconfig/sshd"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0640, Data: []byte("# Configuration file for the sshd service\n")}
	h.files["/usr/lib/systemd/system/sshd.service"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("[Service]\nEnvironment=OPTIONS=\nEnvironmentFile=-/etc/sysconfig/sshd\nExecStart=/usr/sbin/sshd -D $OPTIONS\n")}
	h.files["/usr/lib/systemd/system/service.d/10-timeout-abort.conf"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("# Fedora vendor timeout abort\n[Service]\nTimeoutStopFailureMode=abort\n")}

	h.sshService = "sshd.service"
	h.sshExec = "/usr/sbin/sshd -D $OPTIONS"
	h.sshDropInPaths = "/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
	h.sshEnvFiles = "/etc/sysconfig/sshd (ignore_errors=yes)"
	h.sshEnv = "OPTIONS="
	h.sshConfigDirRestricted = true
	return h, n, m
}

func (h *memoryHost) read(p string) (hostFile, error) {
	if h.sshConfigDirRestricted && filepath.Dir(p) == sshConfigDir {
		return hostFile{}, os.ErrPermission
	}
	f, ok := h.files[p]
	if !ok {
		return hostFile{}, os.ErrNotExist
	}
	if f.Regular && f.Mode.Perm()&0004 == 0 && !(f.UID == 1000) {
		// Simulate unreadable 0600/0640 root file content when accessed unprivileged.
		f.Data = nil
		return f, os.ErrPermission
	}
	f.Data = bytes.Clone(f.Data)
	return f, nil
}

func (h *memoryHost) list(p string) ([]string, error) {
	if h.sshConfigDirRestricted && p == sshConfigDir {
		return nil, os.ErrPermission
	}
	f, ok := h.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	if !f.Directory {
		return nil, errors.New("not a directory")
	}
	var names []string
	for n := range h.files {
		if filepath.Dir(n) == p && n != p {
			names = append(names, filepath.Base(n))
		}
	}
	return names, nil
}

func (h *memoryHost) run(ctx context.Context, b []byte, a ...string) ([]byte, error) {
	h.calls = append(h.calls, append([]string(nil), a...))
	arg := func(flag string) string {
		for i, s := range a {
			if s == flag && i+1 < len(a) {
				return a[i+1]
			}
		}
		return ""
	}
	switch a[0] {
	case "/usr/bin/find":
		if len(a) != 8 || a[1] != sshConfigDir || a[7] != "%U:%G:%m:%y:%s:%f\\0" {
			return nil, errors.New("unexpected find invocation")
		}
		var out bytes.Buffer
		for p, f := range h.files {
			if filepath.Dir(p) != sshConfigDir || p == sshConfigDir {
				continue
			}
			ftype := "f"
			if f.Directory {
				ftype = "d"
			} else if f.Mode&os.ModeSymlink != 0 {
				ftype = "l"
			}
			var modeBits uint32 = uint32(f.Mode.Perm())
			if f.Mode&os.ModeSetuid != 0 {
				modeBits |= 0o4000
			}
			if f.Mode&os.ModeSetgid != 0 {
				modeBits |= 0o2000
			}
			if f.Mode&os.ModeSticky != 0 {
				modeBits |= 0o1000
			}
			fmt.Fprintf(&out, "%d:%d:%o:%s:%d:%s\x00", f.UID, f.GID, modeBits, ftype, len(f.Data), filepath.Base(p))
		}
		return out.Bytes(), nil
	case "/usr/bin/flock":
		if len(a) == 2 {
			return []byte("flock fixture version"), nil
		}
		return h.run(ctx, nil, a[4:]...)
	case "/usr/bin/cat":
		f, ok := h.files[a[len(a)-1]]
		if !ok {
			return nil, os.ErrNotExist
		}
		return bytes.Clone(f.Data), nil
	case "/usr/bin/cmp":
		l, lok := h.files[a[len(a)-2]]
		r, rok := h.files[a[len(a)-1]]
		if !lok || !rok || !bytes.Equal(l.Data, r.Data) {
			return nil, errors.New("different immutable input")
		}
		return nil, nil
	case "/usr/bin/install":
		uid, _ := strconv.Atoi(arg("-o"))
		gid, _ := strconv.Atoi(arg("-g"))
		mode, _ := strconv.ParseUint(arg("-m"), 8, 32)
		dir := false
		for _, tok := range a {
			if tok == "-d" {
				dir = true
			}
		}
		input := b
		if !dir && len(a) >= 2 && a[len(a)-2] != "/dev/stdin" && a[len(a)-2] != "/dev/null" {
			src, ok := h.files[a[len(a)-2]]
			if !ok {
				return nil, os.ErrNotExist
			}
			input = src.Data
		}
		f := hostFile{UID: uid, GID: gid, Mode: os.FileMode(mode), Regular: !dir, Directory: dir, Data: bytes.Clone(input)}
		if dir {
			f.Mode |= os.ModeDir
		}
		h.files[a[len(a)-1]] = f
	case "/usr/bin/sync":
	case "/usr/bin/ln":
		target := a[len(a)-1]
		if _, ok := h.files[target]; ok {
			return nil, os.ErrExist
		}
		src, ok := h.files[a[len(a)-2]]
		if !ok {
			return nil, os.ErrNotExist
		}
		h.files[target] = src
	case "/usr/bin/rm":
		if _, ok := h.files[a[len(a)-1]]; !ok {
			return nil, os.ErrNotExist
		}
		delete(h.files, a[len(a)-1])
	case "/usr/sbin/sshd":
		if a[1] == "-t" {
			if h.requireSSHDRunDir {
				dir, e := h.read("/run/sshd")
				if e != nil || !dir.Directory || dir.UID != 0 || dir.GID != 0 || dir.Mode.Perm()&0022 != 0 {
					return nil, errors.New("missing or unsafe privilege separation directory /run/sshd")
				}
			}
			return nil, nil
		}
		locked := false
		for p, f := range h.files {
			if strings.HasPrefix(p, sshConfigDir+"/") && strings.HasSuffix(p, ".conf") && bytes.Contains(f.Data, []byte("PermitRootLogin no")) {
				locked = true
			}
		}
		if locked {
			return []byte("permitrootlogin no\npasswordauthentication no\nkbdinteractiveauthentication no\n"), nil
		}
		return []byte("permitrootlogin prohibit-password\npasswordauthentication yes\nkbdinteractiveauthentication yes\n"), nil
	case "/usr/sbin/nft":
		if h.nftFail {
			return nil, errors.New("fixture missing nft executable")
		}
		if a[1] == "--version" {
			return []byte("nft fake version"), nil
		}
		if a[1] == "list" {
			var names []string
			for name := range h.tables {
				names = append(names, "table inet "+name)
			}
			return []byte(strings.Join(names, "\n") + "\n"), nil
		}
		if (a[1] == "--stateless" || a[1] == "--json") && a[2] == "list" {
			name := a[5]
			raw, ok := h.tables[name]
			if !ok {
				return nil, errors.New("missing table")
			}
			if a[1] == "--json" {
				if h.kernelJSON != nil {
					return h.kernelJSON, nil
				}
				return encodedKernelTable(name, string(raw)), nil
			}
			return raw, nil
		}
		if a[1] == "--check" {
			return nil, nil
		}
		if a[1] == "--file" {
			rules := string(h.files[a[2]].Data)
			if strings.HasPrefix(rules, "delete table inet ") {
				name := strings.TrimSpace(strings.TrimPrefix(rules, "delete table inet "))
				delete(h.tables, name)
				return nil, nil
			}
			var tableName string
			for _, line := range strings.Split(rules, "\n") {
				if strings.HasPrefix(line, "create table inet ") {
					fields := strings.Fields(line)
					tableName = fields[3]
				}
			}
			if tableName == "" {
				return nil, errors.New("missing table in rules.nft")
			}
			if h.fault == "foreign-nft-edit" {
				h.tables[tableName] = []byte(rules + "# foreign change\n")
			} else {
				h.tables[tableName] = []byte(rules)
			}
			return nil, nil
		}
	case "/usr/bin/systemctl":
		if a[1] == "daemon-reload" {
			return nil, nil
		}
		if a[1] == "is-active" && len(a) >= 4 {
			if a[3] != h.sshService || !h.sshActive {
				return nil, errors.New("inactive")
			}
			return nil, nil
		}
		if a[1] == "kill" {
			h.serviceCalls = append(h.serviceCalls, append([]string(nil), a...))
			return nil, nil
		}
		unit := a[len(a)-1]
		switch a[1] {
		case "enable":
			h.enabled[unit] = true
			if strings.HasSuffix(unit, "-rollback.service") {
				h.files["/etc/systemd/system/multi-user.target.wants/"+unit] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + unit}
			}
			if strings.HasSuffix(unit, "-persist.service") {
				sockDir := "/etc/systemd/system/ssh.socket.requires"
				svcDir := "/etc/systemd/system/ssh.service.requires"
				if h.sshService == "sshd.service" {
					sockDir = "/etc/systemd/system/sshd.socket.requires"
					svcDir = "/etc/systemd/system/sshd.service.requires"
				}
				h.files[sockDir] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
				h.files[svcDir] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
				h.files[sockDir+"/"+unit] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + unit}
				h.files[svcDir+"/"+unit] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + unit}
				h.files["/etc/systemd/system/multi-user.target.wants/"+unit] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + unit}
			}
		case "stop":
			h.active[unit] = false
		case "start":
			return nil, h.executeUnit(ctx, unit)
		}
	default:
		return nil, errors.New("unexpected OS tool: " + a[0])
	}
	return nil, nil
}

func (h *memoryHost) executeUnit(ctx context.Context, name string) error {
	f, ok := h.files["/etc/systemd/system/"+name]
	if !ok {
		return errors.New("missing unit " + name)
	}
	if strings.HasSuffix(name, ".timer") {
		h.active[name] = true
		return nil
	}
	if strings.HasSuffix(name, "-apply-actions.service") && h.beforeApply != nil {
		h.beforeApply()
		h.beforeApply = nil
	}
	var condPathNot, condPathExists, condSymlinkNot, execCond, stdoutTrunc string
	var cmds []string
	for _, line := range strings.Split(string(f.Data), "\n") {
		switch {
		case strings.HasPrefix(line, "ConditionPathExists=!"):
			condPathNot = strings.TrimPrefix(line, "ConditionPathExists=!")
		case strings.HasPrefix(line, "ConditionPathExists="):
			condPathExists = strings.TrimPrefix(line, "ConditionPathExists=")
		case strings.HasPrefix(line, "ConditionPathIsSymbolicLink=!"):
			condSymlinkNot = strings.TrimPrefix(line, "ConditionPathIsSymbolicLink=!")
		case strings.HasPrefix(line, "ExecCondition="):
			execCond = strings.TrimPrefix(line, "ExecCondition=")
		case strings.HasPrefix(line, "StandardOutput=truncate:"):
			stdoutTrunc = strings.TrimPrefix(line, "StandardOutput=truncate:")
		case strings.HasPrefix(line, "ExecStart="):
			cmds = append(cmds, strings.TrimPrefix(line, "ExecStart="))
		}
	}
	if condPathNot != "" {
		if _, exists := h.files[condPathNot]; exists {
			return nil
		}
	}
	if condPathExists != "" {
		if _, exists := h.files[condPathExists]; !exists {
			return nil
		}
	}
	if condSymlinkNot != "" {
		if existing, exists := h.files[condSymlinkNot]; exists && existing.Mode&os.ModeSymlink != 0 {
			return nil
		}
		if _, exists := h.files[condSymlinkNot]; exists && condSymlinkNot == condPathNot {
			return nil
		}
	}
	if execCond != "" {
		if _, err := h.run(ctx, nil, strings.Fields(execCond)...); err != nil {
			return nil
		}
	}
	for _, cmd := range cmds {
		ignoreErr := false
		if strings.HasPrefix(cmd, "-") {
			ignoreErr = true
			cmd = strings.TrimPrefix(cmd, "-")
		}
		out, err := h.run(ctx, nil, strings.Fields(cmd)...)
		if stdoutTrunc != "" {
			h.files[stdoutTrunc] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: out}
		}
		if err != nil && !ignoreErr {
			return err
		}
	}
	return nil
}

func encodedKernelTable(name, rules string) []byte {
	var comment string
	for _, line := range strings.Split(rules, "\n") {
		if strings.HasPrefix(line, "create table inet ") {
			_, rest, _ := strings.Cut(line, `comment "`)
			comment, _, _ = strings.Cut(rest, `"`)
		}
	}
	entries := []any{
		map[string]any{"metainfo": map[string]any{"version": "1.0.9", "json_schema_version": 1}},
		map[string]any{"table": map[string]any{"family": "inet", "name": name, "handle": 1, "comment": comment}},
		map[string]any{"chain": map[string]any{"family": "inet", "table": name, "name": "input", "type": "filter", "hook": "input", "prio": 0, "policy": "drop", "handle": 2}},
	}
	for _, line := range strings.Split(rules, "\n") {
		if strings.HasPrefix(line, "add rule inet ") {
			entries = append(entries, map[string]any{"rule": map[string]any{"family": "inet", "table": name, "chain": "input", "handle": len(entries) + 1, "expr": []any{map[string]any{"accept": nil}}}})
		}
	}
	return encoded(map[string]any{"nftables": entries})
}

func networkBegin(t *testing.T) (*memoryHost, networkController, Manifest, Status) {
	t.Helper()
	h, n, m := ubuntuNetworkFixture(t)
	s, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil {
		t.Fatal("network begin failed", err)
	}
	return h, n, m, s
}

func TestUbuntuFormat4LifecycleCleansUnitsAndSurvivesUpgrade(t *testing.T) {
	t.Parallel()
	h, n, m, pending := networkBegin(t)
	if pending.Phase != "pending" {
		t.Fatalf("expected pending, got %+v", pending)
	}
	a, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	if a.SSHService != "" {
		t.Fatalf("expected omitted sshService on Ubuntu, got %q", a.SSHService)
	}
	rules := string(h.files[networkAttemptRoot(m, a.ID)+"/rules.nft"].Data)
	if !strings.Contains(rules, `iifname "tailscale0" tcp dport { 22, 443 } accept`) {
		t.Fatalf("expected default HTTPS port 443 rule in %s", rules)
	}

	h.proof.SessionID = "second-private-connection"
	committed, err := n.confirm(context.Background(), m, pending.AttemptID)
	if err != nil || committed.Phase != "committed" {
		t.Fatalf("confirm failed: %+v %v", committed, err)
	}

	upgraded := m
	upgraded.CoreSHA256 = strings.Repeat("e", 64)
	inspected, err := n.inspect(context.Background(), upgraded)
	if err != nil || inspected.Phase != "committed" {
		t.Fatalf("inspect after core upgrade failed: %+v %v", inspected, err)
	}

	tampered := upgraded
	tampered.Listen = "127.0.0.1:17499"
	if _, err := n.inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "invalid owner network pointer") {
		t.Fatalf("expected invalid owner network pointer on tampered listen, got %v", err)
	}

	rolledBack, err := n.rollback(context.Background(), upgraded)
	if err != nil || rolledBack.Phase != "rolled-back" {
		t.Fatalf("rollback failed: %+v %v", rolledBack, err)
	}
	if _, ok := h.tables[networkTable(upgraded, a)]; ok {
		t.Fatal("owned firewall table remained after rollback")
	}
	if !bytes.Equal(h.tables["foreign"], []byte("foreign firewall bytes")) {
		t.Fatal("foreign firewall table lost")
	}
	for _, role := range networkRoles() {
		if _, exists := h.files["/etc/systemd/system/"+networkUnit(upgraded, a.ID, role)]; exists {
			t.Fatalf("rolled-back unit still present: %s", role)
		}
	}
	if _, exists := h.files[networkAttemptRoot(upgraded, a.ID)+"/units.cleaned"]; !exists {
		t.Fatal("missing units.cleaned marker after rollback")
	}

	if again, err := n.inspect(context.Background(), upgraded); err != nil || again.Phase != "rolled-back" {
		t.Fatalf("inspect after unit cleanup failed: %+v %v", again, err)
	}
	if repeat, err := n.rollback(context.Background(), upgraded); err != nil || repeat.Phase != "rolled-back" {
		t.Fatalf("idempotent second rollback failed: %+v %v", repeat, err)
	}

	nextPending, err := n.begin(context.Background(), upgraded, Approval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil || nextPending.Phase != "pending" || nextPending.AttemptID == pending.AttemptID {
		t.Fatalf("fresh attempt after rolled-back cleanup failed: %+v %v", nextPending, err)
	}
}

func TestDynamicTailscaleServePortInRules(t *testing.T) {
	t.Parallel()
	_, n, m := ubuntuNetworkFixture(t)
	m.PublicOrigin = "https://controlled.owner.ts.net:8443"
	h, n := newMemoryHost(m, "ID=ubuntu\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\n", hostplatform.Platform{
		ID: "ubuntu", Version: "24.04", Codename: "noble", Family: "apt", RestrictedNamespaces: true, Remote: true,
	})
	pending, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	a, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	rules := string(h.files[networkAttemptRoot(m, a.ID)+"/rules.nft"].Data)
	if !strings.Contains(rules, `iifname "tailscale0" tcp dport { 22, 8443 } accept`) {
		t.Fatalf("expected custom tailscale serve port 8443 in %s", rules)
	}
	if _, err := n.inspect(context.Background(), m); err != nil {
		t.Fatalf("inspect with custom port failed: %v", err)
	}
	h.proof.SessionID = "second-private-session"
	if st, err := n.confirm(context.Background(), m, pending.AttemptID); err != nil || st.Phase != "committed" {
		t.Fatalf("confirm with custom port failed: %+v %v", st, err)
	}
}

func TestDebian12And13Profiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version, codename string
	}{
		{"12", "bookworm"},
		{"13", "trixie"},
	} {
		t.Run("debian-"+tc.version, func(t *testing.T) {
			m := packageManifestFixture(1000, 1005, strings.Repeat("d", 32))
			plat := hostplatform.Platform{ID: "debian", Version: tc.version, Codename: tc.codename, Family: "apt", RestrictedNamespaces: false, Remote: true}
			_, n := newMemoryHost(m, fmt.Sprintf("ID=debian\nVERSION_ID=%q\nVERSION_CODENAME=%s\n", tc.version, tc.codename), plat)
			pending, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true})
			if err != nil || pending.Phase != "pending" {
				t.Fatalf("debian %s begin failed: %+v %v", tc.version, pending, err)
			}
		})
	}
}

func TestFedora45RestrictedSSHAndLifecycle(t *testing.T) {
	t.Parallel()
	h, n, m := fedoraNetworkFixture(t)
	pending, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil || pending.Phase != "pending" {
		t.Fatalf("fedora begin failed: %+v %v", pending, err)
	}
	a, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	if a.SSHService != "sshd.service" {
		t.Fatalf("expected sshd.service on Fedora, got %q", a.SSHService)
	}
	units := networkUnits(m, a)
	if !strings.Contains(string(units["ssh-hup"]), "sshd.service") ||
		!strings.Contains(string(units["persist"]), "RequiredBy=sshd.socket sshd.service") {
		t.Fatal("Fedora units did not target sshd.service / sshd.socket")
	}

	h.proof.SessionID = "second-private-connection"
	committed, err := n.confirm(context.Background(), m, pending.AttemptID)
	if err != nil || committed.Phase != "committed" {
		t.Fatalf("fedora confirm failed: %+v %v", committed, err)
	}
	persistUnit := networkUnit(m, a.ID, "persist")
	for _, reqLink := range []string{
		"/etc/systemd/system/sshd.socket.requires/" + persistUnit,
		"/etc/systemd/system/sshd.service.requires/" + persistUnit,
	} {
		if _, ok := h.files[reqLink]; !ok {
			t.Fatalf("missing Fedora persist requirement link %s", reqLink)
		}
	}

	rolledBack, err := n.rollback(context.Background(), m)
	if err != nil || rolledBack.Phase != "rolled-back" {
		t.Fatalf("fedora rollback failed: %+v %v", rolledBack, err)
	}
	for _, reqLink := range []string{
		"/etc/systemd/system/sshd.socket.requires/" + persistUnit,
		"/etc/systemd/system/sshd.service.requires/" + persistUnit,
	} {
		if _, ok := h.files[reqLink]; ok {
			t.Fatalf("Fedora persist requirement link remained after rollback: %s", reqLink)
		}
	}
}

func TestSecurityRefusalsMultiDistro(t *testing.T) {
	t.Parallel()

	t.Run("arbitrary vendor drop-in rejected", func(t *testing.T) {
		h, n, m := fedoraNetworkFixture(t)
		h.sshDropInPaths = "/usr/lib/systemd/system/service.d/99-foreign.conf"
		if _, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true}); err == nil {
			t.Fatal("arbitrary DropInPaths accepted")
		}
	})

	t.Run("tampered 10-timeout-abort.conf rejected", func(t *testing.T) {
		h, n, m := fedoraNetworkFixture(t)
		h.files["/usr/lib/systemd/system/service.d/10-timeout-abort.conf"] = hostFile{
			UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("[Service]\nExecStart=/bin/sh\n"),
		}
		if _, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true}); err == nil {
			t.Fatal("tampered 10-timeout-abort.conf accepted")
		}
	})

	t.Run("non-empty OPTIONS in /etc/sysconfig/sshd rejected", func(t *testing.T) {
		h, n, m := fedoraNetworkFixture(t)
		h.files["/etc/sysconfig/sshd"] = hostFile{
			UID: 0, GID: 0, Regular: true, Mode: 0640, Data: []byte("OPTIONS=\"-o PermitRootLogin=yes\"\n"),
		}
		if _, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true}); err == nil {
			t.Fatal("non-empty /etc/sysconfig/sshd OPTIONS accepted")
		}
	})

	t.Run("crypto-policies symlink escape rejected", func(t *testing.T) {
		h, n, m := fedoraNetworkFixture(t)
		h.files["/etc/crypto-policies/back-ends/opensshserver.config"] = hostFile{
			UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "../../../../tmp/evil.txt",
		}
		if _, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true}); err == nil {
			t.Fatal("escaping crypto-policies symlink accepted")
		}
	})

	t.Run("unsupported non-remote OS rejected", func(t *testing.T) {
		_, n, m := ubuntuNetworkFixture(t)
		n.platform = func(b []byte) (hostplatform.Platform, error) {
			return hostplatform.LookupRemote(string(b), "amd64")
		}
		h, _, m := ubuntuNetworkFixture(t)
		h.files["/etc/os-release"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("ID=ubuntu\nVERSION_ID=\"22.04\"\nVERSION_CODENAME=jammy\n")}
		n.ops.read = h.read
		if _, err := n.begin(context.Background(), m, Approval{PrivateUI: true, ConsoleRecovery: true}); err == nil {
			t.Fatal("non-remote OS accepted")
		}
	})

	t.Run("legacy Ubuntu binding without sshService or units.cleaned inspects and rolls back", func(t *testing.T) {
		h, n, m := ubuntuNetworkFixture(t)
		attemptID := strings.Repeat("1", 32)
		now := h.clock.UTC().Truncate(time.Second)
		a := networkAttempt{
			Version:        1,
			ID:             attemptID,
			ManifestSHA256: manifestDigest(m),
			Access:         h.proof,
			StartedAt:      now,
			Deadline:       now.Add(5 * time.Minute),
		}
		ptrJSON := encoded(networkPointer{Version: 1, AttemptID: a.ID, ManifestSHA256: manifestDigest(m), Attempt: a})
		if bytes.Contains(ptrJSON, []byte("sshService")) {
			t.Fatalf("sshService unexpectedly serialized on Ubuntu: %s", ptrJSON)
		}
		h.journals["network.json"] = ptrJSON
		dir := networkAttemptRoot(m, a.ID)
		h.files[networkRoot(m)] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
		h.files[networkRoot(m)+"/binding.json"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: binding(m)}
		h.files[dir] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
		h.files[dir+"/private"] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0700}
		for name, content := range networkStaticFiles(m, a) {
			h.files[dir+"/"+name] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: content}
		}
		for role, content := range networkUnits(m, a) {
			h.files["/etc/systemd/system/"+networkUnit(m, a.ID, role)] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: content}
		}
		h.files[networkSSHStage(m, a)] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: networkSSHBytes(m, a)}
		h.files[networkSSH(m, a)] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: networkSSHBytes(m, a)}
		rules := string(networkRules(m, a))
		h.tables[networkTable(m, a)] = []byte(rules)
		h.files[dir+"/expected.nft"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte(rules)}
		h.files[dir+"/outcome"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: networkToken(a, "commit")}
		persist := networkUnit(m, a.ID, "persist")
		h.enabled[persist] = true
		h.files["/etc/systemd/system/ssh.socket.requires"] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
		h.files["/etc/systemd/system/ssh.service.requires"] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
		h.files["/etc/systemd/system/ssh.socket.requires/"+persist] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + persist}
		h.files["/etc/systemd/system/ssh.service.requires/"+persist] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + persist}

		st, err := n.inspect(context.Background(), m)
		if err != nil || st.Phase != "committed" {
			t.Fatalf("inspect pre-existing Ubuntu lockdown failed: %+v %v", st, err)
		}
		rb, err := n.rollback(context.Background(), m)
		if err != nil || rb.Phase != "rolled-back" {
			t.Fatalf("rollback pre-existing Ubuntu lockdown failed: %+v %v", rb, err)
		}
	})
}
