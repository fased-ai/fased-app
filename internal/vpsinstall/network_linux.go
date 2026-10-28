//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

type networkController struct {
	ops   hostOps
	now   func() time.Time
	proof func(context.Context, Manifest) (networkAccess, error)
}

func controller(s *custodyinstall.System) (networkController, error) {
	if s == nil {
		return networkController{}, errors.New("ordinary owner OS authorization required")
	}
	if s.Authorization == "" {
		s.Authorization = "Authorize explicit Remote network lockdown: an owned nftables input table and SSH key-only policy, protected by a five-minute rollback. Private UI and independent console recovery must already work. Foreign host policies are preserved."
	}
	return networkController{ops: liveOps(s), now: time.Now, proof: networkProof}, nil
}
func BeginNetwork(ctx context.Context, m Manifest, s *custodyinstall.System, a NetworkApproval) (NetworkStatus, error) {
	n, e := controller(s)
	if e != nil {
		return NetworkStatus{}, e
	}
	return n.begin(ctx, m, a)
}
func ConfirmNetwork(ctx context.Context, m Manifest, s *custodyinstall.System, id string) (NetworkStatus, error) {
	n, e := controller(s)
	if e != nil {
		return NetworkStatus{}, e
	}
	return n.confirm(ctx, m, id)
}
func RollbackNetwork(ctx context.Context, m Manifest, s *custodyinstall.System) (NetworkStatus, error) {
	n, e := controller(s)
	if e != nil {
		return NetworkStatus{}, e
	}
	return n.rollback(ctx, m)
}
func InspectNetwork(ctx context.Context, m Manifest, s *custodyinstall.System) (NetworkStatus, error) {
	n, e := controller(s)
	if e != nil {
		return NetworkStatus{}, e
	}
	return n.inspect(ctx, m)
}
func (n networkController) guard(ctx context.Context, m Manifest) error {
	if e := verify(ctx, n.ops, m); e != nil {
		return e
	}
	release, e := osRelease(n.ops)
	if e != nil {
		return e
	}
	f := fields(release.Data)
	if strings.Trim(f["ID"], "\"'") != "ubuntu" || strings.Trim(f["VERSION_ID"], "\"'") != "24.04" {
		return errors.New("managed network policy requires Ubuntu 24.04")
	}
	return nil
}
func (n networkController) withLock(ctx context.Context, m Manifest, f func() (NetworkStatus, error)) (NetworkStatus, error) {
	if e := ownedBy(n.ops, m); e != nil {
		return NetworkStatus{}, e
	}
	l, e := n.ops.lock()
	if e != nil {
		return NetworkStatus{}, e
	}
	defer l.Close()
	if e = n.guard(ctx, m); e != nil {
		return NetworkStatus{}, e
	}
	return f()
}
func (n networkController) validate(m Manifest, a networkAttempt) error {
	if a.Version != 1 || !canonicalHex(a.ID, 16) || a.ManifestSHA256 != hash(encoded(m)) || a.StartedAt.Unix() <= 0 || !a.Deadline.Equal(a.StartedAt.Add(5*time.Minute)) || a.Access.SSHPort != 22 || a.Access.SessionID == "" || len(a.Access.SessionID) > 512 || strings.ContainsAny(a.Access.SessionID, "\r\n\x00") {
		return errors.New("invalid immutable network attempt")
	}
	return nil
}
func (n networkController) load(m Manifest) (networkAttempt, error) {
	var a networkAttempt
	var p networkPointer
	b, e := n.ops.readJournal("network.json")
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			if _, re := n.ops.read(networkRoot(m)); re == nil || !errors.Is(re, os.ErrNotExist) {
				return a, fmt.Errorf("owner network pointer missing for retained inventory %s; console inspection required", networkRoot(m))
			}
		}
		return a, e
	}

	if strict(b, &p) != nil || p.Version != 1 || p.ManifestSHA256 != hash(encoded(m)) || !canonicalHex(p.AttemptID, 16) || p.Attempt.ID != p.AttemptID || n.validate(m, p.Attempt) != nil {
		return a, errors.New("invalid owner network pointer")
	}
	a = p.Attempt
	f, e := rootFile(n.ops, networkAttemptRoot(m, p.AttemptID)+"/attempt.json")
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			names, be := n.ops.list(networkRoot(m))
			if be != nil && !errors.Is(be, os.ErrNotExist) {
				return a, be
			}
			for _, name := range names {
				if strings.HasPrefix(name, "attempt-") && name != "attempt-"+a.ID {
					return a, errors.New("missing attempt record alongside retained network history; preserve policies and inspect via console")
				}
			}
			names, de := n.ops.list(networkAttemptRoot(m, a.ID))
			if de != nil && !errors.Is(de, os.ErrNotExist) {
				return a, de
			}
			for _, name := range names {
				if name != "private" && name != ".attempt.json-"+a.ID+".pending" {
					return a, errors.New("network attempt record disappeared after preparation; preserve policy and use console recovery")
				}
			}
		}
		return a, e
	}

	if strict(f.Data, &a) != nil || n.validate(m, a) != nil || a.ID != p.AttemptID || a != p.Attempt || !known(f, encoded(a), 0644) {
		return a, errors.New("network attempt differs from immutable owner binding")
	}
	return a, nil
}
func (n networkController) savePointer(m Manifest, a networkAttempt) error {
	return n.ops.writeJournal("network.json", networkPointer{Version: 1, AttemptID: a.ID, ManifestSHA256: hash(encoded(m)), Attempt: a})
}
func binding(m Manifest) []byte {
	return encoded(struct {
		Version, OwnerUID              int
		InstallationID, ManifestSHA256 string
	}{1, m.OwnerUID, m.ID, hash(encoded(m))})
}
func (n networkController) prepare(ctx context.Context, m Manifest, a networkAttempt) error {
	o := n.ops
	base := networkRoot(m)
	dir := networkAttemptRoot(m, a.ID)
	if e := parents(o, filepathParent(base)); e != nil {
		return e
	}
	if e := directory(ctx, o, base, 0, 0, 0755); e != nil {
		return e
	}
	if e := publish(ctx, o, base+"/binding.json", m.ID, binding(m), 0644); e != nil {
		return e
	}
	if e := directory(ctx, o, dir, 0, 0, 0755); e != nil {
		return e
	}
	if e := directory(ctx, o, dir+"/private", 0, 0, 0700); e != nil {
		return e
	}
	fs := networkStaticFiles(m, a)
	names := make([]string, 0, len(fs))
	for name := range fs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if e := publish(ctx, o, dir+"/"+name, a.ID, fs[name], 0644); e != nil {
			return e
		}
	}
	if e := publish(ctx, o, networkSSHStage(m, a), a.ID, networkSSHBytes(m, a), 0644); e != nil {
		return e
	}
	for _, role := range networkRoles() {
		name := networkUnit(m, a.ID, role)
		if e := publish(ctx, o, "/etc/systemd/system/"+name, a.ID, fs[name], 0644); e != nil {
			return e
		}
	}
	if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return e
	}
	return n.verifyUnits(ctx, m, a)
}
func filepathParent(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i <= 0 {
		return "/"
	}
	return p[:i]
}
func (n networkController) verifyStatic(m Manifest, a networkAttempt) error {
	o := n.ops
	f, e := rootFile(o, networkRoot(m)+"/binding.json")
	if e != nil || !known(f, binding(m), 0644) {
		return errors.New("network root binding is foreign or changed")
	}
	for name, b := range networkStaticFiles(m, a) {
		f, e := rootFile(o, networkAttemptRoot(m, a.ID)+"/"+name)
		if e != nil || !known(f, b, 0644) {
			return errors.New("immutable network input changed; preserve it")
		}
	}
	f, e = rootFile(o, networkSSHStage(m, a))
	if e != nil || !known(f, networkSSHBytes(m, a), 0644) {
		return errors.New("owned same-directory SSH stage changed")
	}
	return nil
}
func (n networkController) properties(ctx context.Context, m Manifest, a networkAttempt, role string) (map[string]string, error) {
	b, e := n.ops.command(ctx, "/usr/bin/systemctl", "show", networkUnit(m, a.ID, role), "--property=LoadState,FragmentPath,DropInPaths,User,Group,ActiveState,UnitFileState")
	return fields(b), e
}
func (n networkController) verifyUnits(ctx context.Context, m Manifest, a networkAttempt) error {
	if e := n.verifyStatic(m, a); e != nil {
		return e
	}
	for role, b := range networkUnits(m, a) {
		name := networkUnit(m, a.ID, role)
		f, e := rootFile(n.ops, "/etc/systemd/system/"+name)
		if e != nil || !known(f, b, 0644) {
			return errors.New("owned network system unit changed")
		}
		p, e := n.properties(ctx, m, a, role)
		if e != nil {
			return e
		}
		if p["LoadState"] != "loaded" || p["FragmentPath"] != "/etc/systemd/system/"+name || p["DropInPaths"] != "" || (!strings.HasSuffix(role, ".timer") && (p["User"] != "0" || p["Group"] != "0")) {
			return errors.New("foreign loaded network unit or override; preserve it")
		}
	}
	return nil
}
func (n networkController) runUnit(ctx context.Context, m Manifest, a networkAttempt, action, role string) error {
	_, e := n.ops.run(ctx, nil, "/usr/bin/systemctl", action, "--", networkUnit(m, a.ID, role))
	return e
}
func (n networkController) enable(ctx context.Context, m Manifest, a networkAttempt, role string) error {
	copy := m
	copy.Unit = networkUnit(m, a.ID, role)
	copy.UnitPath = "/etc/systemd/system/" + copy.Unit
	if e := enableLink(n.ops, copy); e != nil {
		return e
	}
	if role == "persist" {
		if e := n.persistRequirements(m, a, false); e != nil {
			return e
		}
	}
	if e := n.runUnit(ctx, m, a, "enable", role); e != nil {
		return e
	}
	if role == "persist" {
		return n.persistRequirements(m, a, true)
	}
	return nil
}
func (n networkController) arm(ctx context.Context, m Manifest, a networkAttempt) error {
	if e := n.enable(ctx, m, a, "rollback"); e != nil {
		return e
	}
	p, e := n.properties(ctx, m, a, "rollback.timer")
	if e != nil {
		return e
	}
	if p["ActiveState"] != "active" {
		if e = n.runUnit(ctx, m, a, "start", "rollback.timer"); e != nil {
			return e
		}
	}
	p, e = n.properties(ctx, m, a, "rollback.timer")
	if e != nil || p["ActiveState"] != "active" {
		return errors.New("rollback timer is not armed; network unchanged")
	}
	p, e = n.properties(ctx, m, a, "rollback")
	if e != nil || p["UnitFileState"] != "enabled" {
		return errors.New("pending-on-boot recovery is not enabled; network unchanged")
	}
	return nil
}
func (n networkController) table(ctx context.Context, m Manifest, a networkAttempt) ([]byte, bool, error) {
	list, e := n.ops.run(ctx, nil, "/usr/sbin/nft", "list", "tables")
	if e != nil {
		return nil, false, e
	}
	found := false
	for _, line := range strings.Split(string(list), "\n") {
		if strings.TrimSpace(line) == "table inet "+networkTable(m, a) {
			found = true
		}
	}
	if !found {
		return nil, false, nil
	}
	b, e := n.ops.run(ctx, nil, "/usr/sbin/nft", "--stateless", "list", "table", "inet", networkTable(m, a))
	return b, true, e
}
func (n networkController) outcome(m Manifest, a networkAttempt) (string, error) {
	f, e := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/outcome")
	if errors.Is(e, os.ErrNotExist) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	for _, p := range []string{"commit", "rollback"} {
		if known(f, networkToken(a, p), 0644) {
			return p, nil
		}
	}
	return "", errors.New("foreign network outcome; preserve it")
}
func (n networkController) sshPolicy(ctx context.Context, m Manifest, a networkAttempt) error {
	if e := n.checkSSHProfile(ctx); e != nil {
		return e
	}
	if _, e := n.ops.run(ctx, nil, "/usr/sbin/sshd", "-t"); e != nil {
		return e
	}
	owner, e := n.ops.command(ctx, "/usr/bin/getent", "passwd", fmt.Sprint(m.OwnerUID))
	if e != nil {
		return e
	}
	parts := strings.Split(strings.TrimSpace(string(owner)), ":")
	if len(parts) != 7 || parts[2] != fmt.Sprint(m.OwnerUID) || parts[0] == "" || strings.ContainsAny(parts[0], ",= \r\n") {
		return errors.New("cannot bind effective SSH policy to the OS owner")
	}
	for _, user := range []string{parts[0], "root"} {
		connect := "user=" + user + ",addr=" + a.Access.PeerIP + ",laddr=" + a.Access.ServerIP + ",lport=22"
		b, e := n.ops.run(ctx, nil, "/usr/sbin/sshd", "-T", "-C", connect)
		if e != nil {
			return e
		}
		values := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 {
				values[f[0]] = f[1]
			}
		}
		for _, key := range []string{"permitrootlogin", "passwordauthentication", "kbdinteractiveauthentication"} {
			if values[key] != "no" {
				return errors.New("sshd effective owner/root policy does not enforce the reviewed key-only restrictions")
			}
		}
	}

	return nil
}
func (n networkController) applied(ctx context.Context, m Manifest, a networkAttempt) error {
	kernel, e := n.ops.run(ctx, nil, "/usr/sbin/nft", "--json", "list", "table", "inet", networkTable(m, a))
	if e != nil {
		return e
	}
	if e = verifyNetworkTableJSON(kernel, m, a); e != nil {
		return e
	}
	f, e := rootFile(n.ops, networkSSH(m, a))
	if e != nil || !known(f, networkSSHBytes(m, a), 0644) {
		return errors.New("owned SSH drop-in changed or is absent")
	}
	actual, present, e := n.table(ctx, m, a)
	if e != nil {
		return e
	}
	expected, e := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/expected.nft")
	if e != nil || !present || expected.Mode.Perm() != 0644 || len(expected.Data) == 0 || !bytes.Equal(actual, expected.Data) {
		return errors.New("owned firewall differs from the immutable applied snapshot; console inspection is required")
	}
	return n.sshPolicy(ctx, m, a)
}
func (n networkController) state(ctx context.Context, m Manifest, a networkAttempt) (NetworkStatus, error) {
	status := NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}
	if e := n.verifyUnits(ctx, m, a); e != nil {
		return status, e
	}
	outcome, e := n.outcome(m, a)
	if e != nil {
		return status, e
	}
	if outcome == "commit" {
		if e = n.persistRequirements(m, a, true); e != nil {
			return status, e
		}
		p, pe := n.properties(ctx, m, a, "persist")
		if pe != nil || p["UnitFileState"] != "enabled" {
			return status, errors.New("committed firewall persistence is disabled; console recovery required")
		}
		if e = n.applied(ctx, m, a); e != nil {
			return status, e
		}
		status.Phase = "committed"
		return status, nil
	}
	if outcome == "rollback" {
		_, present, e := n.table(ctx, m, a)
		if e != nil {
			return status, e
		}
		_, sshErr := n.ops.read(networkSSH(m, a))
		sshDone, de := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/ssh.done")
		if present || !errors.Is(sshErr, os.ErrNotExist) || de != nil || !known(sshDone, nil, 0644) {
			return status, errors.New("rollback is partial: foreign policy is preserved; use the VPS console to inspect remaining owned firewall/SSH policy")
		}
		status.Phase = "rolled-back"
		return status, nil
	}
	if e = n.applied(ctx, m, a); e != nil {
		return status, e
	}
	timer, te := n.properties(ctx, m, a, "rollback.timer")
	if te != nil || timer["ActiveState"] != "active" {
		return status, errors.New("pending rollback timer is inactive; console recovery required")
	}
	status.Phase = "pending"
	status.Deadline = a.Deadline
	if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline) {
		status.Phase = "recovery-required"
		return status, errors.New("network confirmation deadline or clock is invalid; retain console access and run network rollback")
	}
	return status, nil
}
func (n networkController) begin(ctx context.Context, m Manifest, approval NetworkApproval) (NetworkStatus, error) {
	if !approval.PrivateUI || !approval.ConsoleRecovery {
		return NetworkStatus{}, errors.New("explicit private UI and independent console recovery approval are required")
	}
	if m.PublicOrigin == "" {
		return NetworkStatus{}, errors.New("SSH-only instances cannot enable network lockdown; prepare Tailscale HTTPS first")
	}
	return n.withLock(ctx, m, func() (NetworkStatus, error) {
		proof, e := n.proof(ctx, m)
		if e != nil {
			return NetworkStatus{}, e
		}
		if proof.SessionID == "" || proof.SSHPort != 22 {
			return NetworkStatus{}, errors.New("verified private SSH connection required")
		}
		if e = n.checkSSHProfile(ctx); e != nil {
			return NetworkStatus{}, e
		}
		if _, e = n.ops.run(ctx, nil, "/usr/bin/flock", "--version"); e != nil {
			return NetworkStatus{}, fmt.Errorf("trusted OS flock is required for recoverable network operations: %w", e)
		}
		if _, e = n.ops.run(ctx, nil, "/usr/sbin/nft", "--version"); e != nil {
			return NetworkStatus{}, fmt.Errorf("install Ubuntu's nftables OS package before network lockdown; no package is installed automatically: %w", e)
		}
		a, le := n.load(m)
		resume := a.ID != ""
		if le != nil && !errors.Is(le, os.ErrNotExist) {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, le
		}
		if le == nil {
			outcome, oe := n.outcome(m, a)
			if oe != nil {
				return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, oe
			}
			if outcome == "commit" {
				return n.state(ctx, m, a)
			}
			if outcome == "rollback" {
				st, se := n.state(ctx, m, a)
				if se != nil || st.Phase != "rolled-back" {
					return st, se
				}
				resume = false
			}
		}
		if !resume {
			if f, re := n.ops.read(networkRoot(m)); re == nil {
				if !f.Directory {
					return NetworkStatus{}, errors.New("foreign network root")
				}
				bf, be := rootFile(n.ops, networkRoot(m)+"/binding.json")
				if be != nil || !known(bf, binding(m), 0644) {
					return NetworkStatus{}, errors.New("unclassified network root exists; preserve it")
				}
			} else if !errors.Is(re, os.ErrNotExist) {
				return NetworkStatus{}, re
			}
			id := make([]byte, 16)
			if _, e = rand.Read(id); e != nil {
				return NetworkStatus{}, e
			}
			now := n.now().UTC().Truncate(time.Second)
			a = networkAttempt{Version: 1, ID: hex.EncodeToString(id), ManifestSHA256: hash(encoded(m)), Access: proof, StartedAt: now, Deadline: now.Add(5 * time.Minute)}
		}

		if e = n.validate(m, a); e != nil {
			return NetworkStatus{}, e
		}
		if e = n.savePointer(m, a); e != nil {
			return NetworkStatus{}, e
		}
		if e = n.prepare(ctx, m, a); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
		if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline) {
			return n.rollbackAttempt(ctx, m, a)
		}
		actual, present, te := n.table(ctx, m, a)
		if te != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, te
		}
		if present {
			expected, re := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/expected.nft")
			if !resume || re != nil || len(expected.Data) == 0 || !bytes.Equal(actual, expected.Data) {
				return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, errors.New("owned-name table exists without matching completed apply snapshot; console inspection required")
			}
		}
		if existing, se := rootFile(n.ops, networkSSH(m, a)); se == nil {
			if !resume || !known(existing, networkSSHBytes(m, a), 0644) {
				return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, errors.New("preexisting or changed SSH drop-in; preserving it")
			}
		} else if !errors.Is(se, os.ErrNotExist) {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, se
		}
		if present {
			if _, se := n.ops.read(networkSSH(m, a)); errors.Is(se, os.ErrNotExist) {
				return n.rollbackAttempt(ctx, m, a)
			}
			timer, pe := n.properties(ctx, m, a, "rollback.timer")
			if pe != nil {
				return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, pe
			}
			if timer["ActiveState"] != "active" {
				return n.rollbackAttempt(ctx, m, a)
			}
		}

		if _, e = n.ops.run(ctx, nil, "/usr/sbin/sshd", "-t"); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
		if e = n.arm(ctx, m, a); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
		if !present {
			if e = n.runUnit(ctx, m, a, "start", "apply"); e != nil {
				return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
			}
		}
		if outcome, oe := n.outcome(m, a); oe != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, oe
		} else if outcome == "rollback" {
			return n.state(ctx, m, a)
		}

		if e = n.sshPolicy(ctx, m, a); e != nil {
			st, re := n.rollbackAttempt(ctx, m, a)
			return st, errors.Join(e, re)
		}
		if e = n.runUnit(ctx, m, a, "start", "ssh-hup"); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
		if e = n.applied(ctx, m, a); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
		if !n.now().Before(a.Deadline) || n.now().Before(a.StartedAt) {
			return n.rollbackAttempt(ctx, m, a)
		}
		return NetworkStatus{Phase: "pending", AttemptID: a.ID, Deadline: a.Deadline}, nil
	})
}
func (n networkController) confirm(ctx context.Context, m Manifest, id string) (NetworkStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return n.withLock(ctx, m, func() (NetworkStatus, error) {
		a, e := n.load(m)
		if e != nil {
			return NetworkStatus{}, e
		}
		if id != a.ID || !canonicalHex(id, 16) {
			return NetworkStatus{}, errors.New("exact pending network attempt required")
		}
		if e = n.verifyUnits(ctx, m, a); e != nil {
			return NetworkStatus{}, e
		}
		outcome, e := n.outcome(m, a)
		if e != nil {
			return NetworkStatus{}, e
		}
		if outcome == "rollback" {
			return n.state(ctx, m, a)
		}
		p, e := n.proof(ctx, m)
		if e != nil {
			return NetworkStatus{}, e
		}
		if p.SessionID == a.Access.SessionID || p.ServerIP != a.Access.ServerIP || p.SSHPort != a.Access.SSHPort {
			return NetworkStatus{}, errors.New("confirm from a different private SSH connection to this VPS")
		}
		if outcome == "commit" {
			return n.state(ctx, m, a)
		}
		if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline.Add(-30*time.Second)) {
			st, re := n.rollbackAttempt(ctx, m, a)
			return st, errors.Join(errors.New("confirmation needs at least 30 seconds before expiry; policy rolled back or requires console recovery"), re)
		}
		if e = n.applied(ctx, m, a); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: id}, e
		}
		if e = n.enable(ctx, m, a, "persist"); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: id}, e
		}
		dir := networkAttemptRoot(m, id)
		if _, e = n.ops.run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", dir+"/commit.token", dir+"/outcome"); e != nil {
			st, se := n.state(ctx, m, a)
			return st, errors.Join(e, se)
		}
		if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline) || n.applied(ctx, m, a) != nil {
			st, re := n.rollbackAttempt(ctx, m, a)
			return st, errors.Join(errors.New("policy/time changed during confirmation; recovery required"), re)
		}
		if _, e = n.ops.run(ctx, nil, "/usr/bin/sync", "-f", dir); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: id}, e
		}
		if e = n.runUnit(ctx, m, a, "stop", "rollback.timer"); e != nil {
			return NetworkStatus{Phase: "committed", AttemptID: id}, e
		}
		return n.state(ctx, m, a)
	})
}
func (n networkController) rollbackAttempt(ctx context.Context, m Manifest, a networkAttempt) (NetworkStatus, error) {
	if e := n.verifyUnits(ctx, m, a); e != nil {
		return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
	}
	outcome, e := n.outcome(m, a)
	if e != nil {
		return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
	}
	if e = n.removePersistRequirements(ctx, m, a); e != nil {
		return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
	}
	if outcome == "commit" {
		dir := networkAttemptRoot(m, a.ID)
		if _, e = n.ops.run(ctx, nil, "/usr/bin/cmp", "--silent", "--", dir+"/commit.token", dir+"/outcome"); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
		if _, e = n.ops.run(ctx, nil, "/usr/bin/rm", "--", dir+"/outcome"); e != nil {
			return NetworkStatus{Phase: "recovery-required", AttemptID: a.ID}, e
		}
	}
	runErr := n.runUnit(ctx, m, a, "start", "rollback")
	st, se := n.state(ctx, m, a)
	if st.Phase != "rolled-back" {
		return st, errors.Join(runErr, se, errors.New("rollback incomplete; foreign edits preserved, console recovery required"))
	}
	return st, runErr
}
func (n networkController) rollback(ctx context.Context, m Manifest) (NetworkStatus, error) {
	return n.withLock(ctx, m, func() (NetworkStatus, error) {
		a, e := n.load(m)
		if errors.Is(e, os.ErrNotExist) {
			return NetworkStatus{Phase: "none"}, nil
		}
		if e != nil {
			return NetworkStatus{Phase: "recovery-required"}, e
		}
		return n.rollbackAttempt(ctx, m, a)
	})
}
func (n networkController) inspect(ctx context.Context, m Manifest) (NetworkStatus, error) {
	return n.withLock(ctx, m, func() (NetworkStatus, error) {
		a, e := n.load(m)
		if errors.Is(e, os.ErrNotExist) {
			return NetworkStatus{Phase: "none"}, nil
		}
		if e != nil {
			return NetworkStatus{Phase: "recovery-required"}, e
		}
		return n.state(ctx, m, a)
	})
}
