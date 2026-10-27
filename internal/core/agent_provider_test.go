package core

import (
	"bytes"
	"os"
	"testing"
)

// Preference changes use an explicit disposable account/current model. They do
// not authorize requests; history/holds assertions stay independent of providers.
func fixtureProfileProvider(t *testing.T, s *Store, c AgentCommand) AgentView {
	t.Helper()
	account := addNativeAccount(t, s, c.Provider, false)
	c.Model = registryForProvider(c.Provider).Models[0].ID
	c.AccountRef = &account.Ref
	return agentApply(t, s, c)
}

func TestProviderCatalogTracksConnectionWithoutEntitlementClaims(t *testing.T) {
	s := fixtureStore(t)
	v, err := s.AIConnection()
	if err != nil || v.Catalog.Version != 1 || len(v.Catalog.Providers) != 3 || len(v.Catalog.Models) != 3 {
		t.Fatalf("catalog: %+v %v", v.Catalog, err)
	}
	for i, id := range []string{"openai", "anthropic", "xai"} {
		p := v.Catalog.Providers[i]
		if p.ID != id || len(p.Methods) != 1 || p.Methods[0].ID != "api-key" {
			t.Fatal("provider methods must describe only implemented API adapters", p)
		}
	}
	if m := v.Catalog.Models[0]; m.Selectable || m.Availability != "disconnected" || m.ID != admittedOpenAIModel {
		t.Fatal("missing account offered a usable model", m)
	}
	v = saveTestConnection(t, s)
	if m := v.Catalog.Models[0]; !m.Selectable || m.Availability != "fixture" || m.ConnectionRef != v.Ref || !m.Recommended {
		t.Fatal("controlled connection not bound to catalog", m)
	}
	if v.Catalog.Providers[0].Methods[0].ID != "api-key" || v.LiveCallsEnabled {
		t.Fatal("authentication route or live hold changed")
	}
	s.lockAIConnection()
	v, err = s.AIConnection()
	if err != nil || v.Catalog.Models[0].Selectable || v.Catalog.Models[0].Availability != "locked" {
		t.Fatal("locked credential offered as ready", err)
	}
	policy := openAIModelPolicy()
	catalog := providerCatalog(AIConnectionView{Ref: "retained", TestOnly: true}, policy.ExpiresAt)
	if catalog.Models[0].Selectable || catalog.Models[0].Availability != "expired" {
		t.Fatal("expired admission offered")
	}
	catalog = providerCatalog(AIConnectionView{Ref: "retained", CredentialMode: "openai"}, policy.ReviewedAt+1)
	if catalog.Models[0].Selectable || catalog.Models[0].Availability != "unverified" {
		t.Fatal("catalog enabled ordinary live dispatch")
	}
}

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
		rewriteReminderFixture(t, s, func(d *agentDocument) {
			i := agentProfile(*d, v.ProfileID)
			d.Profiles[i].Provider = provider
			d.Profiles[i].Model = "unavailable"
		})
		v, _ = s.Agents(t.Context(), v.ProfileID, "")
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
