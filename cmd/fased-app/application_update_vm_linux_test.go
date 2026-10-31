//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

const applicationUpdateVMInputs = "/var/lib/fased-update-fixture"
const applicationUpdateVMImage = applicationUpdateVMInputs + "/fased-app-tests"
const applicationUpdateVMGuest = ".evidence/b01-connected-install/installed-remote"
const applicationUpdateVMSetup = "/etc/sudoers.d/98-fased-update-fixture-setup"

// The controller runs as the disposable ubuntu account. Root executes only the
// finite OS setup tools; the product and its maintenance actor always use their
// separate ordinary identities. The setup grant is removed before launch.
func TestApplicationUpdateInstalledRemotePrepareUbuntuVM(t *testing.T) {
	maintenanceVMGuard(t, 1000)
	if cwd, _ := os.Getwd(); cwd != "/home/ubuntu/fased" {
		t.Fatal("finite guest fixture workspace required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 130*time.Second)
	defer cancel()
	setup := func(tool string, input []byte, args ...string) {
		t.Helper()
		if b, e := vmOSTool(ctx, tool, input, args...); e != nil {
			t.Fatalf("guest OS setup %s: %v %s", tool, e, b)
		}
	}
	setup("/usr/sbin/useradd", nil, "--uid", "1100", "--user-group", "--create-home", "--home-dir", maintenanceProbeHome, "--shell", "/bin/bash", "--password", "!", maintenanceProbeUser)
	input := filepath.Join(applicationUpdateVMGuest, "inputs")
	allowed := map[string]bool{"fased-app-tests": true, "seed.json": true, "fixture.json": true, "ca.pem": true, "cert.pem": true, "key.pem": true, "repo": true}
	if e := filepath.WalkDir(input, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(input, path)
		if e != nil {
			return e
		}
		first := strings.Split(rel, string(filepath.Separator))[0]
		if rel != "." && !allowed[first] || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unknown fixture input %q", rel)
		}
		target := filepath.Join(applicationUpdateVMInputs, rel)
		if entry.IsDir() {
			setup("/usr/bin/install", nil, "-d", "-m", "755", "-o", "root", "-g", "root", "--", target)
			return nil
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("regular fixture input required: %s", rel)
		}
		mode := "644"
		if rel == "fased-app-tests" {
			mode = "755"
		}
		setup("/usr/bin/install", nil, "-m", mode, "-o", "root", "-g", "root", "--", path, target)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	policy := "Defaults:" + maintenanceProbeUser + " verifypw=any\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/install, /usr/bin/mkdir, /usr/bin/ln, /usr/bin/mv, /usr/bin/sync, /usr/bin/rm, /usr/bin/cat, /usr/bin/find, /usr/bin/systemctl, /usr/sbin/useradd, /usr/sbin/apparmor_parser\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/passwd -S fased-vps-1100\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/apt-get install --yes --no-install-recommends polkitd\n" + maintenanceProbeUser + " ALL=(root) NOPASSWD: /usr/bin/setpriv ^--.*$\n"
	setup("/usr/bin/install", []byte(policy), "-m", "440", "-o", "root", "-g", "root", "--", "/dev/stdin", applicationUpdateVMSetup)
	// Failure cleanup is also finite. A successful launch intentionally keeps its
	// protected install and unit on this disk so the next boot resumes that ID.
	defer func() {
		_, _ = vmOSTool(context.Background(), "/usr/bin/rm", nil, "-f", "--", applicationUpdateVMSetup)
	}()
	var manifest vpsinstall.Manifest
	prepared, launched := false, false
	output := applicationUpdateVMRunOwner(t, ctx, "TestApplicationUpdateInstalledRemoteOwnerUbuntuVM", func(line string, in io.Writer) {
		if strings.HasPrefix(line, "FASED_UPDATE_PREPARED ") {
			if prepared || json.Unmarshal([]byte(strings.TrimPrefix(line, "FASED_UPDATE_PREPARED ")), &manifest) != nil || manifest.OwnerUID != 1100 || manifest.AppUID == 1100 || manifest.AppUID <= 0 {
				t.Fatal("invalid or repeated prepared installation")
			}
			setup("/usr/bin/rm", nil, "--", applicationUpdateVMSetup)
			prepared = true
			_, _ = io.WriteString(in, "LIMITED_POLICY_ONLY\n")
		}
		if line == "FASED_UPDATE_LAUNCHED" {
			launched = true
		}
	})
	if !prepared || !launched {
		t.Fatal("signed update was not prepared and launched", output)
	}
	applicationUpdateVMReceipt(t, "prepared", map[string]any{"result": "PASS", "fixtureOnly": true, "temporarySetupGrantRemoved": true, "manifest": manifest})
}

func TestApplicationUpdateInstalledRemoteOwnerUbuntuVM(t *testing.T) {
	maintenanceVMGuard(t, 1100)
	if os.Getenv("FASED_UPDATE_OWNER") != "1" || os.Getenv("HOME") != maintenanceProbeHome {
		t.Fatal("fixed ordinary owner launch required")
	}
	groups, e := exec.Command("/usr/bin/id", "-Gn").Output()
	if e != nil || strings.Contains(" "+strings.TrimSpace(string(groups))+" ", " sudo ") {
		t.Fatal("isolated owner inherited broad administrator group", e, string(groups))
	}
	m, e := applicationUpdateVMOwnerPrepare(t, applicationUpdateVMInputs)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(m)
	fmt.Println("FASED_UPDATE_PREPARED " + string(b))
	line, e := bufio.NewReader(os.Stdin).ReadString('\n')
	if e != nil || line != "LIMITED_POLICY_ONLY\n" {
		t.Fatal("missing setup grant removal acknowledgement", e)
	}
	if _, e = os.Stat(applicationUpdateVMSetup); !os.IsNotExist(e) {
		t.Fatal("temporary setup authority remains", e)
	}
	for _, args := range [][]string{{"-n", "--", "/usr/bin/id", "-u"}, {"-n", "--", "/usr/bin/systemctl", "stop", "--", "ssh.service"}} {
		if b, e := exec.Command("/usr/bin/sudo", args...).CombinedOutput(); e == nil {
			t.Fatalf("unrelated privileged action allowed: %v %s", args, b)
		}
	}
	if e = applicationUpdateVMOwnerLaunch(t, m); e != nil {
		t.Fatal(e)
	}
	fmt.Println("FASED_UPDATE_LAUNCHED")
}

func TestApplicationUpdateInstalledRemoteVerifyUbuntuVM(t *testing.T) {
	maintenanceVMGuard(t, 1000)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var result map[string]any
	output := applicationUpdateVMRunOwner(t, ctx, "TestApplicationUpdateInstalledRemoteOwnerVerifyUbuntuVM", func(line string, _ io.Writer) {
		if strings.HasPrefix(line, "FASED_UPDATE_VERIFIED ") {
			if result != nil || json.Unmarshal([]byte(strings.TrimPrefix(line, "FASED_UPDATE_VERIFIED ")), &result) != nil {
				t.Fatal("invalid or repeated verified result")
			}
		}
	})
	if result == nil || result["result"] != "PASS" || result["fixtureOnly"] != true {
		t.Fatal("installed update recovery did not complete", output)
	}
	var prepared struct {
		Manifest vpsinstall.Manifest `json:"manifest"`
	}
	b, e := os.ReadFile(filepath.Join(applicationUpdateVMGuest, "prepared.json"))
	if e != nil || json.Unmarshal(b, &prepared) != nil || prepared.Manifest.OwnerUID != 1100 || prepared.Manifest.Data != "/var/lib/fased-app-vps-1100/app-state" {
		t.Fatal("exact original installed identity required", e)
	}
	// Only the controlling ubuntu fixture reads the app-owned ciphertext. The
	// limited maintenance grant never gives owner1100 a broad private-data cat.
	b, e = vmOSTool(ctx, "/usr/bin/cat", nil, "--", maintenanceProbeHome+"/.config/fased-app-vps-installer/update-fixture-before.json")
	var before struct {
		CiphertextSHA256 string `json:"ciphertextSha256"`
	}
	if e != nil || json.Unmarshal(b, &before) != nil || len(before.CiphertextSHA256) != 64 {
		t.Fatal("original ciphertext proof required", e)
	}
	b, e = vmOSTool(ctx, "/usr/bin/cat", nil, "--", prepared.Manifest.Data+"/ai-credentials.json")
	if e != nil {
		t.Fatal("read disposable encrypted credential proof", e)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(b))
	if digest != before.CiphertextSHA256 {
		t.Fatal("installed update changed encrypted credential bytes")
	}
	result["ciphertextPreserved"] = true
	result["ciphertextSha256"] = digest
	var cut, resumed struct {
		Result           string `json:"result"`
		ID               string `json:"id"`
		BootID           string `json:"bootId"`
		OwnerLeaseSHA256 string `json:"ownerLeaseSha256"`
		CheckpointSHA256 string `json:"checkpointSha256"`
	}
	for name, dst := range map[string]any{"cut": &cut, "resumed": &resumed} {
		b, e = vmOSTool(ctx, "/usr/bin/cat", nil, "--", prepared.Manifest.Data+"/update-fixture-"+name+".json")
		if e != nil || json.Unmarshal(b, dst) != nil {
			t.Fatal("retained exact-operation recovery proof missing", name, e)
		}
	}
	if cut.Result != "CUT" || resumed.Result != "RESUMED" || cut.ID != result["id"] || cut.ID != resumed.ID || cut.BootID == "" || resumed.BootID == "" || cut.BootID == resumed.BootID || len(cut.OwnerLeaseSHA256) != 64 || cut.OwnerLeaseSHA256 != resumed.OwnerLeaseSHA256 || len(cut.CheckpointSHA256) != 64 || cut.CheckpointSHA256 != resumed.CheckpointSHA256 {
		t.Fatal("original lease/checkpoint was replaced or recovery did not cross reboot")
	}
	result["originalCheckpointPreserved"] = true
	result["ownerLeaseSha256"] = cut.OwnerLeaseSHA256
	result["checkpointSha256"] = cut.CheckpointSHA256
	result["cutBootId"] = cut.BootID
	result["resumedBootId"] = resumed.BootID
	applicationUpdateVMReceipt(t, "verified", result)
}

func TestApplicationUpdateInstalledRemoteOwnerVerifyUbuntuVM(t *testing.T) {
	maintenanceVMGuard(t, 1100)
	if os.Getenv("FASED_UPDATE_OWNER") != "1" {
		t.Fatal("fixed ordinary owner launch required")
	}
	if _, e := os.Stat(applicationUpdateVMSetup); !os.IsNotExist(e) {
		t.Fatal("temporary setup authority reappeared", e)
	}
	m, e := vpsinstall.Load(1100)
	if e != nil {
		t.Fatal(e)
	}
	result, e := applicationUpdateVMOwnerVerify(t, m)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(result)
	fmt.Println("FASED_UPDATE_VERIFIED " + string(b))
}

func applicationUpdateVMRunOwner(t *testing.T, ctx context.Context, name string, line func(string, io.Writer)) string {
	t.Helper()
	if name != "TestApplicationUpdateInstalledRemoteOwnerUbuntuVM" && name != "TestApplicationUpdateInstalledRemoteOwnerVerifyUbuntuVM" {
		t.Fatal("fixed owner fixture action required")
	}
	args := []string{"-n", "--", "/usr/sbin/runuser", "-u", maintenanceProbeUser, "--", "/usr/bin/env", "--ignore-environment", "--chdir=" + maintenanceProbeHome, "HOME=" + maintenanceProbeHome, "XDG_CONFIG_HOME=" + maintenanceProbeHome + "/.config", "PATH=/usr/bin:/bin", "FASED_DISPOSABLE_VM=1", "FASED_UPDATE_OWNER=1", applicationUpdateVMImage, "-test.run=" + name, "-test.v"}
	child := exec.CommandContext(ctx, "/usr/bin/sudo", args...)
	in, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	pipe, e := child.StdoutPipe()
	if e != nil {
		_ = in.Close()
		t.Fatal(e)
	}
	var diagnostic bytes.Buffer
	child.Stderr = &diagnostic
	child.WaitDelay = 2 * time.Second
	child.Cancel = func() error { _ = in.Close(); _ = pipe.Close(); return child.Process.Kill() }
	if e = child.Start(); e != nil {
		_ = in.Close()
		_ = pipe.Close()
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
	var output strings.Builder
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		text := scanner.Text()
		fmt.Fprintln(&output, text)
		line(text, in)
	}
	_ = in.Close()
	_ = pipe.Close()
	if e = child.Wait(); e != nil || scanner.Err() != nil {
		t.Fatalf("ordinary owner fixture failed: %v %v\n%s\n%s", e, scanner.Err(), output.String(), diagnostic.String())
	}
	return output.String()
}

func applicationUpdateVMReceipt(t *testing.T, name string, result map[string]any) {
	t.Helper()
	b, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(applicationUpdateVMGuest, 0700); e == nil {
		e = os.WriteFile(filepath.Join(applicationUpdateVMGuest, name+".json"), append(b, '\n'), 0600)
	}
	if e != nil {
		t.Fatal(e)
	}
}
