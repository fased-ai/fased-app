package update

import (
	"encoding/json"
	"errors"
	"strconv"
)

// The read-only predecessor has no signer update intent. Version2 authenticates
// a finite, same-format reader handoff; ordinary update admission still refuses
// BootstrapFrom. It never pretends the old signer granted maintenance authority.
func verifyOrdinaryBootstrap(b BootstrapBundle, trust []byte, now int64) (BootstrapPlan, error) {
	var p BootstrapPlan
	t, e := ParseTrust(trust, now)
	if e != nil {
		return p, e
	}
	p.Maintenance, p.MaintenanceSHA256, e = authenticateComponents(b.Maintenance, t, now, ordinaryPredecessorComponents)
	if e != nil {
		return p, e
	}
	p.Target, p.TargetSHA256, e = Authenticate(b.Target, t, now)
	if e != nil {
		return p, e
	}
	var a, z Envelope
	if StrictJSON(b.Maintenance, &a) != nil || StrictJSON(b.Target, &z) != nil || a.KeyID != z.KeyID || p.Maintenance.BootstrapFrom != "" || p.Target.BootstrapFrom != p.MaintenanceSHA256 || p.Maintenance.Target != (Target{"linux", "amd64"}) || p.Target.Target != p.Maintenance.Target || p.Target.Sequence != p.Maintenance.Sequence || p.Target.IssuedAt != p.Maintenance.IssuedAt || p.Target.ExpiresAt != p.Maintenance.ExpiresAt || !ordinaryComponents(p.Target.Components) {
		return p, errors.New("exact ordinary predecessor/target binding required")
	}
	for _, old := range p.Maintenance.Components {
		var next *Component
		for i := range p.Target.Components {
			if p.Target.Components[i].Contract.Component == old.Contract.Component {
				next = &p.Target.Components[i]
			}
		}
		if next == nil || old.Artifact.File != fixedArtifact(old.Contract.Component) || next.Artifact.File != old.Artifact.File {
			return p, errors.New("fixed complete ordinary pair required")
		}
		c := next.Contract
		c.State = append([]StateSupport(nil), c.State...)
		c.Features = withoutFeature(c.Features, OrdinaryMaintenanceFeature)
		c.RequiresPeer = old.Contract.RequiresPeer
		if c.Component == "core" {
			c.PeerProtocol = old.Contract.PeerProtocol
		}
		for i := range c.State {
			c.State[i].Features = withoutFeature(withoutFeature(c.State[i].Features, "ordinary-maintenance-v1"), "install-completion-v1")
		}
		if SnapshotDigest("contract", c) != SnapshotDigest("contract", old.Contract) {
			return p, errors.New("ordinary reader handoff cannot change schema/protocol/provenance or financial capabilities")
		}
	}
	return p, nil
}
func withoutFeature(fs []string, remove string) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		if f != remove {
			out = append(out, f)
		}
	}
	return out
}
func ordinaryPredecessorComponents(cs []Component) error {
	if len(cs) != 2 {
		return errors.New("complete read-only predecessor pair required")
	}
	checked := append([]Component(nil), cs...)
	for i := range checked {
		c := checked[i].Contract
		if c.Component == "core" {
			if c.Protocol != 1 || c.PeerProtocol != (Range{1, 1}) || !containsAll(c.Features, []string{"ordinary-custody-admission-v1"}) {
				return errors.New("known immutable ordinary core required")
			}
			c.PeerProtocol = Range{3, 3}
		} else if c.Component == "signer" {
			if c.Protocol != 3 || c.PeerProtocol != (Range{1, 1}) || len(c.Features) != 1 || c.Features[0] != "ordinary-custody-read-v1" || len(c.RequiresPeer) != 0 {
				return errors.New("read-only ordinary predecessor required")
			}
		} else {
			return errors.New("unknown ordinary predecessor component")
		}
		if c.Component == "signer" {
			c.State = append([]StateSupport(nil), c.State...)
			for j := range c.State {
				if c.State[j].Name == "operations" {
					c.State[j].Features = append(append([]string(nil), c.State[j].Features...), "ordinary-maintenance-v1", "install-completion-v1")
				}
			}
		}
		c.Features = append(append([]string(nil), c.Features...), OrdinaryMaintenanceFeature)
		c.RequiresPeer = []string{OrdinaryMaintenanceFeature}
		checked[i].Contract = c
	}
	return validateComponents(checked)
}

// The handoff only reserves a non-rewinding reader floor. It is not a completed
// release, a spending grant, a core pause receipt, or a reauthentication token.
type OrdinaryHandoffReceipt struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	Sequence        uint64 `json:"sequence,string"`
	ManifestSHA256  string `json:"manifestSha256"`
	AuthoritySHA256 string `json:"authoritySha256"`
	GenesisSHA256   string `json:"genesisSha256"`
	PreviousCore    string `json:"previousCore"`
	PreviousSigner  string `json:"previousSigner"`
	TargetCore      string `json:"targetCore"`
	TargetSigner    string `json:"targetSigner"`
}

func ValidOrdinaryHandoff(r OrdinaryHandoffReceipt) bool {
	return r.Version == 1 && IDOK(r.ID) && r.Sequence > 0 && hashOK(r.ManifestSHA256) && hashOK(r.AuthoritySHA256) && hashOK(r.GenesisSHA256) && hashOK(r.PreviousCore) && hashOK(r.PreviousSigner) && hashOK(r.TargetCore) && hashOK(r.TargetSigner) && r.PreviousSigner != r.TargetSigner
}

// Root-published description consumed ONLY by the candidate signer under its
// own non-root UID, after the fixed controller's forward-only fence is durable.
type OrdinaryHandoffAuthority struct {
	SourceFilesSHA256 string                 `json:"sourceFilesSha256"`
	Version           int                    `json:"version"`
	Digest            string                 `json:"digest"`
	ConfigSHA256      string                 `json:"configSha256"`
	Root              string                 `json:"root"`
	Owner             int                    `json:"owner"`
	App               int                    `json:"app"`
	Signer            int                    `json:"signer"`
	SignerID          string                 `json:"signerId"`
	Network           string                 `json:"network"`
	Before            SignerSnapshot         `json:"before"`
	Receipt           OrdinaryHandoffReceipt `json:"receipt"`
}

func (a OrdinaryHandoffAuthority) Valid() bool {
	return a.Version == 1 && hashOK(a.SourceFilesSHA256) && hashOK(a.Digest) && hashOK(a.ConfigSHA256) && a.Owner > 0 && a.App > 0 && a.Signer > 0 && a.Owner != a.App && a.Owner != a.Signer && a.App != a.Signer && a.Root == "/var/lib/fased-app-custody-"+strconv.Itoa(a.Owner) && IDOK(a.SignerID) && (a.Network == "devnet" || a.Network == "mainnet-beta") && ValidOrdinaryHandoff(a.Receipt) && validOrdinarySignerShape(a.Before) && a.Before.ActiveIntentID == "" && a.Before.SequenceFloor == 0 && a.Before.InstalledSequence == 0 && len(a.Before.States[1].Features) == 1 && a.Before.States[1].Features[0] == "ordinary-key-genesis-v1" && a.Receipt.AuthoritySHA256 == a.Before.AuthoritySHA256 && a.Receipt.GenesisSHA256 == a.Before.States[1].Anchor
}
func SameOrdinarySnapshot(a, b SignerSnapshot) bool {
	a.ProbeID = ""
	a.BootID = ""
	b.ProbeID = ""
	b.BootID = ""
	return SnapshotDigest("ordinary-reader-state", a) == SnapshotDigest("ordinary-reader-state", b)
}

// VerifyTransaction binds the offline signer descriptor to the same immutable,
// signed controller transaction; a shaped digest is never sufficient authority.
func (a OrdinaryHandoffAuthority) VerifyTransaction(raw []byte) error {
	var t struct {
		Version    int             `json:"version"`
		ID         string          `json:"id"`
		Base       json.RawMessage `json:"base"`
		Target     json.RawMessage `json:"target"`
		Bundle     BootstrapBundle `json:"bundle"`
		Trust      []byte          `json:"trust"`
		ReviewedAt int64           `json:"reviewedAt"`
		Core       CoreSnapshot    `json:"core"`
		Signer     SignerSnapshot  `json:"signer"`
	}
	if !a.Valid() || StrictJSON(raw, &t) != nil || t.Version != 2 || t.Bundle.Version != 2 || t.ID != a.Receipt.ID || SnapshotDigest("reader-bootstrap:v1", json.RawMessage(raw)) != a.Digest || SnapshotDigest("empty-signer", t.Signer) != SnapshotDigest("empty-signer", SignerSnapshot{}) {
		return errors.New("exact immutable ordinary bootstrap transaction required")
	}
	p, e := VerifyBootstrap(t.Bundle, t.Trust, t.ReviewedAt)
	if e != nil {
		return e
	}
	if p.Target.Sequence != a.Receipt.Sequence || p.TargetSHA256 != a.Receipt.ManifestSHA256 {
		return errors.New("signed handoff binding changed")
	}
	type identity struct {
		UID int `json:"uid"`
	}
	type image struct {
		Version    int      `json:"version"`
		Owner      int      `json:"owner"`
		Root       string   `json:"root"`
		ID         string   `json:"id"`
		Network    string   `json:"network"`
		CoreHash   string   `json:"coreHash"`
		SignerHash string   `json:"signerHash"`
		App        identity `json:"app"`
		Signer     identity `json:"signer"`
	}
	var old, next image
	if json.Unmarshal(t.Base, &old) != nil || json.Unmarshal(t.Target, &next) != nil || old.Version != 3 || old.Root != a.Root || old.Owner != a.Owner || old.App.UID != a.App || old.Signer.UID != a.Signer || old.ID != a.SignerID || old.Network != a.Network || old.CoreHash != a.Receipt.PreviousCore || old.SignerHash != a.Receipt.PreviousSigner || next.CoreHash != a.Receipt.TargetCore || next.SignerHash != a.Receipt.TargetSigner {
		return errors.New("handoff installation identity changed")
	}
	var base, target map[string]json.RawMessage
	if json.Unmarshal(t.Base, &base) != nil || json.Unmarshal(t.Target, &target) != nil {
		return errors.New("invalid handoff manifests")
	}
	target["coreHash"], target["signerHash"] = base["coreHash"], base["signerHash"]
	if SnapshotDigest("handoff-topology", base) != SnapshotDigest("handoff-topology", target) {
		return errors.New("handoff cannot alter custody topology")
	}
	for _, g := range []struct {
		m            Manifest
		core, signer string
	}{{p.Maintenance, old.CoreHash, old.SignerHash}, {p.Target, next.CoreHash, next.SignerHash}} {
		for _, c := range g.m.Components {
			want := g.core
			if c.Contract.Component == "signer" {
				want = g.signer
			}
			if c.Artifact.SHA256 != want {
				return errors.New("signed handoff pair changed")
			}
		}
	}
	before := a.Before
	before.ProbeID = t.Core.ProbeID
	_, e = JoinSnapshots(t.Core, before, p.Target.Target)
	return e
}
