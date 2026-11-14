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

	"github.com/fased-ai/fased-app/internal/update"
)

// Exercise the same pair description used immediately before the protected
// worker stages an update, with only the OS identities and files substituted.
func TestProtectedPairChecksNativeReleaseBinding(t *testing.T) {
	source := strings.Repeat("a", 40)
	coreHash, signerHash, desktopHash := strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	for _, tc := range []struct {
		name, bindingSource, version, bindingSigner, bindingDesktop string
		bindingVersion                                              int
		bindingErr                                                  bool
		wantOK                                                      bool
	}{
		{"matching", source, "0.1.12", signerHash, desktopHash, 1, false, true},
		{"different signer", source, "0.1.12", strings.Repeat("e", 64), desktopHash, 1, false, false},
		{"invalid desktop", source, "0.1.12", signerHash, "invalid", 1, false, false},
		{"wrong version", source, "0.1.11", signerHash, desktopHash, 1, false, false},
		{"wrong source", strings.Repeat("f", 40), "0.1.12", signerHash, desktopHash, 1, false, false},
		{"binding unavailable", source, "0.1.12", signerHash, desktopHash, 1, true, false},
		{"legacy target", "", "0.1.11", "", "", 1, true, true},
		{"headless signer", source, "0.1.12", signerHash, "", 2, false, true},
		{"headless mismatched signer", source, "0.1.12", strings.Repeat("e", 64), "", 2, false, false},
		{"headless claimed desktop", source, "0.1.12", signerHash, desktopHash, 2, false, false},
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
					value = map[string]any{"version": tc.bindingVersion, "productVersion": tc.version, "source": source, "desktopSha256": tc.bindingDesktop, "signerSha256": tc.bindingSigner}
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

// A Local custody update cannot claim that its native client was updated just
// because the target core contains a syntactically valid desktop hash.
func TestProtectedLocalPairRequiresMatchingDesktopBytes(t *testing.T) {
	source := strings.Repeat("a", 40)
	coreHash, signerHash, desktopHash := strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	dir := filepath.Join(t.TempDir(), "operation")
	bindingVersion, bindingDesktop := 1, desktopHash
	read := func(_ context.Context, _ Manifest, _, _ string, args []string, out any) error {
		var value any
		switch strings.Join(args, " ") {
		case "updates ordinary-custody-compatibility":
			value = map[string]any{"version": 1, "contract": map[string]any{"component": "core", "features": []string{update.NativeDesktopJournalFeature}}}
		case "ordinary-custody-compatibility":
			value = map[string]any{"component": "signer", "features": []string{update.NativeDesktopJournalFeature}}
		case "native-release-binding":
			value = map[string]any{"version": bindingVersion, "productVersion": "0.1.12", "source": source, "desktopSha256": bindingDesktop, "signerSha256": signerHash}
		default:
			return errors.New("unexpected inspection")
		}
		b, _ := json.Marshal(value)
		return json.Unmarshal(b, out)
	}
	for _, tc := range []struct {
		name, desktop string
		version       int
		wantOK        bool
	}{
		{"matching", desktopHash, 1, true},
		{"wrong desktop", strings.Repeat("e", 64), 1, false},
		{"missing desktop", "", 1, false},
		{"headless cannot replace desktop", desktopHash, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindingVersion, bindingDesktop = tc.version, desktopHash
			if tc.version == 2 {
				bindingDesktop = ""
			}
			digest := func(path string, _ os.FileMode, _ int64) (string, int64, error) {
				switch filepath.Base(path) {
				case "fased-app":
					return coreHash, 100, nil
				case "fased-signer":
					return signerHash, 100, nil
				case "fased-desktop":
					if tc.desktop != "" {
						return tc.desktop, 100, nil
					}
				}
				return "", 0, os.ErrNotExist
			}
			_, err := describeProtectedPair(t.Context(), Manifest{Owner: 1000, NativeOwnerAccess: true}, dir, "0.1.12", source, read, digest)
			if (err == nil) != tc.wantOK {
				t.Fatal("desktop admission did not match authenticated core binding", err)
			}
		})
	}
}
