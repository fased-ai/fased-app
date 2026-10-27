package core

import (
	"reflect"
	"testing"
)

func TestConfiguredAgentCreation(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic", "xai"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			account := addNativeAccount(t, s, provider, false)
			model := registryForProvider(provider).Models[0].ID
			v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "My analyst", Text: "Summarize sources", Provider: provider, AccountRef: &account.Ref, Model: model})
			p := v.Profiles[agentProfile(agentDocument{Profiles: v.Profiles}, v.ProfileID)]
			if p.Name != "My analyst" || p.Persona != "Summarize sources" || p.Provider != provider || p.ConnectionRef != account.Ref || p.Model != model {
				t.Fatal("creation did not save the selected account/model atomically", p)
			}
			if len(v.AIRequests) != 0 || len(v.Tasks) != 0 || len(v.Sessions) != 0 || len(v.Messages) != 0 {
				t.Fatal("agent creation dispatched or authorized work")
			}
			for _, action := range []string{"chat-review", "task-ai-prepare"} {
				c := AgentCommand{Action: action}
				if err := s.resolveAgentProvider(&c, p); err != nil || c.ConnectionRef != account.Ref || c.Model != model {
					t.Fatal("Chat and task selection diverged", action, c.ConnectionRef, c.Model, err)
				}
			}
		})
	}
}

func TestConfiguredAgentCreationRefusalsAreAtomic(t *testing.T) {
	for _, name := range []string{"missing-account", "stale-account", "wrong-provider", "old-model", "no-ai-model", "no-ai-account", "locked-account"} {
		t.Run(name, func(t *testing.T) {
			s := fixtureStore(t)
			account := addNativeAccount(t, s, "openai", false)
			c := AgentCommand{Action: "profile-create", Name: "Retain this draft", Text: "Instructions", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel}
			switch name {
			case "missing-account":
				c.AccountRef = nil
			case "stale-account":
				ref, _ := randomToken()
				c.AccountRef = &ref
			case "wrong-provider":
				c.Provider, c.Model = "xai", registryForProvider("xai").Models[0].ID
			case "old-model":
				c.Model = "retired-model"
			case "no-ai-model":
				c.Provider, c.AccountRef = "none", nil
			case "no-ai-account":
				c.Provider, c.Model = "none", ""
			case "locked-account":
				s.lockAIConnection()
			}
			before, revision, err := readAgents(t.Context(), s.db)
			if err != nil {
				t.Fatal(err)
			}
			c.Revision = revision
			if _, err = s.ChangeAgents(t.Context(), c); err == nil {
				t.Fatal("invalid creation accepted", name)
			}
			after, next, err := readAgents(t.Context(), s.db)
			if err != nil || next != revision || !reflect.DeepEqual(before, after) {
				t.Fatal("refused creation changed saved state", err)
			}
		})
	}
}

func TestConfiguredAgentCreationPreservesProviderFreeAndRestart(t *testing.T) {
	s := fixtureStore(t)
	legacy := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Legacy no AI"})
	plain := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "No AI", Provider: "none"})
	for _, v := range []AgentView{legacy, plain} {
		p := v.Profiles[agentProfile(agentDocument{Profiles: v.Profiles}, v.ProfileID)]
		if p.Provider != "none" || p.ConnectionRef != "" || p.Model != "" {
			t.Fatal("provider-free creation acquired an account", p)
		}
	}
	account := addNativeAccount(t, s, "openai", false)
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Persistent", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.providerFixture = true
	retained, err := n.Agents(t.Context(), v.ProfileID, "")
	if err != nil || !reflect.DeepEqual(v.Profiles, retained.Profiles) {
		t.Fatal("restart lost configured profiles", err)
	}
	if _, err := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	p := retained.Profiles[agentProfile(agentDocument{Profiles: retained.Profiles}, retained.ProfileID)]
	c := AgentCommand{Action: "chat-review"}
	if err := n.resolveAgentProvider(&c, p); err != nil || c.ConnectionRef != account.Ref || c.Model != admittedOpenAIModel {
		t.Fatal("restart silently changed the configured provider", err)
	}
}
