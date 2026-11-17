//go:build linux

package bootstrap

import (
	"bytes"
	"encoding/base64"
	"github.com/fased-ai/fased-app/internal/bundle"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWalletEnrollmentUsesAuthenticatedPairAndExistingController(t *testing.T) {
	instance := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	for _, kind := range []string{"complete", "missing-signer", "changed-signer", "wrong-instance", "wrong-profile", "wrong-payload-source", "wrong-payload-version"} {
		t.Run(kind, func(t *testing.T) {
			mode := "wallets"
			if kind == "missing-signer" {
				mode = "no-wallets"
			}
			f := newFirstFixture(t, mode)
			p, err := Prepare(t.Context(), f.options())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			payload := &WalletPayload{Data: []byte("inert disposable signer"), Version: f.descriptor.ProductVersion, Source: f.descriptor.Source}
			payload.SHA256 = digest(payload.Data)
			opts := InstallOptions{WalletSigner: payload, Start: true, Profile: "remote", WalletInstance: instance, WalletNetwork: "devnet"}
			switch kind {
			case "missing-signer":
				opts.WalletSigner = nil
			case "changed-signer":
				payload.Data = []byte("substituted")
			case "wrong-payload-source":
				payload.Source = "foreign"
			case "wrong-payload-version":
				payload.Version = "foreign"
			case "wrong-instance":
				opts.WalletInstance = "foreign"
			case "wrong-profile":
				opts.Profile = "local"
			}
			var output bytes.Buffer
			err = p.Run(t.Context(), opts, nil, &output, io.Discard)
			if kind != "complete" {
				if err == nil {
					t.Fatal("invalid pair/intent executed")
				}
				if _, e := os.Stat(f.marker); !os.IsNotExist(e) {
					t.Fatal("refused setup executed code")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if output.String() != "Wallets setup complete.\n" {
				t.Fatalf("wallet setup result was not shown: %q", output.String())
			}
			b, err := os.ReadFile(f.arguments)
			if err != nil {
				t.Fatal(err)
			}
			args := string(b)
			for _, want := range []string{"custody\nenroll\n--ordinary-custody\n--source\nremote\n", "--expected-instance\n" + instance + "\n", "--network\ndevnet\n", "--signer-binary\n/proc/self/fd/4\n", "--disposable-fixture\n--prepare-host\n--start-existing\n"} {
				if !strings.Contains(args, want) {
					t.Fatal("handoff lost reviewed binding", want, args)
				}
			}
			received, err := os.ReadFile(filepath.Join(f.parent, "received-signer"))
			if err != nil || !bytes.Equal(received, payload.Data) {
				t.Fatalf("successor did not receive the verified signer bytes: %v", err)
			}
			if _, err = os.Stat(filepath.Join(p.options.Directory, "wallet-signer")); !os.IsNotExist(err) {
				t.Fatalf("signer was left at a mutable handoff path: %v", err)
			}
			// The normal archive and its extracted inventory remain unchanged, so
			// older UI/CLI updaters never need to parse a new signer inventory entry.
			if digest(f.archive) != p.report.ArchiveSHA256 {
				t.Fatal("ordinary archive changed")
			}
			m, e := bundle.Verify(p.report.BundleDirectory)
			if e != nil || m.Files["fased-signer"].SHA256 != "" {
				t.Fatal("signer leaked into ordinary update archive", e)
			}
			if strings.Contains(args, "--interactive") || strings.Contains(args, "--start=false") {
				t.Fatal("wallet setup became installation/update wizard", args)
			}
			if err = p.Run(t.Context(), opts, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("same prepared execution replayed")
			}
		})
	}
}

func TestWalletEnrollmentRefusesPayloadFromUnattestedInstaller(t *testing.T) {
	f, options, _ := attestedFixture(t, "")
	p, err := Prepare(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	instance := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	payload := &WalletPayload{
		Data:    []byte("substituted disposable signer"),
		Version: p.descriptor.ProductVersion,
		Source:  p.descriptor.Source,
	}
	payload.SHA256 = digest(payload.Data)
	err = p.Run(t.Context(), InstallOptions{
		WalletSigner:   payload,
		WalletInstance: instance,
		WalletNetwork:  "devnet",
		Profile:        "remote",
		Start:          true,
	}, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("unattested running installer supplied a signer payload")
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatalf("refused enrollment executed the successor: %v", err)
	}
	if _, err = os.Stat(filepath.Join(options.Directory, "wallet-signer")); !os.IsNotExist(err) {
		t.Fatalf("refused enrollment staged the signer: %v", err)
	}
}

func TestWalletEnrollmentRefusesLegacyReleaseWithoutInstallerDigest(t *testing.T) {
	f, options, _ := productionFixture(t, "complete")
	p, err := Prepare(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.descriptor.Release.InstallerSHA256 != "" {
		t.Fatal("fixture unexpectedly binds an installer image")
	}

	payload := &WalletPayload{
		Data:    []byte("legacy disposable signer"),
		Version: p.descriptor.ProductVersion,
		Source:  p.descriptor.Source,
	}
	payload.SHA256 = digest(payload.Data)
	err = p.Run(t.Context(), InstallOptions{
		WalletSigner:   payload,
		WalletInstance: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		WalletNetwork:  "devnet",
		Profile:        "remote",
		Start:          true,
	}, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("release without an authenticated installer admitted signer enrollment")
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatalf("refused enrollment executed the successor: %v", err)
	}
	if _, err = os.Stat(filepath.Join(options.Directory, "wallet-signer")); !os.IsNotExist(err) {
		t.Fatalf("refused enrollment staged the signer: %v", err)
	}
}

func TestWalletEnrollmentAcceptsMatchingInstallerImage(t *testing.T) {
	f, options, _ := attestedFixture(t, "running-installer")
	p, err := Prepare(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	instance := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	// The fixture supplies a payload directly. The test proves the running-image
	// check, while the release build separately supplies the embedded bytes.
	payload := &WalletPayload{
		Data:    []byte("attested disposable signer"),
		Version: p.descriptor.ProductVersion,
		Source:  p.descriptor.Source,
	}
	payload.SHA256 = digest(payload.Data)
	err = p.Run(t.Context(), InstallOptions{
		WalletSigner:   payload,
		WalletInstance: instance,
		WalletNetwork:  "devnet",
		Profile:        "remote",
		Start:          true,
	}, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(f.marker); err != nil {
		t.Fatalf("attested enrollment did not execute its verified successor: %v", err)
	}
	if _, err = os.Stat(filepath.Join(options.Directory, "wallet-signer")); !os.IsNotExist(err) {
		t.Fatalf("signer was left at a mutable handoff path: %v", err)
	}
}

func TestRunningInstallerRefusesUnsealedMemoryImage(t *testing.T) {
	if os.Getenv("FASED_UNSEALED_INSTALLER_CHILD") == "1" {
		image, err := os.ReadFile("/proc/self/exe")
		if err != nil {
			t.Fatal(err)
		}
		if err = verifyRunningInstaller(digest(image)); err == nil {
			t.Fatal("mutable running installer was accepted")
		}
		return
	}

	fd, err := unix.MemfdCreate("fased-installer-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if err == unix.EINVAL {
		fd, err = unix.MemfdCreate("fased-installer-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	}
	if err != nil {
		t.Fatal(err)
	}
	image := os.NewFile(uintptr(fd), "unsealed-installer")
	defer image.Close()
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err = io.Copy(image, source); err != nil {
		t.Fatal(err)
	}
	if err = image.Chmod(0500); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "/proc/self/fd/3", "-test.run=^TestRunningInstallerRefusesUnsealedMemoryImage$")
	command.ExtraFiles = []*os.File{image}
	command.Env = append(os.Environ(), "FASED_UNSEALED_INSTALLER_CHILD=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("running installer validation: %v: %s", err, output)
	}
}
