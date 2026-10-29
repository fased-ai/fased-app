//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func TestManagedOwnerBrowserUbuntuVM(t *testing.T) {
	if os.Getenv("FASED_DISPOSABLE_VM") != "1" {
		t.Skip("disposable Ubuntu VM only")
	}
	cwd, _ := os.Getwd()
	release, _ := os.ReadFile("/etc/os-release")
	product, _ := os.ReadFile("/sys/class/dmi/id/product_name")
	if os.Getuid() != 1000 || os.Geteuid() != os.Getuid() || cwd != "/home/ubuntu/fased" || !strings.Contains(string(release), "VERSION_ID=\"24.04\"") || !strings.Contains(string(release), "ID=ubuntu") || !(strings.Contains(string(product), "QEMU") || strings.Contains(string(product), "Standard PC")) {
		t.Fatal("ordinary disposable Ubuntu24.04 QEMU workspace required")
	}
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sys := &custodyinstall.System{In: strings.NewReader(""), Out: io.Discard}
	m, err := vpsinstall.Install(ctx, vpsinstall.Options{Core: image, CoreSHA256: hex.EncodeToString(sum[:]), Listen: "127.0.0.1:17447", PrepareHost: true, Start: true}, sys)
	if err != nil {
		t.Fatal(err)
	}
	status, err := vpsinstall.Status(ctx, m, sys)
	if err != nil || !status.Active || status.InstanceID == "" {
		t.Fatal("managed instance not ready", status, err)
	}
	dir := "/home/ubuntu/fased/.evidence/p4-fault-reboot/guest"
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"result": "PASS", "fixtureOnly": true, "manifest": m, "instanceId": status.InstanceID, "realAI": false, "realWallets": false, "publicActivation": false}
	b, err = json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "managed-owner-provision.json"), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	// Host browser and protected terminal actions now use the actual installed
	// service through SSH. No auth shortcut or owner-session copy is provisioned.
}
