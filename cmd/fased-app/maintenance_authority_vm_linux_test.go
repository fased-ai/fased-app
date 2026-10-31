//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
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
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

const maintenanceProbeUser = "fased-probe"
const maintenanceProbeHome = "/home/fased-probe"
const maintenanceProbeImage = "/var/lib/fased-maintenance-probe-input/fased-app-tests"
const maintenanceProbeSetup = "/etc/sudoers.d/98-fased-maintenance-probe-setup"
const maintenanceProbeMarker = "/run/fased-maintenance-authority-probe"
const maintenanceProbeID = "22222222222222222222222222222222"

func maintenanceVMGuard(t *testing.T, uid int) {
	t.Helper()
	if os.Getenv("FASED_DISPOSABLE_VM") != "1" {
		t.Skip("disposable Ubuntu VM only")
	}
	if os.Getuid() != uid || os.Geteuid() != uid {
		t.Fatal("ordinary isolated fixture identity required")
	}
	b, e := os.ReadFile("/sys/class/dmi/id/product_name")
	if e != nil || !(strings.Contains(string(b), "QEMU") || strings.Contains(string(b), "Standard PC")) {
		t.Fatal("QEMU only")
	}
}

func vmOSTool(ctx context.Context, tool string, input []byte, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--", tool}, args...)...)
	c.Stdin = bytes.NewReader(input)
	return c.CombinedOutput()
}

// This inert actor is test-binary-only. It proves the actual template's kernel
// UID/capability boundary without admitting a release or running an update.
func maintenanceAuthorityActorFixture(image string, args []string) (bool, error) {
	if image != "/var/lib/fased-app-vps-maintenance-1100/fased-app-maintenance" || len(args) == 0 || args[0] != "maintain-update" {
		return false, nil
	}
	if os.Getuid() != 1100 || os.Geteuid() != 1100 {
		return true, errors.New("fixture actor refuses privileged identity")
	}
	b, e := os.ReadFile(maintenanceProbeMarker)
	if e != nil || string(b) != "B01 disposable kernel authority only\n" {
		return false, nil
	}
	info, e := os.Lstat(maintenanceProbeMarker)
	if e != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return true, errors.New("root-owned disposable marker required")
	}
	m, e := vpsinstall.Load(1100)
	if e != nil {
		return true, e
	}
	if e = vpsinstall.ValidateApplicationMaintenanceEntry(m, image); e != nil {
		return true, e
	}
	status, e := os.ReadFile("/proc/self/status")
	if e != nil {
		return true, e
	}
	record := map[string]any{"uid": os.Getuid(), "euid": os.Geteuid(), "pid": os.Getpid(), "status": string(status), "inertActor": true, "signedUpdateExecuted": false}
	raw, _ := json.Marshal(record)
	if e = core.WritePrivateJSON(filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/kernel-actor.json"), json.RawMessage(raw)); e != nil {
		return true, e
	}
	time.Sleep(7 * time.Second)
	return true, nil
}

func TestRemoteMaintenanceAuthorityUbuntuVM(t *testing.T) {
	maintenanceVMGuard(t, 1000)
	if cwd, _ := os.Getwd(); cwd != "/home/ubuntu/fased" {
		t.Fatal("finite guest fixture workspace required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 130*time.Second)
	defer cancel()
	image, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	setup := func(tool string, input []byte, args ...string) {
		t.Helper()
		if b, e := vmOSTool(ctx, tool, input, args...); e != nil {
			t.Fatalf("guest OS setup %s: %v %s", tool, e, b)
		}
	}
	setup("/usr/sbin/useradd", nil, "--uid", "1100", "--user-group", "--create-home", "--home-dir", maintenanceProbeHome, "--shell", "/bin/bash", "--password", "!", maintenanceProbeUser)
	setup("/usr/bin/install", nil, "-d", "-m", "755", "-o", "root", "-g", "root", "--", "/var/lib/fased-maintenance-probe-input")
	setup("/usr/bin/install", nil, "-m", "755", "-o", "root", "-g", "root", "--", image, maintenanceProbeImage)
	setup("/usr/bin/install", []byte("B01 disposable kernel authority only\n"), "-m", "644", "-o", "root", "-g", "root", "--", "/dev/stdin", maintenanceProbeMarker)
	policy := "Defaults:" + maintenanceProbeUser + " verifypw=any\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/install, /usr/bin/mkdir, /usr/bin/ln, /usr/bin/mv, /usr/bin/sync, /usr/bin/rm, /usr/bin/cat, /usr/bin/find, /usr/bin/systemctl, /usr/sbin/useradd, /usr/sbin/apparmor_parser\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/passwd -S fased-vps-1100\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/apt-get install --yes --no-install-recommends polkitd\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/setpriv ^--.*$\n"
	setup("/usr/bin/install", []byte(policy), "-m", "440", "-o", "root", "-g", "root", "--", "/dev/stdin", maintenanceProbeSetup)
	defer func() {
		for _, path := range []string{maintenanceProbeSetup, maintenanceProbeMarker, "/etc/polkit-1/rules.d/60-fased-vps-1100-updates.rules", "/etc/sudoers.d/fased-vps-1100-updates"} {
			_, _ = vmOSTool(context.Background(), "/usr/bin/rm", nil, "--", path)
		}
	}()
	args := []string{"-n", "--", "/usr/sbin/runuser", "-u", maintenanceProbeUser, "--", "/usr/bin/env", "--ignore-environment", "--chdir=" + maintenanceProbeHome, "HOME=" + maintenanceProbeHome, "XDG_CONFIG_HOME=" + maintenanceProbeHome + "/.config", "PATH=/usr/bin:/bin", "FASED_DISPOSABLE_VM=1", "FASED_MAINTENANCE_OWNER=1", maintenanceProbeImage, "-test.run=TestRemoteMaintenanceOwnerUbuntuVM", "-test.v"}
	child := exec.CommandContext(ctx, "/usr/bin/sudo", args...)
	in, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	pipe, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var diagnostic bytes.Buffer
	child.Stderr = &diagnostic
	// sudo/runuser descendants can retain stderr after the tested child exits.
	// Close the ACK/scanner pipes before waiting so cancellation cannot strand
	// the owner in ReadString or hide a failed assertion behind inherited FDs.
	child.WaitDelay = 2 * time.Second
	child.Cancel = func() error {
		_ = in.Close()
		_ = pipe.Close()
		return child.Process.Kill()
	}
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_ = in.Close()
		_ = pipe.Close()
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var m vpsinstall.Manifest
	prepared, limited, appProof := false, false, false
	var output strings.Builder
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(&output, line)
		if strings.HasPrefix(line, "FASED_MAINTENANCE_PREPARED ") {
			if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "FASED_MAINTENANCE_PREPARED ")), &m); e != nil {
				t.Fatal(e)
			}
			setup("/usr/bin/rm", nil, "--", maintenanceProbeSetup)
			prepared = true
			_, _ = io.WriteString(in, "LIMITED_POLICY_ONLY\n")
		}
		if line == "FASED_MAINTENANCE_LIMITED_PASS" {
			limited = true
			probeArgs := []string{"-n", "--", "/usr/bin/setpriv", "--reuid", strconv.Itoa(m.AppUID), "--regid", strconv.Itoa(m.AppGID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", "/usr/bin/env", "--ignore-environment", "--chdir=/", "HOME=/nonexistent", "PATH=/usr/bin:/bin", "FASED_DISPOSABLE_VM=1", maintenanceProbeImage, "-test.run=^TestRemoteMaintenanceAppUbuntuVM$", "-test.v"}
			b, e := exec.CommandContext(ctx, "/usr/bin/sudo", probeArgs...).CombinedOutput()
			if e != nil {
				t.Fatalf("actual NNP AppUID Polkit probe: %v %s", e, b)
			}
			fmt.Fprint(&output, string(b))
			appProof = bytes.Contains(b, []byte("FASED_MAINTENANCE_APP_PASS"))
			var actor struct {
				UID, EUID, PID int
				InertActor     bool `json:"inertActor"`
			}
			for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				raw, err := vmOSTool(ctx, "/usr/bin/cat", nil, "--", maintenanceProbeHome+"/.config/fased-app-vps-installer/kernel-actor.json")
				if err == nil && json.Unmarshal(raw, &actor) == nil && actor.UID == 1100 && actor.EUID == 1100 && actor.InertActor {
					break
				}
			}
			if actor.UID != 1100 || actor.EUID != 1100 || !actor.InertActor {
				t.Fatal("fixed owner actor did not validate installed image/environment")
			}
			_, _ = io.WriteString(in, "APP_PROBE_COMPLETE\n")
		}
	}
	if e = child.Wait(); e != nil || scanner.Err() != nil || !prepared || !limited || !appProof {
		t.Fatalf("authority probe failed %v %v %s %s", e, scanner.Err(), output.String(), diagnostic.String())
	}
	for _, unit := range []string{"fased-vps-maintenance-1100@" + maintenanceProbeID + ".service", m.Unit} {
		_, _ = vmOSTool(ctx, "/usr/bin/systemctl", nil, "stop", "--", unit)
	}
	receipt := map[string]any{"result": "PASS", "fixtureOnly": true, "realWallets": false, "publicActivation": false, "ownerUID": 1100, "appUID": m.AppUID, "temporarySetupGrantRemoved": true, "isolatedOwnerNoSudoGroup": true, "unrelatedRootCommandDenied": true, "foreignServiceDenied": true, "boundedRootImportAndOwnServiceOperations": true, "appUIDNoNewPrivilegesPolkitStart": true, "actualOwnerTemplateUID": 1100, "inertActor": true, "signedInstalledUpdateAcceptance": false, "output": output.String()}
	raw, _ := json.MarshalIndent(receipt, "", "  ")
	if e = os.WriteFile(".evidence/p4-fault-reboot/guest/maintenance-authority.json", append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestRemoteMaintenanceOwnerUbuntuVM(t *testing.T) {
	maintenanceVMGuard(t, 1100)
	if os.Getenv("FASED_MAINTENANCE_OWNER") != "1" {
		t.Skip("isolated controller child only")
	}
	groups, e := exec.Command("/usr/bin/id", "-Gn").Output()
	if e != nil || strings.Contains(string(groups), "sudo") {
		t.Fatal("isolated owner inherited broad administrator group", e, string(groups))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 110*time.Second)
	defer cancel()
	image, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(image)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	system := &custodyinstall.System{In: strings.NewReader(""), Out: os.Stderr}
	m, e := vpsinstall.Install(ctx, vpsinstall.Options{Core: image, CoreSHA256: hex.EncodeToString(sum[:]), Listen: "127.0.0.1:17446", PrepareHost: true, Start: true}, system)
	if e != nil {
		t.Fatal(e)
	}
	if e = vpsinstall.InstallApplicationMaintenance(ctx, m, system); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(m)
	fmt.Println("FASED_MAINTENANCE_PREPARED " + string(b))
	reader := bufio.NewReader(os.Stdin)
	line, e := reader.ReadString('\n')
	if e != nil || line != "LIMITED_POLICY_ONLY\n" {
		t.Fatal("missing controller grant-removal acknowledgement", e)
	}
	if _, e = exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", "/usr/bin/id", "-u").Output(); e == nil {
		t.Fatal("unrelated root command allowed")
	}
	if _, e = exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", "/usr/bin/systemctl", "stop", "--", "ssh.service").Output(); e == nil {
		t.Fatal("foreign system service action allowed")
	}
	limited := &custodyinstall.System{NonInteractive: true, Out: os.Stderr}
	id := update.NewProbe()
	dir := m.Root + "-update-" + id
	pending := dir + "/.next-install.json-" + id + ".pending"
	for _, op := range [][]string{{"/usr/bin/install", "-d", "-m", "755", "-o", "0", "-g", "0", "--", dir}, {"/usr/bin/install", "-m", "644", "-o", "root", "-g", "root", "--", "/dev/stdin", pending}, {"/usr/bin/sync", "-f", pending}, {"/usr/bin/ln", "--no-target-directory", "--", pending, dir + "/next-install.json"}, {"/usr/bin/rm", "--", pending}} {
		if b, e = limited.Run(ctx, []byte("{\"inertPublicFixture\":true}"), op...); e != nil {
			t.Fatalf("bounded root operation failed %v %s", e, b)
		}
	}
	info, e := os.Stat(dir + "/next-install.json")
	if e != nil || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm() != 0644 {
		t.Fatal("root import identity not enforced", e)
	}
	if e = vpsinstall.Stop(ctx, m, limited); e != nil {
		t.Fatal(e)
	}
	if e = vpsinstall.Start(ctx, m, limited); e != nil {
		t.Fatal("own service readiness under limited policy", e)
	}
	fmt.Println("FASED_MAINTENANCE_LIMITED_PASS")
	line, e = reader.ReadString('\n')
	if e != nil || line != "APP_PROBE_COMPLETE\n" {
		t.Fatal("missing app proof acknowledgement", e)
	}
}

func TestRemoteMaintenanceAppUbuntuVM(t *testing.T) {
	if os.Getenv("FASED_DISPOSABLE_VM") != "1" {
		t.Skip("disposable AppUID only")
	}
	var m vpsinstall.Manifest
	b, e := os.ReadFile(vpsinstall.ConfigPath(1100))
	if e != nil || json.Unmarshal(b, &m) != nil || os.Getuid() != m.AppUID || os.Geteuid() != m.AppUID {
		t.Fatal("dedicated app UID required", e)
	}
	status, e := os.ReadFile("/proc/self/status")
	if e != nil || !bytes.Contains(status, []byte("NoNewPrivs:\t1\n")) {
		t.Fatal("actual NoNewPrivileges app required", e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 14*time.Second)
	defer cancel()
	if _, e = exec.CommandContext(ctx, "/usr/bin/systemctl", "--no-ask-password", "stop", "--", "ssh.service").Output(); e == nil {
		t.Fatal("AppUID received foreign service control")
	}
	if _, e = vpsinstall.LaunchApplicationMaintenance(ctx, m, maintenanceProbeID, nil); e != nil {
		t.Fatal("NoNewPrivileges AppUID could not start fixed owner template", e)
	}
	unit := "fased-vps-maintenance-1100@" + maintenanceProbeID + ".service"
	var pid int
	var proc []byte
	var procErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		b, e = exec.CommandContext(ctx, "/usr/bin/systemctl", "show", unit, "--property=MainPID", "--value").Output()
		if e == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 {
				// systemd publishes MainPID before its child drops credentials and
				// execs the fixed image. Observe the final ordinary process, not
				// that transient OS pre-exec child.
				proc, procErr = os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
				if procErr == nil && bytes.Contains(proc, []byte("Uid:\t1100\t1100\t1100\t1100\n")) && bytes.Contains(proc, []byte("CapEff:\t0000000000000000\n")) {
					break
				}
			}
		}
	}
	if procErr != nil || !bytes.Contains(proc, []byte("Uid:\t1100\t1100\t1100\t1100\n")) || !bytes.Contains(proc, []byte("CapEff:\t0000000000000000\n")) {
		t.Fatal("actual owner actor UID/capability proof missing", pid, procErr, string(proc))
	}
	fmt.Println("FASED_MAINTENANCE_APP_PASS")
}
