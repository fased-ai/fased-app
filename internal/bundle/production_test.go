package bundle

import (
	"os"
	"path/filepath"
	"runtime"
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
