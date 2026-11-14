//go:build linux

package custodyinstall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the same pair description used immediately before the protected
// worker stages an update, with only the OS identities and files substituted.
func TestProtectedPairChecksNativeReleaseBinding(t *testing.T) {
	source := strings.Repeat("a", 40)
	coreHash, signerHash, desktopHash := strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	for _, tc := range []struct {
		name, bindingSource, version, bindingSigner, bindingDesktop string
		bindingErr                                                  bool
		wantOK                                                      bool
	}{
		{"matching", source, "0.1.12", signerHash, desktopHash, false, true},
		{"different signer", source, "0.1.12", strings.Repeat("e", 64), desktopHash, false, false},
		{"invalid desktop", source, "0.1.12", signerHash, "invalid", false, false},
		{"wrong version", source, "0.1.11", signerHash, desktopHash, false, false},
		{"wrong source", strings.Repeat("f", 40), "0.1.12", signerHash, desktopHash, false, false},
		{"binding unavailable", source, "0.1.12", signerHash, desktopHash, true, false},
		{"legacy target", "", "0.1.11", "", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "operation")
			m := Manifest{Owner: 1000}
			bindingCalls := 0
			read := func(_ context.Context, _ Manifest, role, image string, args []string, out any) error {
				var value any
				switch strings.Join(args, " ") {
				case "updates ordinary-custody-compatibility":
					if role != "app" || image != filepath.Join(dir, "fased-app") {
						t.Fatal("core inspection used another image")
					}
					value = map[string]any{"version": 1, "contract": map[string]any{"component": "core"}}
				case "ordinary-custody-compatibility":
					if role != "signer" || image != filepath.Join(dir, "fased-signer") {
						t.Fatal("signer inspection used another image")
					}
					value = map[string]any{"component": "signer"}
				case "native-release-binding":
					bindingCalls++
					if role != "app" || image != filepath.Join(dir, "fased-app") || tc.bindingErr {
						return errors.New("binding unavailable")
					}
					value = map[string]any{"version": 1, "productVersion": tc.version, "source": source, "desktopSha256": tc.bindingDesktop, "signerSha256": tc.bindingSigner}
				default:
					return errors.New("unexpected inspection")
				}
				b, _ := json.Marshal(value)
				return json.Unmarshal(b, out)
			}
			digest := func(path string, mode os.FileMode, max int64) (string, int64, error) {
				if mode != 0755 || max != 64<<20 || filepath.Dir(path) != dir {
					t.Fatal("unexpected protected digest request", path)
				}
				switch filepath.Base(path) {
				case "fased-app":
					return coreHash, 100, nil
				case "fased-signer":
					return signerHash, 100, nil
				default:
					return "", 0, errors.New("unexpected file")
				}
			}
			_, err := describeProtectedPair(t.Context(), m, dir, "0.1.12", tc.bindingSource, read, digest)
			if (err == nil) != tc.wantOK {
				t.Fatal("unexpected protected pair admission", err)
			}
			if tc.bindingSource == "" && bindingCalls != 0 || tc.bindingSource != "" && bindingCalls != 1 {
				t.Fatal("binding was not checked as required", bindingCalls)
			}
		})
	}
}
