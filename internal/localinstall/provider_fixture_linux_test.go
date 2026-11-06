package localinstall

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestInstalledProviderFixtureLocalIdentity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "provider"}[enabled], func(t *testing.T) {
			p, engine := installFixture(t)
			m, err := Install(t.Context(), p, Options{Engine: engine, ProviderFixture: enabled})
			if err != nil || m.ProviderFixture != enabled {
				t.Fatal("install", err)
			}
			raw, err := os.ReadFile(filepath.Join(p.Root, "install.json"))
			if err != nil || bytes.Contains(raw, []byte(`"providerFixture"`)) != enabled {
				t.Fatal("ordinary manifest bytes changed or fixture identity omitted", err)
			}
			unit, err := os.ReadFile(filepath.Join(p.Root, definitionName))
			if err != nil || bytes.Contains(unit, []byte("--ai-fixture")) != enabled {
				t.Fatal("generated command disagrees with fixture identity", err)
			}
			if err = checkProviderFixture(p, enabled, filepath.Join(p.Root, engineName)); err != nil {
				t.Fatal("exact runtime identity", err)
			}
			if err = checkProviderFixture(p, !enabled, filepath.Join(p.Root, engineName)); err == nil {
				t.Fatal("runtime mode mismatch accepted")
			}
			if err = checkProviderFixture(p, enabled, engine); err == nil {
				t.Fatal("uninstalled executable accepted")
			}
			if _, err = Install(t.Context(), p, Options{Engine: engine, ProviderFixture: !enabled}); err == nil {
				t.Fatal("retry silently switched provider mode")
			}
			after, _ := os.ReadFile(filepath.Join(p.Root, "install.json"))
			if !bytes.Equal(raw, after) {
				t.Fatal("refused retry changed the manifest")
			}
			if !enabled {
				m.ProviderFixture = true
				if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
					t.Fatal(err)
				}
				if err = checkProviderFixture(p, true, filepath.Join(p.Root, engineName)); err == nil {
					t.Fatal("manifest-only edit enabled a fixture with an unmatching service")
				}
			}
		})
	}
}

func TestInstalledProviderFixtureRetainsOrdinaryUnit(t *testing.T) {
	p, engine := installFixture(t)
	m, err := Install(t.Context(), p, Options{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.Root, definitionName)
	unit, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unit = append(unit, []byte("\n# Retained earlier verified unit\n")...)
	if err = os.WriteFile(path, unit, 0600); err != nil {
		t.Fatal(err)
	}
	m.Files[definitionName] = bytesDigest(unit)
	if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
		t.Fatal(err)
	}
	if err = checkProviderFixture(p, false, filepath.Join(p.Root, engineName)); err != nil {
		t.Fatal("ordinary startup required the latest unit template", err)
	}
}

func TestInstalledProviderFixtureUpdateCannotSwitchMode(t *testing.T) {
	f := applicationFixtureFor(t)
	if _, err := f.apply(t, func(phase string) error {
		if phase == "before-stop" {
			return errors.New("controlled interruption before replacement")
		}
		return nil
	}, false); err == nil {
		t.Fatal("interruption did not hold the update")
	}
	j, err := readApplicationJournal(f.paths)
	if err != nil || !validApplicationJournal(j, f.paths) {
		t.Fatal("retained update journal invalid", err)
	}
	j.Target.ProviderFixture = !j.Previous.ProviderFixture
	j.Status.TargetManifestSHA256 = ManifestSHA256(j.Target)
	if validApplicationJournal(j, f.paths) {
		t.Fatal("rehashing a journal changed provider-mode authority")
	}
	f.preserved(t)
}
