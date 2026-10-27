package core

import "github.com/fased-ai/fased-app/internal/update"

const StateSchema = 25

// UpdateContract advertises formats, not update authority.
func UpdateContract() update.Contract {
	return update.Contract{Lifecycle: update.CurrentLifecycle(), Version: 1, Component: "core", Protocol: 1,
		PeerProtocol: update.Range{Min: 1, Max: 1},
		Features:     []string{"account-registry-v1", "fixture-proposals-v1"},
		RequiresPeer: []string{"fixture-public-status-v1", "fixture-approval-receipts-v1", "fixture-update-intents-v1", "fixture-update-staging-v1", "fixture-update-preflight-v1", "fixture-install-controller-v1"},
		State:        []update.StateSupport{{Name: "core-db", Versions: update.Range{Min: StateSchema, Max: StateSchema}, Features: []string{"accounts-v1", "accounts-rpc-v1", "accounts-rpc-providers-v2", "managed-key-records-v1", "owner-sessions-v1", "setup-v1", "core-pause-v1", "agents-local-v1", "agents-ai-review-v1", "agents-ai-tasks-v1", "agents-ai-connections-v1", "agents-ai-accounts-v1", "agents-current-models-v1", "agents-native-providers-v1", "agents-news-v1", "agents-ai-admission-v1", "agents-wen-paper-v1", "wen-research-v1", "wen-research-turn-v1", "wen-research-continuation-v2", "wen-session-rehearsal-v1", "wen-session-fixture-auto-v1", "agents-playbooks-v1", "agents-calendar-v1", "agents-cron-v1", "agents-history-v1", "agents-recall-v1", "agents-chat-permission-v1"}}}}
}

func ManagedUpdateContract() update.Contract {
	c := UpdateContract()
	c.PeerProtocol = update.Range{Min: 2, Max: 2}
	c.Features = append(c.Features, "managed-custody-updates-v2")
	c.RequiresPeer = []string{"managed-custody-updates-v2"}
	return c
}
