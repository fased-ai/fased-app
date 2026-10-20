package update

import (
	"errors"
	"fmt"
	"strings"
)

type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func (r Range) valid() bool    { return r.Min > 0 && r.Max >= r.Min && r.Max <= 1000 }
func (r Range) has(v int) bool { return r.valid() && v >= r.Min && v <= r.Max }

type StateSupport struct {
	Name     string   `json:"name"`
	Versions Range    `json:"versions"`
	Features []string `json:"features"`
}

// LifecycleContract versions the immutable owner helper independently from
// runtime IPC and financial state. Candidate artifacts are never executed here.
type LifecycleContract struct {
	Version     int      `json:"version"`
	Entrypoint  int      `json:"entrypoint"`
	StageFormat int      `json:"stageFormat"`
	Features    []string `json:"features"`
}

func CurrentLifecycle() *LifecycleContract {
	return &LifecycleContract{Version: 1, Entrypoint: 1, StageFormat: 1, Features: []string{"owner-update-review-v1", "signer-owned-staging-v1", "no-install-v1"}}
}
func lifecycleOK(c *LifecycleContract) bool {
	return c != nil && c.Version == 1 && c.Entrypoint == 1 && c.StageFormat == 1 && listOK(c.Features) && containsAll(c.Features, CurrentLifecycle().Features)
}

type Contract struct {
	Lifecycle    *LifecycleContract `json:"lifecycle,omitempty"`
	Version      int                `json:"version"`
	Component    string             `json:"component"`
	Protocol     int                `json:"protocol"`
	PeerProtocol Range              `json:"peerProtocol"`
	Features     []string           `json:"features"`
	RequiresPeer []string           `json:"requiresPeer"`
	State        []StateSupport     `json:"state"`
}

type State struct {
	Name     string   `json:"name"`
	Version  int      `json:"version"`
	Features []string `json:"features"`
	Anchor   string   `json:"anchor"`
}

// Inventory is disposable input for this read-only engineering checker. A
// future lifecycle controller must obtain it from the owning services and bind
// it to a paused, exclusive transaction. It must not trust an app-written file.
type Inventory struct {
	Version       int     `json:"version"`
	FixtureOnly   bool    `json:"fixtureOnly"`
	Target        Target  `json:"target"`
	SequenceFloor uint64  `json:"sequenceFloor,string"`
	States        []State `json:"states"`
	Pending       int     `json:"pending"`
	Unknown       int     `json:"unknown"`
	RecoveryHold  bool    `json:"recoveryHold"`
}

type Plan struct {
	Result             string   `json:"result"`
	ManifestSHA256     string   `json:"manifestSha256"`
	Sequence           uint64   `json:"sequence,string"`
	Target             Target   `json:"target"`
	Compatible         bool     `json:"compatible"`
	ArtifactsVerified  bool     `json:"artifactsVerified"`
	InstallAllowed     bool     `json:"installAllowed"`
	ResumeAllowed      bool     `json:"resumeAllowed"`
	UpdaterEnabled     bool     `json:"updaterEnabled"`
	WalletSigner       bool     `json:"walletSigner"`
	FinancialExecution bool     `json:"financialExecution"`
	InventoryAuthority string   `json:"inventoryAuthority"`
	Preserve           []State  `json:"preserve"`
	Pending            int      `json:"pending"`
	Unknown            int      `json:"unknown"`
	RecoveryHold       bool     `json:"recoveryHold"`
	RequiredActions    []string `json:"requiredActions"`
}

var stateOwner = map[string]string{"core-db": "core", "vault": "signer", "operations": "signer", "mock-receipts": "signer"}

// These are format features, not claims of signing support or profitability.
var requiredStateFeatures = map[string][]string{
	"core-db":       {"accounts-v1", "owner-sessions-v1", "setup-v1"},
	"vault":         {"fixture-key-v1", "authority-epoch-v1"},
	"operations":    {"policy-v1", "reservations-v1", "originals-v1", "authority-epoch-v1", "approvals-v1", "update-intents-v1", "update-staging-v1"},
	"mock-receipts": {"mock-outcomes-v1"},
}

func listOK(a []string) bool {
	if len(a) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		if !tokenOK(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func containsAll(have, need []string) bool {
	for _, n := range need {
		found := false
		for _, h := range have {
			if h == n {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validateComponents(cs []Component) error {
	if len(cs) != 2 {
		return errors.New("complete core and signer pair required")
	}
	seen, files := map[string]bool{}, map[string]bool{}
	contracts := map[string]Contract{}
	for _, c := range cs {
		x := c.Contract
		if !tokenOK(c.Version) || x.Version != 1 || (x.Component != "core" && x.Component != "signer") || seen[x.Component] || x.Protocol <= 0 || x.Protocol > 1000 || !x.PeerProtocol.valid() || !listOK(x.Features) || !listOK(x.RequiresPeer) {
			return errors.New("invalid component contract")
		}
		seen[x.Component] = true
		contracts[x.Component] = x
		a := c.Artifact
		if !tokenOK(a.File) || a.File == "." || a.File == ".." || strings.Contains(a.File, ":") || strings.HasSuffix(a.File, ".") || a.Size <= 0 || a.Size > MaxArtifact || !hashOK(a.SHA256) || files[strings.ToLower(a.File)] {
			return errors.New("invalid artifact contract")
		}
		files[strings.ToLower(a.File)] = true
		states := map[string]bool{}
		for _, s := range x.State {
			if stateOwner[s.Name] != x.Component || states[s.Name] || !s.Versions.valid() || !listOK(s.Features) {
				return errors.New("invalid state contract")
			}
			states[s.Name] = true
		}
		for name, owner := range stateOwner {
			if owner == x.Component && !states[name] {
				return errors.New("missing state contract")
			}
		}
	}
	core, signer := contracts["core"], contracts["signer"]
	if !lifecycleOK(core.Lifecycle) {
		return errors.New("incompatible or missing owner lifecycle entrypoint/staging format")
	}
	if !core.PeerProtocol.has(signer.Protocol) || !signer.PeerProtocol.has(core.Protocol) || !containsAll(signer.Features, core.RequiresPeer) || !containsAll(core.Features, signer.RequiresPeer) {
		return errors.New("component protocol or peer feature mismatch")
	}
	// This package begins after schema 2/approval journals. An old protocol-1
	// signer without approval support is not a compatible downgrade.
	if !containsAll(core.Features, []string{"account-registry-v1", "fixture-proposals-v1"}) || !containsAll(signer.Features, []string{"fixture-public-status-v1", "fixture-approval-receipts-v1"}) {
		return errors.New("pre-approval component is unsupported")
	}
	return nil
}

func Compatible(m Manifest, sha string, inv Inventory) (Plan, error) {
	p := Plan{}
	if err := validateComponents(m.Components); err != nil {
		return p, err
	}
	if inv.Version != 1 || !inv.FixtureOnly || !targetOK(inv.Target) || inv.Target != m.Target || !hashOK(sha) || m.Sequence == 0 || m.Sequence <= inv.SequenceFloor {
		return p, errors.New("fixture inventory target or sequence floor mismatch")
	}
	if len(inv.States) != len(stateOwner) || inv.Pending < 0 || inv.Pending > 1024 || inv.Unknown < 0 || inv.Unknown > inv.Pending {
		return p, errors.New("invalid state inventory")
	}
	support := map[string]StateSupport{}
	for _, c := range m.Components {
		for _, s := range c.Contract.State {
			support[s.Name] = s
		}
	}
	seen := map[string]bool{}
	for _, s := range inv.States {
		x, ok := support[s.Name]
		if !ok || seen[s.Name] || !hashOK(s.Anchor) || !listOK(s.Features) || !containsAll(s.Features, requiredStateFeatures[s.Name]) || !x.Versions.has(s.Version) || !containsAll(x.Features, s.Features) {
			return p, fmt.Errorf("incompatible durable state: %s", s.Name)
		}
		seen[s.Name] = true
	}
	p = Plan{Result: "verified-development-plan", ManifestSHA256: sha, Sequence: m.Sequence, Target: m.Target, Compatible: true, InventoryAuthority: "disposable-input-only", Pending: inv.Pending, Unknown: inv.Unknown, RecoveryHold: inv.RecoveryHold, RequiredActions: []string{"acquire-trusted-exclusive-inventory-and-sequence-floor", "reverify-staged-artifacts", "preserve-state-without-counter-or-journal-rollback"}}
	p.Preserve = make([]State, len(inv.States))
	for i, s := range inv.States {
		p.Preserve[i] = s
		p.Preserve[i].Features = append([]string{}, s.Features...)
	}
	if inv.Pending > 0 {
		p.RequiredActions = append(p.RequiredActions, "pause-new-operations-and-reconcile-originals")
	}
	if inv.RecoveryHold {
		p.RequiredActions = append(p.RequiredActions, "keep-recovery-hold")
	}
	return p, nil
}
