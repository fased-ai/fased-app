//go:build linux

package bootstrap

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
)

func TestNativeBundleUsesVerifiedArchiveAndMatchingInstallerPayload(t *testing.T) {
	for _, production := range []bool{false, true} {
		name := "development fixture"
		if production {
			name = "attested running installer"
		}
		t.Run(name, func(t *testing.T) {
			var options Options
			if production {
				_, options, _ = attestedFixture(t, "running-installer")
			} else {
				options = newFirstFixture(t).options()
			}
			p, err := Prepare(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			payload := func(text string) *WalletPayload {
				data := []byte(text)
				return &WalletPayload{Data: data, SHA256: digest(data), Version: p.descriptor.ProductVersion, Source: p.descriptor.Source}
			}
			desktop, signer := payload("native client"), payload("matching signer")
			path, pin, cleanup, err := p.composeNativeBundle(desktop, signer)
			if err != nil {
				t.Fatal(err)
			}
			m, err := bundle.VerifyPinned(path, pin)
			if err != nil || !m.Desktop || m.Files["fased-signer"].SHA256 != signer.SHA256 || m.Source != p.descriptor.Source || m.Version != p.descriptor.ProductVersion {
				t.Fatal("native inventory is not bound to the release", m, err)
			}
			if production && (m.Schema != 3 || m.UnsignedEngineering) || !production && (m.Schema != 1 || !m.UnsignedEngineering) {
				t.Fatal("fixture and attested bundle classifications were confused", m.Schema)
			}
			_, desktopName := bundle.Names()
			if b, err := os.ReadFile(filepath.Join(path, desktopName)); err != nil || !bytes.Equal(b, desktop.Data) {
				t.Fatal("native client bytes changed", err)
			}
			original, err := bundle.VerifyPinned(p.report.BundleDirectory, p.report.BundleSHA256)
			if err != nil || original.Desktop || original.Files["fased-signer"].SHA256 != "" {
				t.Fatal("original headless archive inventory changed", err)
			}
			if err = cleanup(); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("temporary native bundle retained", err)
			}
		})
	}
}

func TestNativeBundleReachesTheExistingLocalInstaller(t *testing.T) {
	f := newFirstFixture(t, "native")
	p, err := Prepare(t.Context(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	payload := func(value string) *WalletPayload {
		b := []byte(value)
		return &WalletPayload{Data: b, SHA256: digest(b), Version: p.descriptor.ProductVersion, Source: p.descriptor.Source}
	}
	if err = p.Run(t.Context(), InstallOptions{Profile: "local", WalletDesktop: payload("desktop"), WalletSigner: payload("signer")}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(f.arguments)
	if err != nil || !strings.Contains(string(args), "--native-bundle\n") || !strings.Contains(string(args), "--native-bundle-sha256\n") {
		t.Fatal("native bundle was not handed to the existing installer", string(args), err)
	}
	b, err := os.ReadFile(filepath.Join(f.parent, "native-inventory"))
	var m bundle.Manifest
	if err != nil || json.Unmarshal(b, &m) != nil || !m.Desktop || m.Files["fased-signer"].SHA256 != digest([]byte("signer")) {
		t.Fatal("installer did not receive the matching pair", err)
	}
	if _, err = os.Lstat(filepath.Join(p.options.Directory, "native-bundle")); !os.IsNotExist(err) {
		t.Fatal("native staging was not cleaned", err)
	}
}

func TestNativeBundleRejectsDifferentPayloadBeforeCreatingFiles(t *testing.T) {
	f := newFirstFixture(t)
	p, err := Prepare(t.Context(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	desktop := &WalletPayload{Data: []byte("native"), SHA256: digest([]byte("native")), Version: p.descriptor.ProductVersion, Source: p.descriptor.Source}
	signer := &WalletPayload{Data: []byte("changed signer"), SHA256: digest([]byte("other")), Version: p.descriptor.ProductVersion, Source: p.descriptor.Source}
	if _, _, _, err = p.composeNativeBundle(desktop, signer); err == nil {
		t.Fatal("unmatched signer was accepted")
	}
	if _, err = os.Lstat(filepath.Join(p.options.Directory, "native-bundle")); !os.IsNotExist(err) {
		t.Fatal("refused native payload created staging", err)
	}
}

func TestNativeBundleCleanupPreservesUnexpectedEntryAndOriginals(t *testing.T) {
	f := newFirstFixture(t)
	p, err := Prepare(t.Context(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	payload := func(value string) *WalletPayload {
		b := []byte(value)
		return &WalletPayload{Data: b, SHA256: digest(b), Version: p.descriptor.ProductVersion, Source: p.descriptor.Source}
	}
	path, _, cleanup, err := p.composeNativeBundle(payload("desktop"), payload("signer"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "unclassified"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err == nil {
		t.Fatal("cleanup accepted an unclassified file")
	}
	for _, name := range []string{"unclassified", "bundle.json", "fased-signer"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err != nil {
			t.Fatal("cleanup discarded retained evidence", name, err)
		}
	}
}
