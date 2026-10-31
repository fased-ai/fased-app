//go:build linux

package bootstrap

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func upgradeEntryFixture(t *testing.T, f *firstFixture) UpgradeOptions {
	t.Helper()
	o := UpgradeOptions{Prefix: filepath.Join(f.parent, "existing-app"), DataDirectory: filepath.Join(f.parent, "existing-data")}
	for _, d := range []string{o.Prefix, o.DataDirectory} {
		if e := os.Mkdir(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	old := []byte("inert installed predecessor")
	unit := []byte("inert existing service")
	if e := os.WriteFile(filepath.Join(o.Prefix, "fased-app"), old, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(o.Prefix, "fased-app.service"), unit, 0600); e != nil {
		t.Fatal(e)
	}
	m := localinstall.Manifest{Schema: 1, Phase: "ready", Version: "old", Root: o.Prefix, Data: o.DataDirectory, Files: map[string]string{"fased-app": digest(old), "fased-app.service": digest(unit)}}
	if e := core.WritePrivateJSON(filepath.Join(o.Prefix, "install.json"), m); e != nil {
		t.Fatal(e)
	}
	return o
}

func TestApplicationUpgradeEntryUsesHeldVerifiedMaintenanceArgv(t *testing.T) {
	f := newFirstFixture(t)
	p, e := Prepare(t.Context(), f.options())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	o := upgradeEntryFixture(t, f)
	if e = p.RunUpgrade(t.Context(), o, nil, io.Discard, io.Discard); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(f.arguments)
	if e != nil {
		t.Fatal(e)
	}
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(args) != 7 || args[0] != "maintain-update" || args[1] != "--operation" || !update.IDOK(args[2]) || args[3] != "--prefix" || args[4] != o.Prefix || args[5] != "--data-dir" || args[6] != o.DataDirectory {
		t.Fatal("nonfixed maintenance entry", args)
	}
	if p.Report().PublicActivation {
		t.Fatal("upgrade entry activated public channel")
	}
	if e = p.RunUpgrade(t.Context(), o, nil, io.Discard, io.Discard); e == nil {
		t.Fatal("verified entry invoked twice")
	}
}

func TestApplicationUpgradeEntryRefusesTamperAndProfileOverrides(t *testing.T) {
	for _, kind := range []string{"tampered-code", "remote-paths", "overlap"} {
		t.Run(kind, func(t *testing.T) {
			f := newFirstFixture(t)
			p, e := Prepare(t.Context(), f.options())
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			o := upgradeEntryFixture(t, f)
			switch kind {
			case "tampered-code":
				if e = os.WriteFile(filepath.Join(p.report.BundleDirectory, "fased-app"), []byte("foreign"), 0700); e != nil {
					t.Fatal(e)
				}
			case "remote-paths":
				o.Remote = true
			case "overlap":
				o.Prefix = p.report.BundleDirectory
			}
			if e = p.RunUpgrade(t.Context(), o, nil, io.Discard, io.Discard); e == nil {
				t.Fatal("unsafe upgrade entry admitted")
			}
			if _, e = os.Stat(f.marker); !os.IsNotExist(e) {
				t.Fatal("invalid entry executed signed fixture", e)
			}
		})
	}
}
