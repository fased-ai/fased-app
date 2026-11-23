package update

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func desktopManifestFixture() DesktopManifest {
	return DesktopManifest{
		SchemaVersion: 1,
		Version:       "0.1.37",
		ReleaseTag:    "v0.1.37-desktop",
		Source:        strings.Repeat("a", 40),
		Signed:        false,
		Assets: map[string]DesktopAsset{
			"fased-app-0.1.37-linux-amd64.AppImage": {
				URL:    "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-0.1.37-linux-amd64.AppImage",
				SHA256: strings.Repeat("b", 64),
				Bytes:  55966200,
				OS:     "linux",
				Arch:   "amd64",
				Kind:   "desktop",
			},
			"SHA256SUMS.txt": {
				URL:    "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/SHA256SUMS.txt",
				SHA256: strings.Repeat("c", 64),
				Bytes:  1400,
				OS:     "all",
				Arch:   "all",
				Kind:   "checksums",
			},
		},
	}
}

func TestParseDesktopManifestValidAndUpdateComparison(t *testing.T) {
	t.Parallel()
	want := desktopManifestFixture()
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDesktopManifest(raw)
	if err != nil {
		t.Fatalf("ParseDesktopManifest rejected valid manifest: %v", err)
	}
	if got.Version != want.Version || got.ReleaseTag != want.ReleaseTag || len(got.Assets) != 2 {
		t.Fatalf("unexpected parsed manifest: %+v", got)
	}
	for _, tc := range []struct {
		current string
		newer   bool
	}{
		{"0.1.36", true},
		{"v0.1.36-desktop", true},
		{"0.1.37", false},
		{"v0.1.37-desktop", false},
		{"0.1.38", false},
		{"0.2.0", false},
	} {
		avail, err := DesktopUpdateAvailable(tc.current, got)
		if err != nil || avail != tc.newer {
			t.Fatalf("DesktopUpdateAvailable(%q) = (%v, %v), want (%v, nil)", tc.current, avail, err, tc.newer)
		}
	}
}

func TestParseDesktopManifestRejectsTamperingAndAmbiguity(t *testing.T) {
	t.Parallel()
	base := desktopManifestFixture()
	validRaw, _ := json.Marshal(base)

	badMutations := []struct {
		name   string
		mutate func(*DesktopManifest)
	}{
		{"wrong schemaVersion", func(m *DesktopManifest) { m.SchemaVersion = 2 }},
		{"bad semver", func(m *DesktopManifest) { m.Version = "0.01.37"; m.ReleaseTag = "v0.01.37-desktop" }},
		{"mismatched releaseTag", func(m *DesktopManifest) { m.ReleaseTag = "v0.1.37" }},
		{"bad source commit", func(m *DesktopManifest) { m.Source = "not-a-commit" }},
		{"empty assets", func(m *DesktopManifest) { m.Assets = map[string]DesktopAsset{} }},
		{"too many assets", func(m *DesktopManifest) {
			for i := 0; i <= MaxDesktopAssets; i++ {
				name := string(rune('a'+i%26)) + "-" + string(rune('0'+i/26)) + ".tar.gz"
				m.Assets[name] = DesktopAsset{
					URL:    desktopReleaseDownloadBase + m.ReleaseTag + "/" + name,
					SHA256: strings.Repeat("d", 64),
					Bytes:  1024,
					OS:     "linux",
					Arch:   "amd64",
					Kind:   "cli",
				}
			}
		}},
		{"url host tampering", func(m *DesktopManifest) {
			a := m.Assets["fased-app-0.1.37-linux-amd64.AppImage"]
			a.URL = "https://evil.example/fased-app-0.1.37-linux-amd64.AppImage"
			m.Assets["fased-app-0.1.37-linux-amd64.AppImage"] = a
		}},
		{"path traversal name", func(m *DesktopManifest) {
			a := m.Assets["SHA256SUMS.txt"]
			delete(m.Assets, "SHA256SUMS.txt")
			m.Assets["../evil"] = a
		}},
		{"bad sha256", func(m *DesktopManifest) {
			a := m.Assets["SHA256SUMS.txt"]
			a.SHA256 = strings.Repeat("Z", 64)
			m.Assets["SHA256SUMS.txt"] = a
		}},
		{"zero bytes", func(m *DesktopManifest) {
			a := m.Assets["SHA256SUMS.txt"]
			a.Bytes = 0
			m.Assets["SHA256SUMS.txt"] = a
		}},
	}
	for _, tc := range badMutations {
		m := desktopManifestFixture()
		tc.mutate(&m)
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseDesktopManifest(raw); err == nil {
			t.Fatalf("%s unexpectedly accepted", tc.name)
		}
	}

	unknownField := bytes.Replace(validRaw, []byte(`"signed":false`), []byte(`"signed":false,"extra":1`), 1)
	if _, err := ParseDesktopManifest(unknownField); err == nil {
		t.Fatal("unknown field accepted")
	}
	oversized := append(bytes.Repeat([]byte(" "), MaxDesktopDocument), validRaw...)
	if _, err := ParseDesktopManifest(oversized); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
