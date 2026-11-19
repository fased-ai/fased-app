package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/localinstall"
)

func TestLegacyLocalHandoffPreflight(t *testing.T) {
	dir := t.TempDir()
	paths := localinstall.Paths{Root: filepath.Join(dir, "app"), Data: filepath.Join(dir, "data")}
	for _, path := range []string{paths.Root, paths.Data} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	put := func(path string, content []byte) {
		t.Helper()
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	engine := []byte("recognized predecessor engine")
	unit := []byte("original unit")
	engineSHA := digest(engine)
	m := localinstall.Manifest{
		Schema: 1, Phase: "ready", Version: "0.1.6", Location: "local",
		Root: paths.Root, Data: paths.Data,
		ApplicationSequence: 1000007, ApplicationReleaseSHA256: strings.Repeat("a", 64),
		Files: map[string]string{localinstall.EngineName(): engineSHA, "fased-app.service": digest(unit)},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(paths.Root, localinstall.EngineName()), engine)
	put(filepath.Join(paths.Root, "fased-app.service"), unit)
	put(filepath.Join(paths.Root, "install.json"), raw)
	got, err := inspectLegacyLocalHandoff(paths, engineSHA)
	if err != nil || got != localinstall.ManifestSHA256(m) {
		t.Fatalf("exact legacy identity: %q, %v", got, err)
	}
	if _, err := inspectLegacyLocalHandoff(paths, strings.Repeat("b", 64)); err == nil {
		t.Fatal("substituted predecessor executable admitted")
	}
	enrollment := filepath.Join(paths.Data, "enrollment.json")
	put(enrollment, []byte("preserved ownership"))
	if _, err := inspectLegacyLocalHandoff(paths, engineSHA); err == nil || !strings.Contains(err.Error(), "custody enrollment") {
		t.Fatalf("existing enrollment was admitted: %v", err)
	}
	if err := os.Remove(enrollment); err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Join(dir, ".app-updates")
	if err := os.Mkdir(journalDir, 0700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(journalDir, "current.json")
	held := []byte("original unfinished recovery")
	put(journal, held)
	if _, err := inspectLegacyLocalHandoff(paths, engineSHA); err == nil || !strings.Contains(err.Error(), "application update files") {
		t.Fatalf("existing journal was admitted: %v", err)
	}
	if got, err := os.ReadFile(journal); err != nil || string(got) != string(held) {
		t.Fatalf("recovery journal changed: %v", err)
	}
}

func TestLegacyLocalHandoffRefusesRepinnedInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, AttestedPacketFile), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyHandoffPacket(Options{}, dir, localHandoffTarget, true); err == nil || !strings.Contains(err.Error(), "exact approved packet") {
		t.Fatalf("wrong packet reached verification: %v", err)
	}
}
