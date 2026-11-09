package update_test

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"testing"
)

func TestOrdinaryBootstrapSignedFiniteReaderDelta(t *testing.T) {
	for _, bad := range []string{"", "new-reader-support", "omit-maintenance-reader", "omit-completion-reader", "peer", "schema", "provenance", "financial-feature", "unknown-reader-feature", "wrong-source", "unsigned", "normal-slot"} {
		t.Run(bad, func(t *testing.T) {
			base, _, trust, key := fixture(t)
			base.Components = nil
			next := base
			for _, c := range []update.Contract{core.UpdateContract(), signer.OrdinaryCustodyReadContract()} {
				file := "fased-app"
				if c.Component == "signer" {
					file = "fased-signer"
				}
				base.Components = append(base.Components, update.Component{Version: "0.0.0-test", Contract: c, Artifact: update.Artifact{File: file, Size: 3, SHA256: sha([]byte("old" + file))}})
			}
			for _, c := range []update.Contract{core.OrdinaryUpdateContract(), signer.OrdinaryUpdateContract()} {
				file := "fased-app"
				if c.Component == "signer" {
					file = "fased-signer"
				}
				next.Components = append(next.Components, update.Component{Version: "0.0.0-test", Contract: c, Artifact: update.Artifact{File: file, Size: 4, SHA256: sha([]byte("next" + file))}})
			}
			switch bad {
			case "omit-maintenance-reader":
				next.Components[1].Contract.State[1].Features = []string{"ordinary-key-genesis-v1", "install-completion-v1"}
			case "omit-completion-reader":
				next.Components[1].Contract.State[1].Features = []string{"ordinary-key-genesis-v1", "ordinary-maintenance-v1"}
			case "peer":
				next.Components[0].Contract.RequiresPeer = nil
			case "schema":
				next.Components[0].Contract.State[0].Versions.Max++
			case "provenance":
				next.Components[1].Contract.State[0].Features = append(next.Components[1].Contract.State[0].Features, "other-owner-provenance")
			case "new-reader-support":
				base.Components[0].Contract.State = append([]update.StateSupport(nil), base.Components[0].Contract.State...)
				fs := []string{}
				for _, feature := range base.Components[0].Contract.State[0].Features {
					if feature != "agents-ordinary-trade-fixture-v1" {
						fs = append(fs, feature)
					}
				}
				base.Components[0].Contract.State[0].Features = fs
			case "unknown-reader-feature":
				next.Components[1].Contract.State[1].Features = append(next.Components[1].Contract.State[1].Features, "unreviewed-trade-reader")
			case "financial-feature":
				next.Components[1].Contract.Features = append(next.Components[1].Contract.Features, "trade-authority")
			}
			next.BootstrapFrom = sha(j(base))
			if bad == "wrong-source" {
				next.BootstrapFrom = sha([]byte("foreign"))
			}
			b := update.BootstrapBundle{Version: 2, Maintenance: signed(base, trust, key), Target: signed(next, trust, key)}
			if bad == "unsigned" {
				b.Target = nil
			}
			if bad == "normal-slot" {
				b.Version = 1
			}
			p, e := update.VerifyBootstrap(b, j(trust), now)
			if bad != "" && bad != "new-reader-support" {
				if e == nil {
					t.Fatal("unsafe handoff accepted", bad)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			tr, _ := update.ParseTrust(j(trust), now)
			if _, _, e = update.Authenticate(b.Maintenance, tr, now); e == nil {
				t.Fatal("read-only predecessor admitted as normal maintenance")
			}
			if _, e = update.Compatible(p.Target, p.TargetSHA256, update.Inventory{}); e == nil {
				t.Fatal("handoff admitted in normal update slot")
			}
			wire, _ := json.Marshal(p)
			if len(wire) == 0 {
				t.Fatal("missing exact signed plan")
			}
		})
	}
}
