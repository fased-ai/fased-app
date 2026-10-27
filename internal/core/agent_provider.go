package core

import (
	"errors"
	"strings"
)

// The catalogue describes selection, not authority. Only the existing admission,
// credential and reviewed-request checks may authorize a provider operation.
type ProviderMethod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ProviderEntry struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Methods []ProviderMethod `json:"methods"`
}
type ProviderModel struct {
	Tier          string             `json:"tier,omitempty"`
	Policy        *OpenAIModelPolicy `json:"policy,omitempty"`
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Provider      string             `json:"provider"`
	Method        string             `json:"method"`
	ConnectionRef string             `json:"connectionRef"`
	Recommended   bool               `json:"recommended"`
	Selectable    bool               `json:"selectable"`
	Availability  string             `json:"availability"`
}
type ProviderCatalog struct {
	Policy    string          `json:"policy"`
	State     string          `json:"state"`
	FetchedAt int64           `json:"fetchedAt,omitempty"`
	ExpiresAt int64           `json:"expiresAt,omitempty"`
	SHA256    string          `json:"sha256,omitempty"`
	Source    string          `json:"source,omitempty"`
	Version   int             `json:"version"`
	Providers []ProviderEntry `json:"providers"`
	Models    []ProviderModel `json:"models"`
}

func providerCatalog(v AIConnectionView, now int64) ProviderCatalog {
	// Status without discovered evidence still shows the reviewed current set,
	// never an older model or account-inferred entitlement.
	return discoveredProviderCatalog(v, modelDiscovery{}, now)
}

// Creation commits one complete preference, without granting Chat/task access.
// Name-only legacy commands remain provider-free; an AI selection must name its
// exact account rather than resolving the default account after creation.
func (s *Store) configuredAgent(c AgentCommand, id string) (AgentProfile, error) {
	provider := c.Provider
	if provider == "" {
		provider = "none"
	}
	p := AgentProfile{ID: id, Name: strings.TrimSpace(c.Name), Persona: c.Text, Provider: provider, Model: c.Model}
	if c.AccountRef != nil {
		p.ConnectionRef = *c.AccountRef
	}
	if c.ConnectionRef != "" {
		return AgentProfile{}, errors.New("select the agent account")
	}
	if provider == "none" {
		if p.ConnectionRef != "" || p.Model != "" {
			return AgentProfile{}, errors.New("no-AI agents cannot select an account or model")
		}
		return p, nil
	}
	if !validConnectionRef(p.ConnectionRef) || p.Model == "" || p.Model == fixtureAIModel {
		return AgentProfile{}, errors.New("select an account and current model")
	}
	selection := AgentCommand{ConnectionRef: p.ConnectionRef, Model: p.Model}
	if err := s.resolveAgentProvider(&selection, p); err != nil {
		return AgentProfile{}, err
	}
	return p, nil
}

// Resolve saved preferences only while preparing a new review. The resulting
// concrete connection/model are bound by existing consent, never re-resolved
// during authorization, dispatch, retry or restart.
func (s *Store) resolveAgentProvider(c *AgentCommand, profile AgentProfile) error {
	ref, model := "", ""
	switch profile.Provider {
	case "none":
		if profile.Model != "" && profile.Model != fixtureAIModel {
			return errors.New("agent model unavailable")
		}
	case "openai", "anthropic", "xai":
		policy := openAIModelPolicy(profile.Model)
		if !currentModelRequest(AgentAIRequest{Model: profile.Model, Policy: &policy}) || profile.Model != policy.Model || policy.ExpiresAt <= s.now().Unix() {
			return errors.New("agent model unavailable")
		}
		if profile.Provider != "openai" && profile.ConnectionRef == "" {
			return errors.New("select this provider account")
		}
		connection, err := s.AIConnection(profile.ConnectionRef)
		if err != nil || connection.Ref == "" || connection.Locked || connection.Provider != profile.Provider || policyProvider(&policy) != profile.Provider {
			return errors.New("unlock the selected connection first")
		}
		available := false
		for _, m := range connection.Catalog.Models {
			if m.ID == profile.Model && m.Selectable {
				available = true
			}
		}
		if !available {
			return errors.New("refresh this account and select an available current model")
		}
		ref, model = connection.Ref, profile.Model
	default:
		return errors.New("agent provider unavailable")
	}
	// UI previews may supply their observed selection; refuse replacement rather
	// than silently switching credentials between preview and review.
	if (c.ConnectionRef != "" && c.ConnectionRef != ref) || (c.Model != "" && c.Model != model && !(model == "" && c.Model == fixtureAIModel)) {
		return ErrConflict
	}
	c.ConnectionRef, c.Model = ref, model
	return nil
}
