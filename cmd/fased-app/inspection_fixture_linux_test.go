//go:build linux

package main

import (
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"strings"
	"testing"
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
