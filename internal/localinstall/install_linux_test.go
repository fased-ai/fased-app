package localinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func installFixture(t *testing.T) (Paths, string) {
	t.Helper()
	root, err := filepath.Abs("../../.evidence/install-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "f-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	engine := filepath.Join(dir, "source-core")
	if err = os.WriteFile(engine, []byte("reconstructible binary fixture, never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	return Paths{Root: filepath.Join(dir, "app"), Data: filepath.Join(dir, "state")}, engine
}
func TestInstallRetryIdentityAndData(t *testing.T) {
	p, engine := installFixture(t)
	m, err := Install(t.Context(), p, Options{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if m.Phase != "ready" || m.Autostart {
		t.Fatal("unexpected install state")
	}
	keep := filepath.Join(p.Data, "owner-state")
	if err = os.WriteFile(keep, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(p.Root, "fased-app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(p.Root, "fased-app"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("same identity was rewritten")
	}
	if err = os.WriteFile(engine, []byte("different source"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine}); err == nil {
		t.Fatal("unverified update was accepted")
	}
	data, err := os.ReadFile(keep)
	if err != nil || string(data) != "preserve" {
		t.Fatal("app data changed")
	}
	if err = os.WriteFile(filepath.Join(p.Root, "fased-app"), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = verify(m); err == nil {
		t.Fatal("tampered executable accepted")
	}
}

func TestVerifiedInstallRefusesChangedInputBeforeMutation(t *testing.T) {
	for _, kind := range []string{"engine", "desktop", "malformed", "uppercase", "missing-desktop"} {
		t.Run(kind, func(t *testing.T) {
			p, engine := installFixture(t)
			want, err := digest(engine)
			if err != nil {
				t.Fatal(err)
			}
			o := Options{Engine: engine, EngineSHA256: want}
			switch kind {
			case "engine":
				if err = os.WriteFile(engine, []byte("changed after bundle verification"), 0700); err != nil {
					t.Fatal(err)
				}
			case "desktop":
				o.Desktop = engine
				o.DesktopSHA256 = strings.Repeat("0", 64)
			case "malformed":
				o.EngineSHA256 = "not-a-digest"
			case "uppercase":
				o.EngineSHA256 = strings.ToUpper(want)
			case "missing-desktop":
				o.DesktopSHA256 = want
			}
			if _, err = Install(t.Context(), p, o); err == nil {
				t.Fatal("verified source mismatch accepted", kind)
			}
			for _, path := range []string{p.Root, p.Data} {
				if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("invalid source touched installation/state", path, err)
				}
			}
		})
	}
}

func TestVerifiedStagingRejectsChangedCopyAndAllowsRetry(t *testing.T) {
	p, engine := installFixture(t)
	want, err := digest(engine)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.EnsurePrivateDirectory(p.Root); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(p.Root, engineName)
	if err = writeNew(target, strings.NewReader("changed while source was copied"), 0700, want); err == nil {
		t.Fatal("changed copy published as verified application")
	}
	for _, path := range []string{target, filepath.Join(p.Root, ".staging-"+engineName)} {
		if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected copy left an owned file", path, err)
		}
	}
	// An interrupted preparing receipt may retry the same verified source; bad
	// staged bytes must not poison its final target or affect the state directory.
	input, err := os.Open(engine)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = writeNew(target, input, 0700, want); err != nil {
		t.Fatal("verified source could not retry", err)
	}
	if got, err := digest(target); err != nil || got != want {
		t.Fatal("restored source identity changed", got, err)
	}
}

func TestVerifiedInstallRetryPreservesExistingState(t *testing.T) {
	p, engine := installFixture(t)
	want, err := digest(engine)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Engine: engine, EngineSHA256: want}
	if _, err = Install(t.Context(), p, o); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(p.Data, "retained-owner-record")
	if err = os.WriteFile(state, []byte("disposable preserved record"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(p.Root, "install.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, o); err != nil {
		t.Fatal("identical verified retry failed", err)
	}
	if err = os.WriteFile(engine, []byte("substituted after bundle verification"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, o); err == nil {
		t.Fatal("verified retry accepted substituted source")
	}
	after, err := os.ReadFile(filepath.Join(p.Root, "install.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("refused retry changed installation identity", err)
	}
	if data, err := os.ReadFile(state); err != nil || string(data) != "disposable preserved record" {
		t.Fatal("verified retry changed owner state", err)
	}
	if got, err := digest(filepath.Join(p.Root, engineName)); err != nil || got != want {
		t.Fatal("verified retry replaced installed bytes", got, err)
	}
}
func TestInterruptedFirstInstallResumesWithoutAdoptingUnknownFiles(t *testing.T) {
	p, engine := installFixture(t)
	m, err := Install(t.Context(), p, Options{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	m.Phase = "preparing"
	if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(p.Root, "fased-app")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.Root, ".staging-fased-app"), []byte("partial copy"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(p.Root, ".staging-fased-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging copy remains")
	}
	other, source := installFixture(t)
	if err = os.MkdirAll(other.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(other.Root, "unknown"), []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(context.Background(), other, Options{Engine: source}); err == nil {
		t.Fatal("unknown directory was adopted")
	}
}
func TestDefinitionRejectsExposureAndEscapesPaths(t *testing.T) {
	p, _ := installFixture(t)
	if _, err := definition(p, Options{Listen: "0.0.0.0:7331"}); err == nil {
		t.Fatal("public listener allowed")
	}
	if _, err := definition(p, Options{Listen: "127.0.0.1:7331", PublicOrigin: "http://public.example"}); err == nil {
		t.Fatal("insecure proxy origin allowed")
	}
	p.Root += " space%$\""
	unit, err := linuxDefinition(p, Options{Listen: "127.0.0.1:7331"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "space%%$$\\\"") || !strings.Contains(string(unit), "NoNewPrivileges=yes") || !strings.Contains(string(unit), "ProtectHome=read-only") || !strings.Contains(string(unit), "ReadWritePaths=") {
		t.Fatal("service quoting or hardening absent")
	}
	if !strings.Contains(string(unit), "PrivateUsers=yes") || !strings.Contains(string(unit), "--require-sandbox") {
		t.Fatal("user namespace or enforcement check missing")
	}
	unit, err = linuxDefinition(p, Options{Listen: "127.0.0.1:7331"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ExecStart=/usr/bin/bwrap --unshare-user --disable-userns --cap-drop ALL", "--ro-bind / / --bind " + quote(p.Data) + " " + quote(p.Data), "--unshare-pid", "--require-sandbox", "space%%$$\\\"", "NoNewPrivileges=yes"} {
		if !strings.Contains(string(unit), required) {
			t.Fatalf("Ubuntu sandbox lacks %q", required)
		}
	}
	for _, forbidden := range []string{"PrivateUsers=", "PrivateTmp=", "ProtectSystem=", "ProtectHome=", "ProtectKernelTunables=", "ProtectControlGroups=", "ReadOnlyPaths=", "ReadWritePaths="} {
		if strings.Contains(string(unit), forbidden) {
			t.Fatalf("systemd would create a restricted parent namespace: %s", forbidden)
		}
	}
}

func TestUbuntuSandboxSelection(t *testing.T) {
	for _, release := range []string{"ID=ubuntu\nVERSION_ID=24.04\n", "ID=\"ubuntu\"\nVERSION_ID=\"24.04\"\n"} {
		if !ubuntu2404(release) {
			t.Fatal("Ubuntu 24.04 did not select its profiled helper")
		}
	}
	for _, release := range []string{"ID=fedora\nVERSION_ID=44\n", "ID=ubuntu\nVERSION_ID=22.04\n", "ID_LIKE=ubuntu\nVERSION_ID=24.04\n", ""} {
		if ubuntu2404(release) {
			t.Fatal("unsupported OS selected Ubuntu prerequisites")
		}
	}
}

func TestNestedPathsAndInterruptedRemoval(t *testing.T) {
	p, engine := installFixture(t)
	for _, bad := range []Paths{{Root: filepath.Join(p.Data, "app"), Data: p.Data}, {Root: p.Root, Data: filepath.Join(p.Root, "state")}} {
		if _, e := Install(t.Context(), bad, Options{Engine: engine}); e == nil {
			t.Fatal("code/state containment accepted")
		}
	}
	m, e := Install(t.Context(), p, Options{Engine: engine})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p.Data, "keep"), []byte("preserve owner data"), 0600); e != nil {
		t.Fatal(e)
	}
	// Unit fixture represents the durable point after external unregistration;
	// it is not evidence of killing a real service transaction.
	m.Phase = "removing"
	if e = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(p.Root, engineName)); e != nil {
		t.Fatal(e)
	}
	if e = Uninstall(t.Context(), p); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(filepath.Join(p.Data, "keep")); e != nil || string(b) != "preserve owner data" {
		t.Fatal("state was removed")
	}
}

func TestInstalledSandboxRefusesOrdinaryWritableProcess(t *testing.T) {
	p, _ := installFixture(t)
	for _, dir := range []string{p.Root, p.Data} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckSandbox(p.Root, p.Data); err == nil {
		t.Fatal("writable ordinary process passed installed enforcement")
	}
}
