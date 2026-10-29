//go:build linux

package vpsinstall

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// Only the existing disposable QEMU runner may execute these OS-mutating cases.
// Fixture proof injection is private to this test binary, never a CLI option.
func networkVMGuard(t *testing.T) {
	t.Helper()
	if os.Getenv("FASED_DISPOSABLE_VM") != "1" {
		t.Skip("disposable Ubuntu VM only")
	}
	if os.Getuid() != 1000 || os.Geteuid() != os.Getuid() {
		t.Fatal("ordinary disposable owner required")
	}
	cwd, _ := os.Getwd()
	release, _ := os.ReadFile("/etc/os-release")
	product, _ := os.ReadFile("/sys/class/dmi/id/product_name")
	if cwd != "/home/ubuntu/fased" || !strings.Contains(string(release), "VERSION_ID=\"24.04\"") || !strings.Contains(string(release), "ID=ubuntu") || !(strings.Contains(string(product), "QEMU") || strings.Contains(string(product), "Standard PC")) {
		t.Fatal("finite Ubuntu24.04 QEMU fixture required")
	}
}

func networkVMOS(t *testing.T, ctx context.Context, input []byte, args ...string) []byte {
	t.Helper()
	// Fixed test-owned root operations, using OS utilities only; no root shell,
	// project executable, real credential or host networking is involved.
	if len(args) == 0 || (args[0] != "/usr/sbin/nft" && args[0] != "/usr/bin/install" && args[0] != "/usr/bin/cat" && args[0] != "/usr/bin/systemctl" && args[0] != "/usr/bin/mv") {
		t.Fatal("unknown fixture OS tool")
	}
	c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--"}, args...)...)
	c.Stdin = bytes.NewReader(input)
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture OS operation %s: %v: %s", args[0], err, b)
	}
	return b
}

func networkVMReceipt(t *testing.T, name string, data map[string]any) {
	t.Helper()
	dir := "/home/ubuntu/fased/.evidence/p4-fault-reboot/guest"
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	f, err := os.OpenFile(filepath.Join(dir, name+".json"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	d, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Sync()
	closeErr = d.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
}

func networkVMArmConsoleRescue(t *testing.T, ctx context.Context, m Manifest, a networkAttempt) {
	t.Helper()
	dir := networkAttemptRoot(m, a.ID)
	// This is the disposable VM's console recovery, not production policy. It
	// emits boot readiness on the emulated serial console, then restores access
	// after 45s using OS utilities only. No project code runs as root at boot.
	units := map[string]string{
		"fased-network-fixture-ready.service":  "[Unit]\nDescription=Disposable network boot marker\nAfter=" + networkUnit(m, a.ID, "persist") + "\nRequires=" + networkUnit(m, a.ID, "persist") + "\n[Service]\nType=oneshot\nExecCondition=/usr/bin/systemctl is-active --quiet " + networkUnit(m, a.ID, "persist") + "\nExecStart=/usr/bin/cat " + dir + "/commit.token\nStandardOutput=tty\nTTYPath=/dev/ttyS0\n[Install]\nWantedBy=multi-user.target\n",
		"fased-network-fixture-failed.service": "[Unit]\nDescription=Disposable failed-persistence boot marker\nAfter=" + networkUnit(m, a.ID, "persist") + "\n[Service]\nType=oneshot\nExecCondition=/usr/bin/systemctl is-failed --quiet " + networkUnit(m, a.ID, "persist") + "\nExecStart=/usr/bin/cat /var/lib/fased-network-fixture-failed-marker\nStandardOutput=tty\nTTYPath=/dev/ttyS0\n[Install]\nWantedBy=multi-user.target\n",
		"fased-network-fixture-rescue.service": "[Unit]\nDescription=Disposable provider-console rescue\n[Service]\nType=oneshot\nExecCondition=/usr/bin/cmp --silent -- " + dir + "/commit.token " + dir + "/outcome\nExecStart=-/usr/bin/mv --no-clobber -- /usr/sbin/nft.fased-fixture-held /usr/sbin/nft\nExecStart=/usr/bin/systemctl disable -- " + networkUnit(m, a.ID, "persist") + "\nExecStart=/usr/bin/rm -- " + dir + "/outcome\nExecStart=/usr/bin/systemctl start -- " + networkUnit(m, a.ID, "rollback") + "\nExecStart=/usr/bin/systemctl start -- ssh.socket ssh.service\n",
		"fased-network-fixture-rescue.timer":   "[Unit]\nDescription=Disposable provider-console rescue timer\n[Timer]\nOnBootSec=45s\nUnit=fased-network-fixture-rescue.service\n[Install]\nWantedBy=timers.target\n",
	}
	networkVMOS(t, ctx, []byte("Fased network "+a.ID+" persist-failed\n"), "/usr/bin/install", "-m", "0644", "-o", "root", "-g", "root", "/dev/stdin", "/var/lib/fased-network-fixture-failed-marker")
	for name, body := range units {
		networkVMOS(t, ctx, []byte(body), "/usr/bin/install", "-m", "0644", "-o", "root", "-g", "root", "/dev/stdin", "/etc/systemd/system/"+name)
	}
	networkVMOS(t, ctx, nil, "/usr/bin/systemctl", "daemon-reload")
	networkVMOS(t, ctx, nil, "/usr/bin/systemctl", "enable", "fased-network-fixture-ready.service", "fased-network-fixture-failed.service", "fased-network-fixture-rescue.timer")
	if err := exec.CommandContext(ctx, "/usr/bin/sync").Run(); err != nil {
		t.Fatal(err)
	}
}

func networkVMPublicSSHDrops(t *testing.T, ctx context.Context, m Manifest, a networkAttempt) float64 {
	t.Helper()
	b := networkVMOS(t, ctx, nil, "/usr/sbin/nft", "--json", "list", "table", "inet", networkTable(m, a))
	var data struct {
		NFTables []struct {
			Rule struct {
				Comment string `json:"comment"`
				Expr    []struct {
					Counter *struct {
						Packets float64 `json:"packets"`
					} `json:"counter"`
				} `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	for _, item := range data.NFTables {
		if item.Rule.Comment == "Fased public SSH" {
			for _, expr := range item.Rule.Expr {
				if expr.Counter != nil {
					return expr.Counter.Packets
				}
			}
		}
	}
	t.Fatal("missing actual kernel public-SSH counter")
	return 0
}

func TestNetworkUbuntuVM(t *testing.T) {
	networkVMGuard(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	core := "/home/ubuntu/fased/.evidence/bin/fased-app"
	b, err := os.ReadFile(core)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(b)
	sys := &custodyinstall.System{In: strings.NewReader(""), Out: os.Stderr}
	m, err := Install(ctx, Options{Core: core, CoreSHA256: hex.EncodeToString(hash[:]), Listen: "127.0.0.1:17446", PublicOrigin: "https://controlled.owner.ts.net", PrepareHost: true, Start: true}, sys)
	if err != nil {
		t.Fatal(err)
	}
	proof := networkAccess{SessionID: "controlled-private-connection-one", PeerIP: "100.90.0.1", ServerIP: "100.90.0.2", SSHPort: 22}
	// The private test clock makes the calendar deadline arrive in 30 seconds;
	// production still writes the fixed five-minute policy and has no override.
	n := networkController{ops: liveOps(sys), now: func() time.Time { return time.Now().Add(-270 * time.Second) }, proof: func(context.Context, Manifest) (networkAccess, error) { return proof, nil }}
	approval := NetworkApproval{PrivateUI: true, ConsoleRecovery: true}
	foreignSSH := "/etc/ssh/sshd_config.d/99-fased-network-foreign.conf"
	foreignBytes := []byte("# disposable unrelated configuration; preserve exactly\n")
	networkVMOS(t, ctx, foreignBytes, "/usr/bin/install", "-m", "0644", "-o", "root", "-g", "root", "/dev/stdin", foreignSSH)
	networkVMOS(t, ctx, []byte("table inet fased_network_foreign {\n chain marker {\n }\n}\n"), "/usr/sbin/nft", "-f", "/dev/stdin")
	foreignTable := networkVMOS(t, ctx, nil, "/usr/sbin/nft", "list", "table", "inet", "fased_network_foreign")
	assertForeign := func() {
		t.Helper()
		if got := networkVMOS(t, ctx, nil, "/usr/bin/cat", "--", foreignSSH); !bytes.Equal(got, foreignBytes) {
			t.Fatal("unrelated SSH file changed")
		}
		if got := networkVMOS(t, ctx, nil, "/usr/sbin/nft", "list", "table", "inet", "fased_network_foreign"); !bytes.Equal(got, foreignTable) {
			t.Fatal("unrelated firewall table changed")
		}
	}
	pending, err := n.begin(ctx, m, approval)
	if err != nil || pending.Phase != "pending" {
		t.Fatal("pending network policy", pending, err)
	}
	if _, err = n.confirm(ctx, m, pending.AttemptID); err == nil {
		t.Fatal("same private connection committed")
	}
	activeAttempt, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	beforeDrops := networkVMPublicSSHDrops(t, ctx, m, activeAttempt)
	// Existing SSH transport stays open. The host probes a NEW public connection
	// while these actual kernel rules are installed, then acknowledges over stdin.
	fmt.Fprintln(os.Stdout, "FASED_NETWORK_PENDING")
	ack := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(os.Stdin).ReadString('\n'); ack <- strings.TrimSpace(line) }()
	select {
	case line := <-ack:
		if line != "PUBLIC_SSH_BLOCKED" {
			t.Fatal("missing external connectivity proof", line)
		}
	case <-ctx.Done():
		t.Fatal("external connectivity proof deadline")
	}
	if drops := networkVMPublicSSHDrops(t, ctx, m, activeAttempt); drops <= beforeDrops {
		t.Fatal("new public SSH did not hit the actual kernel drop rule", beforeDrops, drops)
	}
	if wait := time.Until(pending.Deadline.Add(2 * time.Second)); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			t.Fatal("calendar timer deadline")
		}
	}
	trigger := networkVMOS(t, ctx, nil, "/usr/bin/systemctl", "show", "--property=LastTriggerUSec", "--value", networkUnit(m, pending.AttemptID, "rollback.timer"))
	if value := strings.TrimSpace(string(trigger)); value == "" || value == "n/a" {
		t.Fatal("actual OS timer did not fire")
	}
	rolledBack, err := n.inspect(ctx, m)
	if err != nil || rolledBack.Phase != "rolled-back" {
		t.Fatal("OS rollback target", rolledBack, err)
	}
	assertForeign()
	n.now = time.Now
	retry, err := n.begin(ctx, m, approval)
	if err != nil || retry.AttemptID == pending.AttemptID {
		t.Fatal("recoverable retry", retry, err)
	}
	proof.SessionID = "controlled-private-connection-two"
	committed, err := n.confirm(ctx, m, retry.AttemptID)
	if err != nil || committed.Phase != "committed" {
		t.Fatal("second private connection commit", committed, err)
	}
	if _, err = n.rollback(ctx, m); err != nil {
		t.Fatal("explicit committed rollback", err)
	}
	assertForeign()
	last, err := n.begin(ctx, m, approval)
	if err != nil || last.Phase != "pending" {
		t.Fatal("pending reboot preparation", last, err)
	}
	proof.SessionID = "controlled-private-connection-three"
	if committed, err = n.confirm(ctx, m, last.AttemptID); err != nil || committed.Phase != "committed" {
		t.Fatal("committed boot preparation", committed, err)
	}
	a, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	// The supported stock Ubuntu loader contains a global flush. Enable it for
	// the next boot, without flushing the active fixture policy now. Its bytes
	// remain unchanged; persistence must run after this foreign loader.
	vendorConfig, err := os.ReadFile("/etc/nftables.conf")
	if err != nil || !bytes.Contains(vendorConfig, []byte("flush ruleset")) {
		t.Fatal("stock Ubuntu nftables loader fixture unavailable", err)
	}
	vendorHash := sha256.Sum256(vendorConfig)
	networkVMOS(t, ctx, nil, "/usr/bin/systemctl", "enable", "nftables.service")
	networkVMArmConsoleRescue(t, ctx, m, a)
	assertForeign()
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	networkVMReceipt(t, "network-before-commit-boot", map[string]any{"result": "PASS", "fixtureOnly": true, "realWallets": false, "automaticUpdates": false, "attemptId": last.AttemptID, "boot": strings.TrimSpace(string(boot)), "vendorNftablesEnabled": true, "vendorNftablesConfigSHA256": hex.EncodeToString(vendorHash[:]), "osRollback": true, "calendarTimerExpiry": true, "timerFixtureOffsetSeconds": 270, "retry": true, "sameConnectionRefused": true, "secondFixtureConnectionCommit": true, "foreignPreservation": true, "accessProof": "private test seam; no real Tailscale enrollment"})
	fmt.Fprintln(os.Stdout, "FASED_NETWORK_COMMITTED", last.AttemptID)
}

func TestNetworkUbuntuVMCommittedBoot(t *testing.T) {
	networkVMGuard(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m, err := Load(os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	sys := &custodyinstall.System{In: strings.NewReader(""), Out: os.Stderr}
	proof := networkAccess{SessionID: "controlled-postboot-connection", PeerIP: "100.90.0.1", ServerIP: "100.90.0.2", SSHPort: 22}
	n := networkController{ops: liveOps(sys), now: time.Now, proof: func(context.Context, Manifest) (networkAccess, error) { return proof, nil }}
	status, err := n.inspect(ctx, m)
	if err != nil || status.Phase != "rolled-back" {
		t.Fatal("disposable console rescue", status, err)
	}
	data, err := os.ReadFile("/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/network-before-commit-boot.json")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if result["boot"] == strings.TrimSpace(string(boot)) {
		t.Fatal("not committed new kernel boot")
	}
	// Check actual systemd execution timestamps as well as the external SSH
	// denial observed before the disposable console rescue restored access.
	previousAttempt, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	monotonic := func(unit, property string) uint64 {
		t.Helper()
		raw := networkVMOS(t, ctx, nil, "/usr/bin/systemctl", "show", "--property="+property, "--value", unit)
		value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || value == 0 {
			t.Fatal("missing actual systemd timestamp", unit, property, err)
		}
		return value
	}
	for property, want := range map[string]string{"Result": "success", "ExecMainStatus": "0"} {
		value := networkVMOS(t, ctx, nil, "/usr/bin/systemctl", "show", "--property="+property, "--value", "nftables.service")
		if strings.TrimSpace(string(value)) != want {
			t.Fatal("stock vendor loader did not succeed", property, string(value))
		}
	}
	loaderEnd := monotonic("nftables.service", "ExecMainExitTimestampMonotonic")
	persistStart := monotonic(networkUnit(m, previousAttempt.ID, "persist"), "ExecMainStartTimestampMonotonic")
	if loaderEnd > persistStart {
		t.Fatal("vendor loader ran after committed policy", loaderEnd, persistStart)
	}
	vendorConfig, err := os.ReadFile("/etc/nftables.conf")
	vendorHash := sha256.Sum256(vendorConfig)
	if err != nil || hex.EncodeToString(vendorHash[:]) != result["vendorNftablesConfigSHA256"] {
		t.Fatal("vendor firewall configuration changed", err)
	}
	result["vendorLoaderBeforeCommittedPolicy"] = true
	result["vendorNftablesExitMonotonic"] = loaderEnd
	result["persistStartMonotonic"] = persistStart
	result["committedBoot"] = strings.TrimSpace(string(boot))
	result["disposableConsoleRescue"] = true
	pending, err := n.begin(ctx, m, NetworkApproval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil || pending.Phase != "pending" {
		t.Fatal("failed-persistence preparation", pending, err)
	}
	proof.SessionID = "controlled-postboot-connection-two"
	committed, err := n.confirm(ctx, m, pending.AttemptID)
	if err != nil || committed.Phase != "committed" {
		t.Fatal("fault boot commit", committed, err)
	}
	a, err := n.load(m)
	if err != nil {
		t.Fatal(err)
	}
	networkVMArmConsoleRescue(t, ctx, m, a)
	result["boot"] = strings.TrimSpace(string(boot))
	result["attemptId"] = pending.AttemptID
	networkVMReceipt(t, "network-before-failed-boot", result)
	// Simulate a missing OS tool only inside this throwaway VM. The OS-only
	// console rescue restores it and starts SSH after removing owned gates.
	networkVMOS(t, ctx, nil, "/usr/bin/mv", "--no-clobber", "--", "/usr/sbin/nft", "/usr/sbin/nft.fased-fixture-held")
	if _, err = os.Lstat("/usr/sbin/nft"); !os.IsNotExist(err) {
		t.Fatal("fixture nft failure was not armed", err)
	}
	if err = exec.CommandContext(ctx, "/usr/bin/sync").Run(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "FASED_NETWORK_FAULT_COMMITTED", pending.AttemptID)
}

func TestNetworkUbuntuVMFailedBoot(t *testing.T) {
	networkVMGuard(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m, err := Load(os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	sys := &custodyinstall.System{In: strings.NewReader(""), Out: os.Stderr}
	n := networkController{ops: liveOps(sys), now: time.Now, proof: func(context.Context, Manifest) (networkAccess, error) {
		return networkAccess{SessionID: "controlled-after-fault-boot", PeerIP: "100.90.0.1", ServerIP: "100.90.0.2", SSHPort: 22}, nil
	}}
	status, err := n.inspect(ctx, m)
	if err != nil || status.Phase != "rolled-back" {
		t.Fatal("failed-persistence console recovery", status, err)
	}
	data, err := os.ReadFile("/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/network-before-failed-boot.json")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if result["boot"] == strings.TrimSpace(string(boot)) {
		t.Fatal("not failed-persistence new kernel boot")
	}
	result["failedPersistBoot"] = strings.TrimSpace(string(boot))
	result["failedPersistConsoleRecovery"] = true
	pending, err := n.begin(ctx, m, NetworkApproval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil || pending.Phase != "pending" {
		t.Fatal("pending reboot preparation", pending, err)
	}
	result["boot"] = strings.TrimSpace(string(boot))
	result["attemptId"] = pending.AttemptID
	networkVMReceipt(t, "network-before-reboot", result)
}

func TestNetworkUbuntuVMPostBoot(t *testing.T) {
	networkVMGuard(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, err := Load(os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	sys := &custodyinstall.System{In: strings.NewReader(""), Out: os.Stderr}
	n := networkController{ops: liveOps(sys), now: time.Now, proof: networkProof}
	status, err := n.inspect(ctx, m)
	if err != nil || status.Phase != "rolled-back" {
		t.Fatal("pending boot rollback", status, err)
	}
	before, err := os.ReadFile("/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/network-before-reboot.json")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(before, &result); err != nil {
		t.Fatal(err)
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if result["boot"] == strings.TrimSpace(string(boot)) {
		t.Fatal("not a new kernel boot")
	}
	result["afterBoot"] = strings.TrimSpace(string(boot))
	result["pendingBootRollback"] = true
	result["phase"] = status.Phase
	networkVMReceipt(t, "network", result)
}
