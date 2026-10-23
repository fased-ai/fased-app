package core

import "github.com/fased-ai/fased-app/internal/update"

const StateSchema = 12

// UpdateContract advertises formats, not update authority.
func UpdateContract() update.Contract {
	return update.Contract{Lifecycle: update.CurrentLifecycle(), Version: 1, Component: "core", Protocol: 1,
		PeerProtocol: update.Range{Min: 1, Max: 1},
		Features:     []string{"account-registry-v1", "fixture-proposals-v1"},
		RequiresPeer: []string{"fixture-public-status-v1", "fixture-approval-receipts-v1", "fixture-update-intents-v1", "fixture-update-staging-v1", "fixture-update-preflight-v1", "fixture-install-controller-v1"},
		State:        []update.StateSupport{{Name: "core-db", Versions: update.Range{Min: StateSchema, Max: StateSchema}, Features: []string{"accounts-v1", "owner-sessions-v1", "setup-v1", "core-pause-v1", "agents-local-v1", "agents-ai-review-v1", "agents-ai-tasks-v1", "agents-ai-connections-v1", "agents-news-v1", "agents-ai-admission-v1", "agents-wen-paper-v1", "wen-research-v1", "wen-research-turn-v1"}}}}
}
