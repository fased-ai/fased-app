package core

import "github.com/fased-ai/fased-app/internal/update"

const StateSchema = 2

// UpdateContract advertises formats, not update authority.
func UpdateContract() update.Contract {
	return update.Contract{Version: 1, Component: "core", Protocol: 1,
		PeerProtocol: update.Range{Min: 1, Max: 1},
		Features:     []string{"account-registry-v1", "fixture-proposals-v1"},
		RequiresPeer: []string{"fixture-public-status-v1", "fixture-approval-receipts-v1"},
		State:        []update.StateSupport{{Name: "core-db", Versions: update.Range{Min: StateSchema, Max: StateSchema}, Features: []string{"accounts-v1", "owner-sessions-v1", "setup-v1"}}}}
}
