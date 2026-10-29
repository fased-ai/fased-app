//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

type noClose struct{}

func (noClose) Close() error { return nil }

type memoryHost struct {
	files                         map[string]hostFile
	journals                      map[string][]byte
	calls                         [][]string
	fail                          int
	user, group, groups, password string
	active, override, live        bool
	helperActions                 []string
}

func fixtureHost() (*memoryHost, hostOps, Options) {
	h := &memoryHost{files: map[string]hostFile{}, journals: map[string][]byte{}, password: "L"}
	for _, p := range []string{"/", "/var", "/var/lib", "/etc", "/etc/systemd", "/etc/systemd/system", "/etc/systemd/system/multi-user.target.wants"} {
		h.files[p] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	}
	h.files["/etc/os-release"] = hostFile{Regular: true, Mode: 0644, Data: []byte("ID=ubuntu\nVERSION_ID=\"24.04\"\n")}
	binary := []byte("inert controlled core bytes; never executed")
	h.files["/verified/fased-app"] = hostFile{UID: 1000, GID: 1000, Regular: true, Mode: 0755, Data: binary}
	o := hostOps{uid: 1000, euid: 1000, gid: 1000, read: h.read, list: h.list, run: h.run, command: h.command, runtime: func() error { return nil }, lock: func() (io.Closer, error) { return noClose{}, nil }, groups: func() ([]int, error) { return []int{991}, nil }, executable: func() (string, error) { return Root(1000) + "/fased-app", nil }}
	o.readJournal = func(n string) ([]byte, error) {
		b, ok := h.journals[n]
		if !ok {
			return nil, os.ErrNotExist
		}
		return bytes.Clone(b), nil
	}
	o.writeJournal = func(n string, v any) error { h.journals[n] = encoded(v); return nil }
	o.helper = func(_ context.Context, args []string, out io.Writer) error {
		if len(args) < 15 || args[0] != "/usr/bin/setpriv" || !contains(args, "--clear-groups") || !contains(args, "--bounding-set=-all") {
			return errors.New("project code was not privilege-dropped")
		}
		action := args[len(args)-3]
		if action != "claim-code" && action != "recover" {
			return errors.New("unknown owner helper action")
		}
		h.helperActions = append(h.helperActions, action)
		_, e := io.WriteString(out, "controlled owner helper output\n")
		return e
	}
	return h, o, Options{Core: "/verified/fased-app", CoreSHA256: hash(binary), Listen: "127.0.0.1:7331", PrepareHost: true}
}
func contains(a []string, v string) bool {
	for _, s := range a {
		if s == v {
			return true
		}
	}
	return false
}
func (h *memoryHost) read(p string) (hostFile, error) {
	f, ok := h.files[p]
	if !ok {
		return f, os.ErrNotExist
	}
	f.Data = bytes.Clone(f.Data)
	return f, nil
}
func (h *memoryHost) list(p string) ([]string, error) {
	f, e := h.read(p)
	if e != nil {
		return nil, e
	}
	if !f.Directory {
		return nil, errors.New("not a directory")
	}
	var r []string
	for n := range h.files {
		if filepath.Dir(n) == p && n != p {
			r = append(r, filepath.Base(n))
		}
	}
	return r, nil
}
func (h *memoryHost) manifest() Manifest {
	var m Manifest
	_ = json.Unmarshal(h.files[ConfigPath(1000)].Data, &m)
	return m
}
func (h *memoryHost) command(_ context.Context, a ...string) ([]byte, error) {
	switch a[0] {
	case "/usr/bin/getent":
		if a[1] == "passwd" {
			if h.user == "" {
				return nil, errMissingAccount
			}
			parts := strings.Split(h.user, ":")
			if a[2] == parts[0] || a[2] == parts[2] {
				return []byte(h.user), nil
			}
			return nil, errMissingAccount
		}
		if h.group == "" {
			return nil, errMissingAccount
		}
		parts := strings.Split(h.group, ":")
		if a[2] == parts[0] || a[2] == parts[2] {
			return []byte(h.group), nil
		}
		return nil, errMissingAccount
	case "/usr/bin/id":
		return []byte(h.groups), nil
	case "/usr/bin/systemctl":
		m := h.manifest()
		path := "/etc/systemd/system/" + a[2]
		if _, ok := h.files[path]; !ok {
			return []byte("LoadState=not-found\nFragmentPath=\nDropInPaths=\n"), nil
		}
		active := "inactive"
		pid := 0
		if h.active {
			active = "active"
			pid = 4321
		}
		drop := ""
		if h.override {
			drop = "/etc/systemd/system/foreign.conf"
		}
		return []byte(fmt.Sprintf("LoadState=loaded\nActiveState=%s\nMainPID=%d\nFragmentPath=%s\nDropInPaths=%s\nUser=%d\nGroup=%d\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nReadWritePaths=%s\nCapabilityBoundingSet=\nAmbientCapabilities=\nExecStart={ path=%s ; argv[]=%s serve --remote-config %s ; }\n", active, pid, path, drop, m.AppUID, m.AppGID, m.Data, m.Core, m.Core, m.Config)), nil
	}
	return nil, errors.New("unexpected read-only OS command")
}
func (h *memoryHost) run(_ context.Context, b []byte, a ...string) (out []byte, err error) {
	h.calls = append(h.calls, append([]string(nil), a...))
	defer func() {
		if h.fail == len(h.calls) {
			err = errors.New("controlled interruption after OS side effect")
		}
	}()
	arg := func(flag string) string {
		for i, s := range a {
			if s == flag && i+1 < len(a) {
				return a[i+1]
			}
		}
		return ""
	}
	switch a[0] {
	case "/usr/bin/install":
		uid, _ := strconv.Atoi(arg("-o"))
		gid, _ := strconv.Atoi(arg("-g"))
		mode, _ := strconv.ParseUint(arg("-m"), 8, 32)
		dir := contains(a, "-d")
		f := hostFile{UID: uid, GID: gid, Mode: os.FileMode(mode), Regular: !dir, Directory: dir, Data: bytes.Clone(b)}
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
		f, ok := h.files[a[len(a)-2]]
		if !ok {
			return nil, os.ErrNotExist
		}
		h.files[target] = f
	case "/usr/bin/rm":
		delete(h.files, a[len(a)-1])
	case "/usr/bin/mv":
		from, to := a[len(a)-2], a[len(a)-1]
		if _, ok := h.files[to]; ok {
			return nil, os.ErrExist
		}
		for p, f := range h.files {
			if p == from || strings.HasPrefix(p, from+"/") {
				delete(h.files, p)
				h.files[to+strings.TrimPrefix(p, from)] = f
			}
		}
	case "/usr/sbin/useradd":
		name := a[len(a)-1]
		h.user = name + ":x:991:991:" + arg("--comment") + ":/nonexistent:/usr/sbin/nologin\n"
		h.group = name + ":x:991:\n"
		h.groups = "991\n"
	case "/usr/bin/passwd":
		return []byte(accountName(1000) + " " + h.password + " 2026-10-28 0 99999 7 -1\n"), nil
	case "/usr/bin/systemctl":
		m := h.manifest()
		link := "/etc/systemd/system/multi-user.target.wants/" + m.Unit
		switch a[1] {
		case "daemon-reload":
		case "enable":
			h.files[link] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: m.UnitPath}
		case "disable":
			delete(h.files, link)
		case "start":
			h.active = true
			uid := strconv.Itoa(m.AppUID)
			h.files["/proc/4321/status"] = hostFile{Regular: true, Data: []byte("Uid:\t" + strings.Repeat(uid+"\t", 3) + uid + "\nNoNewPrivs:\t1\n")}
		case "stop":
			h.active = false
		default:
			return nil, errors.New("unknown service mutation")
		}
	case "/usr/bin/setpriv":
		if !contains(a, "--clear-groups") || !contains(a, "--no-new-privs") || !contains(a, "--inh-caps=-all") || !contains(a, "--ambient-caps=-all") || !contains(a, "--bounding-set=-all") {
			return nil, errors.New("project child retained privilege")
		}
		if a[len(a)-3] != "status" {
			return nil, errors.New("unknown noninteractive helper")
		}
		caps := map[string]bool{"walletSigner": false, "financialExecution": h.live, "updater": false}
		return encoded(core.Health{Status: "ok", InstanceID: "controlled-instance", APIVersion: core.APIVersion, Capabilities: caps}), nil
	default:
		return nil, errors.New("unexpected elevated OS tool")
	}
	return nil, nil
}
func installFixture(t *testing.T) (*memoryHost, hostOps, Manifest) {
	t.Helper()
	h, o, opt := fixtureHost()
	m, e := install(context.Background(), opt, o)
	if e != nil {
		t.Fatal(e)
	}
	return h, o, m
}
func TestRemoteInstallDeferredStartAndIdentity(t *testing.T) {
	h, o, opt := fixtureHost()
	m, e := install(context.Background(), opt, o)
	if e != nil {
		t.Fatal(e)
	}
	if m.AppUID == o.uid || m.AppGID == o.gid || m.AppUser != accountName(o.uid) {
		t.Fatal("owner and runtime identities overlap")
	}
	for _, a := range h.calls {
		if a[0] == "/usr/bin/systemctl" && (a[1] == "start" || a[1] == "enable") {
			t.Fatal("deferred installation started or enabled")
		}
		if contains(a, "/etc/shadow") {
			t.Fatal("credential material read")
		}
	}
	definition := string(unit(m))
	for _, part := range []string{"--instance-location remote", "--remote-config " + m.Config, "--require-sandbox", "ProtectSystem=strict", "ReadOnlyPaths=" + m.Root, "ReadWritePaths=" + m.Data, "NoNewPrivileges=yes", "User=991", "CapabilityBoundingSet=\n"} {
		if !strings.Contains(definition, part) {
			t.Fatal("missing confinement", part)
		}
	}
	if strings.Contains(definition, "custody") || strings.Contains(definition, "signer") {
		t.Fatal("ordinary Remote depends on wallet custody")
	}
	if e = start(context.Background(), o, m); e != nil {
		t.Fatal(e)
	}
	r, e := status(context.Background(), o, m)
	if e != nil || !r.Active || r.InstanceID != "controlled-instance" {
		t.Fatal(r, e)
	}
	var output bytes.Buffer
	if e = claim(context.Background(), o, m, false, &output); e != nil {
		t.Fatal(e)
	}
	if e = claim(context.Background(), o, m, true, &output); e == nil {
		t.Fatal("recovery allowed while running")
	}
	h.active = false
	if e = claim(context.Background(), o, m, true, &output); e != nil {
		t.Fatal(e)
	}
	if strings.Join(h.helperActions, ",") != "claim-code,recover" {
		t.Fatal(h.helperActions)
	}
}
func TestEveryInstallInterruptionRetriesWithoutReplacingInputs(t *testing.T) {
	baseline, _, _ := installFixture(t)
	count := len(baseline.calls)
	for fault := 1; fault <= count; fault++ {
		t.Run(strconv.Itoa(fault), func(t *testing.T) {
			h, o, opt := fixtureHost()
			h.fail = fault
			if _, e := install(context.Background(), opt, o); e == nil {
				t.Fatal("fault not reached")
			}
			h.fail = 0
			m, e := install(context.Background(), opt, o)
			if e != nil {
				t.Fatal(e)
			}
			if e = verifyFiles(o, m); e != nil {
				t.Fatal(e)
			}
			for p := range h.files {
				if strings.HasSuffix(p, ".pending") {
					t.Fatal("published retry retained staging", p)
				}
			}
		})
	}
}
func TestAccountCollisionsGroupsAndPasswordLockFailClosed(t *testing.T) {
	for _, change := range []string{"marker", "shell", "shared-group", "sudo-group", "unlocked", "uid", "gid", "uid-alias", "group-alias"} {
		t.Run(change, func(t *testing.T) {
			h, o, m := installFixture(t)
			before := len(h.calls)
			switch change {
			case "marker":
				h.user = strings.Replace(h.user, marker(m.ID), "foreign", 1)
			case "shell":
				h.user = strings.Replace(h.user, "/usr/sbin/nologin", "/bin/bash", 1)
			case "shared-group":
				h.group = strings.Replace(h.group, "991:\n", "991:other\n", 1)
			case "sudo-group":
				h.groups = "991 27"
			case "unlocked":
				h.password = "P"
			case "uid":
				h.user = strings.Replace(h.user, ":991:991:", ":1000:991:", 1)
			case "gid":
				h.user = strings.Replace(h.user, ":991:991:", ":991:1000:", 1)
			case "uid-alias":
				base := o.command
				o.command = func(ctx context.Context, a ...string) ([]byte, error) {
					if a[0] == "/usr/bin/getent" && a[1] == "passwd" && a[2] == "991" {
						return []byte("foreign:x:991:991:foreign:/home/foreign:/bin/bash\n"), nil
					}
					return base(ctx, a...)
				}
			case "group-alias":
				base := o.command
				o.command = func(ctx context.Context, a ...string) ([]byte, error) {
					if a[0] == "/usr/bin/getent" && a[1] == "group" && a[2] == "991" {
						return []byte("foreign:x:991:\n"), nil
					}
					return base(ctx, a...)
				}
			}
			if e := start(context.Background(), o, m); e == nil {
				t.Fatal("unsafe account started")
			}
			for _, a := range h.calls[before:] {
				if a[0] != "/usr/bin/passwd" {
					t.Fatal("unsafe account reached host mutation", a)
				}
			}
		})
	}
}
func TestForeignFilesUnitsOverridesAndOwnerRefused(t *testing.T) {
	for _, change := range []string{"binary", "setuid", "manifest-owner", "probe", "state", "root-extra", "unit", "dropin", "enable-link", "owner", "root-owner"} {
		t.Run(change, func(t *testing.T) {
			h, o, m := installFixture(t)
			switch change {
			case "binary":
				f := h.files[m.Core]
				f.Data = []byte("foreign")
				h.files[m.Core] = f
			case "setuid":
				f := h.files[m.Core]
				f.Mode |= os.ModeSetuid
				h.files[m.Core] = f
			case "manifest-owner":
				f := h.files[m.Config]
				f.UID = 1000
				h.files[m.Config] = f
			case "probe":
				f := h.files[m.Root+"/app-readonly-probe"]
				f.UID = 0
				h.files[m.Root+"/app-readonly-probe"] = f
			case "state":
				f := h.files[m.Data]
				f.GID = 1000
				h.files[m.Data] = f
			case "root-extra":
				h.files[m.Root+"/owner-backup"] = hostFile{Regular: true, Data: []byte("preserve")}
			case "unit":
				f := h.files[m.UnitPath]
				f.Data = []byte("foreign")
				h.files[m.UnitPath] = f
			case "dropin":
				h.override = true
			case "enable-link":
				h.files["/etc/systemd/system/multi-user.target.wants/"+m.Unit] = hostFile{UID: 0, Mode: os.ModeSymlink | 0777, Link: "/foreign.service"}
			case "owner":
				o.uid = 1001
				o.euid = 1001
			case "root-owner":
				o.euid = 0
			}
			before := len(h.calls)
			if e := start(context.Background(), o, m); e == nil {
				t.Fatal("foreign installation started")
			}
			for _, a := range h.calls[before:] {
				if a[0] != "/usr/bin/passwd" {
					t.Fatal("foreign state mutated", a)
				}
			}
		})
	}
}
func TestRuntimeIdentityAndArgumentBinding(t *testing.T) {
	for _, change := range []string{"none", "uid", "euid", "gid", "groups", "core", "config", "data", "listen", "origin", "image"} {
		t.Run(change, func(t *testing.T) {
			_, o, m := installFixture(t)
			o.uid = m.AppUID
			o.euid = m.AppUID
			o.gid = m.AppGID
			config, root, data, listen, origin := m.Config, m.Root, m.Data, m.Listen, m.PublicOrigin
			switch change {
			case "uid":
				o.uid = m.OwnerUID
			case "euid":
				o.euid = 0
			case "gid":
				o.gid = m.OwnerGID
			case "groups":
				o.groups = func() ([]int, error) { return []int{m.AppGID, 27}, nil }
			case "core":
				root = "/foreign"
			case "config":
				config = "/foreign.json"
			case "data":
				data += "-other"
			case "listen":
				listen = "127.0.0.1:7444"
			case "origin":
				origin = "https://other.ts.net"
			case "image":
				o.executable = func() (string, error) { return "/foreign/fased-app", nil }
			}
			e := checkApplication(config, root, data, listen, origin, o)
			if (e == nil) != (change == "none") {
				t.Fatal(change, e)
			}
		})
	}
}
func TestRemovalRetryPreservesAllStateAndIdentities(t *testing.T) {
	h, o, m := installFixture(t)
	if e := start(context.Background(), o, m); e != nil {
		t.Fatal(e)
	}
	state := m.Data + "/owner.sqlite"
	h.files[state] = hostFile{UID: m.AppUID, GID: m.AppGID, Regular: true, Mode: 0600, Data: []byte("unique owner records")}
	savedUser := h.user
	h.fail = len(h.calls) + 3
	if e := remove(context.Background(), o, m); e == nil {
		t.Fatal("removal interruption missing")
	}
	h.fail = 0
	if e := remove(context.Background(), o, m); e != nil {
		t.Fatal(e)
	}
	if e := remove(context.Background(), o, m); e != nil {
		t.Fatal("completed removal not idempotent", e)
	}
	if string(h.files[state].Data) != "unique owner records" || h.user != savedUser {
		t.Fatal("removal deleted state or identity")
	}
	if e := verifyFiles(o, m); e != nil {
		t.Fatal("preserved immutable inventory changed", e)
	}
	r, e := status(context.Background(), o, m)
	if e != nil || r.Installed || r.Active || r.State != "removed" {
		t.Fatal("removed service status retained installation flags", r, e)
	}
	var l lifecycle
	if strict(h.journals["lifecycle.json"], &l) != nil || l.Phase != "removed" {
		t.Fatal("removal completion not retained")
	}
}
func TestConsentSourceAndPlatformRefusedBeforeMutation(t *testing.T) {
	for _, change := range []string{"consent", "root", "source", "platform", "listen", "origin"} {
		t.Run(change, func(t *testing.T) {
			h, o, opt := fixtureHost()
			switch change {
			case "consent":
				opt.PrepareHost = false
			case "root":
				o.uid = 0
				o.euid = 0
			case "source":
				opt.CoreSHA256 = strings.Repeat("0", 64)
			case "platform":
				f := h.files["/etc/os-release"]
				f.Data = []byte("ID=fedora\nVERSION_ID=43\n")
				h.files["/etc/os-release"] = f
			case "listen":
				opt.Listen = "0.0.0.0:7331"
			case "origin":
				opt.PublicOrigin = "https://host.ts.net/evil"
			}
			if _, e := install(context.Background(), opt, o); e == nil || len(h.calls) != 0 {
				t.Fatal("invalid inputs reached host mutation", e)
			}
		})
	}
}

func TestUbuntuOSReleaseTrustedAlias(t *testing.T) {
	for _, variant := range []string{"absolute", "relative", "foreign-alias", "foreign-destination", "writable-destination", "foreign-parent", "writable-parent", "parent-symlink", "chained-alias", "unexpected-target", "foreign-regular"} {
		t.Run(variant, func(t *testing.T) {
			h, o, opt := fixtureHost()
			release := h.files["/etc/os-release"]
			for _, p := range []string{"/usr", "/usr/lib"} {
				h.files[p] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
			}
			h.files["/usr/lib/os-release"] = release
			alias := hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/usr/lib/os-release"}
			switch variant {
			case "relative":
				alias.Link = "../usr/lib/os-release"
			case "foreign-alias":
				alias.UID = 1000
			case "foreign-destination":
				release.UID = 1000
				h.files["/usr/lib/os-release"] = release
			case "writable-destination":
				release.Mode = 0666
				h.files["/usr/lib/os-release"] = release
			case "foreign-parent":
				parent := h.files["/usr/lib"]
				parent.UID = 1000
				h.files["/usr/lib"] = parent
			case "writable-parent":
				parent := h.files["/usr/lib"]
				parent.Mode = os.ModeDir | 0777
				h.files["/usr/lib"] = parent
			case "parent-symlink":
				h.files["/usr/lib"] = hostFile{UID: 0, Mode: os.ModeSymlink | 0777, Link: "/foreign/lib"}
			case "chained-alias":
				h.files["/usr/lib/os-release"] = hostFile{UID: 0, Mode: os.ModeSymlink | 0777, Link: "/foreign/os-release"}
			case "unexpected-target":
				alias.Link = "/tmp/os-release"
			case "foreign-regular":
				alias = release
				alias.UID = 1000
			}
			h.files["/etc/os-release"] = alias
			_, e := install(context.Background(), opt, o)
			accepted := variant == "absolute" || variant == "relative"
			if (e == nil) != accepted {
				t.Fatalf("alias admission %s: %v", variant, e)
			}
			if !accepted && len(h.calls) != 0 {
				t.Fatal("untrusted OS-release alias reached host mutation")
			}
		})
	}
}

func TestSharedPasswordToolRejectsEveryMutationForm(t *testing.T) {
	for _, args := range [][]string{{"/usr/bin/passwd"}, {"/usr/bin/passwd", "-l", "fased-vps-1000"}, {"/usr/bin/passwd", "-S"}, {"/usr/bin/passwd", "-S", "--all"}, {"/usr/bin/passwd", "-S", "fased-vps-1000", "--unlock"}} {
		if _, e := (&custodyinstall.System{Out: io.Discard}).Run(context.Background(), nil, args...); e == nil || !strings.Contains(e.Error(), "status") {
			t.Fatal("password mutation form admitted", args, e)
		}
	}
}
