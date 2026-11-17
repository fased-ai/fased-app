//go:build linux

package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttestedNativeUpdateUsesTargetCoreAndInstaller(t *testing.T) {
	for _, variant := range []string{"matching", "substituted-desktop", "substituted-signer"} {
		t.Run(variant, func(t *testing.T) {
			desktop, signer := nativeFixtureDesktop, nativeFixtureSigner
			if variant == "substituted-desktop" {
				desktop = "different native client"
			}
			if variant == "substituted-signer" {
				signer = "different signer"
			}
			installer := filepath.Join(t.TempDir(), "installer")
			if err := os.WriteFile(installer, nativeFixtureInstaller(t, desktop, signer), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FASED_TEST_CANDIDATE_INSTALLER", installer)
			f, options, payloads := attestedFixture(t, "staged-installer-file", "native-binding")
			candidate := filepath.Join(f.parent, "candidate")
			if err := os.Mkdir(candidate, 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range payloads {
				if err := os.WriteFile(filepath.Join(candidate, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			id := strings.Repeat("1", 32)
			admission := CandidateAdmission{PacketSHA256: digest(payloads[AttestedPacketFile]), OperationID: id, FreshProfile: "local", FreshOwner: os.Getuid(), FreshRoot: filepath.Join(f.parent, "app"), FreshData: filepath.Join(f.parent, "data"), ExpiresAt: f.now.Add(10 * time.Minute).Unix()}
			o := Options{Seed: options.Seed, SeedSHA256: options.SeedSHA256, Directory: filepath.Join(f.parent, "held"), now: options.now}
			p, err := PrepareAttestedCandidate(t.Context(), o, candidate, admission)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			p.mu.Lock()
			got, cleanup, err := p.nativeUpdateFiles(t.Context(), id)
			p.mu.Unlock()
			if variant != "matching" {
				if err == nil || cleanup != nil {
					t.Fatal("installer signer substitution was accepted", err)
				}
				matches, _ := filepath.Glob(filepath.Join(o.Directory, "native-"+id+"-*"))
				if len(matches) != 0 {
					t.Fatal("refused pair left owned scratch files", matches)
				}
				return
			}
			if err != nil {
				t.Fatal("attested pair refused", err)
			}
			for path, expected := range map[string]string{got.Desktop: nativeFixtureDesktop, got.Signer: nativeFixtureSigner} {
				body, err := os.ReadFile(path)
				if err != nil || string(body) != expected {
					t.Fatal("wrong extracted payload", path, err)
				}
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
