package bootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func legacyLocalFixture(t *testing.T) (localinstall.Paths, string, string) {
	t.Helper()
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
	return paths, engineSHA, localinstall.ManifestSHA256(m)
}

func TestLegacyLocalHandoffPreflight(t *testing.T) {
	paths, engineSHA, manifestSHA := legacyLocalFixture(t)
	put := func(path string, content []byte) {
		t.Helper()
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := inspectLegacyLocalHandoff(paths, engineSHA)
	if err != nil || got != manifestSHA {
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
	journalDir := filepath.Join(filepath.Dir(paths.Root), ".app-updates")
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

func TestLegacyLocalHandoffStageKeepsOriginalState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an ordinary user")
	}
	paths, engineSHA, manifestSHA := legacyLocalFixture(t)
	db, err := core.OpenStore(paths.Data)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := db.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const password = "disposable fixture password"
	if _, err := db.Claim(t.Context(), claim.Code, password); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	save := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	originalDB := filepath.Join(paths.Data, "core.sqlite")
	originalBytes := save(originalDB)
	originalManifest := filepath.Join(paths.Root, "install.json")
	manifestBytes := save(originalManifest)
	m := LocalHandoffPreflight{Owner: os.Getuid(), LegacyManifestSHA256: manifestSHA, LegacyPacketSHA256: legacyLocalPacket, TargetPacketSHA256: localHandoffTarget}
	parent := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "prepared")
	prepare := func(destination string) (LocalHandoffStage, error) {
		return prepareLegacyLocalStage(t.Context(), m, engineSHA, paths, destination)
	}
	installLockPath := filepath.Join(paths.Root, "install.lock")
	if err := os.WriteFile(installLockPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	instanceLockPath := filepath.Join(paths.Data, "instance.lock")
	if _, err := prepare(target); err == nil {
		t.Fatal("missing source lock was admitted")
	}
	if _, err := os.Lstat(instanceLockPath); !os.IsNotExist(err) {
		t.Fatalf("source lock was created: %v", err)
	}
	busy, err := core.LockInstance(instanceLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(target); err == nil {
		t.Fatal("running predecessor was admitted")
	}
	if err := busy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(filepath.Join(paths.Data, "nested")); err == nil {
		t.Fatal("stage inside predecessor was admitted")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("a refusal published a stage: %v", err)
	}
	stage, err := prepare(target)
	if err != nil {
		t.Fatal(err)
	}
	if stage.InstanceID == "" || stage.StateSchema != core.StateSchema || len(stage.StateSHA256) != 64 ||
		stage.LocalHandoffPreflight != m || stage.ApplyAllowed {
		t.Fatal("stage does not bind the original identity and packet")
	}
	var receipt LocalHandoffStage
	receiptBytes := save(filepath.Join(target, "handoff.json"))
	if err := update.StrictJSON(receiptBytes, &receipt); err != nil || receipt != stage {
		t.Fatal("receipt does not describe the verified stage", err)
	}
	if _, err := prepare(target); err == nil || !bytes.Equal(receiptBytes, save(filepath.Join(target, "handoff.json"))) {
		t.Fatal("existing stage was replaced")
	}
	staged, err := core.OpenStore(filepath.Join(target, "app-state"))
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	if _, err := staged.Login(t.Context(), password); err != nil {
		t.Fatal("original owner cannot sign in to staged state", err)
	}
	if !bytes.Equal(originalBytes, save(originalDB)) || !bytes.Equal(manifestBytes, save(originalManifest)) {
		t.Fatal("predecessor bytes changed")
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
