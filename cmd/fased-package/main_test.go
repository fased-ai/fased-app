package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
)

func TestRenderMacOSInfoPlistMetadata(t *testing.T) {
	t.Parallel()
	raw := renderMacOSInfoPlist("0.1.38")
	for _, want := range []string{
		"<key>CFBundleShortVersionString</key><string>0.1.38</string>",
		"<key>CFBundleVersion</key><string>0.1.38</string>",
		"<key>CFBundleIconFile</key><string>fased-app.icns</string>",
		"<key>NSMicrophoneUsageDescription</key>",
		"<key>NSSpeechRecognitionUsageDescription</key>",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("Info.plist missing %q", want)
		}
	}
	var doc any
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("Info.plist XML invalid: %v", err)
	}
	if !strings.Contains(renderMacOSInfoPlist(""), "<string>0.0.0</string>") {
		t.Fatal("empty version should fall back to 0.0.0")
	}
}

func TestUbuntuReleaseFormatProducer(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("headless Ubuntu release format")
	}
	t.Chdir("../..")
	for _, variant := range []string{"engineering", "release", "desktop-refused", "source-refused"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			engine := filepath.Join(root, "inert-engine")
			if err := os.WriteFile(engine, []byte("nonexecutable disposable package fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, "output")
			args := []string{"--engine", engine, "--out", out, "--source", strings.Repeat("a", 40)}
			if variant != "engineering" {
				args = append(args, "--ubuntu-release-format")
			}
			if variant == "desktop-refused" {
				args = append(args, "--desktop", engine)
			}
			if variant == "source-refused" {
				args = append(args, "--source", "working-tree")
			}
			err := runArgs(args)
			if strings.HasSuffix(variant, "-refused") {
				if err == nil {
					t.Fatal("unsupported release producer accepted")
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("refused release producer mutated output")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Archive          string          `json:"archive"`
				Identity         bundle.File     `json:"identity"`
				ManifestIdentity bundle.File     `json:"manifestIdentity"`
				Manifest         bundle.Manifest `json:"manifest"`
				PublicRelease    bool            `json:"publicRelease"`
			}
			b, err := os.ReadFile(filepath.Join(out, "fased-app-linux-amd64.json"))
			if err != nil || json.Unmarshal(b, &receipt) != nil {
				t.Fatal("producer receipt missing", err)
			}
			if receipt.PublicRelease {
				t.Fatal("format asserted public release")
			}
			if (receipt.Manifest.Schema == 2) != (variant == "release") || receipt.Manifest.UnsignedEngineering != (variant == "engineering") {
				t.Fatal("producer format boundary lost")
			}
			manifest, err := bundle.ExtractVerifiedArchive(receipt.Archive, filepath.Join(root, "verified"), receipt.Identity, receipt.ManifestIdentity.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Source != strings.Repeat("a", 40) || manifest.Desktop {
				t.Fatal("release source or headless identity lost")
			}
			if _, ok := manifest.Files["INSTALL.txt"]; !ok {
				t.Fatal("producer omitted inventoried guide")
			}
		})
	}
}

func TestLinuxBrowserBundleContainsTheMatchedSigner(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux amd64 bundle")
	}
	t.Chdir("../..")
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(root, "engine")
	signer := filepath.Join(root, "signer")
	for name, content := range map[string]string{engine: "core fixture", signer: "signer fixture"} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(root, "output")
	if err := runArgs([]string{"--linux-bundle-format", "--engine", engine, "--signer", signer, "--out", out, "--source", strings.Repeat("a", 40)}); err != nil {
		t.Fatal("browser-first paired bundle was refused", err)
	}
	var receipt struct {
		Archive          string          `json:"archive"`
		Identity         bundle.File     `json:"identity"`
		ManifestIdentity bundle.File     `json:"manifestIdentity"`
		Manifest         bundle.Manifest `json:"manifest"`
		PublicRelease    bool            `json:"publicRelease"`
	}
	b, err := os.ReadFile(filepath.Join(out, "fased-app-linux-amd64.json"))
	if err != nil || json.Unmarshal(b, &receipt) != nil || receipt.PublicRelease {
		t.Fatal("bundle receipt invalid", err)
	}
	dir := filepath.Join(root, "verified")
	m, err := bundle.ExtractVerifiedArchive(receipt.Archive, dir, receipt.Identity, receipt.ManifestIdentity.SHA256)
	if err != nil || m.Schema != 4 || m.Desktop || m.Files["fased-signer"].SHA256 == "" {
		t.Fatal("verified bundle did not contain a headless matched pair", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fased-signer"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.VerifyPinned(dir, receipt.ManifestIdentity.SHA256); err == nil {
		t.Fatal("modified signer remained valid")
	}
}
