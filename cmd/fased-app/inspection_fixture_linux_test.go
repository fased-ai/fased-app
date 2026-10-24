//go:build linux

package main

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectionUnchangedFinancialHistory(t *testing.T) {
	before := update.SignerSnapshot{Version: 1, ProbeID: update.NewProbe(), BootID: update.NewProbe(), AuthoritySHA256: strings.Repeat("a", 64), InstalledSequence: 3, SequenceFloor: 4, Spent: 75, ActiveIntentID: update.NewProbe(), States: []update.State{{Name: "operations", Version: 1, Features: []string{"install-completion-v1"}, Anchor: strings.Repeat("b", 64)}}}
	tx := custodyinstall.BootstrapTransaction{}
	tx.Target.InspectionBridge = update.NewProbe()
	now := before
	now.BootID = update.NewProbe()
	now.ProbeID = update.NewProbe()
	if !maintenanceHistoryHeld(tx, before, now, false) {
		t.Fatal("unchanged completion-capable history rejected")
	}
	if update.BootstrapHeld(before, now, false) {
		t.Fatal("test must distinguish the older feature-changing comparison")
	}
	for _, field := range []string{"spent", "held", "installed", "reserved", "authority", "features"} {
		t.Run(field, func(t *testing.T) {
			x := now
			switch field {
			case "spent":
				x.Spent++
			case "held":
				x.Held++
			case "installed":
				x.InstalledSequence++
			case "reserved":
				x.SequenceFloor++
			case "authority":
				x.AuthoritySHA256 = strings.Repeat("c", 64)
			case "features":
				x.States = append([]update.State(nil), x.States...)
				x.States[0].Features = nil
			}
			if maintenanceHistoryHeld(tx, before, x, false) {
				t.Fatal("financial change accepted")
			}
		})
	}
	now.ActiveIntentID = ""
	now.States = append([]update.State(nil), now.States...)
	now.States[0].Anchor = strings.Repeat("d", 64)
	if !maintenanceHistoryHeld(tx, before, now, true) {
		t.Fatal("exact maintenance abort rejected")
	}
	now.InstalledSequence++
	if maintenanceHistoryHeld(tx, before, now, true) {
		t.Fatal("abort rewound/changed installed floor")
	}
}

func TestInspectionPTYDrainsFastCancellation(t *testing.T) {
	raw, err := os.ReadFile("../../scripts/fixture-inspection-journey.mjs")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), "export const fixtureInspectionPTY = String.raw`")
	if !ok {
		t.Fatal("PTY fixture absent")
	}
	code, _, ok := strings.Cut(body, "`;\n")
	if !ok {
		t.Fatal("PTY fixture end absent")
	}
	// Force child exit after consent, while its final message is still unread.
	marker := "  p,status=os.waitpid(pid,os.WNOHANG)"
	if !strings.Contains(code, marker) {
		t.Fatal("missing wait boundary")
	}
	code = strings.Replace(code, marker, "  time.sleep(.05)\n"+marker, 1)
	binary := filepath.Join(t.TempDir(), "fast-cancel")
	script := "#!/usr/bin/python3\nimport sys\nprint('Type BRIDGE FIXTURE '+('a'*64)+':',flush=True)\ninput()\nprint('bridge review cancelled',file=sys.stderr,flush=True)\nsys.exit(1)\n"
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", "-c", code, binary, "provision", "fixture-id", "fixture-tx", "fixture-inputs", "fixture-artifacts", "cancel", "").CombinedOutput()
	if err != nil {
		t.Fatalf("PTY fixture: %v %s", err, out)
	}
	if !strings.Contains(string(out), "bridge review cancelled") || !strings.Contains(string(out), `"exitCode": 1`) {
		t.Fatalf("final cancellation output lost: %s", out)
	}
}
