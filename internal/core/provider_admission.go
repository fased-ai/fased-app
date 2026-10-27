package core

// This release hold is not configurable through environment, CLI, IPC or UI.
// Removing it requires separate live acceptance. Tests use disposable credentials
// and a test-only HTTPS dialer; ordinary constructors always keep it closed.
const providerActivationEnabled = false

func (s *Store) aiExecutionAvailable(fixture bool) bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if s.pilot.target != nil {
		return s.pilotCurrentLocked()
	}
	return fixture || s.provider.apiCredentials
}

// Credential admission does not grant conversation, task or financial authority.
// Existing exact model, consent, budget, expiry and credential-grant checks still
// run. In ordinary provider mode neither synthetic models nor research dispatch
// can borrow permission from the development fixture path.
func (s *Store) providerRequestAllowed(q AgentAIRequest, fixture bool) bool {
	s.provider.mu.Lock()
	scoped, pilot := s.pilot.target != nil, s.pilotTargetLocked(q.ConnectionRef)
	if scoped {
		pilot = pilot && q.Model == s.pilot.target.model && (q.ProfileID == "" || q.ProfileID == s.pilot.target.profile)
	}
	s.provider.mu.Unlock()
	if scoped && !pilot {
		return false
	}
	if q.Model != fixtureAIModel && !currentModelRequest(q) {
		return false
	}
	if q.Model != fixtureAIModel && s.providerModelSuperseded(q.ConnectionRef, q.Model) {
		return false
	}
	if fixture {
		return true
	}
	if (!s.provider.apiCredentials && !pilot) || q.ConnectionRef == "" || !currentModelRequest(q) || isResearchAI(q) {
		return false
	}
	v, err := s.AIConnection(q.ConnectionRef)
	return err == nil && v.Ref == q.ConnectionRef && v.Provider == policyProvider(q.Policy) && v.CredentialMode == v.Provider
}

func (s *Store) providerTaskAllowed(task AgentTask, fixture bool) bool {
	if fixture {
		return true
	}
	return task.AI != nil && task.Playbook == nil && task.WEN == nil && task.BotID == "" && s.providerRequestAllowed(taskAIRequest(task, s.now().Unix()), false)
}

func (s *Store) providerDispatchAllowed(d agentDocument, q AgentAIRequest, fixture bool) bool {
	if !s.providerRequestAllowed(q, fixture) {
		return false
	}
	if !fixture && q.TaskID != "" {
		for _, task := range d.Tasks {
			if task.ID == q.TaskID && task.ProfileID == q.ProfileID {
				return s.providerTaskAllowed(task, false)
			}
		}
		return false
	}
	return true
}

func (s *Store) providerActionAllowed(d agentDocument, c AgentCommand, profile string, fixture bool) bool {
	s.provider.mu.Lock()
	wrongPilot := s.pilot.target != nil && (profile != s.pilot.target.profile || c.Action == "ai-start")
	if s.pilot.target != nil && !s.pilotCurrentLocked() {
		wrongPilot = true
	}
	s.provider.mu.Unlock()
	if wrongPilot {
		return false
	}
	if fixture {
		return true
	}
	switch c.Action {
	case "chat-send":
		for _, session := range d.Sessions {
			if session.ProfileID == profile && session.ID == c.ID && session.Chat != nil {
				p := session.Chat
				return s.providerRequestAllowed(AgentAIRequest{ConnectionRef: p.ConnectionRef, Model: p.Model, Policy: chatModelPolicy(p.Model)}, false)
			}
		}
	case "ai-start":
		for _, q := range d.AIRequests {
			if q.ProfileID == profile && q.ID == c.ID {
				return s.providerRequestAllowed(q, false)
			}
		}
	case "task-ai-authorize", "task-run", "task-resume":
		for _, task := range d.Tasks {
			if task.ProfileID == profile && task.ID == c.ID && task.AI != nil {
				return s.providerTaskAllowed(task, false)
			}
		}
	}
	return false
}
