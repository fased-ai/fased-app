//go:build linux

package vpsinstall

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type networkAccess struct {
	SessionID, PeerIP, ServerIP string
	SSHPort                     int
}
type networkAttempt struct {
	Version        int           `json:"version"`
	ID             string        `json:"id"`
	ManifestSHA256 string        `json:"manifestSha256"`
	Access         networkAccess `json:"access"`
	StartedAt      time.Time     `json:"startedAt"`
	Deadline       time.Time     `json:"deadline"`
}
type networkPointer struct {
	Version                   int
	AttemptID, ManifestSHA256 string
	Attempt                   networkAttempt
}

func networkRoot(m Manifest) string {
	return fmt.Sprintf("/var/lib/fased-app-vps-network-%d", m.OwnerUID)
}
func networkAttemptRoot(m Manifest, id string) string { return networkRoot(m) + "/attempt-" + id }
func networkUnit(m Manifest, id, role string) string {
	suffix := ".service"
	if strings.HasSuffix(role, ".timer") {
		suffix = ""
	}
	return fmt.Sprintf("fased-vps-net-%d-%s-%s%s", m.OwnerUID, id, role, suffix)
}
func networkTable(m Manifest, a networkAttempt) string {
	return fmt.Sprintf("fased_vps_%d_%s_%s", m.OwnerUID, m.ID[:12], a.ID)
}
func networkSSH(m Manifest, a networkAttempt) string {
	return fmt.Sprintf("/etc/ssh/sshd_config.d/00-fased-vps-%d-%s.conf", m.OwnerUID, a.ID)
}
func networkSSHStage(m Manifest, a networkAttempt) string {
	return fmt.Sprintf("/etc/ssh/sshd_config.d/.fased-vps-%d-%s.pending", m.OwnerUID, a.ID)
}
func networkToken(a networkAttempt, phase string) []byte {
	return []byte("Fased network " + a.ID + " " + phase + "\n")
}
func networkSSHBytes(m Manifest, a networkAttempt) []byte {
	return []byte("# Fased app-only Remote " + m.ID + " attempt " + a.ID + "\nPermitRootLogin no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n")
}
func networkRules(m Manifest, a networkAttempt) []byte {
	table := networkTable(m, a)
	lines := []string{
		"create table inet " + table + " { comment \"Fased Remote " + m.ID + " attempt " + a.ID + "\"; }",
		"add chain inet " + table + " input { type filter hook input priority filter; policy drop; }",
	}
	for _, rule := range []string{
		"ct state established,related accept",
		"iifname \"lo\" accept",
		"iifname \"tailscale0\" tcp dport { " + strconv.Itoa(a.Access.SSHPort) + ", 443 } accept",
		"udp dport 41641 accept",
		"meta nfproto ipv4 udp sport 67 udp dport 68 accept",
		"ip6 saddr fe80::/10 udp sport 547 udp dport 546 accept",
		"meta l4proto { icmp, ipv6-icmp } accept",
		"tcp dport 22 counter drop comment \"Fased public SSH\"",
	} {
		lines = append(lines, "add rule inet "+table+" input "+rule)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func networkRoles() []string {
	return []string{"expected", "observed", "apply-actions", "apply", "ssh-remove", "ssh-hup", "ssh-runtime", "ssh-restore", "nft-restore", "complete", "rollback-actions", "rollback", "persist", "rollback.timer"}
}
func networkUnits(m Manifest, a networkAttempt) map[string][]byte {
	dir := networkAttemptRoot(m, a.ID)
	u := func(role string) string { return networkUnit(m, a.ID, role) }
	cmp := func(left, right string) string { return "/usr/bin/cmp --silent -- " + left + " " + right }
	start := func(role string) string { return "/usr/bin/systemctl start -- " + u(role) }
	base := func(extra, commands, install string) []byte {
		return []byte("[Unit]\nDescription=Fased Remote owned network boundary\nDefaultDependencies=no\nAfter=local-fs.target\n" + extra + "\n[Service]\nType=oneshot\nUser=0\nGroup=0\nUMask=0022\nTimeoutStartSec=45\n" + commands + "\n" + install)
	}
	// Captures have exactly one command: truncate output must never be reopened by
	// a later ExecStartPost. Stateless listing has no counters or handles.
	capture := func(path string) []byte {
		return base("", "ExecStart=/usr/sbin/nft --stateless list table inet "+networkTable(m, a)+"\nStandardOutput=truncate:"+path+"\n", "")
	}
	units := map[string][]byte{"expected": capture(dir + "/expected.nft"), "observed": capture(dir + "/observed.nft")}
	units["apply-actions"] = base("ConditionPathExists=!"+dir+"/outcome\n", "ExecStart=/usr/sbin/nft --check --file "+dir+"/rules.nft\nExecStart=/usr/sbin/nft --file "+dir+"/rules.nft\nExecStart="+start("expected")+"\nExecStart=/usr/bin/ln --no-target-directory -- "+networkSSHStage(m, a)+" "+networkSSH(m, a)+"\nExecStart="+start("ssh-runtime")+"\nExecStart=/usr/sbin/sshd -t\nExecStart="+start("ssh-hup")+"\n", "")
	units["apply"] = base("", "ExecStart=/usr/bin/flock --exclusive --no-fork "+dir+"/private/lock "+start("apply-actions")+"\nTimeoutStartSec=infinity\n", "")

	units["ssh-remove"] = base("ConditionPathExists="+networkSSH(m, a)+"\n", "ExecStart="+cmp(dir+"/ssh.conf", networkSSH(m, a))+"\nExecStart=/usr/bin/rm -- "+networkSSH(m, a)+"\n", "")
	units["ssh-runtime"] = base("ConditionPathExists=!/run/sshd\nConditionPathIsSymbolicLink=!/run/sshd\n", "ExecStart=/usr/bin/install -d -m 0755 -o root -g root -- /run/sshd\n", "")
	units["ssh-hup"] = base("", "ExecCondition=/usr/bin/systemctl is-active --quiet ssh.service\nExecStart=/usr/bin/systemctl kill --kill-whom=main --signal=HUP ssh.service\n", "")
	guard := "ExecCondition=" + cmp(dir+"/rollback.token", dir+"/outcome") + "\n"
	units["ssh-restore"] = base("", guard+"ExecStart="+start("ssh-remove")+"\nExecStart="+start("ssh-runtime")+"\nExecStart=/usr/sbin/sshd -t\nExecStart="+start("ssh-hup")+"\nExecStart=/usr/bin/install -m 0644 -o root -g root -- /dev/null "+dir+"/ssh.done\n", "")
	// A different nft snapshot prevents deletion. SSH restoration runs in its own
	// unit so unrelated firewall edits cannot prevent the SSH recovery attempt.
	units["nft-restore"] = base("", guard+"ExecStart="+start("observed")+"\nExecStart="+cmp(dir+"/expected.nft", dir+"/observed.nft")+"\nExecStart=/usr/sbin/nft --file "+dir+"/rollback.nft\nExecStart=/usr/bin/install -m 0644 -o root -g root -- /dev/null "+dir+"/nft.done\n", "")
	units["complete"] = base("", guard+"ExecStart="+cmp(dir+"/empty", dir+"/ssh.done")+"\nExecStart="+cmp(dir+"/empty", dir+"/nft.done")+"\nExecStart=/usr/bin/install -m 0644 -o root -g root -- /dev/null "+dir+"/restored.done\n", "")
	// The outcome link is the cross-process linearization point: timer/boot claim
	// rollback before changes; an existing commit cannot be overwritten by them.
	units["rollback-actions"] = base("", "ExecStart=-/usr/bin/ln --no-target-directory -- "+dir+"/rollback.token "+dir+"/outcome\nExecStart=-"+start("ssh-restore")+"\nExecStart=-"+start("nft-restore")+"\nExecStart=-"+start("complete")+"\n", "")
	units["rollback"] = base("Before=network-pre.target ssh.socket ssh.service "+u("persist")+"\nWants=network-pre.target\n", "ExecStart=/usr/bin/flock --exclusive --no-fork "+dir+"/private/lock "+start("rollback-actions")+"\nTimeoutStartSec=infinity\n", "[Install]\nWantedBy=multi-user.target\n")

	// Ubuntu's vendor loader may flush the ruleset. When enabled, it must
	// finish before the committed table is restored and SSH can activate.
	units["persist"] = base("After="+u("rollback")+" nftables.service\nBefore=network-pre.target ssh.socket ssh.service\nWants=network-pre.target\n", "ExecCondition="+cmp(dir+"/commit.token", dir+"/outcome")+"\nExecStart=/usr/sbin/nft --check --file "+dir+"/rules.nft\nExecStart=/usr/sbin/nft --file "+dir+"/rules.nft\nRemainAfterExit=yes\n", "[Install]\nWantedBy=multi-user.target\nRequiredBy=ssh.socket ssh.service\n")
	units["rollback.timer"] = []byte("[Unit]\nDescription=Fased Remote pending network rollback\n\n[Timer]\nOnActiveSec=300s\nOnCalendar=" + a.Deadline.UTC().Format("2006-01-02 15:04:05 UTC") + "\nAccuracySec=1s\nPersistent=true\nUnit=" + u("rollback") + "\n\n[Install]\nWantedBy=timers.target\n")
	return units
}
func networkStaticFiles(m Manifest, a networkAttempt) map[string][]byte {
	files := map[string][]byte{"attempt.json": encoded(a), "rules.nft": networkRules(m, a), "rollback.nft": []byte("delete table inet " + networkTable(m, a) + "\n"), "ssh.conf": networkSSHBytes(m, a), "commit.token": networkToken(a, "commit"), "rollback.token": networkToken(a, "rollback"), "empty": {}}
	for role, b := range networkUnits(m, a) {
		files[networkUnit(m, a.ID, role)] = b
	}
	return files
}
