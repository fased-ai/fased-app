package update_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

const now int64 = 1800000000

func j(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func fixture(t *testing.T) (update.Manifest, update.Inventory, update.Trust, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	m := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 2, IssuedAt: now - 10, ExpiresAt: now + 600, Target: update.Target{OS: "linux", Arch: "amd64"}}
	inv := update.Inventory{Version: 1, FixtureOnly: true, Target: m.Target, SequenceFloor: 1}
	for _, c := range []update.Contract{core.UpdateContract(), signer.UpdateContract()} {
		m.Components = append(m.Components, update.Component{Version: "0.0.0-fixture.1", Artifact: update.Artifact{File: c.Component, Size: 3, SHA256: sha([]byte("abc"))}, Contract: c})
		for _, s := range c.State {
			inv.States = append(inv.States, update.State{Name: s.Name, Version: s.Versions.Min, Features: append([]string{}, s.Features...), Anchor: sha([]byte(s.Name))})
		}
	}
	trust := update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 100, NotAfter: now + 7*86400}}}
	return m, inv, trust, priv
}
func signed(m update.Manifest, tr update.Trust, key ed25519.PrivateKey) []byte {
	p := j(m)
	return j(update.Envelope{Version: 1, KeyID: tr.Keys[0].ID, Payload: p, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), p...))})
}

func TestDevelopmentAuthenticationRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*update.Manifest, *update.Trust)
	}{
		{"expired", func(m *update.Manifest, _ *update.Trust) { m.ExpiresAt = now }},
		{"future", func(m *update.Manifest, _ *update.Trust) { m.IssuedAt = now + 1 }},
		{"excessive-validity", func(m *update.Manifest, _ *update.Trust) { m.ExpiresAt = m.IssuedAt + 7*86400 + 1 }},
		{"expired-key", func(_ *update.Manifest, tr *update.Trust) { tr.Keys[0].NotAfter = now }},
		{"future-key", func(_ *update.Manifest, tr *update.Trust) { tr.Keys[0].NotBefore = now + 1 }},
		{"signature-predates-key", func(m *update.Manifest, tr *update.Trust) { m.IssuedAt = tr.Keys[0].NotBefore - 1 }},
		{"outlives-key", func(_ *update.Manifest, tr *update.Trust) { tr.Keys[0].NotAfter = now + 1 }},
		{"public-channel", func(m *update.Manifest, _ *update.Trust) { m.Channel = "stable" }},
		{"wrong-product", func(m *update.Manifest, _ *update.Trust) { m.Product = "other" }},
		{"zero-sequence", func(m *update.Manifest, _ *update.Trust) { m.Sequence = 0 }},
		{"wrong-purpose", func(_ *update.Manifest, tr *update.Trust) { tr.Purpose = "release" }},
		{"duplicate-key", func(_ *update.Manifest, tr *update.Trust) { tr.Keys = append(tr.Keys, tr.Keys[0]) }},
		{"wrong-key-id", func(_ *update.Manifest, tr *update.Trust) { tr.Keys[0].ID = strings.Repeat("0", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, tr, key := fixture(t)
			tc.edit(&m, &tr)
			if _, _, err := update.Authenticate(signed(m, tr, key), tr, now); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
	m, _, tr, key := fixture(t)
	b := signed(m, tr, key)
	if _, _, e := update.Authenticate(b, tr, now); e != nil {
		t.Fatal(e)
	}
	for name, bad := range map[string][]byte{
		"tamper":                 bytes.Replace(b, []byte(`"sequence":"2"`), []byte(`"sequence":"3"`), 1),
		"trailing":               append(append([]byte{}, b...), []byte(`{}`)...),
		"unknown-field":          bytes.Replace(b, []byte(`"version":1`), []byte(`"extra":true,"version":1`), 1),
		"duplicate":              bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"case-alias":             bytes.Replace(b, []byte(`"version":1`), []byte(`"Version":1`), 1),
		"oversize":               bytes.Repeat([]byte(" "), update.MaxDocument+1),
		"wrong-envelope-version": bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, e := update.Authenticate(bad, tr, now); e == nil {
				t.Fatal("ambiguous or altered document accepted")
			}
		})
	}
	// A valid signature in another domain must not authorize an update.
	p := j(m)
	e := update.Envelope{Version: 1, KeyID: tr.Keys[0].ID, Payload: p, Signature: ed25519.Sign(key, p)}
	if _, _, err := update.Authenticate(j(e), tr, now); err == nil {
		t.Fatal("cross-domain signature accepted")
	}
	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := update.Authenticate(signed(m, tr, wrongKey), tr, now); err == nil {
		t.Fatal("wrong signer accepted")
	}
}

func TestCompatibilityRejectsUnsafeDowngrades(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*update.Manifest, *update.Inventory)
	}{
		{"replayed-sequence", func(m *update.Manifest, i *update.Inventory) { i.SequenceFloor = m.Sequence }},
		{"lower-sequence", func(m *update.Manifest, i *update.Inventory) { i.SequenceFloor = m.Sequence + 1 }},
		{"platform", func(_ *update.Manifest, i *update.Inventory) { i.Target.OS = "windows" }},
		{"not-fixture", func(_ *update.Manifest, i *update.Inventory) { i.FixtureOnly = false }},
		{"missing-state", func(_ *update.Manifest, i *update.Inventory) { i.States = i.States[:3] }},
		{"duplicate-state", func(_ *update.Manifest, i *update.Inventory) { i.States[3] = i.States[2] }},
		{"unknown-state-version", func(_ *update.Manifest, i *update.Inventory) { i.States[2].Version++ }},
		{"missing-approval-feature", func(m *update.Manifest, _ *update.Inventory) {
			m.Components[1].Contract.State[1].Features = m.Components[1].Contract.State[1].Features[:4]
		}},
		{"inventory-erased-feature", func(_ *update.Manifest, i *update.Inventory) { i.States[2].Features = i.States[2].Features[:4] }},
		{"old-signer-same-protocol", func(m *update.Manifest, _ *update.Inventory) {
			m.Components[1].Contract.Features = m.Components[1].Contract.Features[:1]
		}},
		{"peer-protocol", func(m *update.Manifest, _ *update.Inventory) { m.Components[1].Contract.Protocol++ }},
		{"peer-feature", func(m *update.Manifest, _ *update.Inventory) {
			m.Components[0].Contract.RequiresPeer = append(m.Components[0].Contract.RequiresPeer, "unavailable-v1")
		}},
		{"old-core-schema", func(m *update.Manifest, _ *update.Inventory) {
			m.Components[0].Contract.State[0].Versions = update.Range{Min: 1, Max: 1}
		}},
		{"unknown-format", func(m *update.Manifest, _ *update.Inventory) { m.Components[1].Contract.Version++ }},
		{"missing-component", func(m *update.Manifest, _ *update.Inventory) { m.Components = m.Components[:1] }},
		{"duplicate-component", func(m *update.Manifest, _ *update.Inventory) { m.Components[1] = m.Components[0] }},
		{"path-traversal", func(m *update.Manifest, _ *update.Inventory) { m.Components[0].Artifact.File = "../core" }},
		{"path-absolute", func(m *update.Manifest, _ *update.Inventory) { m.Components[0].Artifact.File = "/core" }},
		{"windows-drive", func(m *update.Manifest, _ *update.Inventory) { m.Components[0].Artifact.File = "c:core" }},
		{"same-artifact", func(m *update.Manifest, _ *update.Inventory) { m.Components[1].Artifact.File = "CORE" }},
		{"unbounded-size", func(m *update.Manifest, _ *update.Inventory) { m.Components[0].Artifact.Size = update.MaxArtifact + 1 }},
		{"invalid-anchor", func(_ *update.Manifest, i *update.Inventory) { i.States[0].Anchor = "" }},
		{"invalid-pending", func(_ *update.Manifest, i *update.Inventory) { i.Unknown = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, i, _, _ := fixture(t)
			tc.edit(&m, &i)
			if _, err := update.Compatible(m, sha(j(m)), i); err == nil {
				t.Fatal("unsafe compatibility accepted")
			}
		})
	}
}

func TestHeldStateIsPreservedWithoutAuthorizingResume(t *testing.T) {
	m, i, _, _ := fixture(t)
	i.Pending = 3
	i.Unknown = 2
	i.RecoveryHold = true
	before := j(i)
	p, e := update.Compatible(m, sha(j(m)), i)
	if e != nil {
		t.Fatal(e)
	}
	if p.InstallAllowed || p.ResumeAllowed || p.UpdaterEnabled || p.WalletSigner || p.FinancialExecution || p.ArtifactsVerified || p.Pending != 3 || p.Unknown != 2 || !p.RecoveryHold || !bytes.Equal(j(p.Preserve), j(i.States)) {
		t.Fatal("authority or state changed")
	}
	p.Preserve[0].Features[0] = "changed"
	if !bytes.Equal(before, j(i)) {
		t.Fatal("plan aliases inventory")
	}
}

func TestCompleteArtifactVerificationIsReadOnly(t *testing.T) {
	root := filepath.Join("..", "..", ".evidence", "p4-updates", "unit-fixtures")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(root, "inspection-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	m, i, tr, key := fixture(t)
	for _, c := range m.Components {
		if e = os.WriteFile(filepath.Join(dir, c.Artifact.File), []byte("abc"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	inspect := func() (update.Plan, error) { return update.Inspect(signed(m, tr, key), j(tr), j(i), dir, now) }
	p, e := inspect()
	if e != nil || !p.ArtifactsVerified || p.InstallAllowed {
		t.Fatal(p, e)
	}
	for _, content := range []string{"", "ab", "abd", "abcd"} {
		if e = os.WriteFile(filepath.Join(dir, "signer"), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = inspect(); e == nil {
			t.Fatal("incomplete or altered artifact accepted")
		}
	}
	if e = os.Remove(filepath.Join(dir, "signer")); e != nil {
		t.Fatal(e)
	}
	if _, e = inspect(); e == nil {
		t.Fatal("missing file accepted")
	}
	if e = os.Mkdir(filepath.Join(dir, "signer"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = inspect(); e == nil {
		t.Fatal("directory accepted")
	}
	if e = os.Remove(filepath.Join(dir, "signer")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("core", filepath.Join(dir, "signer")); e == nil {
		if _, e = inspect(); e == nil {
			t.Fatal("symlink accepted")
		}
	}
	b, e := os.ReadFile(filepath.Join(dir, "core"))
	if e != nil || string(b) != "abc" {
		t.Fatal("inspection changed artifact")
	}
}

func TestStrictJSONRejectsAmbiguity(t *testing.T) {
	for _, s := range []string{`{"version":1,"\u0076ersion":1}`, `{"Version":1}`, `{"version":1,"version":2}`, `{"version":1,"evil":1}`, `{} {}`, `{"version":1,}`, `{"version":1.0}`, strings.Repeat("[", 22) + strings.Repeat("]", 22), "{\"version\":1,\"x\":\"\xff\"}"} {
		var x struct {
			Version int `json:"version"`
		}
		if e := update.StrictJSON([]byte(s), &x); e == nil {
			t.Fatal("ambiguous JSON accepted", s)
		}
	}
}

func TestCandidateMustKeepLifecycleEntrypoint(t *testing.T) {
	m, inv, _, _ := fixture(t)
	// Runtime/state compatibility alone cannot admit a core which lost its
	// protected owner lifecycle entrypoint.
	b := j(m)
	var doc map[string]any
	if e := json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	for _, raw := range doc["components"].([]any) {
		c := raw.(map[string]any)["contract"].(map[string]any)
		if c["component"] == "core" {
			delete(c, "lifecycle")
		}
	}
	m = update.Manifest{}
	if e := json.Unmarshal(j(doc), &m); e != nil {
		t.Fatal(e)
	}
	if _, e := update.Compatible(m, sha([]byte("manifest")), inv); e == nil {
		t.Fatal("candidate without lifecycle entrypoint accepted")
	}
}

func TestLifecycleVersionsAndFeaturesAreIndependent(t *testing.T) {
	for _, tc := range []string{"entrypoint", "stage-format", "features", "journal"} {
		t.Run(tc, func(t *testing.T) {
			m, inv, _, _ := fixture(t)
			for n := range m.Components {
				c := &m.Components[n].Contract
				if c.Component == "core" {
					switch tc {
					case "entrypoint":
						c.Lifecycle.Entrypoint = update.CurrentLifecycle().Entrypoint + 1
					case "stage-format":
						c.Lifecycle.StageFormat = 2
					case "features":
						c.Lifecycle.Features = nil
					}
				}
				if tc == "journal" && c.Component == "signer" {
					for j := range c.State {
						if c.State[j].Name == "operations" {
							c.State[j].Features = c.State[j].Features[:len(c.State[j].Features)-1]
						}
					}
				}
			}
			if _, e := update.Compatible(m, sha([]byte("manifest")), inv); e == nil {
				t.Fatal("lifecycle downgrade accepted")
			}
		})
	}
}
