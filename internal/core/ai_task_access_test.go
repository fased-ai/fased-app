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
