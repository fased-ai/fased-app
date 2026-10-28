//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type networkMemory struct {
	base                      *memoryHost
	ops                       hostOps
	tables                    map[string][]byte
	enabled, active           map[string]bool
	proof                     networkAccess
	clock                     time.Time
	beforeCommit, beforeApply func()
	fault                     string
	sshActive                 bool
	requireSSHDRunDir         bool
	sshExec                   string
	kernelJSON                []byte
	nftFail                   bool
	serviceCalls              [][]string
}

func networkFixture(t *testing.T) (*networkMemory, networkController, Manifest) {
	t.Helper()
	h, o, m := installFixture(t)
	for _, path := range []string{"/run", "/etc/ssh", "/etc/ssh/sshd_config.d", "/etc/default", "/usr", "/usr/lib", "/usr/lib/systemd", "/usr/lib/systemd/system"} {
		h.files[path] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
	}
	h.files["/etc/ssh/sshd_config"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("Include /etc/ssh/sshd_config.d/*.conf\nUsePAM yes\n")}
	h.files["/etc/default/ssh"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("SSHD_OPTS=\n")}
	h.files["/usr/lib/systemd/system/ssh.service"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: []byte("[Service]\nEnvironmentFile=-/etc/default/ssh\nExecStart=/usr/sbin/sshd -D $SSHD_OPTS\n")}
	m.PublicOrigin = "https://controlled.owner.ts.net"
	h.files[m.Config] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: encoded(m)}
	h.files[m.Root+"/intent.json"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: encoded(intentFor(m))}
	h.files[m.Root+"/"+m.Unit] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: unit(m)}
	h.files[m.UnitPath] = h.files[m.Root+"/"+m.Unit]
	host := &networkMemory{base: h, ops: o, tables: map[string][]byte{"foreign": []byte("foreign firewall bytes")}, enabled: map[string]bool{}, active: map[string]bool{}, clock: time.Unix(1800000000, 0).UTC(), proof: networkAccess{SessionID: "100.90.0.1:10001->100.90.0.2:22", PeerIP: "100.90.0.1", ServerIP: "100.90.0.2", SSHPort: 22}, sshActive: true, sshExec: "/usr/sbin/sshd -D $SSHD_OPTS"}
	o.run = host.run
	originalCommand := o.command
	o.command = func(ctx context.Context, a ...string) ([]byte, error) {
		if a[0] == "/usr/bin/getent" && a[1] == "passwd" && a[2] == "1000" {
			return []byte("owner:x:1000:1000:Owner:/home/owner:/bin/bash\n"), nil
		}
		if a[0] == "/usr/bin/systemctl" && len(a) > 2 && a[2] == "ssh.service" {
			return []byte("LoadState=loaded\nFragmentPath=/usr/lib/systemd/system/ssh.service\nDropInPaths=\nEnvironmentFiles=/etc/default/ssh (ignore_errors=yes)\nEnvironment=\nPassEnvironment=\nExecStart={ path=/usr/sbin/sshd ; argv[]=" + host.sshExec + " ; }\n"), nil
		}
		if a[0] == "/usr/bin/systemctl" && len(a) > 2 && strings.HasPrefix(a[2], "fased-vps-net-") {
			name := a[2]
			path := "/etc/systemd/system/" + name
			if _, ok := h.files[path]; !ok {
				return []byte("LoadState=not-found\nFragmentPath=\nDropInPaths=\n"), nil
			}
			active := "inactive"
			if host.active[name] {
				active = "active"
			}
			state := "disabled"
			if host.enabled[name] {
				state = "enabled"
			}
			return []byte(fmt.Sprintf("LoadState=loaded\nFragmentPath=%s\nDropInPaths=\nUser=0\nGroup=0\nActiveState=%s\nUnitFileState=%s\n", path, active, state)), nil
		}
		return originalCommand(ctx, a...)
	}
	n := networkController{ops: o, now: func() time.Time { return host.clock }, proof: func(context.Context, Manifest) (networkAccess, error) { return host.proof, nil }}
	return host, n, m
}
func (h *networkMemory) run(ctx context.Context, b []byte, a ...string) ([]byte, error) {
	switch a[0] {
	case "/usr/bin/flock":
		if len(a) == 2 {
			return []byte("flock fixture version"), nil
		}
		return h.run(ctx, nil, a[4:]...)
	case "/usr/bin/cat":
		f, e := h.base.read(a[len(a)-1])
		return f.Data, e
	case "/usr/bin/cmp":
		l, le := h.base.read(a[len(a)-2])
		r, re := h.base.read(a[len(a)-1])
		if le != nil || re != nil || !bytes.Equal(l.Data, r.Data) {
			return nil, errors.New("different immutable input")
		}
		return nil, nil
	case "/usr/sbin/sshd":
		if a[1] == "-t" {
			if h.requireSSHDRunDir {
				dir, e := h.base.read("/run/sshd")
				if e != nil || !dir.Directory || dir.UID != 0 || dir.GID != 0 || dir.Mode.Perm()&0022 != 0 {
					return nil, errors.New("missing or unsafe privilege separation directory /run/sshd")
				}
			}
			return nil, nil
		}
		locked := false
		for p, f := range h.base.files {
			if strings.HasPrefix(p, "/etc/ssh/sshd_config.d/") && strings.HasSuffix(p, ".conf") && bytes.Contains(f.Data, []byte("PermitRootLogin no")) {
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
				names = append(names, name)
			}
			sort.Strings(names)
			var out string
			for _, name := range names {
				out += "table inet " + name + "\n"
			}
			return []byte(out), nil
		}
		if a[1] == "--json" {
			if _, ok := h.tables[a[len(a)-1]]; !ok {
				return nil, os.ErrNotExist
			}
			if h.kernelJSON != nil {
				return bytes.Clone(h.kernelJSON), nil
			}
			var p networkPointer
			if e := json.Unmarshal(h.base.journals["network.json"], &p); e != nil {
				return nil, e
			}
			return networkPolicyFixture(h.base.manifest(), p.Attempt), nil
		}
		if a[1] == "--stateless" {
			table, ok := h.tables[a[len(a)-1]]
			if !ok {
				return nil, os.ErrNotExist
			}
			return bytes.Clone(table), nil
		}
		f, e := h.base.read(a[len(a)-1])
		if e != nil {
			return nil, e
		}
		if a[1] == "--check" {
			return nil, nil
		}
		words := strings.Fields(string(f.Data))
		if len(words) < 4 {
			return nil, errors.New("invalid fake nft input")
		}
		table := words[3]
		if words[0] == "create" {
			if _, ok := h.tables[table]; ok {
				return nil, os.ErrExist
			}
			h.tables[table] = bytes.Clone(f.Data)
		} else if words[0] == "delete" {
			if _, ok := h.tables[table]; !ok {
				return nil, os.ErrNotExist
			}
			delete(h.tables, table)
		} else {
			return nil, errors.New("unexpected global firewall operation")
		}
		return nil, nil
	case "/usr/bin/ln":
		if strings.HasSuffix(a[len(a)-2], "/commit.token") && h.beforeCommit != nil {
			f := h.beforeCommit
			h.beforeCommit = nil
			f()
		}
	case "/usr/bin/systemctl":
		h.serviceCalls = append(h.serviceCalls, append([]string(nil), a...))
		if a[1] == "is-active" {
			if h.sshActive {
				return nil, nil
			}
			return nil, errors.New("inactive")
		}
		if a[1] == "kill" {
			if !h.sshActive {
				return nil, errors.New("inactive main process")
			}
			if !contains(a, "--kill-whom=main") || !contains(a, "--signal=HUP") {
				return nil, errors.New("not a bounded ssh main-process HUP")
			}
			return nil, nil
		}
		name := a[len(a)-1]
		if strings.HasPrefix(name, "fased-vps-net-") {
			switch a[1] {
			case "enable":
				h.enabled[name] = true
				h.base.files["/etc/systemd/system/multi-user.target.wants/"+name] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + name}
				if strings.HasSuffix(name, "-persist.service") {
					for _, unit := range []string{"ssh.socket", "ssh.service"} {
						dir := "/etc/systemd/system/" + unit + ".requires"
						h.base.files[dir] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
						h.base.files[dir+"/"+name] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "../" + name}
					}
				}
				return nil, nil
			case "disable":
				h.enabled[name] = false
				delete(h.base.files, "/etc/systemd/system/multi-user.target.wants/"+name)
				return nil, nil
			case "stop":
				h.active[name] = false
				return nil, nil
			case "start":
				if strings.HasSuffix(name, ".timer") {
					h.active[name] = true
					return nil, nil
				}
				return nil, h.executeUnit(ctx, name)
			}
		}
	}
	return h.base.run(ctx, b, a...)
}

// Interpret only the finite generated declarative OS-unit subset. This is not
// an OS/sandbox proof; actual systemd/nft/sshd acceptance is a separate VM gate.
func (h *networkMemory) executeUnit(ctx context.Context, name string) error {
	file, e := h.base.read("/etc/systemd/system/" + name)
	if e != nil {
		return e
	}
	text := string(file.Data)
	if strings.HasSuffix(name, "-apply-actions.service") && h.beforeApply != nil {
		f := h.beforeApply
		h.beforeApply = nil
		f()
	}
	var stdout string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "ConditionPathExists=") {
			p := strings.TrimPrefix(line, "ConditionPathExists=")
			neg := strings.HasPrefix(p, "!")
			p = strings.TrimPrefix(p, "!")
			f, ok := h.base.files[p]
			if ok && f.Mode&os.ModeSymlink != 0 {
				target := f.Link
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(p), target)
				}
				_, ok = h.base.files[filepath.Clean(target)]
			}
			if ok == neg {
				return nil
			}
		}
		if strings.HasPrefix(line, "ConditionPathIsSymbolicLink=") {
			p := strings.TrimPrefix(line, "ConditionPathIsSymbolicLink=")
			neg := strings.HasPrefix(p, "!")
			p = strings.TrimPrefix(p, "!")
			f, exists := h.base.files[p]
			isLink := exists && f.Mode&os.ModeSymlink != 0
			if isLink == neg {
				return nil
			}
		}
		if strings.HasPrefix(line, "StandardOutput=truncate:") {
			stdout = strings.TrimPrefix(line, "StandardOutput=truncate:")
		}
	}
	for _, line := range strings.Split(text, "\n") {
		condition := strings.HasPrefix(line, "ExecCondition=")
		if !condition && !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		command := strings.SplitN(line, "=", 2)[1]
		ignore := strings.HasPrefix(command, "-")
		command = strings.TrimPrefix(command, "-")
		if stdout != "" {
			h.base.files[stdout] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644}
		}
		out, e := h.run(ctx, nil, strings.Fields(command)...)
		if stdout != "" {
			f := h.base.files[stdout]
			f.Data = out
			h.base.files[stdout] = f
		}
		if condition && e != nil {
			return nil
		}
		if e != nil && !ignore {
			return e
		}
	}
	if strings.HasSuffix(name, "-apply.service") && h.fault == "after-apply" {
		h.fault = ""
		return errors.New("lost client after independent systemd apply job")
	}
	return nil
}
func networkBegin(t *testing.T) (*networkMemory, networkController, Manifest, NetworkStatus) {
	t.Helper()
	h, n, m := networkFixture(t)
	s, e := n.begin(context.Background(), m, NetworkApproval{true, true})
	if e != nil || s.Phase != "pending" {
		t.Fatal(s, e)
	}
	return h, n, m, s
}
func TestNetworkControllerFinitePolicyAndBootOrdering(t *testing.T) {
	h, n, m, s := networkBegin(t)
	a, e := n.load(m)
	if e != nil {
		t.Fatal(e)
	}
	rules := string(networkRules(m, a))
	if strings.Count(rules, "add chain inet ") != 1 || strings.Count(rules, "add rule inet ") != 8 || len(strings.Split(strings.TrimSpace(rules), "\n")) != 10 {
		t.Fatal("explicit atomic table/chain/eight-rule program required")
	}
	for _, part := range []string{"policy drop", "ct state established,related accept", "iifname \"lo\" accept", "iifname \"tailscale0\" tcp dport { 22, 443 }", "udp dport 41641", "udp sport 67 udp dport 68", "ip6 saddr fe80::/10 udp sport 547 udp dport 546", "meta l4proto { icmp, ipv6-icmp }"} {
		if !strings.Contains(rules, part) {
			t.Fatal("missing finite reachability rule", part)
		}
	}
	for _, bad := range []string{"flush ruleset", "hook output", "hook forward", "udp accept"} {
		if strings.Contains(rules, bad) {
			t.Fatal("broadened host policy", bad)
		}
	}
	units := networkUnits(m, a)
	for _, role := range []string{"rollback", "persist"} {
		text := string(units[role])
		if !strings.Contains(text, "DefaultDependencies=no") || !strings.Contains(text, "Before=network-pre.target ssh.socket ssh.service") || !strings.Contains(text, "After=local-fs.target") {
			t.Fatal("boot socket exposure or dependency regression", role)
		}
	}
	if !strings.Contains(string(units["persist"]), "After="+networkUnit(m, a.ID, "rollback")+" nftables.service\n") {
		t.Fatal("vendor nftables loader can flush committed policy after persistence")
	}
	for role, b := range units {
		if strings.Contains(string(b), "/bin/sh") || strings.Contains(string(b), m.Core) {
			t.Fatal("project/shell root code", role)
		}
	}
	if !strings.Contains(string(units["apply"]), "flock --exclusive --no-fork") || !strings.Contains(string(units["apply-actions"]), "ConditionPathExists=!"+networkAttemptRoot(m, a.ID)+"/outcome") {
		t.Fatal("apply is not serialized with rollback")
	}
	if f := h.base.files[networkAttemptRoot(m, a.ID)+"/private"]; f.UID != 0 || f.Mode.Perm() != 0700 {
		t.Fatal("owner could hold rollback lock")
	}
	if _, e = n.confirm(context.Background(), m, s.AttemptID); e == nil {
		t.Fatal("same connection committed")
	}
	h.proof.SessionID = "second-private-tcp-tuple"
	c, e := n.confirm(context.Background(), m, s.AttemptID)
	if e != nil || c.Phase != "committed" {
		t.Fatal(c, e)
	}
	r, e := n.rollback(context.Background(), m)
	if e != nil || r.Phase != "rolled-back" {
		t.Fatal(r, e)
	}
	retry, e := n.begin(context.Background(), m, NetworkApproval{true, true})
	if e != nil || retry.AttemptID == s.AttemptID {
		t.Fatal("retry identity reused", retry, e)
	}
	if e = h.executeUnit(context.Background(), networkUnit(m, s.AttemptID, "rollback")); e != nil {
		t.Fatal(e)
	}
	if st, e := n.inspect(context.Background(), m); e != nil || st.Phase != "pending" {
		t.Fatal("old rollback reverted retry", st, e)
	}
}
func TestNetworkControllerForeignEditsAndRemovedAppRecovery(t *testing.T) {
	for _, change := range []string{"firewall", "ssh", "app-removed"} {
		t.Run(change, func(t *testing.T) {
			h, n, m, s := networkBegin(t)
			a, _ := n.load(m)
			foreignSSH := []byte("# operator edit; preserve\nPermitRootLogin prohibit-password\n")
			switch change {
			case "firewall":
				h.tables[networkTable(m, a)] = []byte("foreign modified owned-name table")
			case "ssh":
				f := h.base.files[networkSSH(m, a)]
				f.Data = foreignSSH
				h.base.files[networkSSH(m, a)] = f
			case "app-removed":
				delete(h.base.files, m.UnitPath)
				h.base.active = false
			}
			r, e := n.rollback(context.Background(), m)
			if change == "app-removed" {
				if e != nil || r.Phase != "rolled-back" {
					t.Fatal(r, e)
				}
			} else if e == nil || r.Phase != "recovery-required" {
				t.Fatal("partial rollback falsely succeeded", r, e)
			}
			if change == "firewall" {
				if !bytes.Equal(h.tables[networkTable(m, a)], []byte("foreign modified owned-name table")) {
					t.Fatal("foreign nft changed")
				}
				if _, ok := h.base.files[networkSSH(m, a)]; ok {
					t.Fatal("firewall conflict blocked independent SSH restore")
				}
			}
			if change == "ssh" && !bytes.Equal(h.base.files[networkSSH(m, a)].Data, foreignSSH) {
				t.Fatal("foreign SSH changed")
			}
			if !bytes.Equal(h.tables["foreign"], []byte("foreign firewall bytes")) {
				t.Fatal("unrelated table changed")
			}
			_ = s
		})
	}
}
func TestNetworkControllerRollbackWinsApplyAndCommitRaces(t *testing.T) {
	for _, which := range []string{"apply", "commit"} {
		t.Run(which, func(t *testing.T) {
			h, n, m := networkFixture(t)
			var current networkAttempt
			rollback := func() {
				var e error
				current, e = n.load(m)
				if e != nil {
					t.Fatal(e)
				}
				if e = h.executeUnit(context.Background(), networkUnit(m, current.ID, "rollback")); e != nil {
					t.Fatal(e)
				}
			}
			if which == "apply" {
				h.beforeApply = rollback
			}
			s, e := n.begin(context.Background(), m, NetworkApproval{true, true})
			if which == "apply" {
				if e != nil || s.Phase != "rolled-back" {
					t.Fatal("late apply followed rollback", s, e)
				}
				if _, ok := h.base.files[networkSSH(m, current)]; ok {
					t.Fatal("late SSH publication")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			h.proof.SessionID = "different-private-connection"
			h.beforeCommit = rollback
			c, e := n.confirm(context.Background(), m, s.AttemptID)
			if e == nil || c.Phase == "committed" {
				t.Fatal("commit bypassed winning rollback", c, e)
			}
			st, se := n.inspect(context.Background(), m)
			if se != nil || st.Phase != "rolled-back" {
				t.Fatal(st, se)
			}
		})
	}
}
func TestNetworkControllerDeadlineClockAndPendingBoot(t *testing.T) {
	for _, which := range []string{"forward", "backward", "boot"} {
		t.Run(which, func(t *testing.T) {
			h, n, m, s := networkBegin(t)
			a, _ := n.load(m)
			if which == "boot" {
				h.tables = map[string][]byte{"foreign": []byte("foreign firewall bytes")}
				h.sshActive = false
				delete(h.base.files, "/run/sshd")
				h.requireSSHDRunDir = true
				if e := h.executeUnit(context.Background(), networkUnit(m, a.ID, "rollback")); e != nil {
					t.Fatal(e)
				}
				st, e := n.inspect(context.Background(), m)
				if e != nil || st.Phase != "rolled-back" {
					t.Fatal(st, e)
				}
				return
			}
			if which == "forward" {
				h.clock = h.clock.Add(6 * time.Minute)
			} else {
				h.clock = h.clock.Add(-time.Minute)
			}
			h.proof.SessionID = "different-private-connection"
			st, e := n.confirm(context.Background(), m, s.AttemptID)
			if e == nil || st.Phase != "rolled-back" {
				t.Fatal("invalid clock committed", st, e)
			}
		})
	}
}
func TestNetworkControllerInterruptedClientResumesSameAttempt(t *testing.T) {
	h, n, m := networkFixture(t)
	h.fault = "after-apply"
	failed, e := n.begin(context.Background(), m, NetworkApproval{true, true})
	if e == nil {
		t.Fatal("missing client interruption")
	}
	s, e := n.begin(context.Background(), m, NetworkApproval{true, true})
	if e != nil || s.Phase != "pending" || s.AttemptID != failed.AttemptID {
		t.Fatal("interrupted apply did not resume", s, e)
	}
}
func TestNetworkControllerSSHProfileRefusesConditionalOrForeignInputs(t *testing.T) {
	for _, change := range []string{"match", "include", "foreign", "no-approval", "ssh-only"} {
		t.Run(change, func(t *testing.T) {
			h, n, m := networkFixture(t)
			approval := NetworkApproval{true, true}
			switch change {
			case "match":
				f := h.base.files["/etc/ssh/sshd_config"]
				f.Data = append(f.Data, []byte("Match User owner\n PasswordAuthentication yes\n")...)
				h.base.files["/etc/ssh/sshd_config"] = f
			case "include":
				f := h.base.files["/etc/ssh/sshd_config"]
				f.Data = []byte("Include /foreign/*.conf\n")
				h.base.files["/etc/ssh/sshd_config"] = f
			case "foreign":
				f := h.base.files["/etc/ssh/sshd_config"]
				f.UID = 1000
				h.base.files["/etc/ssh/sshd_config"] = f
			case "no-approval":
				approval.ConsoleRecovery = false
			case "ssh-only":
				m.PublicOrigin = ""
			}
			before := len(h.base.calls)
			if _, e := n.begin(context.Background(), m, approval); e == nil {
				t.Fatal("unsupported SSH profile/consent accepted")
			}
			for _, a := range h.base.calls[before:] {
				if a[0] != "/usr/bin/passwd" {
					t.Fatal("unsupported profile reached host mutation", a)
				}
			}
		})
	}
}
func TestNetworkTrustedUtilityFormsRefuseMutationEscapes(t *testing.T) {
	for _, args := range [][]string{{"/usr/sbin/nft", "flush", "ruleset"}, {"/usr/sbin/nft", "--file", "/tmp/foreign.nft"}, {"/usr/sbin/sshd", "-D"}, {"/usr/sbin/sshd", "-T", "-f", "/foreign"}, {"/usr/bin/cmp", "--silent", "--", "/etc/shadow", "/tmp/outcome"}, {"/usr/bin/flock", "/tmp/foreign", "/bin/sh"}} {
		if _, e := (&custodyinstall.System{}).Run(context.Background(), nil, args...); e == nil {
			t.Fatal("unsafe OS form admitted", args)
		}
	}
}

func TestNetworkControllerLostPointersDoNotOrphanPolicy(t *testing.T) {
	for _, which := range []string{"pointer", "attempt"} {
		t.Run(which, func(t *testing.T) {
			h, n, m, s := networkBegin(t)
			a, _ := n.load(m)
			saved := bytes.Clone(h.tables[networkTable(m, a)])
			if which == "pointer" {
				delete(h.base.journals, "network.json")
			} else {
				delete(h.base.files, networkAttemptRoot(m, a.ID)+"/attempt.json")
			}
			st, e := n.inspect(context.Background(), m)
			if e == nil || st.Phase != "recovery-required" {
				t.Fatal("retained policy reported absent", st, e)
			}
			next, e := n.begin(context.Background(), m, NetworkApproval{true, true})
			if e == nil || next.Phase != "recovery-required" {
				t.Fatal("lost metadata created another attempt", next, e)
			}
			if !bytes.Equal(saved, h.tables[networkTable(m, a)]) {
				t.Fatal("policy was orphaned or altered")
			}
			if _, ok := h.base.files[networkSSH(m, a)]; !ok {
				t.Fatal("SSH policy disappeared")
			}
			_ = s
		})
	}
}
func TestNetworkControllerFirstPreparationRetryKeepsAttempt(t *testing.T) {
	h, n, m := networkFixture(t)
	h.base.fail = len(h.base.calls) + 2
	first, e := n.begin(context.Background(), m, NetworkApproval{true, true})
	if e == nil {
		t.Fatal("preparation interruption not reached")
	}
	h.base.fail = 0
	second, e := n.begin(context.Background(), m, NetworkApproval{true, true})
	if e != nil || second.Phase != "pending" || second.AttemptID != first.AttemptID {
		t.Fatal("initial preparation did not resume exact attempt", first, second, e)
	}
}

func TestNetworkControllerSSHVendorInvocation(t *testing.T) {
	for _, change := range []string{"vendor", "options-o", "custom-f", "loaded-o", "changed-after-begin"} {
		t.Run(change, func(t *testing.T) {
			h, n, m := networkFixture(t)
			if change == "changed-after-begin" {
				pending, e := n.begin(context.Background(), m, NetworkApproval{true, true})
				if e != nil {
					t.Fatal(e)
				}
				h.proof.SessionID = "different-private-tcp-tuple"
				f := h.base.files["/etc/default/ssh"]
				f.Data = []byte("SSHD_OPTS='-o PasswordAuthentication=yes'\n")
				h.base.files["/etc/default/ssh"] = f
				if _, e = n.confirm(context.Background(), m, pending.AttemptID); e == nil {
					t.Fatal("custom daemon options passed confirmation")
				}
				r, e := n.rollback(context.Background(), m)
				if e != nil || r.Phase != "rolled-back" {
					t.Fatal("custom options blocked restoration", r, e)
				}
				if !bytes.Equal(h.base.files["/etc/default/ssh"].Data, f.Data) {
					t.Fatal("custom daemon options overwritten")
				}
				return
			}
			switch change {
			case "options-o":
				f := h.base.files["/etc/default/ssh"]
				f.Data = []byte("SSHD_OPTS='-o PasswordAuthentication=yes'\n")
				h.base.files["/etc/default/ssh"] = f
			case "custom-f":
				h.sshExec = "/usr/sbin/sshd -D -f /etc/ssh/custom.conf"
			case "loaded-o":
				h.sshExec = "/usr/sbin/sshd -D -oPasswordAuthentication=yes"
			}
			before := len(h.base.calls)
			e := n.checkSSHProfile(context.Background())
			if (e == nil) != (change == "vendor") {
				t.Fatal(change, e)
			}
			if len(h.base.calls) != before {
				t.Fatal("profile admission mutated host state")
			}
		})
	}
}

// This finite dependency model verifies our declarative contract, not systemd.
// The disposable VM separately proves real boot failure and console recovery.
func (h *networkMemory) bootProtectedSSH(ctx context.Context, unit string) error {
	prefix := "/etc/systemd/system/" + unit + ".requires/"
	for path, link := range h.base.files {
		if strings.HasPrefix(path, prefix) {
			if e := h.executeUnit(ctx, filepath.Base(link.Link)); e != nil {
				return e
			}
		}
	}
	h.sshActive = true
	return nil
}
func TestNetworkPersistRequirementsAndManualRollback(t *testing.T) {
	h, n, m, s := networkBegin(t)
	a, _ := n.load(m)
	if !strings.Contains(string(networkUnits(m, a)["persist"]), "RequiredBy=ssh.socket ssh.service") {
		t.Fatal("persistent firewall does not gate both SSH units")
	}
	h.proof.SessionID = "second-private-tcp-tuple"
	if committed, e := n.confirm(context.Background(), m, s.AttemptID); e != nil || committed.Phase != "committed" {
		t.Fatal(committed, e)
	}
	if e := n.persistRequirements(m, a, true); e != nil {
		t.Fatal(e)
	}
	foreign := "/etc/systemd/system/operator.target.wants/" + networkUnit(m, a.ID, "persist")
	h.base.files[foreign] = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/" + networkUnit(m, a.ID, "persist")}
	beforeServices, beforeCalls := len(h.serviceCalls), len(h.base.calls)
	if rolled, e := n.rollback(context.Background(), m); e != nil || rolled.Phase != "rolled-back" {
		t.Fatal(rolled, e)
	}
	for _, path := range networkPersistRequirementPaths(m, a) {
		if _, ok := h.base.files[path]; ok {
			t.Fatal("requiring SSH dependency retained", path)
		}
	}
	if _, ok := h.base.files[foreign]; !ok {
		t.Fatal("unrelated administrator dependency removed")
	}
	for _, call := range h.serviceCalls[beforeServices:] {
		if len(call) > 1 && call[1] == "stop" && (call[len(call)-1] == "ssh.socket" || call[len(call)-1] == "ssh.service" || call[len(call)-1] == networkUnit(m, a.ID, "persist")) {
			t.Fatal("rollback stopped a required SSH/persist unit", call)
		}
	}
	outcomeRemoval := -1
	removed := map[string]int{}
	for i, call := range h.base.calls[beforeCalls:] {
		if call[0] == "/usr/bin/rm" {
			path := call[len(call)-1]
			removed[path] = i
			if path == networkAttemptRoot(m, a.ID)+"/outcome" {
				outcomeRemoval = i
			}
		}
	}
	for _, path := range networkPersistRequirementPaths(m, a) {
		i, ok := removed[path]
		if !ok || outcomeRemoval < 0 || i >= outcomeRemoval {
			t.Fatal("SSH dependency was not removed before retiring commit", path)
		}
	}
}
func TestNetworkPersistForeignRequirementsRefused(t *testing.T) {
	for _, when := range []string{"enable", "rollback"} {
		for _, index := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s-%d", when, index), func(t *testing.T) {
				h, n, m, s := networkBegin(t)
				a, _ := n.load(m)
				h.proof.SessionID = "second-private-connection"
				if when == "rollback" {
					if _, e := n.confirm(context.Background(), m, s.AttemptID); e != nil {
						t.Fatal(e)
					}
				}
				path := networkPersistRequirementPaths(m, a)[index]
				h.base.files[filepath.Dir(path)] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
				foreign := hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/etc/systemd/system/operator.service"}
				h.base.files[path] = foreign
				var e error
				if when == "enable" {
					_, e = n.confirm(context.Background(), m, s.AttemptID)
				} else {
					_, e = n.rollback(context.Background(), m)
				}
				if e == nil {
					t.Fatal("foreign required link admitted")
				}
				if got := h.base.files[path]; got.Link != foreign.Link {
					t.Fatal("foreign required link changed")
				}
			})
		}
	}
}
func TestNetworkBootPersistFailureDependencyModel(t *testing.T) {
	h, n, m, s := networkBegin(t)
	a, _ := n.load(m)
	h.proof.SessionID = "second-private-connection"
	if _, e := n.confirm(context.Background(), m, s.AttemptID); e != nil {
		t.Fatal(e)
	}
	delete(h.tables, networkTable(m, a))
	h.nftFail = true
	for _, unit := range []string{"ssh.socket", "ssh.service"} {
		h.sshActive = false
		if e := h.bootProtectedSSH(context.Background(), unit); e == nil || h.sshActive {
			t.Fatal("SSH started despite a required firewall boot failure", unit, e)
		}
	}
	// An intentionally skipped persistence job after rollback is not a failed
	// dependency. The actual OS model is checked independently in the VM gate.
	dir := networkAttemptRoot(m, a.ID)
	h.base.files[dir+"/outcome"] = h.base.files[dir+"/rollback.token"]
	h.sshActive = false
	if e := h.bootProtectedSSH(context.Background(), "ssh.socket"); e != nil || !h.sshActive {
		t.Fatal("skipped rolled-back persistence prevented SSH recovery", e)
	}
}

func TestNetworkSSHRuntimePrerequisiteConditionsAndOrdering(t *testing.T) {
	for _, variant := range []string{"missing", "owned", "foreign-dir", "foreign-file", "foreign-symlink", "dangling-symlink"} {
		t.Run(variant, func(t *testing.T) {
			h, n, m, _ := networkBegin(t)
			a, _ := n.load(m)
			units := networkUnits(m, a)
			runtime := string(units["ssh-runtime"])
			for _, part := range []string{"ConditionPathExists=!/run/sshd", "ConditionPathIsSymbolicLink=!/run/sshd", "User=0", "Group=0", "ExecStart=/usr/bin/install -d -m 0755 -o root -g root -- /run/sshd"} {
				if !strings.Contains(runtime, part) {
					t.Fatal("runtime prerequisite lacks fixed guard", part)
				}
			}
			if strings.Contains(runtime, "RuntimeDirectory=") || strings.Contains(runtime, "rm ") || strings.Contains(runtime, m.Core) || strings.Contains(runtime, "/bin/sh") {
				t.Fatal("runtime prerequisite can alter/remove existing vendor state")
			}
			for _, role := range []string{"apply-actions", "ssh-restore"} {
				text := string(units[role])
				prepare := strings.Index(text, "ExecStart=/usr/bin/systemctl start -- "+networkUnit(m, a.ID, "ssh-runtime"))
				validate := strings.Index(text, "ExecStart=/usr/sbin/sshd -t")
				if prepare < 0 || validate < 0 || prepare >= validate {
					t.Fatal("runtime prerequisite must precede early SSH validation", role)
				}
			}
			original := hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
			switch variant {
			case "missing":
				delete(h.base.files, "/run/sshd")
			case "foreign-dir":
				original.UID = 1000
				h.base.files["/run/sshd"] = original
			case "foreign-file":
				original = hostFile{UID: 1000, GID: 1000, Regular: true, Mode: 0600, Data: []byte("foreign data")}
				h.base.files["/run/sshd"] = original
			case "foreign-symlink", "dangling-symlink":
				original = hostFile{UID: 0, GID: 0, Mode: os.ModeSymlink | 0777, Link: "/foreign-runtime"}
				h.base.files["/run/sshd"] = original
				if variant == "foreign-symlink" {
					h.base.files["/foreign-runtime"] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
				}
			}
			before := len(h.base.calls)
			if e := h.executeUnit(context.Background(), networkUnit(m, a.ID, "ssh-runtime")); e != nil {
				t.Fatal(e)
			}
			actual := h.base.files["/run/sshd"]
			if variant == "missing" {
				if !actual.Directory || actual.UID != 0 || actual.GID != 0 || actual.Mode.Perm() != 0755 {
					t.Fatal("missing vendor runtime not prepared safely")
				}
			} else {
				if len(h.base.calls) != before || actual.UID != original.UID || actual.GID != original.GID || actual.Mode != original.Mode || actual.Link != original.Link || !bytes.Equal(actual.Data, original.Data) {
					t.Fatal("existing vendor/foreign object changed")
				}
			}
		})
	}
}
