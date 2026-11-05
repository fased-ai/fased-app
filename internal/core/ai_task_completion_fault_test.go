package core

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

func TestDurablePauseFailurePreservesUncertainDispatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("protected restart credentials are Linux-only")
	}
	s := fixtureStore(t)
	key := &testTaskSecrets{key: bytes.Repeat([]byte{71}, 32)}
	s.taskSecrets = key
	account := addLocalFixtureAccount(t, s)
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Local failure isolation", Provider: "local", AccountRef: &account.Ref, Model: "gemma4:e2b"})
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Synthetic background"})
	limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[len(v.Sessions)-1].ID, UseAgentDefaults: true, Name: "Bounded synthetic note", Text: "Summarize a fictional note", IntervalSeconds: 3600, MaxRuns: 2, AuthorizationSeconds: 900, Limits: limits})
	v = taskAIAuthorize(t, s, v)
	r, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: v.ProfileID, Revision: v.Revision, TaskIDs: []string{v.Tasks[len(v.Tasks)-1].ID}, AuthorizationSeconds: 900, ContinueOnSignOut: true, ContinueAfterRestart: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: r.AccessReview, ConsentDigest: r.AccessReview.ID, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	v = taskAIRun(t, s, v)
	calls := 0
	e = s.advanceAgentAI(t.Context(), aiTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != "POST" {
			return (fixtureAITransport{}).RoundTrip(request)
		}
		calls++
		s.providerVaultFault = func() error { return errors.New("controlled revocation persistence failure") }
		return &http.Response{StatusCode: 401, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"controlled account refusal"}}`)), Request: request}, nil
	}), true)
	if e == nil || !strings.Contains(e.Error(), "revocation could not be saved") || calls != 1 {
		t.Fatal("completion swallowed revocation failure", e, calls)
	}
	d, _, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	q := d.AIRequests[len(d.AIRequests)-1]
	if q.State != "running" || !aiHasUsageHold(q) || len(s.credentialLocked(account.Ref).key) != 0 {
		t.Fatal("uncertain dispatch or RAM lock lost", q.State)
	}
	s.providerVaultFault = nil
	col, e := s.readAIVaultCollection()
	if e != nil || len(col.TaskAccess) != 1 || col.TaskAccess[0].Allowance.UsedRequests != 1 {
		t.Fatal("write-ahead charge missing", e)
	}
	s = restartDurable(t, s, key)
	view, e := s.AIConnection(account.Ref)
	if e != nil || !view.Locked || view.TaskAccess != nil {
		t.Fatal("unsafe dispatch regained credentials", e)
	}
	if e = s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("uncertain dispatch replayed")
		return nil, errors.New("no replay")
	}), true); e != nil {
		t.Fatal(e)
	}
	d, _, e = readAgents(t.Context(), s.db)
	if e != nil || d.AIRequests[len(d.AIRequests)-1].State != "unknown" {
		t.Fatal("uncertainty not retained", e)
	}
}
