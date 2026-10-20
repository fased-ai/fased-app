package update_test

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/update"
	"testing"
)

func bootstrapFixture(t *testing.T) (update.BootstrapBundle, []byte) {
	target, _, trust, key := fixture(t)
	for n := range target.Components {
		file := "fased-app"
		if target.Components[n].Contract.Component == "signer" {
			file = "fased-signer"
		}
		target.Components[n].Artifact.File = file
	}
	var source update.Manifest
	json.Unmarshal(j(target), &source)
	for n := range source.Components {
		c := &source.Components[n].Contract
		if c.Component == "core" {
			c.Lifecycle.Entrypoint = 3
			c.Lifecycle.Features = c.Lifecycle.Features[:5]
		}
		for m := range c.State {
			if c.State[m].Name == "operations" {
				c.State[m].Features = c.State[m].Features[:len(c.State[m].Features)-1]
			}
		}
	}
	target.BootstrapFrom = sha(j(source))
	return update.BootstrapBundle{Version: 1, Maintenance: signed(source, trust, key), Target: signed(target, trust, key)}, j(trust)
}
func TestSignedBootstrapBindingAndOrdinaryAdmission(t *testing.T) {
	bundle, trust := bootstrapFixture(t)
	plan, e := update.VerifyBootstrap(bundle, trust, now)
	if e != nil {
		t.Fatal(e)
	}
	tr, e := update.ParseTrust(trust, now)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = update.Authenticate(bundle.Maintenance, tr, now); e == nil {
		t.Fatal("ordinary authentication accepted predecessor")
	}
	if _, e = update.Compatible(plan.Target, plan.TargetSHA256, update.Inventory{}); e == nil {
		t.Fatal("ordinary admission accepted bootstrap target")
	}
	for _, bad := range []string{"unsigned", "cross-target", "expiry", "wrong-predecessor", "signature"} {
		t.Run(bad, func(t *testing.T) {
			b, trust := bootstrapFixture(t)
			at := now
			switch bad {
			case "unsigned":
				b.Target = nil
			case "cross-target":
				b.Target, b.Maintenance = b.Maintenance, b.Target
			case "expiry":
				at += 1000
			case "wrong-predecessor":
				b.Version = 2
			case "signature":
				b.Target = append([]byte{}, b.Target...)
				b.Target[len(b.Target)-8] ^= 1
			}
			if _, e := update.VerifyBootstrap(b, trust, at); e == nil {
				t.Fatal("unsafe bundle admitted")
			}
		})
	}
}
func TestBootstrapFinancialComparison(t *testing.T) {
	before := update.SignerSnapshot{ActiveIntentID: "held", SequenceFloor: 4, Spent: 70, Held: 30, Unknown: 1, RecoveryHold: true, States: []update.State{{Name: "operations", Features: []string{"install-controller-v1"}, Anchor: "head"}, {Name: "vault", Anchor: "key"}}}
	copySnapshot := func() update.SignerSnapshot {
		var s update.SignerSnapshot
		json.Unmarshal(j(before), &s)
		s.States[0].Features = append(s.States[0].Features, "install-completion-v1")
		return s
	}
	if !update.BootstrapHeld(before, copySnapshot(), false) {
		t.Fatal("declared reader bridge refused")
	}
	for _, change := range []string{"head", "floor", "unknown", "spent", "key", "installed", "feature"} {
		s := copySnapshot()
		switch change {
		case "head":
			s.States[0].Anchor = "new"
		case "floor":
			s.SequenceFloor++
		case "unknown":
			s.Unknown = 0
		case "spent":
			s.Spent = 0
		case "key":
			s.States[1].Anchor = "different"
		case "installed":
			s.InstalledSequence = 1
		case "feature":
			s.States[0].Features = append(s.States[0].Features, "unknown-v9")
		}
		if update.BootstrapHeld(before, s, false) {
			t.Fatal("financial change accepted", change)
		}
	}
	s := copySnapshot()
	s.ActiveIntentID = ""
	s.States[0].Anchor = "abort-head"
	if !update.BootstrapHeld(before, s, true) {
		t.Fatal("actual abort comparison refused")
	}
}
