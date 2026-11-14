package bundle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseBundleFormatKeepsEngineeringBoundary(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Ubuntu release format")
	}
	for _, variant := range []string{"release", "missing-guide", "engineering-marker", "desktop", "other-schema"} {
		t.Run(variant, func(t *testing.T) {
			dir, m := bundleFixture(t)
			m.Schema, m.UnsignedEngineering = 2, false
			if err := os.WriteFile(filepath.Join(dir, "INSTALL.txt"), []byte("release guide"), 0600); err != nil {
				t.Fatal(err)
			}
			guide, err := Inspect(filepath.Join(dir, "INSTALL.txt"))
			if err != nil {
				t.Fatal(err)
			}
			m.Files["INSTALL.txt"] = guide
			switch variant {
			case "missing-guide":
				delete(m.Files, "INSTALL.txt")
			case "engineering-marker":
				m.UnsignedEngineering = true
			case "desktop":
				m.Desktop = true
			case "other-schema":
				m.Schema = 3
			}
			writeBundleManifest(t, dir, m)
			_, err = Verify(dir)
			if (err == nil) != (variant == "release") {
				t.Fatalf("release format %s admission = %v", variant, err)
			}
		})
	}
}

func TestComposedNativeBundleRequiresCompleteReleasePair(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux native bundle")
	}
	for _, variant := range []string{"valid", "missing-signer", "missing-desktop", "missing-guide", "engineering-marker", "wrong-source"} {
		t.Run(variant, func(t *testing.T) {
			dir, m := bundleFixture(t)
			m.Schema, m.Desktop, m.UnsignedEngineering = 3, true, false
			m.Version, m.Source = "0.1.12", strings.Repeat("a", 40)
			_, desktop := Names()
			for _, name := range []string{desktop, "fased-signer", "INSTALL.txt"} {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("fixture "+name), 0700); err != nil {
					t.Fatal(err)
				}
				var err error
				m.Files[name], err = Inspect(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			switch variant {
			case "missing-signer":
				delete(m.Files, "fased-signer")
				os.Remove(filepath.Join(dir, "fased-signer"))
			case "missing-desktop":
				m.Desktop = false
				delete(m.Files, desktop)
				os.Remove(filepath.Join(dir, desktop))
			case "missing-guide":
				delete(m.Files, "INSTALL.txt")
				os.Remove(filepath.Join(dir, "INSTALL.txt"))
			case "engineering-marker":
				m.UnsignedEngineering = true
			case "wrong-source":
				m.Source = "unbound"
			}
			writeBundleManifest(t, dir, m)
			if _, err := Verify(dir); (err == nil) != (variant == "valid") {
				t.Fatalf("native release format %s admission = %v", variant, err)
			}
		})
	}
}
