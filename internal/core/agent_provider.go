package core

import "errors"

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
	case "openai":
		policy := openAIModelPolicy()
		if profile.Model != policy.Model || policy.ExpiresAt <= s.now().Unix() {
			return errors.New("agent model unavailable")
		}
		connection, err := s.AIConnection()
		if err != nil || connection.Ref == "" || connection.Locked || !connection.TestOnly {
			return errors.New("unlock the selected connection first")
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
