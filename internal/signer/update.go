package signer

import "github.com/fased-ai/fased-app/internal/update"

const VaultFormat = 1
const JournalFormat = 1

// UpdateContract distinguishes approval-aware replay from older protocol-1
// binaries. It grants no signing or installed-custody capability.
func UpdateContract() update.Contract {
	return update.Contract{Version: 1, Component: "signer", Protocol: 1,
		PeerProtocol: update.Range{Min: 1, Max: 1},
		Features:     []string{"fixture-public-status-v1", "fixture-approval-receipts-v1", "fixture-update-intents-v1", "fixture-update-staging-v1", "fixture-update-preflight-v1", "fixture-install-controller-v1"}, RequiresPeer: []string{},
		State: []update.StateSupport{
			{Name: "vault", Versions: update.Range{Min: VaultFormat, Max: VaultFormat}, Features: []string{"fixture-key-v1", "authority-epoch-v1"}},
			{Name: "operations", Versions: update.Range{Min: JournalFormat, Max: JournalFormat}, Features: []string{"policy-v1", "reservations-v1", "originals-v1", "authority-epoch-v1", "approvals-v1", "onchain-swap-fixture-v1", "update-intents-v1", "update-staging-v1", "lifecycle-v2-v1", "install-controller-v1", "install-completion-v1"}},
			{Name: "mock-receipts", Versions: update.Range{Min: JournalFormat, Max: JournalFormat}, Features: []string{"mock-outcomes-v1"}},
		}}
}

func ManagedUpdateContract() update.Contract {
	return update.Contract{Version: 1, Component: "signer", Protocol: 2, PeerProtocol: update.Range{Min: 1, Max: 1}, Features: []string{"managed-custody-updates-v2"}, RequiresPeer: []string{"managed-custody-updates-v2"}, State: []update.StateSupport{
		{Name: "vault", Versions: update.Range{Min: 2, Max: 3}, Features: []string{"managed-key-v2", "authority-epoch-v1", "managed-fresh-history-v1"}},
		{Name: "operations", Versions: update.Range{Min: 1, Max: 1}, Features: []string{"managed-key-history-v1", "managed-maintenance-v2", "install-completion-v1", "managed-swap-fixture-v1"}},
	}}
}
