package core

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A provider rejection or uncertain use must stop the shared account, including
// permissions held by another profile. No monetary hold is required for quota.
func TestQuotaAPIFailurePausesSharedAccount(t *testing.T) {
	for _, provider := range []string{"kimi-code", "ollama-cloud"} {
		for _, native := range []bool{false, true} {
			for _, outcome := range []string{"quota", "unknown"} {
				name := provider + "/fixture/" + outcome
				if native {
					name = provider + "/native/" + outcome
				}
				t.Run(name, func(t *testing.T) {
					s := fixtureStore(t)
					var account AIConnectionView
					var err error
					if native {
						s.provider.apiCredentials = true
						key := disposableAPIInput
						if provider == "kimi-code" {
							key = "sk-kimi-disposable-pause-regression-key"
						}
						account, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: provider, CredentialMode: provider, Key: key, Passphrase: testVaultPassword})
					} else {
						account = addNativeAccount(t, s, provider, false)
					}
					if err != nil {
						t.Fatal(err)
					}
					before, _ := ReadPrivateFile(s.aiVaultPath())
					model := registryForProvider(provider).Models[0].ID
					view, first := nativePermission(t, s, account, model)
					other := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "news-researcher", Name: "Shared quota account"})
					other = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: other.ProfileID, Name: "Researcher", Provider: provider, Model: model, AccountRef: &account.Ref})
					other = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: other.ProfileID, ID: other.Sessions[len(other.Sessions)-1].ID, UseAgentDefaults: true, Limits: &first.Limits, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 2, AuthorizationSeconds: 900})
					second := *other.ChatReview
					other = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: other.ProfileID, ID: second.SessionID, ChatPermission: &second, ConsentDigest: second.ID})
					if provider == "ollama-cloud" {
						view = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: view.ProfileID, ID: first.SessionID, UseAgentDefaults: true, Name: "Bounded quota task", Text: "Synthetic", IntervalSeconds: 3600, MaxRuns: 1, Limits: &first.Limits, AuthorizationSeconds: 7200})
						task := view.Tasks[len(view.Tasks)-1]
						view = taskAIAuthorize(t, s, view)
						grant, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: view.ProfileID, Revision: view.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 3600})
						if e != nil {
							t.Fatal(e)
						}
						if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); e != nil {
							t.Fatal(e)
						}
					}
					view, _ = s.Agents(t.Context(), view.ProfileID, "")
					view = chatSend(t, s, view, first, "Synthetic provider failure")
					epoch := s.credentialLocked(account.Ref).epoch
					posts := 0
					transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
						if r.Method != "POST" {
							return (fixtureAITransport{}).RoundTrip(r)
						}
						posts++
						if outcome == "unknown" {
							return nil, errors.New("disposable interrupted transport")
						}
						return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"rate_limit_exceeded","message":"Synthetic quota limit"}}`))}, nil
					})
					if err = s.advanceAgentAI(t.Context(), transport, true); err != nil {
						t.Fatal(err)
					}
					status, _ := s.AIConnection(account.Ref)
					state := s.credentialLocked(account.Ref)
					if !status.PlanPaused || state.epoch <= epoch || state.taskAccess != nil || state.taskAccessLast != 0 || s.aiConnectionValid(account.Ref) || posts != 1 {
						t.Fatal("failed quota account retained shared authority", status.PlanPaused, state.taskAccess != nil, posts)
					}
					for _, permission := range []ChatPermission{first, second} {
						current, _ := s.Agents(t.Context(), permission.ProfileID, "")
						if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "chat-send", ProfileID: permission.ProfileID, ID: permission.SessionID, Revision: current.Revision, Text: "Must not run", ConsentDigest: permission.ID}, true); e == nil {
							t.Fatal("paused account accepted an existing profile permission")
						}
					}
					if _, err = s.refreshProviderModels(t.Context(), account.Ref, fixtureAITransport{}); err != nil {
						t.Fatal(err)
					}
					status, _ = s.AIConnection(account.Ref)
					if !status.PlanPaused {
						t.Fatal("model refresh silently resumed quota account")
					}
					status, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword})
					if err != nil || status.PlanPaused || status.Locked || s.credentialLocked(account.Ref).taskAccess != nil {
						t.Fatal("explicit unlock did not require new task authority", err)
					}
					after, _ := ReadPrivateFile(s.aiVaultPath())
					if !bytes.Equal(before, after) {
						t.Fatal("failure recovery rewrote credentials")
					}
					view, _ = s.Agents(t.Context(), first.ProfileID, "")
					q := view.AIRequests[len(view.AIRequests)-1]
					if (outcome == "unknown") != aiHasUsageHold(q) {
						t.Fatal("recovery changed uncertain/known usage disposition", q.State, q.CostBasis)
					}
				})
			}
		}
	}
}
