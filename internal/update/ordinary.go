package update

import "slices"

const OrdinaryMaintenanceFeature = "ordinary-custody-updates-v3"

func ordinaryStateProfile() (map[string]string, map[string][]string) {
	return map[string]string{"core-db": "core", "vault": "signer", "operations": "signer"}, map[string][]string{
		"core-db":    requiredStateFeatures["core-db"],
		"vault":      {"ordinary-custody-admission-v1", "key-only-recovery-hold-v1"},
		"operations": {"ordinary-key-genesis-v1"},
	}
}

// OrdinaryContracts is explicit paired maintenance authority, never a financial
// capability or an assumption that a read-only protocol-3 predecessor can update.
func OrdinaryContracts(cs []Contract) bool {
	if len(cs) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if (c.Component != "core" && c.Component != "signer") || seen[c.Component] || !containsAll(c.Features, []string{OrdinaryMaintenanceFeature}) {
			return false
		}
		seen[c.Component] = true
		if len(c.RequiresPeer) != 1 || c.RequiresPeer[0] != OrdinaryMaintenanceFeature {
			return false
		}
		if c.Component == "core" && (c.Protocol != 1 || c.PeerProtocol != (Range{3, 3})) {
			return false
		}
		if c.Component == "signer" {
			if c.Protocol != 3 || c.PeerProtocol != (Range{1, 1}) {
				return false
			}
			found := false
			for _, s := range c.State {
				if s.Name == "operations" && containsAll(s.Features, []string{"ordinary-key-genesis-v1", "ordinary-maintenance-v1", "install-completion-v1"}) {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		if c.Component == "signer" && c.Protocol != 3 {
			return false
		}
	}
	return seen["core"] && seen["signer"]
}
func ordinaryComponents(cs []Component) bool {
	contracts := make([]Contract, 0, len(cs))
	for _, c := range cs {
		contracts = append(contracts, c.Contract)
	}
	return OrdinaryContracts(contracts)
}
func validOrdinarySignerShape(s SignerSnapshot) bool {
	tradeCounts := len(s.States) == 2 && slices.Contains(s.States[1].Features, "ordinary-typed-trade-fixture-v1") && s.Pending >= 0 && s.Pending <= 64 && s.Unknown >= 0 && s.Unknown <= s.Pending
	return s.Version == 3 && len(s.States) == 2 && s.States[0].Name == "vault" && s.States[0].Version == 4 && s.States[1].Name == "operations" && s.States[1].Version == 1 && s.RecoveryHold && s.Spent == 0 && s.Held == 0 && (s.Pending == 0 && s.Unknown == 0 || tradeCounts) && s.InstalledSequence <= s.SequenceFloor && slices.Contains(s.States[0].Features, "ordinary-custody-admission-v1") && slices.Contains(s.States[0].Features, "key-only-recovery-hold-v1") && slices.Contains(s.States[1].Features, "ordinary-key-genesis-v1")
}
