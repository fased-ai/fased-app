//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func TestApplicationOwnerOfflineTerminalCleanupProof(t *testing.T) {
	for _, phase := range []string{"completed", "rolled-back"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			engine := filepath.Join(root, "inert-core")
			if e := os.WriteFile(engine, []byte("inert application; never execute"), 0700); e != nil {
				t.Fatal(e)
			}
			paths := localinstall.Paths{Root: filepath.Join(root, "installed"), Data: filepath.Join(root, "data")}
			previous, e := localinstall.Install(t.Context(), paths, localinstall.Options{Engine: engine})
			if e != nil {
				t.Fatal(e)
			}
			target := previous
			target.Version = "0.0.0-next"
			target.ApplicationSequence = 7
			target.ApplicationReleaseSHA256 = strings.Repeat("a", 64)
			installed := target
			if phase == "rolled-back" {
				installed = previous
			}
			if e = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), installed); e != nil {
				t.Fatal(e)
			}
			journal := localinstall.ApplicationUpdateStatus{Version: 1, ID: strings.Repeat("1", 32), Phase: phase, Sequence: 7, ReleaseSHA256: target.ApplicationReleaseSHA256, EngineSHA256: target.Files["fased-app"], PreviousManifestSHA256: localinstall.ManifestSHA256(previous), TargetManifestSHA256: localinstall.ManifestSHA256(target)}
			dir := filepath.Join(root, ".installed-updates")
			if e = os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e = core.WritePrivateJSON(filepath.Join(dir, "current.json"), map[string]any{"status": journal, "previous": previous, "target": target}); e != nil {
				t.Fatal(e)
			}
			state := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Operation: core.ApplicationUpdateOperation{ID: journal.ID, Phase: phase, Release: &core.ApplicationRelease{Sequence: journal.Sequence, ManifestSHA256: journal.ReleaseSHA256}, Receipt: &core.ApplicationUpdateReceipt{ID: journal.ID, Phase: phase, Sequence: journal.Sequence, ManifestSHA256: journal.ReleaseSHA256, EngineSHA256: installed.Files["fased-app"]}}}}
			if e = verifyLocalTerminalOutcome(context.Background(), installed, state, journal); e != nil {
				t.Fatal(e)
			}
			state.View.Operation.Receipt.ID = strings.Repeat("2", 32)
			if e = verifyLocalTerminalOutcome(t.Context(), installed, state, journal); e == nil {
				t.Fatal("foreign receipt authorized cleanup")
			}
			state.View.Operation.Receipt.ID = journal.ID
			if e = os.WriteFile(filepath.Join(paths.Root, "fased-app"), []byte("changed"), 0700); e != nil {
				t.Fatal(e)
			}
			if e = verifyLocalTerminalOutcome(t.Context(), installed, state, journal); e == nil {
				t.Fatal("changed installed bytes authorized cleanup")
			}
		})
	}
}
