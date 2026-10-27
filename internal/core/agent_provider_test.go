package core

import (
	"bytes"
	"os"
	"testing"
)

func TestAgentProviderSharedReviewSelection(t *testing.T) {
	s := fixtureStore(t)
	connection := saveTestConnection(t, s)
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Provider review"})
	id := v.Sessions[len(v.Sessions)-1].ID
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "openai", Model: admittedOpenAIModel})
	limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 1, BudgetMicrousd: 30000}
	base := AgentCommand{UseAgentDefaults: true, ProfileID: v.ProfileID, ID: id, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 2, AuthorizationSeconds: 3600, TotalBudgetMicrousd: 60000}
	review := base
	review.Action = "chat-review"
	review.Revision = v.Revision
	got, err := s.ChangeAgents(t.Context(), review)
	if err != nil || got.ChatReview == nil || got.ChatReview.ConnectionRef != connection.Ref || got.ChatReview.Model != admittedOpenAIModel {
		t.Fatal("saved provider not inherited", err)
	}
	if got.Revision != v.Revision || len(got.AIRequests) != 0 {
		t.Fatal("review mutated or dispatched")
	}
	for _, action := range []string{"ai-prepare", "task-ai-prepare"} {
		c := base
		c.Action = action
		if action == "task-ai-prepare" {
			c.MemorySelection = nil
		}
		c.Name = "Provider task"
		c.Text = "Summarize saved sources"
		c.IntervalSeconds = 60
		v = agentApply(t, s, c)
		if action == "ai-prepare" {
			q := v.AIRequests[len(v.AIRequests)-1]
			if q.ConnectionRef != connection.Ref || q.Model != admittedOpenAIModel || q.State != "prepared" {
				t.Fatal("request selection")
			}
		} else {
			q := v.Tasks[len(v.Tasks)-1]
			if q.AI.ConnectionRef != connection.Ref || q.AI.Model != admittedOpenAIModel || q.State != "review" {
				t.Fatal("task selection")
			}
		}
	}
	review.Revision = v.Revision
	review.ConnectionRef = "stale-connection"
	if _, err = s.ChangeAgents(t.Context(), review); err == nil {
		t.Fatal("stale selection silently replaced")
	}
	review.ConnectionRef = ""
	s.lockAIConnection()
	if _, err = s.ChangeAgents(t.Context(), review); err == nil {
		t.Fatal("locked provider fell back to demo")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: connection.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"xai", "anthropic"} {
		v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: provider, Model: "unavailable"})
		review.Revision = v.Revision
		if _, err = s.ChangeAgents(t.Context(), review); err == nil {
			t.Fatal("unsupported provider fell back", provider)
		}
	}
	// A deliberate explicit Demo override remains backward-compatible.
	review.UseAgentDefaults = false
	got, err = s.ChangeAgents(t.Context(), review)
	if err != nil || got.ChatReview.ConnectionRef != "" || got.ChatReview.Model != fixtureAIModel {
		t.Fatal("explicit override", err)
	}
	review.UseAgentDefaults = true
	review.Action = "chat-send"
	if _, err = s.ChangeAgents(t.Context(), review); err == nil {
		t.Fatal("defaults resolved at dispatch")
	}
}

func TestAICredentialModeCannotActivateOrAlterVault(t *testing.T) {
	s := fixtureStore(t)
	v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", CredentialMode: "test", Key: testAPIKey, Passphrase: testVaultPassword})
	if err != nil || v.CredentialMode != "test" || !v.TestOnly || v.LiveCallsEnabled {
		t.Fatal("test mode", err)
	}
	before, err := os.ReadFile(s.aiVaultPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"openai", "live", "unknown"} {
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", CredentialMode: mode, Ref: v.Ref, Key: testAPIKey, Passphrase: testVaultPassword}); err == nil {
			t.Fatal("unsupported credential mode accepted", mode)
		}
	}
	after, _ := os.ReadFile(s.aiVaultPath())
	if !bytes.Equal(before, after) {
		t.Fatal("refused mode changed vault")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", CredentialMode: "test", Ref: v.Ref, Key: "not-a-disposable-test-key", Passphrase: testVaultPassword}); err == nil {
		t.Fatal("real credential admission changed")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", CredentialMode: "test", Ref: v.Ref}); err == nil {
		t.Fatal("mode accepted outside save")
	}
	s.lockAIConnection()
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal("legacy unlock", err)
	}
}
