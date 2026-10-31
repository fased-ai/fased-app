//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
)

func strict(b []byte, v any) error {
	if len(b) > 16*1024 {
		return errors.New("oversized installation record")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return errors.New("invalid installation record")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing installation record")
	}
	return nil
}
func source(path string) ([]byte, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > 64*1024*1024 {
		return nil, errors.New("fixture input must be a bounded regular binary")
	}
	return os.ReadFile(path)
}
func ownerJournal() (string, error) {
	b, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	p := filepath.Join(b, "fased-app-custody-installer")
	if e = core.EnsurePrivateDirectory(p); e != nil {
		return "", e
	}
	return p, nil
}
func OwnerLock() (io.Closer, error) {
	dir, e := ownerJournal()
	if e != nil {
		return nil, e
	}
	return core.LockInstance(dir + "/install.lock")
}

func Load(uid int) (Manifest, error) {
	var m Manifest
	if uid <= 0 {
		return m, errors.New("ordinary owner required")
	}
	f, e := readRoot(filepath.Join(Root(uid), "install.json"))
	if e != nil {
		return m, e
	}
	if e = strict(f.Data, &m); e != nil {
		return m, e
	}
	if e = m.validate(); e != nil {
		return m, e
	}
	if m.Owner != uid {
		return m, errors.New("installation belongs to another owner")
	}
	return m, nil
}

func directory(ctx context.Context, path string, uid, gid int, mode os.FileMode, s *System) error {
	i, e := os.Lstat(path)
	if e == nil {
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || int(st.Uid) != uid || int(st.Gid) != gid || i.Mode().Perm() != mode {
			return errors.New("foreign managed directory; ownership is never repaired over existing state")
		}
		return nil
	}
	if !os.IsNotExist(e) {
		return e
	}
	_, e = s.Run(ctx, nil, "/usr/bin/install", "-d", "-m", strconv.FormatUint(uint64(mode), 8), "-o", strconv.Itoa(uid), "-g", strconv.Itoa(gid), "--", path)
	return e
}
func account(ctx context.Context, name, id, role string, s *System) (Identity, error) {
	lookup := func() (Identity, error) {
		b, e := exec.CommandContext(ctx, "/usr/bin/getent", "passwd", name).Output()
		if e != nil {
			return Identity{}, e
		}
		return parseAccount(string(b), name, marker(id, role))
	}
	i, e := lookup()
	if e == nil {
		return i, nil
	}
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 2 {
		return i, e
	}
	// Do not adopt a foreign group under a managed name either.
	if b, ge := exec.CommandContext(ctx, "/usr/bin/getent", "group", name).Output(); ge == nil || len(b) != 0 {
		return i, errors.New("foreign managed group; preserving it")
	} else if !errors.As(ge, &exit) || exit.ExitCode() != 2 {
		return i, ge
	}
	if _, e = s.Run(ctx, nil, "/usr/sbin/useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "--password", "!", "--comment", marker(id, role), name); e != nil {
		return i, e
	}
	return lookup()
}
func identityConfig(m Manifest) signer.Config {
	return signer.Config{Version: m.Version, Profile: m.Profile, Provenance: m.Provenance, Mode: m.Mode, Network: m.Network, SignerID: m.ID, OwnerUID: m.Owner, AppUID: m.App.UID, SignerUID: m.Signer.UID, StateDir: m.Root + "/signer-state", SocketDir: m.Root + "/signer-ipc"}
}

func Install(ctx context.Context, o Options, s *System) (Manifest, error) {
	return install(ctx, o, s, false)
}

// InstallControlledOwnerCustody binds a compiled disposable owner profile. The
// ordinary CLI cannot select this constructor or supply a runtime override.
func InstallControlledOwnerCustody(ctx context.Context, o Options, s *System) (Manifest, error) {
	if o.Mode != signer.OrdinaryCustodyMode || o.Profile != signer.OwnerCustodyProfile || o.Provenance != signer.ControlledCustodyProfile || !o.StageOnly {
		return Manifest{}, signer.ErrUnavailable
	}
	return install(ctx, o, s, true)
}
func install(ctx context.Context, o Options, s *System, controlled bool) (Manifest, error) {
	var m Manifest
	if o.Mode == signer.OrdinaryCustodyMode && (!o.StageOnly || !controlled && (!signer.OrdinaryOwnerAdmissionEnabled() || o.Provenance != signer.OwnerKeyProvenance)) {
		return m, signer.ErrUnavailable
	}
	if controlled && (o.Profile != signer.OwnerCustodyProfile || o.Provenance != signer.ControlledCustodyProfile) {
		return m, signer.ErrUnavailable
	}
	if !o.Disposable || !o.PrepareHost {
		return m, errors.New("explicit --disposable-fixture and --prepare-host consent are required before OS mutation")
	}
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return m, errors.New("ordinary owner required")
	}
	osRelease, e := os.ReadFile("/etc/os-release")
	if e != nil || !bytes.Contains(osRelease, []byte("ID=ubuntu\n")) || !bytes.Contains(osRelease, []byte("VERSION_ID=\"24.04\"")) {
		return m, errors.New("managed fixture installation currently requires Ubuntu 24.04")
	}
	versionNumber := 1
	if o.Network != "" {
		versionNumber = 2
	}
	mode := modeFor(o.Network)
	if o.Mode != "" {
		mode = o.Mode
	}
	if mode == signer.OrdinaryCustodyMode {
		versionNumber = 3
	}
	if !custodyContext(versionNumber, o.Profile, o.Provenance) || !custodyMode(versionNumber, mode, o.Network) {
		return m, errors.New("unsupported disposable custody network")
	}
	if !validListen(o.Listen) {
		return m, errors.New("choose 127.0.0.1 and an explicit unprivileged port")
	}
	if !validLocation(o.Location, o.PublicOrigin) || o.StageOnly && o.Location == "" {
		return m, errors.New("choose the original Local or Remote profile")
	}
	coreBytes, e := source(o.Core)
	if e != nil {
		return m, e
	}
	signerBytes, e := source(o.Signer)
	if e != nil {
		return m, e
	}
	probe := "fixture-version"
	if o.Network != "" {
		probe = "custody-version"
	}
	if versionNumber == 3 {
		probe = "ordinary-custody-version"
	}
	version, e := exec.CommandContext(ctx, o.Signer, probe).Output()
	if e != nil {
		return m, errors.New("signer fixture version probe failed")
	}
	var protocol struct {
		Profile      string `json:"profile,omitempty"`
		Provenance   string `json:"provenance,omitempty"`
		Protocol     int    `json:"protocol"`
		OwnerFlow    int    `json:"ownerFlow"`
		Mode         string `json:"mode"`
		WalletSigner bool   `json:"walletSigner"`
	}
	if strict(version, &protocol) != nil || protocol.Protocol != versionNumber || protocol.OwnerFlow != 1 || protocol.Mode != mode || protocol.Profile != o.Profile || protocol.Provenance != o.Provenance || protocol.WalletSigner {
		return m, errors.New("incompatible fixture signer")
	}
	jdir, e := ownerJournal()
	if e != nil {
		return m, e
	}
	lock, e := core.LockInstance(jdir + "/install.lock")
	if e != nil {
		return m, e
	}
	defer lock.Close()
	uid := os.Getuid()
	j := journal{Version: versionNumber, Network: o.Network, Owner: uid, CoreHash: sum(coreBytes), SignerHash: sum(signerBytes), Listen: o.Listen, Enrollment: o.StageOnly, Location: o.Location, PublicOrigin: o.PublicOrigin}
	if versionNumber == 3 {
		j.Mode = mode
		j.Profile = o.Profile
		j.Provenance = o.Provenance
	}
	haveIntent := false
	existing, loadErr := Load(uid)
	if loadErr == nil {
		if e = noActiveController(existing); e != nil {
			return m, e
		}
	}
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return m, loadErr
	}
	if loadErr != nil {
		if _, e := os.Lstat(Root(uid)); e == nil || !os.IsNotExist(e) {
			return m, errors.New("unclassified installation root exists; preserve it instead of adopting it")
		}
	}
	if b, re := core.ReadPrivateFile(jdir + "/intent.json"); re == nil {
		var old journal
		if strict(b, &old) != nil || old.validate(uid) != nil || old.CoreHash != j.CoreHash || old.SignerHash != j.SignerHash || old.Listen != j.Listen || old.Network != j.Network || old.Version != j.Version || old.Mode != j.Mode || old.Profile != j.Profile || old.Provenance != j.Provenance || old.Enrollment != j.Enrollment || old.Location != j.Location || old.PublicOrigin != j.PublicOrigin {
			return m, errors.New("installer intent conflicts with requested inputs; preserving it")
		}
		j = old
		haveIntent = true
	} else if !os.IsNotExist(re) {
		return m, re
	}
	if loadErr == nil {
		if existing.CoreHash != j.CoreHash || existing.SignerHash != j.SignerHash || existing.Listen != j.Listen || existing.Network != j.Network || existing.Version != j.Version || existing.Mode != j.mode() || existing.Profile != j.Profile || existing.Provenance != j.Provenance || existing.Enrollment != j.Enrollment || existing.Location != j.Location || existing.PublicOrigin != j.PublicOrigin || (j.ID != "" && existing.ID != j.ID) {
			return m, errors.New("existing installation differs; automatic replacement/update is unavailable")
		}
		j.ID = existing.ID
		m = existing
	}
	if j.ID == "" {
		id := make([]byte, 16)
		if _, e = rand.Read(id); e != nil {
			return m, e
		}
		j.ID = hex.EncodeToString(id)
	}
	if !haveIntent {
		if e = core.WritePrivateJSON(jdir+"/intent.json", j); e != nil {
			return m, e
		}
	}
	root := Root(uid)
	stage := filepath.Join(filepath.Dir(root), ".fased-custody-"+strconv.Itoa(uid)+"-"+j.ID+".pending")
	if loadErr != nil {
		if e = trustedParents(filepath.Dir(root)); e != nil {
			return m, e
		}
		// A unique durable intent binds this previously absent staging directory.
		if e = directory(ctx, stage, 0, 0, 0755, s); e != nil {
			return m, e
		}
		intent, _ := json.Marshal(j)
		if e = publish(ctx, stage+"/intent.json", j.ID, intent, 0644, readRoot, s.Run); e != nil {
			return m, e
		}
		app, e := account(ctx, accountName(uid, "app"), j.ID, "app", s)
		if e != nil {
			return m, e
		}
		key, e := account(ctx, accountName(uid, "signer"), j.ID, "signer", s)
		if e != nil {
			return m, e
		}
		m = Manifest{Version: versionNumber, Mode: mode, Profile: o.Profile, Provenance: o.Provenance, Network: o.Network, ID: j.ID, Owner: uid, Root: root, Listen: o.Listen, CoreHash: j.CoreHash, SignerHash: j.SignerHash, App: app, Signer: key, Enrollment: j.Enrollment, Location: j.Location, PublicOrigin: j.PublicOrigin}
		if e = m.validate(); e != nil {
			return m, e
		}
		for _, d := range []struct {
			name string
			i    Identity
			mode os.FileMode
		}{{"app-state", app, 0700}, {"signer-state", key, 0700}, {"signer-ipc", key, 0755}} {
			if e = directory(ctx, stage+"/"+d.name, d.i.UID, d.i.GID, d.mode, s); e != nil {
				return m, e
			}
		}
		files := definitions(m)
		if _, pe := os.Lstat(stage + "/app-readonly-probe"); os.IsNotExist(pe) {
			if _, e = s.Run(ctx, nil, "/usr/bin/install", "-m", "0600", "-o", strconv.Itoa(app.UID), "-g", strconv.Itoa(app.GID), "--", "/dev/null", stage+"/app-readonly-probe"); e != nil {
				return m, e
			}
		} else if pe != nil {
			return m, pe
		}
		if e = verifyProbe(stage+"/app-readonly-probe", app); e != nil {
			return m, e
		}
		cfg, _ := json.Marshal(identityConfig(m))
		manifest, _ := json.Marshal(m)
		files["identity.json"] = cfg
		files["install.json"] = manifest
		files["fased-app"] = coreBytes
		files["fased-signer"] = signerBytes
		for _, name := range []string{"fased-app", "fased-signer", "identity.json", "fased-app.service", "fased-signer.service", "install.json"} {
			mode := os.FileMode(0644)
			if name == "fased-app" || name == "fased-signer" {
				mode = 0755
			}
			if e = publish(ctx, stage+"/"+name, j.ID, files[name], mode, readRoot, s.Run); e != nil {
				return m, e
			}
		}
		if _, e = s.Run(ctx, nil, "/usr/bin/sync", "-f", stage); e != nil {
			return m, e
		}
		if _, e = s.Run(ctx, nil, "/usr/bin/mv", "--no-clobber", "--no-target-directory", "--", stage, root); e != nil {
			return m, e
		}
		if _, e = s.Run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(root)); e != nil {
			return m, e
		}
		published, pe := Load(uid)
		if pe != nil || published != m {
			return m, errors.New("published root differs; foreign installation preserved")
		}
	}
	if e = Verify(m); e != nil {
		return m, e
	}
	if o.StageOnly {
		return m, nil
	}
	for _, role := range []string{"signer", "app"} {
		name := "fased-" + role + ".service"
		if e = publish(ctx, "/etc/systemd/system/"+Unit(uid, role), j.ID, definitions(m)[name], 0644, readRoot, s.Run); e != nil {
			return m, e
		}
	}
	if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return m, e
	}
	for _, role := range []string{"signer", "app"} {
		if e = loadedUnit(m, role); e != nil {
			return m, e
		}
	}
	for _, role := range []string{"signer", "app"} {
		if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(uid, role)); e != nil {
			return m, e
		}
	}
	if e = waitReady(ctx, m, s); e != nil {
		return m, e
	}
	ready, _ := json.Marshal(map[string]any{"schema": 1, "manifest": sum(mustJSON(m)), "fixtureOnly": true})
	if e = publish(ctx, root+"/ready.json", j.ID, ready, 0644, readRoot, s.Run); e != nil {
		return m, e
	}
	return m, nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func Verify(m Manifest) error {
	if e := verifyTopology(m); e != nil {
		return e
	}
	for _, pair := range []struct{ name, hash string }{{"fased-app", m.CoreHash}, {"fased-signer", m.SignerHash}} {
		f, e := readRoot(m.Root + "/" + pair.name)
		if e != nil {
			return e
		}
		if sum(f.Data) != pair.hash || f.Mode != 0755 {
			return errors.New("installed executable changed")
		}
	}
	return nil
}
func verifyTopology(m Manifest) error {
	if e := m.validate(); e != nil {
		return e
	}
	if e := verifyProbe(m.Root+"/app-readonly-probe", m.App); e != nil {
		return e
	}
	for _, pair := range []struct {
		i    Identity
		role string
	}{{m.App, "app"}, {m.Signer, "signer"}} {
		b, e := exec.Command("/usr/bin/getent", "passwd", pair.i.Name).Output()
		if e != nil {
			return e
		}
		actual, e := parseAccount(string(b), pair.i.Name, marker(m.ID, pair.role))
		if e != nil || actual != pair.i {
			return errors.New("installed account identity changed; preserving state")
		}
	}
	for _, d := range []struct {
		name string
		i    Identity
		mode os.FileMode
	}{{"app-state", m.App, 0700}, {"signer-state", m.Signer, 0700}, {"signer-ipc", m.Signer, 0755}} {
		i, e := os.Lstat(m.Root + "/" + d.name)
		if e != nil {
			return e
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || int(st.Uid) != d.i.UID || int(st.Gid) != d.i.GID || i.Mode().Perm() != d.mode {
			return errors.New("managed state ownership changed; preserving it")
		}
	}
	cfg, e := signer.LoadConfig(m.Root + "/identity.json")
	if e != nil {
		return e
	}
	if cfg != identityConfig(m) {
		return errors.New("installed signer configuration changed")
	}
	for name, b := range definitions(m) {
		f, e := readRoot(m.Root + "/" + name)
		if e != nil || !known(f, b, 0644) {
			return errors.New("stored unit definition changed")
		}
	}
	return nil
}

func verifyProbe(path string, app Identity) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.Mode().IsRegular() || int(st.Uid) != app.UID || int(st.Gid) != app.GID || i.Mode().Perm() != 0600 {
		return errors.New("foreign read-only marker; preserving it")
	}
	return nil
}

func waitReady(ctx context.Context, m Manifest, system *System) error {
	until := time.NewTimer(15 * time.Second)
	defer until.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}}
	defer client.CloseIdleConnections()
	for {
		if e := Runtime(m); e == nil {
			// Query private IPC under the app UID, then bind the browser response
			// to that instance. An unrelated loopback listener is not readiness.
			args := appArgs(m, "status")
			local, le := system.Run(ctx, nil, args...)
			var private core.Health
			if le != nil || json.Unmarshal(local, &private) != nil || private.InstanceID == "" {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-until.C:
					return errors.New("managed private IPC did not become ready; preserve state and retry")
				case <-ticker.C:
					continue
				}
			}
			r, e := client.Get("http://" + m.Listen + "/api/v1/health")
			if e == nil {
				var h core.Health
				de := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&h)
				r.Body.Close()
				if de == nil && r.StatusCode == 200 && h.Status == "ok" && h.InstanceID == private.InstanceID && h.APIVersion == core.APIVersion && len(h.Capabilities) > 0 && !h.Capabilities["walletSigner"] && !h.Capabilities["financialExecution"] && !h.Capabilities["updater"] {
					// Type=simple/active precedes socket readiness. Authenticate the
					// signer's actual owner RPC before declaring both services ready.
					b, se := exec.CommandContext(ctx, m.Root+"/fased-signer", "owner", "--config", m.Root+"/identity.json", signerModeFlag(m)).Output()
					var status signer.Status
					if se == nil && json.Unmarshal(b, &status) == nil && signerStatusReady(m, status) {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-until.C:
			return errors.New("managed services did not become safely ready; state preserved for same-command retry")
		case <-ticker.C:
		}
	}
}
func Runtime(m Manifest) error {
	for _, role := range []string{"signer", "app"} {
		if e := loadedUnit(m, role); e != nil {
			return e
		}
		b, e := exec.Command("/usr/bin/systemctl", "show", Unit(m.Owner, role), "--property=ActiveState,MainPID,FragmentPath").Output()
		if e != nil {
			return e
		}
		fields := map[string]string{}
		for _, l := range strings.Split(string(b), "\n") {
			p := strings.SplitN(l, "=", 2)
			if len(p) == 2 {
				fields[p[0]] = p[1]
			}
		}
		pid, e := strconv.Atoi(fields["MainPID"])
		if e != nil || pid <= 0 || fields["ActiveState"] != "active" || fields["FragmentPath"] != "/etc/systemd/system/"+Unit(m.Owner, role) {
			return errors.New("managed service is inactive or belongs to another definition")
		}
		i := m.App
		if role == "signer" {
			i = m.Signer
		}
		status, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if e != nil {
			return e
		}
		uidLine := "Uid:\t" + strings.Repeat(strconv.Itoa(i.UID)+"\t", 3) + strconv.Itoa(i.UID)
		if !bytes.Contains(status, []byte(uidLine+"\n")) || !bytes.Contains(status, []byte("NoNewPrivs:\t1\n")) {
			return errors.New("managed process UID or NoNewPrivs was not enforced")
		}
	}
	return nil
}

func loadedUnit(m Manifest, role string) error {
	return loadedUnitPolicy(m, role, definitions(m)["fased-"+role+".service"], m.Root+"/fased-"+role)
}
func loadedUnitPolicy(m Manifest, role string, wanted []byte, image string) error {
	f, e := readRoot("/etc/systemd/system/" + Unit(m.Owner, role))
	if e != nil {
		return e
	}
	if !known(f, wanted, 0644) {
		return errors.New("installed system unit changed; preserving it")
	}
	b, e := exec.Command("/usr/bin/systemctl", "show", Unit(m.Owner, role), "--property=FragmentPath,DropInPaths,User,Group,NoNewPrivileges,ProtectSystem,ExecStart").Output()
	if e != nil {
		return e
	}
	i := m.App
	if role == "signer" {
		i = m.Signer
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.SplitN(line, "=", 2)
		if len(p) == 2 {
			fields[p[0]] = p[1]
		}
	}
	if fields["FragmentPath"] != "/etc/systemd/system/"+Unit(m.Owner, role) || fields["DropInPaths"] != "" || fields["User"] != strconv.Itoa(i.UID) || fields["Group"] != strconv.Itoa(i.GID) || fields["NoNewPrivileges"] != "yes" || fields["ProtectSystem"] != "strict" || !strings.Contains(fields["ExecStart"], "path="+image+" ;") {
		return errors.New("unexpected systemd identity/override; refusing project code under a different policy")
	}
	return nil
}

func Control(ctx context.Context, m Manifest, role, action string, s *System) error {
	if m.Enrollment && action == "start" {
		if _, e := ReadEnrollmentCommit(m); e != nil {
			return e
		}
	}
	if e := noActiveController(m); e != nil {
		return e
	}
	if (role != "app" && role != "signer") || (action != "stop" && action != "start") || m.Owner != os.Getuid() {
		return errors.New("unsupported owner service operation")
	}
	if e := Verify(m); e != nil {
		return e
	}
	if e := loadedUnit(m, role); e != nil {
		return e
	}
	_, e := s.Run(ctx, nil, "/usr/bin/systemctl", action, "--", Unit(m.Owner, role))
	return e
}

// Remove stops verified units and removes their definitions, preserving the
// root inventory, disabled identities and ALL app/signer/recovery data.
func Remove(ctx context.Context, m Manifest, s *System) error {
	if e := noActiveController(m); e != nil {
		return e
	}
	if m.Owner != os.Getuid() || os.Geteuid() != os.Getuid() || os.Getuid() <= 0 {
		return errors.New("ordinary installation owner required")
	}
	jdir, e := ownerJournal()
	if e != nil {
		return e
	}
	lock, e := core.LockInstance(jdir + "/install.lock")
	if e != nil {
		return e
	}
	defer lock.Close()
	if e := Verify(m); e != nil {
		return e
	}
	for _, role := range []string{"app", "signer"} {
		path := "/etc/systemd/system/" + Unit(m.Owner, role)
		f, e := readRoot(path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && !known(f, definitions(m)["fased-"+role+".service"], 0644) {
			return errors.New("foreign installed unit; removal refused")
		}
		if e == nil {
			if e = loadedUnit(m, role); e != nil {
				return e
			}
		}
	}
	for _, role := range []string{"app", "signer"} {
		path := "/etc/systemd/system/" + Unit(m.Owner, role)
		if _, e := os.Lstat(path); os.IsNotExist(e) {
			continue
		}
		if _, e := s.Run(ctx, nil, "/usr/bin/systemctl", "disable", "--now", "--", Unit(m.Owner, role)); e != nil {
			return e
		}
		if _, e := s.Run(ctx, nil, "/usr/bin/rm", "--", path); e != nil {
			return e
		}
	}
	_, e = s.Run(ctx, nil, "/usr/bin/systemctl", "daemon-reload")
	return e
}
