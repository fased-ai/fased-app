//go:build linux

package custodyinstall

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"os/exec"
	"strings"
	"testing"
	"time"
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
	for _, n := range []string{"../escape", "bundle/../foreign", "bundle/a/b", "owner-private.json", "bundle/..", "bundle/arbitrary.exe", "bundle/unknown.json"} {
		if inspectionInputName(n) {
			t.Fatal("unbounded publisher", n)
		}
	}
}

func inspectionBridgePlanFixture(t *testing.T) InspectionBridge {
	t.Helper()
	base := fixtureManifest()
	id := strings.Repeat("b", 32)
	now := time.Now().Unix()
	tx := InspectionBridge{Version: 1, Profile: "fased-app-fixture-reader-bridge:v1", ID: id, Base: base, Target: base, CatalogSHA: strings.Repeat("c", 64), ReviewedAt: now, Inputs: map[string]string{}}
	tx.Target.InspectionBridge = id
	tx.Target.CoreHash = sum([]byte("candidate core"))
	tx.Target.SignerHash = sum([]byte("candidate signer"))
	for i := 0; i < 10; i++ {
		tx.Inputs[fmt.Sprint(i)] = strings.Repeat("d", 64)
	}
	cs := []update.Contract{core.UpdateContract(), signer.UpdateContract()}
	tx.Core = update.CoreSnapshot{Version: 1, ProbeID: id, BootID: id, InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 3, Features: cs[0].State[0].Features, Anchor: sum([]byte("core state"))}}
	tx.Signer = update.SignerSnapshot{Version: 1, ProbeID: id, BootID: id, AuthoritySHA256: sum([]byte("authority")), InstalledSequence: 2, SequenceFloor: 3}
	for _, s := range cs[1].State {
		tx.Signer.States = append(tx.Signer.States, update.State{Name: s.Name, Version: s.Versions.Min, Features: s.Features, Anchor: sum([]byte(s.Name))})
	}
	signInspectionMaintenance(t, &tx, cs)
	return tx
}
func signInspectionMaintenance(t *testing.T, tx *InspectionBridge, contracts []update.Contract) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	trust := update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: tx.ReviewedAt - 60, NotAfter: tx.ReviewedAt + 600}}}
	m := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 4, IssuedAt: tx.ReviewedAt - 1, ExpiresAt: tx.ReviewedAt + 300, Target: update.Target{OS: "linux", Arch: "amd64"}}
	for _, c := range contracts {
		file, hash := "fased-app", tx.Base.CoreHash
		if c.Component == "signer" {
			file, hash = "fased-signer", tx.Base.SignerHash
		}
		m.Components = append(m.Components, update.Component{Version: "0.0.0-disposable", Artifact: update.Artifact{File: file, Size: 17, SHA256: hash}, Contract: c})
	}
	b := mustJSON(m)
	tx.Maintenance = mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: b, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), b...))})
	tx.Trust = mustJSON(trust)
}
func TestInspectionBridgeFiniteLifecycleContracts(t *testing.T) {
	tx := inspectionBridgePlanFixture(t)
	if signer.UpdateContract().Lifecycle != nil {
		t.Fatal("test must cover the actual signer contract shape")
	}
	if _, e := tx.Plan(); e != nil {
		t.Fatal("current unchanged financial pair refused", e)
	}
	t.Run("old-core", func(t *testing.T) {
		x := inspectionBridgePlanFixture(t)
		cs := []update.Contract{core.UpdateContract(), signer.UpdateContract()}
		cs[0].Lifecycle.Entrypoint = 3
		signInspectionMaintenance(t, &x, cs)
		if _, e := x.Plan(); e == nil {
			t.Fatal("old core accepted")
		}
	})
	t.Run("pending-held", func(t *testing.T) {
		x := inspectionBridgePlanFixture(t)
		x.Signer.Pending = 1
		if _, e := x.Plan(); e == nil {
			t.Fatal("pending financial operation accepted")
		}
	})
	t.Run("missing-signer-completion", func(t *testing.T) {
		x := inspectionBridgePlanFixture(t)
		cs := []update.Contract{core.UpdateContract(), signer.UpdateContract()}
		for i := range cs[1].State {
			if cs[1].State[i].Name == "operations" {
				cs[1].State[i].Features = cs[1].State[i].Features[:len(cs[1].State[i].Features)-1]
			}
		}
		signInspectionMaintenance(t, &x, cs)
		if _, e := x.Plan(); e == nil {
			t.Fatal("signer without completion format accepted")
		}
	})
}

func TestInspectionBridgeGeneratorDigest(t *testing.T) {
	c := exec.Command("node", "--input-type=module", "-e", `import{fixtureInspectionDigest as digest}from './scripts/fixture-inspection-journey.mjs';process.stdout.write(digest('fixture-unit-pair:v1',['first\nline','second=policy']));`)
	c.Dir = "../.."
	got, e := c.Output()
	if e != nil {
		t.Fatal(e)
	}
	want := update.SnapshotDigest("fixture-unit-pair:v1", []string{"first\nline", "second=policy"})
	if string(got) != want {
		t.Fatalf("generator digest disagrees with Go: %s != %s", got, want)
	}
}
