//go:build linux

package custodyinstall

import (
	"github.com/fased-ai/fased-app/internal/update"
	"strings"
	"testing"
)

func TestInspectionGuardedUnitsBeforeFence(t *testing.T) {
	m := fixtureManifest()
	m.InspectionBridge = strings.Repeat("a", 32)
	for _, candidate := range []bool{true, false} {
		u := fixtureUnits(m, candidate)
		for _, role := range []string{"app", "signer"} {
			text := string(u["fased-"+role+".service"])
			if !strings.Contains(text, "fixture-reader-guard --owner 1000 --disposable-fixture") || !strings.Contains(text, "NoNewPrivileges=yes") || !strings.Contains(text, "ProtectSystem=strict") {
				t.Fatal("unguarded unit")
			}
			if role == "signer" {
				if !strings.Contains(text, "RestrictAddressFamilies=AF_UNIX\n") || !strings.Contains(text, "/fixture-inspection-v1/signer/anchor") {
					t.Fatal("signer widened sandbox")
				}
			}
		}
		if InspectionUnitDigest(m, candidate) == update.SnapshotDigest("fixture-unit-pair:v1", []string{string(definitions(fixtureManifest())["fased-app.service"]), string(definitions(fixtureManifest())["fased-signer.service"])}) {
			t.Fatal("predecessor accepts guarded units")
		}
	}
	for _, n := range []string{"../escape", "bundle/../foreign", "bundle/a/b", "owner-private.json", "bundle/.."} {
		if inspectionInputName(n) {
			t.Fatal("unbounded publisher", n)
		}
	}
}
