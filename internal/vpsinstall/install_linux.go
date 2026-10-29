//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

type hostFile struct {
	Data               []byte
	UID, GID           int
	Mode               os.FileMode
	Directory, Regular bool
	Link               string
}
type hostOps struct {
	uid, euid, gid    int
	read              func(string) (hostFile, error)
	list              func(string) ([]string, error)
	run               func(context.Context, []byte, ...string) ([]byte, error)
	command           func(context.Context, ...string) ([]byte, error)
	helper            func(context.Context, []string, io.Writer) error
	readJournal       func(string) ([]byte, error)
	writeJournal      func(string, any) error
	lock              func() (io.Closer, error)
	runtime           func() error
	executable        func() (string, error)
	groups            func() ([]int, error)
	enrollmentJournal func() ([]byte, error) // Fixed owner journal; no runtime override.
	enrolledTarget    func(string, int) (custodyinstall.Manifest, error)
	custodyTarget     func(int) (custodyinstall.Manifest, error)
}
type intent struct {
	Version                          int    `json:"version"`
	ID                               string `json:"id"`
	OwnerUID, OwnerGID               int
	CoreSHA256, Listen, PublicOrigin string
}
type lifecycle struct {
	Version                   int `json:"version"`
	ID, ManifestSHA256, Phase string
}

var errMissingAccount = errors.New("missing account") // Private fake-runner sentinel.

func file(path string) (hostFile, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return hostFile{}, e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return hostFile{}, errors.New("unknown host file ownership")
	}
	f := hostFile{UID: int(st.Uid), GID: int(st.Gid), Mode: i.Mode(), Directory: i.IsDir(), Regular: i.Mode().IsRegular()}
	// The app-owned empty marker is inspected by metadata; its 0600 bytes are
	// deliberately unreadable to the administrator. Procfs files report size 0
	// despite having content, so other regular files still use bounded reads.
	if f.Regular && !(i.Size() == 0 && filepath.Base(path) == "app-readonly-probe") {
		if i.Size() > 64<<20 {
			return f, errors.New("oversized host input")
		}
		f.Data, e = os.ReadFile(path)
	}
	if i.Mode()&os.ModeSymlink != 0 {
		f.Link, e = os.Readlink(path)
	}
	return f, e
}
func ownerJournal() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	d = filepath.Join(d, "fased-app-vps-installer")
	return d, core.EnsurePrivateDirectory(d)
}
func liveOps(s *custodyinstall.System) hostOps {
	o := hostOps{uid: os.Getuid(), euid: os.Geteuid(), gid: os.Getgid(), read: file, executable: os.Executable, groups: os.Getgroups}
	bindEnrollmentOps(&o)
	o.list = func(p string) ([]string, error) {
		es, e := os.ReadDir(p)
		var names []string
		for _, v := range es {
			names = append(names, v.Name())
		}
		return names, e
	}
	o.runtime = func() error { return trustedTool(o, "/usr/bin/systemctl") }
	o.command = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) == 0 || (args[0] != "/usr/bin/getent" && args[0] != "/usr/bin/id" && args[0] != "/usr/bin/systemctl") {
			return nil, errors.New("unknown Remote read-only OS tool")
		}
		if e := trustedTool(o, args[0]); e != nil {
			return nil, e
		}
		return exec.CommandContext(ctx, args[0], args[1:]...).Output()
	}
	o.readJournal = func(name string) ([]byte, error) {
		d, e := ownerJournal()
		if e != nil {
			return nil, e
		}
		return core.ReadPrivateFile(filepath.Join(d, name))
	}
	o.writeJournal = func(name string, v any) error {
		d, e := ownerJournal()
		if e != nil {
			return e
		}
		return core.WritePrivateJSON(filepath.Join(d, name), v)
	}
	o.lock = func() (io.Closer, error) {
		d, e := ownerJournal()
		if e != nil {
			return nil, e
		}
		return core.LockInstance(filepath.Join(d, "install.lock"))
	}
	if s != nil {
		if s.In == nil {
			s.In = os.Stdin
		}
		if s.Out == nil {
			s.Out = io.Discard
		}
		if s.Authorization == "" {
			s.Authorization = "Authorize Remote installation: one dedicated locked OS account, immutable application files and an app-only system service. Fased runs without root or sudo access. Instance data and identities are preserved on removal."
		}
		o.run = s.Run
		o.helper = func(ctx context.Context, args []string, out io.Writer) error {
			if _, e := s.Run(ctx, nil, "/usr/bin/setpriv", "--version"); e != nil {
				return e
			}
			c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--"}, args...)...)
			c.Stdin = s.In
			c.Stdout = out
			c.Stderr = s.Out
			return c.Run()
		}
	}
	return o
}
func strict(b []byte, v any) error {
	if len(b) > 16<<10 {
		return errors.New("oversized Remote record")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing Remote record")
	}
	return nil
}
func encoded(v any) []byte { b, _ := json.Marshal(v); return b }
func ordinary(o hostOps) error {
	if o.uid <= 0 || o.euid != o.uid {
		return errors.New("ordinary installation owner required")
	}
	return nil
}
func ownedBy(o hostOps, m Manifest) error {
	if e := ordinary(o); e != nil {
		return e
	}
	if m.OwnerUID != o.uid {
		return errors.New("Remote installation belongs to another OS owner")
	}
	return m.validate()
}
func parents(o hostOps, path string) error {
	for p := path; ; p = filepath.Dir(p) {
		f, e := o.read(p)
		if e != nil {
			return e
		}
		if !f.Directory || f.UID != 0 || f.GID != 0 || f.Mode.Perm()&0022 != 0 || f.Mode&os.ModeSymlink != 0 {
			return errors.New("root-owned immutable directory parents required")
		}
		if p == "/" {
			return nil
		}
	}
}
func trustedTool(o hostOps, p string) error {
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil {
		return e
	}
	if e = parents(o, filepath.Dir(resolved)); e != nil {
		return e
	}
	f, e := o.read(resolved)
	if e != nil {
		return e
	}
	if !f.Regular || f.UID != 0 || f.Mode.Perm()&0022 != 0 || f.Mode.Perm()&0111 == 0 {
		return errors.New("trusted root-owned OS tool required")
	}
	return nil
}
func rootFile(o hostOps, p string) (hostFile, error) {
	if e := parents(o, filepath.Dir(p)); e != nil {
		return hostFile{}, e
	}
	f, e := o.read(p)
	if e != nil {
		return f, e
	}
	if !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm()&0022 != 0 || f.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return f, errors.New("foreign root file; preserve it")
	}
	return f, nil
}

// Ubuntu's OS-owned release file is commonly reached through this one standard
// alias. Resolve only that known alias; managed installation files remain no-follow.
func osRelease(o hostOps) (hostFile, error) {
	f, e := o.read("/etc/os-release")
	if e != nil {
		return f, e
	}
	if f.Mode&os.ModeSymlink == 0 {
		return rootFile(o, "/etc/os-release")
	}
	if e = parents(o, "/etc"); e != nil {
		return f, e
	}
	if f.UID != 0 || f.GID != 0 || (f.Link != "/usr/lib/os-release" && f.Link != "../usr/lib/os-release") {
		return f, errors.New("untrusted OS-release alias; preserving it")
	}
	return rootFile(o, "/usr/lib/os-release")
}
func known(f hostFile, b []byte, mode os.FileMode) bool {
	return f.Regular && f.UID == 0 && f.GID == 0 && f.Mode.Perm() == mode && bytes.Equal(f.Data, b)
}
func manifestAt(o hostOps, owner int) (Manifest, error) {
	var m Manifest
	if owner <= 0 {
		return m, errors.New("ordinary Remote owner required")
	}
	f, e := rootFile(o, ConfigPath(owner))
	if e != nil {
		return m, e
	}
	if f.Mode.Perm() != 0644 || strict(f.Data, &m) != nil || m.validate() != nil || m.OwnerUID != owner {
		return m, errors.New("invalid root-bound Remote manifest")
	}
	return m, nil
}
func Load(owner int) (Manifest, error) {
	return load(liveOps(nil), owner)
}
func load(o hostOps, owner int) (Manifest, error) {
	if e := ordinary(o); e != nil {
		return Manifest{}, e
	}
	if owner != o.uid {
		return Manifest{}, errors.New("current OS owner required")
	}
	m, e := manifestAt(o, owner)
	if e != nil {
		return m, e
	}
	return m, verifyEnrollmentFiles(o, m, true)
}
func checkDirectory(o hostOps, path string, uid, gid int, mode os.FileMode) error {
	f, e := o.read(path)
	if e != nil {
		return e
	}
	if !f.Directory || f.UID != uid || f.GID != gid || f.Mode.Perm() != mode || f.Mode&os.ModeSymlink != 0 {
		return errors.New("foreign managed directory; ownership is never repaired")
	}
	return nil
}
func directory(ctx context.Context, o hostOps, path string, uid, gid int, mode os.FileMode) error {
	if _, e := o.read(path); e == nil {
		return checkDirectory(o, path, uid, gid, mode)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if _, e := o.run(ctx, nil, "/usr/bin/install", "-d", "-m", strconv.FormatUint(uint64(mode), 8), "-o", strconv.Itoa(uid), "-g", strconv.Itoa(gid), "--", path); e != nil {
		return e
	}
	return checkDirectory(o, path, uid, gid, mode)
}

// Exclusive publication only replaces its exact intent-bound staging filename.
func publish(ctx context.Context, o hostOps, target, id string, b []byte, mode os.FileMode) error {
	stage := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	f, e := rootFile(o, target)
	ready := e == nil
	if ready && !known(f, b, mode) {
		return errors.New("foreign managed file; preserving it")
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	st, se := rootFile(o, stage)
	if se == nil && (!st.Regular || st.Mode.Perm() != mode || !bytes.HasPrefix(b, st.Data)) {
		return errors.New("foreign staging file; preserving it")
	}
	if se != nil && !errors.Is(se, os.ErrNotExist) {
		return se
	}
	if !ready {
		if _, e = o.run(ctx, b, "/usr/bin/install", "-m", strconv.FormatUint(uint64(mode), 8), "-o", "root", "-g", "root", "--", "/dev/stdin", stage); e != nil {
			return e
		}
		st, e = rootFile(o, stage)
		if e != nil || !known(st, b, mode) {
			return errors.New("root staging verification failed")
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", stage); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", stage, target); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); e != nil {
			return e
		}
	}
	if !ready || se == nil {
		if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
			return e
		}
		_, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target))
		return e
	}
	return nil
}
func missingAccount(e error) bool {
	var exit *exec.ExitError
	return errors.Is(e, errMissingAccount) || (errors.As(e, &exit) && exit.ExitCode() == 2)
}
func marker(id string) string { return "Fased Remote " + id }
func account(ctx context.Context, o hostOps, j intent, create bool) (int, int, error) {
	name := accountName(j.OwnerUID)
	b, e := o.command(ctx, "/usr/bin/getent", "passwd", name)
	if missingAccount(e) && create {
		if gb, ge := o.command(ctx, "/usr/bin/getent", "group", name); !missingAccount(ge) || len(gb) != 0 {
			return 0, 0, errors.New("foreign managed group; preserving it")
		}
		if _, e = o.run(ctx, nil, "/usr/sbin/useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "--password", "!", "--comment", marker(j.ID), name); e != nil {
			return 0, 0, e
		}
		b, e = o.command(ctx, "/usr/bin/getent", "passwd", name)
	}
	if e != nil {
		return 0, 0, e
	}
	f := strings.Split(strings.TrimSpace(string(b)), ":")
	if len(f) != 7 || f[0] != name || f[1] != "x" || f[4] != marker(j.ID) || f[5] != "/nonexistent" || f[6] != "/usr/sbin/nologin" {
		return 0, 0, errors.New("foreign or interactive Remote account; preserving it")
	}
	uid, ue := strconv.Atoi(f[2])
	gid, ge := strconv.Atoi(f[3])
	if ue != nil || ge != nil || uid <= 0 || gid <= 0 || uid == j.OwnerUID || gid == j.OwnerGID || strconv.Itoa(uid) != f[2] || strconv.Itoa(gid) != f[3] {
		return 0, 0, errors.New("Remote account must have independent UID and group")
	}
	byUID, e := o.command(ctx, "/usr/bin/getent", "passwd", strconv.Itoa(uid))
	if e != nil || !bytes.Equal(byUID, b) {
		return 0, 0, errors.New("Remote UID is ambiguous")
	}
	group, e := o.command(ctx, "/usr/bin/getent", "group", name)
	g := strings.Split(strings.TrimSpace(string(group)), ":")
	if e != nil || len(g) != 4 || g[0] != name || g[2] != strconv.Itoa(gid) || g[3] != "" {
		return 0, 0, errors.New("foreign or shared Remote primary group")
	}
	numeric, e := o.command(ctx, "/usr/bin/getent", "group", strconv.Itoa(gid))
	if e != nil || !bytes.Equal(numeric, group) {
		return 0, 0, errors.New("Remote GID is ambiguous")
	}
	groups, e := o.command(ctx, "/usr/bin/id", "-G", name)
	if e != nil || strings.TrimSpace(string(groups)) != strconv.Itoa(gid) {
		return 0, 0, errors.New("Remote account must have no supplementary or sudo groups")
	}
	status, e := o.run(ctx, nil, "/usr/bin/passwd", "-S", name)
	fields := strings.Fields(string(status))
	if e != nil || len(fields) < 2 || fields[0] != name || fields[1] != "L" {
		return 0, 0, errors.New("Remote runtime password must remain locked")
	}
	return uid, gid, nil
}
func intentFor(m Manifest) intent {
	return intent{Version: 1, ID: m.ID, OwnerUID: m.OwnerUID, OwnerGID: m.OwnerGID, CoreSHA256: m.CoreSHA256, Listen: m.Listen, PublicOrigin: m.PublicOrigin}
}
func verifyFiles(o hostOps, m Manifest) error {
	if e := verifyImmutableFiles(o, m); e != nil {
		return e
	}
	if e := checkDirectory(o, m.Data, m.AppUID, m.AppGID, 0700); e != nil {
		return e
	}
	return verifyRootInventory(o, m, "")
}
func verifyImmutableFiles(o hostOps, m Manifest) error {
	if e := m.validate(); e != nil {
		return e
	}
	actual, e := manifestAt(o, m.OwnerUID)
	if e != nil || actual != m {
		return errors.New("Remote root manifest changed")
	}
	if e = checkDirectory(o, m.Root, 0, 0, 0755); e != nil {
		return e
	}
	probe, e := o.read(m.Root + "/app-readonly-probe")
	if e != nil || !probe.Regular || probe.UID != m.AppUID || probe.GID != m.AppGID || probe.Mode.Perm() != 0600 || len(probe.Data) != 0 {
		return errors.New("Remote read-only marker identity changed")
	}
	for _, v := range []struct {
		p    string
		b    []byte
		mode os.FileMode
	}{{m.Config, encoded(m), 0644}, {m.Root + "/" + m.Unit, unit(m), 0644}, {m.Root + "/intent.json", encoded(intentFor(m)), 0644}} {
		f, e := rootFile(o, v.p)
		if e != nil || !known(f, v.b, v.mode) {
			return errors.New("Remote immutable inventory changed")
		}
	}
	binary, e := rootFile(o, m.Core)
	if e != nil || binary.Mode.Perm() != 0755 || hash(binary.Data) != m.CoreSHA256 {
		return errors.New("Remote installed executable changed")
	}
	return nil
}
func verifyRootInventory(o hostOps, m Manifest, sibling string) error {
	names, e := o.list(m.Root)
	if e != nil {
		return e
	}
	allowed := map[string]bool{"fased-app": true, "install.json": true, "intent.json": true, "app-state": true, "app-readonly-probe": true, m.Unit: true}
	if sibling != "" {
		allowed[sibling] = true
	}
	for _, name := range names {
		if !allowed[name] {
			return errors.New("unclassified Remote installation file; preserving it")
		}
	}
	return nil
}
func verify(ctx context.Context, o hostOps, m Manifest) error {
	if e := ownedBy(o, m); e != nil {
		return e
	}
	if e := verifyFiles(o, m); e != nil {
		return e
	}
	return verifyAccount(ctx, o, m)
}
func verifyAccount(ctx context.Context, o hostOps, m Manifest) error {
	uid, gid, e := account(ctx, o, intentFor(m), false)
	if e != nil {
		return e
	}
	if uid != m.AppUID || gid != m.AppGID {
		return errors.New("Remote runtime identity changed")
	}
	return nil
}
func fields(b []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}
func unitFields(ctx context.Context, o hostOps, m Manifest) (map[string]string, error) {
	b, e := o.command(ctx, "/usr/bin/systemctl", "show", m.Unit, "--property=LoadState,ActiveState,MainPID,FragmentPath,DropInPaths,User,Group,NoNewPrivileges,ProtectSystem,ProtectHome,PrivateTmp,ReadWritePaths,CapabilityBoundingSet,AmbientCapabilities,ExecStart")
	return fields(b), e
}
func noUnit(ctx context.Context, o hostOps, m Manifest) error {
	if _, e := o.read(m.UnitPath); !errors.Is(e, os.ErrNotExist) {
		return errors.New("foreign system service file exists; preserving it")
	}
	f, e := unitFields(ctx, o, m)
	if e != nil {
		return e
	}
	if f["LoadState"] != "not-found" || f["FragmentPath"] != "" || f["DropInPaths"] != "" {
		return errors.New("foreign loaded system service or override exists")
	}
	return nil
}
func loaded(ctx context.Context, o hostOps, m Manifest) error {
	f, e := rootFile(o, m.UnitPath)
	if e != nil || !known(f, unit(m), 0644) {
		return errors.New("Remote system unit changed; preserving it")
	}
	p, e := unitFields(ctx, o, m)
	if e != nil {
		return e
	}
	if p["LoadState"] != "loaded" || p["FragmentPath"] != m.UnitPath || p["DropInPaths"] != "" || p["User"] != strconv.Itoa(m.AppUID) || p["Group"] != strconv.Itoa(m.AppGID) || p["NoNewPrivileges"] != "yes" || p["ProtectSystem"] != "strict" || p["ProtectHome"] != "yes" || p["PrivateTmp"] != "yes" || p["ReadWritePaths"] != m.Data || p["CapabilityBoundingSet"] != "" || p["AmbientCapabilities"] != "" || !strings.Contains(p["ExecStart"], "path="+m.Core+" ;") || !strings.Contains(p["ExecStart"], m.Core+" serve ") || !strings.Contains(p["ExecStart"], "--remote-config "+m.Config) {
		return errors.New("unexpected loaded Remote service identity or override")
	}
	return nil
}
func enableLink(o hostOps, m Manifest) error {
	p := "/etc/systemd/system/multi-user.target.wants/" + m.Unit
	f, e := o.read(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	target := f.Link
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(p), target)
	}
	if f.UID != 0 || f.Mode&os.ModeSymlink == 0 || filepath.Clean(target) != m.UnitPath {
		return errors.New("foreign service enablement link; preserving it")
	}
	return nil
}
func record(o hostOps, m Manifest, phase string) error {
	return o.writeJournal("lifecycle.json", lifecycle{Version: 1, ID: m.ID, ManifestSHA256: hash(encoded(m)), Phase: phase})
}
func Install(ctx context.Context, options Options, s *custodyinstall.System) (Manifest, error) {
	if s == nil {
		return Manifest{}, errors.New("owner OS authorization is required")
	}
	return install(ctx, options, liveOps(s))
}
func install(ctx context.Context, opt Options, o hostOps) (Manifest, error) {
	var m Manifest
	if !opt.PrepareHost {
		return m, errors.New("explicit Remote host preparation consent is required")
	}
	if e := ordinary(o); e != nil {
		return m, e
	}
	if !canonicalHex(opt.CoreSHA256, 32) {
		return m, errors.New("verified core SHA-256 required")
	}
	if e := validateEndpoint(opt.Listen, opt.PublicOrigin); e != nil {
		return m, e
	}
	release, e := osRelease(o)
	if e != nil {
		return m, e
	}
	values := fields(release.Data)
	if strings.Trim(values["ID"], "\"'") != "ubuntu" || strings.Trim(values["VERSION_ID"], "\"'") != "24.04" {
		return m, errors.New("app-only Remote installation requires Ubuntu 24.04")
	}
	binary, e := o.read(opt.Core)
	if e != nil {
		return m, e
	}
	if !binary.Regular || binary.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || hash(binary.Data) != opt.CoreSHA256 {
		return m, errors.New("core input differs from verified binary")
	}
	if e = o.runtime(); e != nil {
		return m, e
	}
	lock, e := o.lock()
	if e != nil {
		return m, e
	}
	defer lock.Close()
	j := intent{Version: 1, OwnerUID: o.uid, OwnerGID: o.gid, CoreSHA256: opt.CoreSHA256, Listen: opt.Listen, PublicOrigin: opt.PublicOrigin}
	existing, le := manifestAt(o, o.uid)
	if le != nil && !errors.Is(le, os.ErrNotExist) {
		return m, le
	}
	if b, e := o.readJournal("intent.json"); e == nil {
		var prior intent
		if strict(b, &prior) != nil || !canonicalHex(prior.ID, 16) {
			return m, errors.New("invalid Remote installer intent")
		}
		j.ID = prior.ID
		if prior != j {
			return m, errors.New("Remote installer intent differs; preserving prior work")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return m, e
	}
	if le == nil {
		j.ID = existing.ID
		if intentFor(existing) != j {
			return m, errors.New("existing Remote inputs differ; in-place replacement is unavailable")
		}
		m = existing
	}
	if b, e := o.readJournal("lifecycle.json"); e == nil {
		var l lifecycle
		if strict(b, &l) != nil || l.Version != 1 || l.ID != j.ID || l.ManifestSHA256 != hash(encoded(existing)) {
			return m, errors.New("invalid Remote lifecycle receipt")
		}
		if l.Phase == "removing" {
			return m, errors.New("resume Remote removal before installing")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return m, e
	}
	if j.ID == "" {
		id := make([]byte, 16)
		if _, e = rand.Read(id); e != nil {
			return m, e
		}
		j.ID = hex.EncodeToString(id)
	}
	if e = o.writeJournal("intent.json", j); e != nil {
		return m, e
	}
	root := Root(o.uid)
	stage := filepath.Join(filepath.Dir(root), ".fased-app-vps-"+strconv.Itoa(o.uid)+"-"+j.ID+".pending")
	if le != nil {
		if _, e = o.read(root); !errors.Is(e, os.ErrNotExist) {
			return m, errors.New("unclassified Remote root exists; preserving it")
		}
		skeleton := Manifest{Unit: unitName(o.uid), UnitPath: "/etc/systemd/system/" + unitName(o.uid)}
		if e = noUnit(ctx, o, skeleton); e != nil {
			return m, e
		}
		if e = parents(o, filepath.Dir(root)); e != nil {
			return m, e
		}
		if e = directory(ctx, o, stage, 0, 0, 0755); e != nil {
			return m, e
		}
		names, e := o.list(stage)
		if e != nil {
			return m, e
		}
		allowed := map[string]bool{"intent.json": true, "install.json": true, "fased-app": true, "app-state": true, "app-readonly-probe": true, unitName(o.uid): true}
		for _, n := range names {
			base := strings.TrimSuffix(strings.TrimPrefix(n, "."), "-"+j.ID+".pending")
			if !allowed[n] && (!strings.HasPrefix(n, ".") || !strings.HasSuffix(n, "-"+j.ID+".pending") || !allowed[base]) {
				return m, errors.New("foreign staging content; preserving it")
			}
		}
		if e = publish(ctx, o, stage+"/intent.json", j.ID, encoded(j), 0644); e != nil {
			return m, e
		}
		appUID, appGID, e := account(ctx, o, j, true)
		if e != nil {
			return m, e
		}
		m = Manifest{Version: 1, ID: j.ID, Phase: "installed", OwnerUID: o.uid, OwnerGID: o.gid, AppUID: appUID, AppGID: appGID, AppUser: accountName(o.uid), Root: root, Data: root + "/app-state", Core: root + "/fased-app", Config: ConfigPath(o.uid), Unit: unitName(o.uid), UnitPath: "/etc/systemd/system/" + unitName(o.uid), Listen: opt.Listen, PublicOrigin: opt.PublicOrigin, CoreSHA256: opt.CoreSHA256}
		if e = m.validate(); e != nil {
			return m, e
		}
		if e = directory(ctx, o, stage+"/app-state", appUID, appGID, 0700); e != nil {
			return m, e
		}
		probe := stage + "/app-readonly-probe"
		if _, e = o.read(probe); errors.Is(e, os.ErrNotExist) {
			if _, e = o.run(ctx, nil, "/usr/bin/install", "-m", "600", "-o", strconv.Itoa(appUID), "-g", strconv.Itoa(appGID), "--", "/dev/null", probe); e != nil {
				return m, e
			}
		} else if e != nil {
			return m, e
		}
		pf, e := o.read(probe)
		if e != nil || !pf.Regular || pf.UID != appUID || pf.GID != appGID || pf.Mode.Perm() != 0600 || len(pf.Data) != 0 {
			return m, errors.New("foreign Remote sandbox marker")
		}
		for _, v := range []struct {
			name string
			b    []byte
			mode os.FileMode
		}{{"fased-app", binary.Data, 0755}, {m.Unit, unit(m), 0644}, {"install.json", encoded(m), 0644}} {
			if e = publish(ctx, o, stage+"/"+v.name, j.ID, v.b, v.mode); e != nil {
				return m, e
			}
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", stage); e != nil {
			return m, e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/mv", "--no-clobber", "--no-target-directory", "--", stage, root); e != nil {
			return m, e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(root)); e != nil {
			return m, e
		}
	}
	if e = verify(ctx, o, m); e != nil {
		return m, e
	}
	if e = publish(ctx, o, m.UnitPath, m.ID, unit(m), 0644); e != nil {
		return m, e
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return m, e
	}
	if e = loaded(ctx, o, m); e != nil {
		return m, e
	}
	if e = record(o, m, "installed"); e != nil {
		return m, e
	}
	if opt.Start {
		e = start(ctx, o, m)
	}
	return m, e
}
func Start(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if s == nil {
		return errors.New("owner OS authorization required")
	}
	return locked(ctx, liveOps(s), m, func(o hostOps) error { return start(ctx, o, m) })
}
func locked(ctx context.Context, o hostOps, m Manifest, f func(hostOps) error) error {
	if e := ownedBy(o, m); e != nil {
		return e
	}
	l, e := o.lock()
	if e != nil {
		return e
	}
	defer l.Close()
	return f(o)
}
func start(ctx context.Context, o hostOps, m Manifest) error {
	if e := verify(ctx, o, m); e != nil {
		return e
	}
	if e := loaded(ctx, o, m); e != nil {
		return e
	}
	if e := enableLink(o, m); e != nil {
		return e
	}
	if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "enable", "--", m.Unit); e != nil {
		return e
	}
	if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", m.Unit); e != nil {
		return e
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		r, e := status(ctx, o, m)
		if e == nil && r.Active && r.InstanceID != "" {
			return record(o, m, "ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Remote core did not become ready; preserve state and retry the same command")
		case <-tick.C:
		}
	}
}
func Stop(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if s == nil {
		return errors.New("owner OS authorization required")
	}
	o := liveOps(s)
	return locked(ctx, o, m, func(o hostOps) error {
		if e := verify(ctx, o, m); e != nil {
			return e
		}
		if e := loaded(ctx, o, m); e != nil {
			return e
		}
		_, e := o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", m.Unit)
		return e
	})
}
func Remove(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if s == nil {
		return errors.New("owner OS authorization required")
	}
	o := liveOps(s)
	return locked(ctx, o, m, func(o hostOps) error { return remove(ctx, o, m) })
}
func remove(ctx context.Context, o hostOps, m Manifest) error {
	if e := verify(ctx, o, m); e != nil {
		return e
	}
	if e := enableLink(o, m); e != nil {
		return e
	}
	_, e := rootFile(o, m.UnitPath)
	present := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if present {
		if e = loaded(ctx, o, m); e != nil {
			return e
		}
	}
	if e = record(o, m, "removing"); e != nil {
		return e
	}
	if present {
		for _, action := range []string{"stop", "disable"} {
			if _, e = o.run(ctx, nil, "/usr/bin/systemctl", action, "--", m.Unit); e != nil {
				return e
			}
		}
		if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", m.UnitPath); e != nil {
			return e
		}
		if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(m.UnitPath)); e != nil {
			return e
		}
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return e
	}
	if e = noUnit(ctx, o, m); e != nil {
		return e
	}
	return record(o, m, "removed")
}
func Claim(ctx context.Context, m Manifest, s *custodyinstall.System, recover bool, out io.Writer) error {
	if s == nil || out == nil {
		return errors.New("owner terminal and output required")
	}
	o := liveOps(s)
	return locked(ctx, o, m, func(o hostOps) error { return claim(ctx, o, m, recover, out) })
}
func claim(ctx context.Context, o hostOps, m Manifest, recover bool, out io.Writer) error {
	if e := verify(ctx, o, m); e != nil {
		return e
	}
	if e := loaded(ctx, o, m); e != nil {
		return e
	}
	p, e := unitFields(ctx, o, m)
	if e != nil {
		return e
	}
	action := "claim-code"
	if recover {
		if p["ActiveState"] != "inactive" && p["ActiveState"] != "failed" {
			return errors.New("stop the Remote core before owner password recovery")
		}
		action = "recover"
	}
	return o.helper(ctx, helperArgs(m, action), out)
}
func Status(ctx context.Context, m Manifest, s *custodyinstall.System) (ServiceStatus, error) {
	if s == nil {
		return ServiceStatus{}, errors.New("owner OS authorization required")
	}
	return status(ctx, liveOps(s), m)
}
func status(ctx context.Context, o hostOps, m Manifest) (ServiceStatus, error) {
	r := ServiceStatus{Installed: true, Unit: m.Unit}
	if e := verify(ctx, o, m); e != nil {
		return r, e
	}
	if _, e := rootFile(o, m.UnitPath); errors.Is(e, os.ErrNotExist) {
		if e = noUnit(ctx, o, m); e != nil {
			return r, e
		}
		r.State = "removed"
		r.Installed = false
		return r, nil
	} else if e != nil {
		return r, e
	}
	if e := loaded(ctx, o, m); e != nil {
		return r, e
	}
	p, e := unitFields(ctx, o, m)
	if e != nil {
		return r, e
	}
	r.State = p["ActiveState"]
	if r.State != "active" {
		return r, nil
	}
	pid, e := strconv.Atoi(p["MainPID"])
	if e != nil || pid <= 0 {
		return r, errors.New("Remote runtime PID unavailable")
	}
	proc, e := o.read("/proc/" + strconv.Itoa(pid) + "/status")
	if e != nil {
		return r, e
	}
	want := "Uid:\t" + strings.Repeat(strconv.Itoa(m.AppUID)+"\t", 3) + strconv.Itoa(m.AppUID) + "\n"
	if !bytes.Contains(proc.Data, []byte(want)) || !bytes.Contains(proc.Data, []byte("NoNewPrivs:\t1\n")) {
		return r, errors.New("Remote runtime UID or NoNewPrivileges not enforced")
	}
	b, e := o.run(ctx, nil, helperArgs(m, "status")...)
	var h core.Health
	if e != nil {
		return r, e
	}
	if json.Unmarshal(b, &h) != nil || h.Status != "ok" || h.InstanceID == "" || h.APIVersion != core.APIVersion || len(h.Capabilities) == 0 || h.Capabilities["walletSigner"] || h.Capabilities["financialExecution"] || h.Capabilities["updater"] {
		return r, errors.New("Remote private IPC identity or capabilities invalid")
	}
	r.Active = true
	r.InstanceID = h.InstanceID
	r.URL = "http://" + m.Listen
	if m.PublicOrigin != "" {
		r.URL = m.PublicOrigin
	}
	return r, nil
}

// CheckApplication runs as the app UID before state/listeners are opened. It
// authenticates immutable code/configuration and refuses runtime retargeting.
func CheckApplication(config, installationRoot, dataDir, listen, publicOrigin string) error {
	return checkApplication(config, installationRoot, dataDir, listen, publicOrigin, liveOps(nil))
}
func checkApplication(config, installationRoot, dataDir, listen, publicOrigin string, o hostOps) error {
	f, e := rootFile(o, config)
	if e != nil {
		return e
	}
	var m Manifest
	if strict(f.Data, &m) != nil || m.validate() != nil || config != m.Config || installationRoot != m.Root || dataDir != m.Data || listen != m.Listen || publicOrigin != m.PublicOrigin {
		return errors.New("Remote runtime arguments differ from immutable manifest")
	}
	if o.uid != m.AppUID || o.euid != m.AppUID || o.gid != m.AppGID {
		return errors.New("dedicated Remote application identity required")
	}
	groups, e := o.groups()
	if e != nil {
		return e
	}
	for _, gid := range groups {
		if gid != m.AppGID {
			return errors.New("Remote application has unexpected supplementary groups")
		}
	}
	if e = verifyFiles(o, m); e != nil {
		return e
	}
	image, e := o.executable()
	if e != nil {
		return e
	}
	if image != m.Core {
		return fmt.Errorf("Remote process must use the immutable installed executable")
	}
	return nil
}
