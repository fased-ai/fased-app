//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestLinuxPackageBindsEmbeddedWalletClientWithoutGlobalDesktop(t *testing.T) {
	oldVersion := core.Version
	core.Version = "0.1.12"
	t.Cleanup(func() { core.Version = oldVersion })
	dir, source := t.TempDir(), strings.Repeat("a", 40)
	writePayload := func(name, data string) bundle.File {
		t.Helper()
		h := sha256.Sum256([]byte(data))
		hash := hex.EncodeToString(h[:])
		var frame bytes.Buffer
		if err := bootstrap.WriteWalletPayload(&frame, &bootstrap.WalletPayload{Data: []byte(data), SHA256: hash, Version: core.Version, Source: source}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".frame"), frame.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return bundle.File{SHA256: hash, Bytes: int64(len(data))}
	}
	signerFile := writePayload("signer", "signer fixture")
	desktop := writePayload("desktop", "wallet client fixture")
	binding, _ := json.Marshal(bootstrap.NativeReleaseBinding{Version: 1, ProductVersion: core.Version, Source: source, SignerSHA256: signerFile.SHA256, DesktopSHA256: desktop.SHA256})
	app := "#!/bin/sh\nprintf '%s\\n' '" + string(binding) + "'\n"
	installer := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in --emit-embedded-signer) cat '%s' ;; --emit-embedded-native) cat '%s' ;; *) exit 1 ;; esac\n", filepath.Join(dir, "signer.frame"), filepath.Join(dir, "desktop.frame"))
	for name, script := range map[string]string{"fased-app": app, "fased-install": installer} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]bundle.File{"fased-signer": signerFile}
	if err := verifyLinuxPackageBinding(dir, source, files); err != nil {
		t.Fatal("embedded matching client was refused", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop.frame"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyLinuxPackageBinding(dir, source, files); err == nil {
		t.Fatal("changed embedded client was accepted")
	}
}

func TestLinuxPackagePublicationIsCompleteAndNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	deb, rpm := filepath.Join(dir, "in.deb"), filepath.Join(dir, "in.rpm")
	for path, data := range map[string]string{deb: "deb fixture", rpm: "rpm fixture"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "output")
	source := strings.Repeat("a", 40)
	if err := publishLinuxPackages(out, source, nil, deb, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("partial pair accepted")
	}
	if entries, err := os.ReadDir(out); err != nil || len(entries) != 0 {
		t.Fatalf("partial output left after failure: %v %v", entries, err)
	}

	// Two publishers racing for the same destination cannot mix or replace
	// their output; exactly one may claim the completed directory.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- publishLinuxPackages(out, source, nil, deb, rpm)
		}()
	}
	wg.Wait()
	close(results)
	passes := 0
	for err := range results {
		if err == nil {
			passes++
		}
	}
	if passes != 1 {
		t.Fatalf("want one committed pair, got %d", passes)
	}
	dest := filepath.Join(out, "fased-linux-system-"+runtime.GOARCH)
	raw, err := os.ReadFile(filepath.Join(dest, "packages.json"))
	var receipt linuxSystemPackageReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.PublicRelease || receipt.Source != source || len(receipt.Packages) != 2 {
		t.Fatalf("invalid private package receipt: %v", err)
	}
	for name, want := range receipt.Packages {
		got, err := bundle.Inspect(filepath.Join(dest, name))
		if err != nil || got != want {
			t.Fatalf("package identity mismatch for %s: %v", name, err)
		}
	}
	if err := publishLinuxPackages(out, source, nil, deb, rpm); err == nil {
		t.Fatal("existing package pair was replaced")
	}
	after, err := os.ReadFile(filepath.Join(dest, "packages.json"))
	if err != nil || string(after) != string(raw) {
		t.Fatal("existing package receipt changed")
	}
}

func TestLinuxPackageRejectsSymlinkAndDynamicExecutable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := stageLinuxPackageInput(link, filepath.Join(dir, "copy"), 0700); err == nil {
		t.Fatal("symlink accepted as a package input")
	}

	// Minimal ELF64 fixtures differ only in whether a dynamic loader is named.
	for _, dynamic := range []bool{false, true} {
		b := make([]byte, 64+56)
		copy(b, []byte{'\x7f', 'E', 'L', 'F', 2, 1, 1})
		binary.LittleEndian.PutUint16(b[16:], 2) // ET_EXEC
		machine := uint16(62)
		if runtime.GOARCH == "arm64" {
			machine = 183
		} else if runtime.GOARCH != "amd64" {
			t.Skip("fixture requires amd64 or arm64")
		}
		binary.LittleEndian.PutUint16(b[18:], machine)
		binary.LittleEndian.PutUint32(b[20:], 1)
		binary.LittleEndian.PutUint64(b[32:], 64) // program table
		binary.LittleEndian.PutUint16(b[52:], 64)
		binary.LittleEndian.PutUint16(b[54:], 56)
		binary.LittleEndian.PutUint16(b[56:], 1)
		if dynamic {
			binary.LittleEndian.PutUint32(b[64:], 3) // PT_INTERP
		}
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyStaticLinuxExecutable(file); (err != nil) != dynamic {
			t.Fatalf("dynamic=%v: %v", dynamic, err)
		}
	}
}

func TestDebianPackageProvidesInstalledFasedCommand(t *testing.T) {
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "fased-app"), []byte("disposable core"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := buildDebPackage(stage, "/usr/bin/true", filepath.Join(stage, "ignored.deb"), map[string]string{"fased-app": "unused"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(stage, "deb-root", "usr", "bin", "fased")
	if target, err := os.Readlink(link); err != nil || target != "../lib/fased-app/fased-app" {
		t.Fatalf("installed command does not select the packaged core: %q %v", target, err)
	}
}

func TestLinuxPackageRequiresCompatibleCoreAndSigner(t *testing.T) {
	dir := t.TempDir()
	app := struct {
		Version            int             `json:"version"`
		Target             update.Target   `json:"target"`
		Contract           update.Contract `json:"contract"`
		WalletSigner       bool            `json:"walletSigner"`
		FinancialExecution bool            `json:"financialExecution"`
		UpdaterEnabled     bool            `json:"updaterEnabled"`
	}{Version: 1, Target: update.Target{OS: "linux", Arch: runtime.GOARCH}, Contract: core.OrdinaryUpdateContract()}
	writeJSONCommand := func(name string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\ncat <<'FASED_JSON'\n" + string(raw) + "\nFASED_JSON\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONCommand("fased-app", app)
	contract := signer.OrdinaryUpdateContract()
	writeJSONCommand("fased-signer", contract)
	if err := verifyLinuxPairContracts(dir); err != nil {
		t.Fatal(err)
	}
	contract.Protocol++
	writeJSONCommand("fased-signer", contract)
	if err := verifyLinuxPairContracts(dir); err == nil {
		t.Fatal("incompatible signer accepted")
	}
}
