package update

import (
	"errors"
)

// The predecessor authorizes an unchanged-pair maintenance intent. The target
// signature binds that exact maintenance manifest; ordinary update admission
// cannot consume a bootstrap target. No private signing key is handled here.
type BootstrapBundle struct {
	Version     int    `json:"version"`
	Maintenance []byte `json:"maintenance"`
	Target      []byte `json:"target"`
}
type BootstrapPlan struct {
	Maintenance       Manifest `json:"maintenance"`
	Target            Manifest `json:"target"`
	MaintenanceSHA256 string   `json:"maintenanceSha256"`
	TargetSHA256      string   `json:"targetSha256"`
}

func predecessorLifecycle(c *LifecycleContract) bool {
	return c != nil && c.Version == 1 && c.Entrypoint == 3 && c.StageFormat == 1 && SnapshotDigest("features", c.Features) == SnapshotDigest("features", CurrentLifecycle().Features[:5])
}
func VerifyBootstrap(b BootstrapBundle, trust []byte, now int64) (BootstrapPlan, error) {
	var p BootstrapPlan
	t, e := ParseTrust(trust, now)
	if e != nil {
		return p, e
	}
	p.Maintenance, p.MaintenanceSHA256, e = authenticate(b.Maintenance, t, now, predecessorLifecycle)
	if e != nil {
		return p, e
	}
	p.Target, p.TargetSHA256, e = Authenticate(b.Target, t, now)
	if e != nil {
		return p, e
	}
	var a, z Envelope
	if b.Version != 1 || StrictJSON(b.Maintenance, &a) != nil || StrictJSON(b.Target, &z) != nil || a.KeyID != z.KeyID || p.Maintenance.BootstrapFrom != "" || p.Target.BootstrapFrom != p.MaintenanceSHA256 || p.Maintenance.Target != (Target{"linux", "amd64"}) || p.Target.Target != p.Maintenance.Target || p.Target.Sequence != p.Maintenance.Sequence || p.Target.IssuedAt != p.Maintenance.IssuedAt || p.Target.ExpiresAt != p.Maintenance.ExpiresAt {
		return p, errors.New("exact signed predecessor-to-target bootstrap binding required")
	}
	for _, a := range p.Maintenance.Components {
		var target *Component
		for n := range p.Target.Components {
			if p.Target.Components[n].Contract.Component == a.Contract.Component {
				target = &p.Target.Components[n]
			}
		}
		if target == nil || a.Artifact.File != fixedArtifact(a.Contract.Component) || target.Artifact.File != a.Artifact.File {
			return p, errors.New("fixed complete reader pair required")
		}
		// This bridge is deliberately finite: schema/protocol/topology stay exact;
		// only lifecycle 3 -> 4 and the known completion reader feature may change.
		next := target.Contract
		next.State = append([]StateSupport(nil), next.State...)
		if next.Component == "signer" {
			found := false
			for _, state := range next.State {
				if state.Name == "operations" && containsAll(state.Features, requiredStateFeatures["operations"]) {
					found = true
				}
			}
			if !found {
				return p, errors.New("target completion reader required")
			}
		}
		if next.Component == "core" {
			next.Lifecycle = a.Contract.Lifecycle
		}
		for n, v := range next.State {
			if v.Name == "operations" {
				features := []string{}
				for _, f := range v.Features {
					if f != "install-completion-v1" {
						features = append(features, f)
					}
				}
				next.State[n].Features = features
			}
		}
		if SnapshotDigest("contract", next) != SnapshotDigest("contract", a.Contract) {
			return p, errors.New("unsupported reader migration; bootstrap preserves state formats")
		}
	}
	return p, nil
}
func fixedArtifact(component string) string {
	if component == "core" {
		return "fased-app"
	}
	return "fased-signer"
}

// Metadata evidence may normalize only the declared reader feature addition.
// Financial anchors, operations head, counters, authority and floor stay exact.
func BootstrapHeld(before, now SignerSnapshot, aborted bool) bool {
	before.BootID, before.ProbeID, now.BootID, now.ProbeID = "", "", "", ""
	if aborted {
		before.ActiveIntentID = ""
		for n := range before.States {
			if before.States[n].Name == "operations" {
				before.States = append([]State(nil), before.States...)
				before.States[n].Anchor = ""
			}
		}
		now.States = append([]State(nil), now.States...)
		for n := range now.States {
			if now.States[n].Name == "operations" {
				now.States[n].Anchor = ""
			}
		}
	}
	now.States = append([]State(nil), now.States...)
	for n, v := range now.States {
		if v.Name == "operations" {
			features := []string{}
			for _, f := range v.Features {
				if f != "install-completion-v1" {
					features = append(features, f)
				}
			}
			now.States[n].Features = features
		}
	}
	return SnapshotDigest("bootstrap-held", before) == SnapshotDigest("bootstrap-held", now)
}
