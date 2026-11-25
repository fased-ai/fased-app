//go:build linux

package vpsnetwork

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostplatform"
)

type networkController struct {
	ops          hostOps
	now          func() time.Time
	proof        func(context.Context, Manifest) (networkAccess, error)
	packageCheck func(Manifest) error
	platform     func([]byte) (hostplatform.Platform, error)
}

func controller(s *custodyinstall.System) (networkController, error) {
	if s == nil {
		return networkController{}, errors.New("ordinary owner OS authorization required")
	}
	if s.Authorization == "" {
		s.Authorization = "Authorize explicit Remote network lockdown: an owned nftables input table and SSH key-only policy, protected by a five-minute rollback. Private UI and independent console recovery must already work. Foreign host policies are preserved."
	}
	return networkController{
		ops:          liveOps(s),
		now:          time.Now,
		proof:        func(ctx context.Context, m Manifest) (networkAccess, error) { return networkProofWithSystem(ctx, m, s) },
		packageCheck: func(m Manifest) error { _, err := verifyPackageInstallation(m); return err },
		platform: func(release []byte) (hostplatform.Platform, error) {
			return hostplatform.LookupRemote(string(release), runtime.GOARCH)
		},
	}, nil
}

// Begin starts or resumes a timed recoverable Remote network lockdown attempt.
func Begin(ctx context.Context, m Manifest, s *custodyinstall.System, approval Approval) (Status, error) {
	n, err := controller(s)
	if err != nil {
		return Status{}, err
	}
	return n.begin(ctx, m, approval)
}

// Confirm promotes a pending lockdown attempt from a second private SSH session.
func Confirm(ctx context.Context, m Manifest, s *custodyinstall.System, attemptID string) (Status, error) {
	n, err := controller(s)
	if err != nil {
		return Status{}, err
	}
	return n.confirm(ctx, m, attemptID)
}

// Rollback reverts owned SSH/firewall state and removes owned temporary systemd units.
func Rollback(ctx context.Context, m Manifest, s *custodyinstall.System) (Status, error) {
	n, err := controller(s)
	if err != nil {
		return Status{}, err
	}
	return n.rollback(ctx, m)
}

// Inspect verifies and reports the current Remote network lockdown state.
func Inspect(ctx context.Context, m Manifest, s *custodyinstall.System) (Status, error) {
	n, err := controller(s)
	if err != nil {
		return Status{}, err
	}
	return n.inspect(ctx, m)
}

func (n networkController) resolvePlatform(release []byte) (hostplatform.Platform, error) {
	if n.platform != nil {
		return n.platform(release)
	}
	return hostplatform.LookupRemote(string(release), runtime.GOARCH)
}

func (n networkController) guard(ctx context.Context, m Manifest) (hostplatform.Platform, error) {
	var zero hostplatform.Platform
	if err := ownedBy(n.ops, m); err != nil {
		return zero, err
	}
	if n.packageCheck != nil {
		if err := n.packageCheck(m); err != nil {
			return zero, err
		}
	} else if _, err := verifyPackageInstallation(m); err != nil {
		return zero, err
	}
	release, err := osRelease(n.ops)
	if err != nil {
		return zero, err
	}
	return n.resolvePlatform(release.Data)
}

func (n networkController) withLock(ctx context.Context, m Manifest, fn func(hostplatform.Platform) (Status, error)) (Status, error) {
	if err := ownedBy(n.ops, m); err != nil {
		return Status{}, err
	}
	lock, err := n.ops.lock()
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	plat, err := n.guard(ctx, m)
	if err != nil {
		return Status{}, err
	}
	return fn(plat)
}

func (n networkController) validate(m Manifest, a networkAttempt) error {
	if a.Version != 1 || !canonicalHex(a.ID, 16) || a.ManifestSHA256 != manifestDigest(m) ||
		a.StartedAt.Unix() <= 0 || !a.Deadline.Equal(a.StartedAt.Add(5*time.Minute)) ||
		a.Access.SSHPort != 22 || a.Access.SessionID == "" || len(a.Access.SessionID) > 512 ||
		strings.ContainsAny(a.Access.SessionID, "\r\n\x00") ||
		(a.SSHService != "" && a.SSHService != "ssh.service" && a.SSHService != "sshd.service") {
		return errors.New("invalid immutable network attempt")
	}
	return nil
}

func (n networkController) load(m Manifest) (networkAttempt, error) {
	var a networkAttempt
	var p networkPointer
	b, err := n.ops.readJournal("network.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, re := n.ops.read(networkRoot(m)); re == nil || !errors.Is(re, os.ErrNotExist) {
				return a, fmt.Errorf("owner network pointer missing for retained inventory %s; console inspection required", networkRoot(m))
			}
		}
		return a, err
	}
	if strict(b, &p) != nil || p.Version != 1 || p.ManifestSHA256 != manifestDigest(m) ||
		!canonicalHex(p.AttemptID, 16) || p.Attempt.ID != p.AttemptID || n.validate(m, p.Attempt) != nil {
		return a, errors.New("invalid owner network pointer")
	}
	a = p.Attempt
	f, err := rootFile(n.ops, networkAttemptRoot(m, p.AttemptID)+"/attempt.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
		return a, err
	}
	if strict(f.Data, &a) != nil || n.validate(m, a) != nil || a.ID != p.AttemptID || a != p.Attempt || !known(f, encoded(a), 0644) {
		return a, errors.New("network attempt differs from immutable owner binding")
	}
	return a, nil
}

func (n networkController) savePointer(m Manifest, a networkAttempt) error {
	return n.ops.writeJournal("network.json", networkPointer{
		Version:        1,
		AttemptID:      a.ID,
		ManifestSHA256: manifestDigest(m),
		Attempt:        a,
	})
}

func binding(m Manifest) []byte {
	return encoded(struct {
		Version, OwnerUID              int
		InstallationID, ManifestSHA256 string
	}{1, m.OwnerUID, m.ID, manifestDigest(m)})
}

func (n networkController) prepare(ctx context.Context, m Manifest, a networkAttempt) error {
	o := n.ops
	base := networkRoot(m)
	dir := networkAttemptRoot(m, a.ID)
	if err := parents(o, filepathParent(base)); err != nil {
		return err
	}
	if err := directory(ctx, o, base, 0, 0, 0755); err != nil {
		return err
	}
	if err := publish(ctx, o, base+"/binding.json", m.ID, binding(m), 0644); err != nil {
		return err
	}
	if err := directory(ctx, o, dir, 0, 0, 0755); err != nil {
		return err
	}
	if err := directory(ctx, o, dir+"/private", 0, 0, 0700); err != nil {
		return err
	}
	fs := networkStaticFiles(m, a)
	names := make([]string, 0, len(fs))
	for name := range fs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := publish(ctx, o, dir+"/"+name, a.ID, fs[name], 0644); err != nil {
			return err
		}
	}
	if err := publish(ctx, o, networkSSHStage(m, a), a.ID, networkSSHBytes(m, a), 0644); err != nil {
		return err
	}
	for _, role := range networkRoles() {
		name := networkUnit(m, a.ID, role)
		if err := publish(ctx, o, "/etc/systemd/system/"+name, a.ID, fs[name], 0644); err != nil {
			return err
		}
	}
	if _, err := o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	return n.verifyUnits(ctx, m, a)
}

func (n networkController) verifyStatic(ctx context.Context, m Manifest, a networkAttempt) error {
	o := n.ops
	f, err := rootFile(o, networkRoot(m)+"/binding.json")
	if err != nil || !known(f, binding(m), 0644) {
		return errors.New("network root binding is foreign or changed")
	}
	for name, b := range networkStaticFiles(m, a) {
		f, err := rootFile(o, networkAttemptRoot(m, a.ID)+"/"+name)
		if err != nil || !known(f, b, 0644) {
			return errors.New("immutable network input changed; preserve it")
		}
	}
	f, err = readRootFileCtx(ctx, o, networkSSHStage(m, a))
	if err != nil || !known(f, networkSSHBytes(m, a), 0644) {
		return errors.New("owned same-directory SSH stage changed")
	}
	return nil
}

func (n networkController) properties(ctx context.Context, m Manifest, a networkAttempt, role string) (map[string]string, error) {
	b, err := n.ops.command(ctx, "/usr/bin/systemctl", "show", networkUnit(m, a.ID, role), "--property=LoadState,FragmentPath,DropInPaths,User,Group,ActiveState,UnitFileState")
	return fields(b), err
}

func (n networkController) verifyUnits(ctx context.Context, m Manifest, a networkAttempt) error {
	if err := n.verifyStatic(ctx, m, a); err != nil {
		return err
	}
	for role, b := range networkUnits(m, a) {
		name := networkUnit(m, a.ID, role)
		f, err := rootFile(n.ops, "/etc/systemd/system/"+name)
		if err != nil || !known(f, b, 0644) {
			return errors.New("owned network system unit changed")
		}
		p, err := n.properties(ctx, m, a, role)
		if err != nil {
			return err
		}
		if p["LoadState"] != "loaded" || p["FragmentPath"] != "/etc/systemd/system/"+name ||
			!allowedVendorDropIns(n.ops, p["DropInPaths"]) ||
			(!strings.HasSuffix(role, ".timer") && (p["User"] != "0" || p["Group"] != "0")) {
			return errors.New("foreign loaded network unit or override; preserve it")
		}
	}
	return nil
}

func (n networkController) runUnit(ctx context.Context, m Manifest, a networkAttempt, action, role string) error {
	_, err := n.ops.run(ctx, nil, "/usr/bin/systemctl", action, "--", networkUnit(m, a.ID, role))
	return err
}

func (n networkController) enable(ctx context.Context, m Manifest, a networkAttempt, role string) error {
	unitName := networkUnit(m, a.ID, role)
	unitPath := "/etc/systemd/system/" + unitName
	if err := enableLink(n.ops, unitName, unitPath); err != nil {
		return err
	}
	if role == "persist" {
		if err := n.persistRequirements(m, a, false); err != nil {
			return err
		}
	}
	if err := n.runUnit(ctx, m, a, "enable", role); err != nil {
		return err
	}
	if role == "persist" {
		return n.persistRequirements(m, a, true)
	}
	return nil
}

func (n networkController) arm(ctx context.Context, m Manifest, a networkAttempt) error {
	if err := n.enable(ctx, m, a, "rollback"); err != nil {
		return err
	}
	p, err := n.properties(ctx, m, a, "rollback.timer")
	if err != nil {
		return err
	}
	if p["ActiveState"] != "active" {
		if err = n.runUnit(ctx, m, a, "start", "rollback.timer"); err != nil {
			return err
		}
	}
	p, err = n.properties(ctx, m, a, "rollback.timer")
	if err != nil || p["ActiveState"] != "active" {
		return errors.New("rollback timer is not armed; network unchanged")
	}
	p, err = n.properties(ctx, m, a, "rollback")
	if err != nil || p["UnitFileState"] != "enabled" {
		return errors.New("pending-on-boot recovery is not enabled; network unchanged")
	}
	return nil
}

func (n networkController) table(ctx context.Context, m Manifest, a networkAttempt) ([]byte, bool, error) {
	list, err := n.ops.run(ctx, nil, "/usr/sbin/nft", "list", "tables")
	if err != nil {
		return nil, false, err
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
	b, err := n.ops.run(ctx, nil, "/usr/sbin/nft", "--stateless", "list", "table", "inet", networkTable(m, a))
	return b, true, err
}

func (n networkController) outcome(m Manifest, a networkAttempt) (string, error) {
	f, err := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/outcome")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, phase := range []string{"commit", "rollback"} {
		if known(f, networkToken(a, phase), 0644) {
			return phase, nil
		}
	}
	return "", errors.New("foreign network outcome; preserve it")
}

func (n networkController) sshPolicy(ctx context.Context, plat hostplatform.Platform, m Manifest, a networkAttempt) error {
	if err := n.checkSSHProfile(ctx, plat); err != nil {
		return err
	}
	if _, err := n.ops.run(ctx, nil, "/usr/sbin/sshd", "-t"); err != nil {
		return err
	}
	owner, err := n.ops.command(ctx, "/usr/bin/getent", "passwd", fmt.Sprint(m.OwnerUID))
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimSpace(string(owner)), ":")
	if len(parts) != 7 || parts[2] != fmt.Sprint(m.OwnerUID) || parts[0] == "" || strings.ContainsAny(parts[0], ",= \r\n") {
		return errors.New("cannot bind effective SSH policy to the OS owner")
	}
	for _, user := range []string{parts[0], "root"} {
		connect := "user=" + user + ",addr=" + a.Access.PeerIP + ",laddr=" + a.Access.ServerIP + ",lport=22"
		b, err := n.ops.run(ctx, nil, "/usr/sbin/sshd", "-T", "-C", connect)
		if err != nil {
			return err
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

func (n networkController) applied(ctx context.Context, plat hostplatform.Platform, m Manifest, a networkAttempt) error {
	kernel, err := n.ops.run(ctx, nil, "/usr/sbin/nft", "--json", "list", "table", "inet", networkTable(m, a))
	if err != nil {
		return err
	}
	if err = verifyTableJSON(kernel, m, a); err != nil {
		return err
	}
	f, err := readRootFileCtx(ctx, n.ops, networkSSH(m, a))
	if err != nil || !known(f, networkSSHBytes(m, a), 0644) {
		return errors.New("owned SSH drop-in changed or is absent")
	}
	actual, present, err := n.table(ctx, m, a)
	if err != nil {
		return err
	}
	expected, err := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/expected.nft")
	if err != nil || !present || expected.Mode.Perm() != 0644 || len(expected.Data) == 0 || !bytes.Equal(actual, expected.Data) {
		return errors.New("owned firewall differs from the immutable applied snapshot; console inspection is required")
	}
	return n.sshPolicy(ctx, plat, m, a)
}

func (n networkController) verifyCleanedRollbackUnits(m Manifest, a networkAttempt) error {
	for _, role := range networkRoles() {
		_, err := n.ops.read("/etc/systemd/system/" + networkUnit(m, a.ID, role))
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("rolled-back network unit remains; console inspection required")
		}
	}
	for _, linkPath := range []string{
		"/etc/systemd/system/multi-user.target.wants/" + networkUnit(m, a.ID, "rollback"),
		"/etc/systemd/system/timers.target.wants/" + networkUnit(m, a.ID, "rollback.timer"),
	} {
		_, err := n.ops.read(linkPath)
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("rolled-back network enablement link remains; console inspection required")
		}
	}
	return n.persistRequirements(m, a, false)
}

func (n networkController) state(ctx context.Context, plat hostplatform.Platform, m Manifest, a networkAttempt) (Status, error) {
	status := Status{Phase: "recovery-required", AttemptID: a.ID}
	if err := n.verifyStatic(ctx, m, a); err != nil {
		return status, err
	}
	outcome, err := n.outcome(m, a)
	if err != nil {
		return status, err
	}
	if outcome == "rollback" {
		cleaned, ce := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/units.cleaned")
		switch {
		case ce == nil && known(cleaned, nil, 0644):
			if err = n.verifyCleanedRollbackUnits(m, a); err != nil {
				return status, err
			}
		case errors.Is(ce, os.ErrNotExist):
			if err = n.verifyUnits(ctx, m, a); err != nil {
				return status, err
			}
		default:
			if ce == nil {
				ce = errors.New("foreign units.cleaned marker; preserve it")
			}
			return status, ce
		}
		_, present, tErr := n.table(ctx, m, a)
		if tErr != nil {
			return status, tErr
		}
		_, sshErr := readRootFileCtx(ctx, n.ops, networkSSH(m, a))
		sshDone, de := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/ssh.done")
		if present || !errors.Is(sshErr, os.ErrNotExist) || de != nil || !known(sshDone, nil, 0644) {
			return status, errors.New("rollback is partial: foreign policy is preserved; use the VPS console to inspect remaining owned firewall/SSH policy")
		}
		status.Phase = "rolled-back"
		return status, nil
	}
	if err = n.verifyUnits(ctx, m, a); err != nil {
		return status, err
	}
	if outcome == "commit" {
		if err = n.persistRequirements(m, a, true); err != nil {
			return status, err
		}
		p, pe := n.properties(ctx, m, a, "persist")
		if pe != nil || p["UnitFileState"] != "enabled" {
			return status, errors.New("committed firewall persistence is disabled; console recovery required")
		}
		if err = n.applied(ctx, plat, m, a); err != nil {
			return status, err
		}
		status.Phase = "committed"
		return status, nil
	}
	if err = n.applied(ctx, plat, m, a); err != nil {
		return status, err
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

func (n networkController) cleanupRolledBackUnits(ctx context.Context, m Manifest, a networkAttempt) error {
	dir := networkAttemptRoot(m, a.ID)
	if cleaned, err := rootFile(n.ops, dir+"/units.cleaned"); err == nil && known(cleaned, nil, 0644) {
		return n.verifyCleanedRollbackUnits(m, a)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err == nil {
		return errors.New("foreign units.cleaned marker; preserve it")
	}
	if p, err := n.properties(ctx, m, a, "rollback.timer"); err == nil && p["ActiveState"] != "" && p["ActiveState"] != "inactive" {
		if err = n.runUnit(ctx, m, a, "stop", "rollback.timer"); err != nil {
			return err
		}
	}
	enableLinks := []struct {
		link, target string
	}{
		{
			link:   "/etc/systemd/system/multi-user.target.wants/" + networkUnit(m, a.ID, "rollback"),
			target: "/etc/systemd/system/" + networkUnit(m, a.ID, "rollback"),
		},
		{
			link:   "/etc/systemd/system/timers.target.wants/" + networkUnit(m, a.ID, "rollback.timer"),
			target: "/etc/systemd/system/" + networkUnit(m, a.ID, "rollback.timer"),
		},
	}
	for _, item := range enableLinks {
		present, err := checkSymlinkTarget(n.ops, item.link, item.target)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if _, err = n.ops.run(ctx, nil, "/usr/bin/rm", "--", item.link); err != nil {
			return err
		}
		if _, err = n.ops.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(item.link)); err != nil {
			return err
		}
	}
	expectedUnits := networkUnits(m, a)
	removedAny := false
	for _, role := range networkRoles() {
		unitPath := "/etc/systemd/system/" + networkUnit(m, a.ID, role)
		f, err := rootFile(n.ops, unitPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !known(f, expectedUnits[role], 0644) {
			return errors.New("owned network system unit changed; preserving it")
		}
		if _, err = n.ops.run(ctx, nil, "/usr/bin/rm", "--", unitPath); err != nil {
			return err
		}
		removedAny = true
	}
	if removedAny {
		if _, err := n.ops.run(ctx, nil, "/usr/bin/sync", "-f", "/etc/systemd/system"); err != nil {
			return err
		}
	}
	if _, err := n.ops.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	return publish(ctx, n.ops, dir+"/units.cleaned", a.ID, nil, 0644)
}

func (n networkController) begin(ctx context.Context, m Manifest, approval Approval) (Status, error) {
	if !approval.PrivateUI || !approval.ConsoleRecovery {
		return Status{}, errors.New("explicit private UI and independent console recovery approval are required")
	}
	if m.PublicOrigin == "" {
		return Status{}, errors.New("SSH-only instances cannot enable network lockdown; prepare Tailscale HTTPS first")
	}
	return n.withLock(ctx, m, func(plat hostplatform.Platform) (Status, error) {
		proof, err := n.proof(ctx, m)
		if err != nil {
			return Status{}, err
		}
		if proof.SessionID == "" || proof.SSHPort != 22 {
			return Status{}, errors.New("verified private SSH connection required")
		}
		if err = n.checkSSHProfile(ctx, plat); err != nil {
			return Status{}, err
		}
		if _, err = n.ops.run(ctx, nil, "/usr/bin/flock", "--version"); err != nil {
			return Status{}, fmt.Errorf("trusted OS flock is required for recoverable network operations: %w", err)
		}
		if _, err = n.ops.run(ctx, nil, "/usr/sbin/nft", "--version"); err != nil {
			return Status{}, fmt.Errorf("install the nftables OS package before network lockdown; no package is installed automatically: %w", err)
		}
		a, le := n.load(m)
		resume := a.ID != ""
		if le != nil && !errors.Is(le, os.ErrNotExist) {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, le
		}
		if le == nil {
			outcome, oe := n.outcome(m, a)
			if oe != nil {
				return Status{Phase: "recovery-required", AttemptID: a.ID}, oe
			}
			if outcome == "commit" {
				return n.state(ctx, plat, m, a)
			}
			if outcome == "rollback" {
				st, se := n.state(ctx, plat, m, a)
				if se != nil || st.Phase != "rolled-back" {
					return st, se
				}
				resume = false
			}
		}
		if !resume {
			if f, re := n.ops.read(networkRoot(m)); re == nil {
				if !f.Directory {
					return Status{}, errors.New("foreign network root")
				}
				bf, be := rootFile(n.ops, networkRoot(m)+"/binding.json")
				if be != nil || !known(bf, binding(m), 0644) {
					return Status{}, errors.New("unclassified network root exists; preserve it")
				}
			} else if !errors.Is(re, os.ErrNotExist) {
				return Status{}, re
			}
			id := make([]byte, 16)
			if _, err = rand.Read(id); err != nil {
				return Status{}, err
			}
			now := n.now().UTC().Truncate(time.Second)
			sshSvc := ""
			if sshServiceForPlatform(plat) == "sshd.service" {
				sshSvc = "sshd.service"
			}
			a = networkAttempt{
				Version:        1,
				ID:             hex.EncodeToString(id),
				ManifestSHA256: manifestDigest(m),
				Access:         proof,
				StartedAt:      now,
				Deadline:       now.Add(5 * time.Minute),
				SSHService:     sshSvc,
			}
		}

		if err = n.validate(m, a); err != nil {
			return Status{}, err
		}
		if err = n.savePointer(m, a); err != nil {
			return Status{}, err
		}
		if err = n.prepare(ctx, m, a); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
		if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline) {
			return n.rollbackAttempt(ctx, plat, m, a)
		}
		actual, present, te := n.table(ctx, m, a)
		if te != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, te
		}
		if present {
			expected, re := rootFile(n.ops, networkAttemptRoot(m, a.ID)+"/expected.nft")
			if !resume || re != nil || len(expected.Data) == 0 || !bytes.Equal(actual, expected.Data) {
				return Status{Phase: "recovery-required", AttemptID: a.ID}, errors.New("owned-name table exists without matching completed apply snapshot; console inspection required")
			}
		}
		if existing, se := readRootFileCtx(ctx, n.ops, networkSSH(m, a)); se == nil {
			if !resume || !known(existing, networkSSHBytes(m, a), 0644) {
				return Status{Phase: "recovery-required", AttemptID: a.ID}, errors.New("preexisting or changed SSH drop-in; preserving it")
			}
		} else if !errors.Is(se, os.ErrNotExist) {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, se
		}
		if present {
			if _, se := readRootFileCtx(ctx, n.ops, networkSSH(m, a)); errors.Is(se, os.ErrNotExist) {
				return n.rollbackAttempt(ctx, plat, m, a)
			}
			timer, pe := n.properties(ctx, m, a, "rollback.timer")
			if pe != nil {
				return Status{Phase: "recovery-required", AttemptID: a.ID}, pe
			}
			if timer["ActiveState"] != "active" {
				return n.rollbackAttempt(ctx, plat, m, a)
			}
		}

		if _, err = n.ops.run(ctx, nil, "/usr/sbin/sshd", "-t"); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
		if err = n.arm(ctx, m, a); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
		if !present {
			if err = n.runUnit(ctx, m, a, "start", "apply"); err != nil {
				return Status{Phase: "recovery-required", AttemptID: a.ID}, err
			}
		}
		if outcome, oe := n.outcome(m, a); oe != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, oe
		} else if outcome == "rollback" {
			return n.state(ctx, plat, m, a)
		}

		if err = n.sshPolicy(ctx, plat, m, a); err != nil {
			st, re := n.rollbackAttempt(ctx, plat, m, a)
			return st, errors.Join(err, re)
		}
		if err = n.runUnit(ctx, m, a, "start", "ssh-hup"); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
		if err = n.applied(ctx, plat, m, a); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
		if !n.now().Before(a.Deadline) || n.now().Before(a.StartedAt) {
			return n.rollbackAttempt(ctx, plat, m, a)
		}
		return Status{Phase: "pending", AttemptID: a.ID, Deadline: a.Deadline}, nil
	})
}

func (n networkController) confirm(ctx context.Context, m Manifest, id string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return n.withLock(ctx, m, func(plat hostplatform.Platform) (Status, error) {
		a, err := n.load(m)
		if err != nil {
			return Status{}, err
		}
		if id != a.ID || !canonicalHex(id, 16) {
			return Status{}, errors.New("exact pending network attempt required")
		}
		if err = n.verifyUnits(ctx, m, a); err != nil {
			return Status{}, err
		}
		outcome, err := n.outcome(m, a)
		if err != nil {
			return Status{}, err
		}
		if outcome == "rollback" {
			return n.state(ctx, plat, m, a)
		}
		p, err := n.proof(ctx, m)
		if err != nil {
			return Status{}, err
		}
		if p.SessionID == a.Access.SessionID || p.ServerIP != a.Access.ServerIP || p.SSHPort != a.Access.SSHPort {
			return Status{}, errors.New("confirm from a different private SSH connection to this VPS")
		}
		if outcome == "commit" {
			return n.state(ctx, plat, m, a)
		}
		if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline.Add(-30*time.Second)) {
			st, re := n.rollbackAttempt(ctx, plat, m, a)
			return st, errors.Join(errors.New("confirmation needs at least 30 seconds before expiry; policy rolled back or requires console recovery"), re)
		}
		if err = n.applied(ctx, plat, m, a); err != nil {
			return Status{Phase: "recovery-required", AttemptID: id}, err
		}
		if err = n.enable(ctx, m, a, "persist"); err != nil {
			return Status{Phase: "recovery-required", AttemptID: id}, err
		}
		dir := networkAttemptRoot(m, id)
		if _, err = n.ops.run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", dir+"/commit.token", dir+"/outcome"); err != nil {
			st, se := n.state(ctx, plat, m, a)
			return st, errors.Join(err, se)
		}
		if n.now().Before(a.StartedAt) || !n.now().Before(a.Deadline) || n.applied(ctx, plat, m, a) != nil {
			st, re := n.rollbackAttempt(ctx, plat, m, a)
			return st, errors.Join(errors.New("policy/time changed during confirmation; recovery required"), re)
		}
		if _, err = n.ops.run(ctx, nil, "/usr/bin/sync", "-f", dir); err != nil {
			return Status{Phase: "recovery-required", AttemptID: id}, err
		}
		if err = n.runUnit(ctx, m, a, "stop", "rollback.timer"); err != nil {
			return Status{Phase: "committed", AttemptID: id}, err
		}
		return n.state(ctx, plat, m, a)
	})
}

func (n networkController) rollbackAttempt(ctx context.Context, plat hostplatform.Platform, m Manifest, a networkAttempt) (Status, error) {
	outcome, err := n.outcome(m, a)
	if err != nil {
		return Status{Phase: "recovery-required", AttemptID: a.ID}, err
	}
	if outcome == "rollback" {
		st, se := n.state(ctx, plat, m, a)
		if se == nil && st.Phase == "rolled-back" {
			if ce := n.cleanupRolledBackUnits(ctx, m, a); ce != nil {
				return Status{Phase: "recovery-required", AttemptID: a.ID}, ce
			}
			return n.state(ctx, plat, m, a)
		}
	}
	if err = n.verifyUnits(ctx, m, a); err != nil {
		return Status{Phase: "recovery-required", AttemptID: a.ID}, err
	}
	if err = n.removePersistRequirements(ctx, m, a); err != nil {
		return Status{Phase: "recovery-required", AttemptID: a.ID}, err
	}
	if outcome == "commit" {
		dir := networkAttemptRoot(m, a.ID)
		if _, err = n.ops.run(ctx, nil, "/usr/bin/cmp", "--silent", "--", dir+"/commit.token", dir+"/outcome"); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
		if _, err = n.ops.run(ctx, nil, "/usr/bin/rm", "--", dir+"/outcome"); err != nil {
			return Status{Phase: "recovery-required", AttemptID: a.ID}, err
		}
	}
	runErr := n.runUnit(ctx, m, a, "start", "rollback")
	st, se := n.state(ctx, plat, m, a)
	if st.Phase != "rolled-back" {
		return st, errors.Join(runErr, se, errors.New("rollback incomplete; foreign edits preserved, console recovery required"))
	}
	if ce := n.cleanupRolledBackUnits(ctx, m, a); ce != nil {
		return Status{Phase: "recovery-required", AttemptID: a.ID}, errors.Join(runErr, ce)
	}
	st, se = n.state(ctx, plat, m, a)
	if st.Phase != "rolled-back" {
		return st, errors.Join(runErr, se, errors.New("rollback unit cleanup verification incomplete; console recovery required"))
	}
	return st, runErr
}

func (n networkController) rollback(ctx context.Context, m Manifest) (Status, error) {
	return n.withLock(ctx, m, func(plat hostplatform.Platform) (Status, error) {
		a, err := n.load(m)
		if errors.Is(err, os.ErrNotExist) {
			return Status{Phase: "none"}, nil
		}
		if err != nil {
			return Status{Phase: "recovery-required"}, err
		}
		return n.rollbackAttempt(ctx, plat, m, a)
	})
}

func (n networkController) inspect(ctx context.Context, m Manifest) (Status, error) {
	return n.withLock(ctx, m, func(plat hostplatform.Platform) (Status, error) {
		a, err := n.load(m)
		if errors.Is(err, os.ErrNotExist) {
			return Status{Phase: "none"}, nil
		}
		if err != nil {
			return Status{Phase: "recovery-required"}, err
		}
		return n.state(ctx, plat, m, a)
	})
}
