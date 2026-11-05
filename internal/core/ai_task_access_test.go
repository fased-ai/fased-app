package core

import (
	"net/http"
	"testing"
	"time"
)

func connectedTask(t *testing.T, s *Store, ref string) AgentView {
	t.Helper()
	v := taskAIPlan(t, s, 2)
	old := v.Tasks[0]
	v = agentApply(t, s, AgentCommand{Action: "task-ai-cancel", ProfileID: v.ProfileID, ID: old.ID})
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: old.AI.SessionID, Name: "Selected credential task", Text: old.Text, ConnectionRef: ref, IntervalSeconds: 3600, MaxRuns: 2, Limits: &old.AI.Limits, TotalBudgetMicrousd: 60000, AuthorizationSeconds: 7200})
	return taskAIAuthorize(t, s, v)
}
func TestTaskAccessRemainsScopedAfterInteractiveExpiry(t *testing.T) {
	s := fixtureStore(t)
	connection := saveTestConnection(t, s)
	v := connectedTask(t, s, connection.Ref)
	task := v.Tasks[len(v.Tasks)-1]
	reviewed, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: connection.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400})
	if e != nil || reviewed.AccessReview == nil {
		t.Fatal("review", e)
	}
	a := reviewed.AccessReview
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: a, ConsentDigest: a.ID, Passphrase: "wrong password"}); e == nil {
		t.Fatal("unattended access without vault owner")
	}
	allowed, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: a, ConsentDigest: a.ID, Passphrase: testVaultPassword})
	if e != nil || allowed.TaskAccess == nil {
		t.Fatal("allow", e)
	}
	later := s.now().Add(61 * time.Minute)
	s.now = func() time.Time { return later }
	status, e := s.AIConnection()
	if e != nil || !status.Locked || status.TaskAccess == nil || s.aiConnectionValid(connection.Ref) {
		t.Fatal("background allowance extended interactive unlock", status, e)
	}
	if e = s.runAgentTasks(t.Context(), true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	calls := 0
	if e = s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("missing controlled credential")
		}
		return (fixtureAITransport{}).RoundTrip(r)
	}), true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[len(v.AIRequests)-1]
	if calls != 1 || q.State != "completed" || v.Tasks[len(v.Tasks)-1].Runs != 1 {
		t.Fatal("bounded task could not run while UI was absent", calls, q.State, v.Tasks)
	}
	status, _ = s.AIConnection()
	if status.TaskAccess.UsedRequests != 1 || status.TaskAccess.AllocatedMicrousd == 0 {
		t.Fatal("credential allowance not charged")
	}
	foreign := q
	foreign.TaskID = "unselected"
	foreign.State = "queued"
	if s.aiRequestConnectionValid(foreign) {
		t.Fatal("unselected task used credential")
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-revoke", Ref: connection.Ref}); e != nil {
		t.Fatal(e)
	}
	v = taskAIRun(t, s, v)
	if e = s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) }), true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if calls != 1 || v.Tasks[len(v.Tasks)-1].State != "held" {
		t.Fatal("revoked access dispatched or erased dependent failure")
	}
}

func TestTaskAccessLogoutPolicy(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-lock", true: "explicit-background"}[keep], func(t *testing.T) {
			s := fixtureStore(t)
			connection := saveTestConnection(t, s)
			v := connectedTask(t, s, connection.Ref)
			task := v.Tasks[len(v.Tasks)-1]
			reviewed, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: connection.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400, ContinueOnSignOut: keep})
			if e != nil || reviewed.AccessReview == nil {
				t.Fatal("review", e)
			}
			a := reviewed.AccessReview
			if a.ContinueOnSignOut != keep {
				t.Fatal("review lost logout policy")
			}
			tampered := *a
			tampered.ContinueOnSignOut = !keep
			if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: &tampered, ConsentDigest: a.ID, Passphrase: testVaultPassword}); e == nil {
				t.Fatal("changed logout scope admitted")
			}
			if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: a, ConsentDigest: a.ID, Passphrase: testVaultPassword}); e != nil {
				t.Fatal(e)
			}
			session, e := s.NewSession(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Logout(t.Context(), session); e != nil {
				t.Fatal(e)
			}
			if s.Session(t.Context(), session) {
				t.Fatal("logout kept UI session")
			}
			status, e := s.AIConnection(connection.Ref)
			if e != nil || !status.Locked || s.aiConnectionValid(connection.Ref) {
				t.Fatal("logout kept interactive access", e)
			}
			if (status.TaskAccess != nil) != keep {
				t.Fatal("logout violated reviewed policy")
			}
			q := taskAIRequest(task, s.now().Unix())
			q.TaskID, q.TaskDigest = task.ID, task.AI.Digest
			if s.aiRequestConnectionValid(q) != keep {
				t.Fatal("selected task policy mismatch")
			}
			q.TaskID = "foreign-task"
			if s.aiRequestConnectionValid(q) {
				t.Fatal("logout broadened task access")
			}
			v = taskAIRun(t, s, v)
			calls := 0
			if e = s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return (fixtureAITransport{}).RoundTrip(r)
			}), true); e != nil {
				t.Fatal(e)
			}
			if (calls == 1) != keep {
				t.Fatal("task dispatch ignored logout policy", calls)
			}
			if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: connection.Ref}); e != nil {
				t.Fatal(e)
			}
			status, _ = s.AIConnection(connection.Ref)
			if status.TaskAccess != nil {
				t.Fatal("explicit lock did not revoke background access")
			}
		})
	}
}

func TestTaskAccessLogoutPolicyRejectsOtherOperations(t *testing.T) {
	s := fixtureStore(t)
	for _, action := range []string{"models-refresh", "sign-in-start", "pilot-review", "lock", "unlock", "task-access-allow", "task-access-revoke"} {
		if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: action, ContinueOnSignOut: true}); e == nil {
			t.Fatal("logout policy accepted outside review", action)
		}
	}
	connection := saveTestConnection(t, s)
	s.pilot.target = &openAIPilotTarget{s.instanceID, connection.Ref, "trading-analyst", admittedOpenAIModel}
	if _, e := s.reviewTaskAccess(agentDocument{}, AIConnectionCommand{Ref: connection.Ref, ContinueOnSignOut: true}, 0); e == nil || e.Error() != "pilot access ends at sign-out" {
		t.Fatal("pilot accepted background policy", e)
	}
}

func TestTaskAccessLogoutPolicyEnds(t *testing.T) {
	for _, end := range []string{"revoke", "expiry", "restart"} {
		t.Run(end, func(t *testing.T) {
			s := fixtureStore(t)
			connection := saveTestConnection(t, s)
			v := connectedTask(t, s, connection.Ref)
			task := v.Tasks[len(v.Tasks)-1]
			reviewed, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: connection.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400, ContinueOnSignOut: true})
			if e != nil {
				t.Fatal(e)
			}
			a := reviewed.AccessReview
			if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: a, ConsentDigest: a.ID, Passphrase: testVaultPassword}); e != nil {
				t.Fatal(e)
			}
			if e = s.Logout(t.Context(), ""); e != nil {
				t.Fatal(e)
			}
			switch end {
			case "revoke":
				if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-revoke", Ref: connection.Ref}); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				later := s.now().Add(5401 * time.Second)
				s.now = func() time.Time { return later }
			case "restart":
				location := s.dir
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
				s, e = OpenStore(location)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = s.Close() })
				s.providerFixture = true
			}
			view, e := s.AIConnection(connection.Ref)
			if e != nil || !view.Locked || view.TaskAccess != nil {
				t.Fatal("background access outlived boundary", end, e)
			}
			if len(s.credentialLocked(connection.Ref).key) != 0 {
				t.Fatal("revoked key retained", end)
			}
		})
	}
}
