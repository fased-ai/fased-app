//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// This case belongs only to the existing disposable Ubuntu/QEMU acceptance VM.
// It never runs through ordinary local unit tests or as host root.
func TestManagedRemoteUbuntuVM(t *testing.T) {
	if os.Getenv("FASED_DISPOSABLE_VM") != "1" {
		t.Skip("disposable Ubuntu VM only")
	}
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Fatal("ordinary VM owner required")
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains(string(release), "VERSION_ID=\"24.04\"") || !strings.Contains(string(release), "ID=ubuntu") {
		t.Fatal("Ubuntu24.04 required")
	}
	product, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil || !(strings.Contains(string(product), "QEMU") || strings.Contains(string(product), "Standard PC")) {
		t.Fatal("disposable QEMU guest required")
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != "/home/ubuntu/fased" {
		t.Fatal("finite VM fixture workspace required")
	}
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sys := &custodyinstall.System{In: strings.NewReader(""), Out: io.Discard}
	m, err := vpsinstall.Install(ctx, vpsinstall.Options{Core: image, CoreSHA256: hex.EncodeToString(sum[:]), Listen: "127.0.0.1:17445", PrepareHost: true, Start: true}, sys)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = vpsinstall.Remove(context.Background(), m, sys)
		}
	}()
	first, err := vpsinstall.Status(ctx, m, sys)
	if err != nil || !first.Active || first.InstanceID == "" {
		t.Fatal("managed system service did not verify actual sandbox/identity", first, err)
	}
	var claim bytes.Buffer
	if err = vpsinstall.Claim(ctx, m, sys, false, &claim); err != nil || strings.TrimSpace(claim.String()) == "" {
		t.Fatal("owner claim helper failed", err)
	}
	if err = vpsinstall.Stop(ctx, m, sys); err != nil {
		t.Fatal(err)
	}
	if err = vpsinstall.Start(ctx, m, sys); err != nil {
		t.Fatal(err)
	}
	restarted, err := vpsinstall.Status(ctx, m, sys)
	if err != nil || !restarted.Active || restarted.InstanceID != first.InstanceID {
		t.Fatal("restart changed identity", restarted, err)
	}
	if err = vpsinstall.Stop(ctx, m, sys); err != nil {
		t.Fatal(err)
	}
	stateHash := func() string {
		b, e := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "/usr/bin/cat", "--", m.Data+"/core.sqlite").Output()
		if e != nil || len(b) == 0 {
			t.Fatal("cannot verify stopped disposable state", e)
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	beforeState := stateHash()
	if err = vpsinstall.Remove(ctx, m, sys); err != nil {
		t.Fatal(err)
	}
	cleanup = false
	final, err := vpsinstall.Status(ctx, m, sys)
	if err != nil || final.Installed || final.Active || final.State != "removed" {
		t.Fatal("removal did not preserve inspectable identity", final, err)
	}
	if _, err = os.Lstat(m.Data); err != nil {
		t.Fatal("removal lost instance state", err)
	}
	if afterState := stateHash(); afterState != beforeState {
		t.Fatal("removal changed persisted disposable state")
	}

	receipt := map[string]any{"result": "PASS", "fixtureOnly": true, "ownerUID": m.OwnerUID, "appUID": m.AppUID, "instanceId": first.InstanceID, "restartPreservedIdentity": true, "statePreserved": true, "stoppedDatabaseSHA256": beforeState, "privateClaimHelper": true, "realWallets": false, "publicActivation": false}
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cwd, ".evidence/p4-fault-reboot/guest")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "managed-remote.json"), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
