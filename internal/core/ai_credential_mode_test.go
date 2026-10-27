package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const disposableAPIInput = "disposable-api-input-not-issued-by-any-provider"

// Only test code sets admission. No CLI, environment or IPC sets this field.
func admitDisposableAPI(t *testing.T, s *Store, ref string) AIConnectionView {
	t.Helper()
	s.provider.apiCredentials = true
	v, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", CredentialMode: "openai", Ref: ref, Key: disposableAPIInput, Passphrase: testVaultPassword})
	if e != nil || v.CredentialMode != "openai" || v.TestOnly || v.Locked || v.LiveCallsEnabled {
		t.Fatal("API fixture save", e)
	}
	return v
}
func TestAPICredentialVaultLifecycleAndClosedRestart(t *testing.T) {
	s := fixtureStore(t)
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", CredentialMode: "openai", Key: disposableAPIInput, Passphrase: testVaultPassword}); e == nil {
		t.Fatal("ordinary runtime admitted API mode")
	}
	if _, e := os.Stat(s.aiVaultPath()); !os.IsNotExist(e) {
		t.Fatal("refused save wrote vault")
	}
	v := admitDisposableAPI(t, s, "")
	original, e := os.ReadFile(s.aiVaultPath())
	if e != nil || bytes.Contains(original, []byte(disposableAPIInput)) {
		t.Fatal("plaintext", e)
	}
	var env aiVaultEnvelope
	if json.Unmarshal(original, &env) != nil || env.Version != 2 || env.Mode != "openai" {
		t.Fatal("mode-bound envelope")
	}
	for _, mutate := range []func(*aiVaultEnvelope){func(v *aiVaultEnvelope) { v.Mode = "test" }, func(v *aiVaultEnvelope) { v.Version = 1; v.Mode = "" }, func(v *aiVaultEnvelope) { v.Ref = "changed" }, func(v *aiVaultEnvelope) { v.Ciphertext = append([]byte(nil), v.Ciphertext...); v.Ciphertext[0] ^= 1 }} {
		bad := env
		mutate(&bad)
		if plain, err := s.openAIVault(bad, testVaultPassword); err == nil {
			clear(plain)
			t.Fatal("tamper accepted")
		}
	}
	originalID := s.instanceID
	s.instanceID = "foreign-instance"
	if plain, err := s.openAIVault(env, testVaultPassword); err == nil {
		clear(plain)
		t.Fatal("foreign vault accepted")
	}
	s.instanceID = originalID
	s.lockAIConnection()
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: "wrong-password"}); e == nil {
		t.Fatal("wrong password")
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	rotated := admitDisposableAPI(t, s, v.Ref)
	if rotated.Ref == v.Ref || s.aiConnectionValid(v.Ref) {
		t.Fatal("rotation retained authority")
	}
	dir := s.dir
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	restarted, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	locked, e := restarted.AIConnection()
	if e != nil || !locked.Locked || locked.TestOnly || locked.CredentialMode != "openai" || locked.Ref != rotated.Ref {
		t.Fatal("restart state", e)
	}
	before, _ := os.ReadFile(restarted.aiVaultPath())
	for _, action := range []string{"unlock", "task-access-review"} {
		if _, e = restarted.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: action, Ref: rotated.Ref, Passphrase: testVaultPassword}); e == nil {
			t.Fatal("inactive mode admitted", action)
		}
	}
	after, _ := os.ReadFile(restarted.aiVaultPath())
	if !bytes.Equal(before, after) {
		t.Fatal("blocked operation altered vault")
	}
	called := false
	rt := authenticatedAITransport{store: restarted, ref: rotated.Ref, next: aiTestTransport(func(*http.Request) (*http.Response, error) { called = true; return nil, nil })}
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/responses", nil)
	if _, e = rt.RoundTrip(req); e == nil || called {
		t.Fatal("inactive credential dispatched")
	}
	if _, e = restarted.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: rotated.Ref, Passphrase: "wrong-password"}); e == nil {
		t.Fatal("unauthorized removal")
	}
	if _, e = restarted.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: rotated.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal("inactive removal", e)
	}
}
func TestAPICredentialExplicitReplacementPreservesLegacy(t *testing.T) {
	s := fixtureStore(t)
	legacy := saveTestConnection(t, s)
	before, _ := os.ReadFile(s.aiVaultPath())
	s.lockAIConnection()
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: legacy.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(s.aiVaultPath())
	if !bytes.Equal(before, after) {
		t.Fatal("implicit legacy rewrite")
	}
	v := admitDisposableAPI(t, s, legacy.Ref)
	s.provider.apiCredentials = false
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", CredentialMode: "test", Ref: v.Ref, Key: testAPIKey, Passphrase: testVaultPassword}); e != nil {
		t.Fatal("inactive replacement", e)
	}
	env, e := s.readAIVault()
	if e != nil || env.Version != 1 || env.Mode != "" {
		t.Fatal("legacy test replacement", e)
	}
}
func TestAPICredentialControlledHTTPSChat(t *testing.T) {
	s := fixtureStore(t)
	connection := admitDisposableAPI(t, s, "")
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "API-mode chat"})
	id := v.Sessions[len(v.Sessions)-1].ID
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "openai", Model: admittedOpenAIModel})
	bad := AgentCommand{Action: "chat-review", Revision: v.Revision, ProfileID: v.ProfileID, ID: id, ConnectionRef: connection.Ref, Model: fixtureAIModel, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 30000}, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 2, AuthorizationSeconds: 900, TotalBudgetMicrousd: 60000}
	if _, e := s.ChangeAgents(t.Context(), bad); e == nil {
		t.Fatal("API credential admitted synthetic model")
	}
	v = agentApply(t, s, AgentCommand{Action: "chat-review", UseAgentDefaults: true, ProfileID: v.ProfileID, ID: id, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 30000}, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 2, AuthorizationSeconds: 900, TotalBudgetMicrousd: 60000})
	p := *v.ChatReview
	if p.ConnectionRef != connection.Ref || p.Model != admittedOpenAIModel {
		t.Fatal("API mode not bound")
	}
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: v.ProfileID, ID: id, ChatPermission: &p, ConsentDigest: p.ID})
	v = agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: v.ProfileID, ID: p.SessionID, Text: "Review saved sources", ConsentDigest: p.ID})
	calls := 0
	fixture := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+disposableAPIInput {
			t.Error("wrong credential")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = "https", "api.openai.com"
		copy.URL = &u
		res, err := (fixtureAITransport{}).RoundTrip(copy)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer res.Body.Close()
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		_, _ = io.Copy(w, res.Body)
	}).(testOpenAITransport)
	// Only the fixture prefix check is bypassed; the test-only loopback TLS dialer
	// and actual transport/admission remain unchanged. No external host is used.
	if e := s.advanceAgentAI(t.Context(), fixture.next, false); e != nil {
		t.Fatal(e)
	}
	got, e := s.Agents(t.Context(), v.ProfileID, "")
	if e != nil || len(got.AIRequests) != 1 || got.AIRequests[0].State != "completed" || calls != 3 {
		t.Fatal("API-mode HTTPS completion", e, calls)
	}
	// A finite task grant can use the API-mode vault after interactive expiry.
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", UseAgentDefaults: true, ProfileID: v.ProfileID, ID: id, Name: "Bounded API task", Text: "Review saved sources", IntervalSeconds: 60, MaxRuns: 1, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 30000}, AuthorizationSeconds: 3600, TotalBudgetMicrousd: 30000})
	reviewed := v.Tasks[len(v.Tasks)-1]
	if !s.providerTaskAllowed(reviewed, false) {
		t.Fatal("ordinary provider task refused")
	}
	for _, alter := range []func(*AgentTask){
		func(task *AgentTask) { task.Playbook = &PlaybookOrigin{} },
		func(task *AgentTask) { task.WEN = &WENPaperPlan{} },
		func(task *AgentTask) { task.BotID = "held-bot" },
	} {
		held := reviewed
		alter(&held)
		if s.providerTaskAllowed(held, false) {
			t.Fatal("held task kind gained provider execution")
		}
		q := taskAIRequest(held, s.now().Unix())
		q.TaskID = held.ID
		if s.providerDispatchAllowed(agentDocument{Tasks: []AgentTask{held}}, q, false) {
			t.Fatal("retained held task reached dispatch")
		}
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, ID: reviewed.ID, ConsentDigest: reviewed.AI.Digest})
	task := v.Tasks[len(v.Tasks)-1]
	grant, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: connection.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 3600})
	if e != nil || grant.AccessReview == nil {
		t.Fatal("API task review", e)
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); e != nil {
		t.Fatal("API task grant", e)
	}
	later := s.now().Add(16 * time.Minute)
	s.now = func() time.Time { return later }
	status, e := s.AIConnection()
	if e != nil || !status.Locked || status.TaskAccess == nil {
		t.Fatal("task grant extended interactive unlock", e)
	}
	if e = s.runAgentTasks(t.Context(), false); e != nil {
		t.Fatal(e)
	}
	if e = s.advanceAgentAI(t.Context(), fixture.next, false); e != nil {
		t.Fatal(e)
	}
	got, e = s.Agents(t.Context(), v.ProfileID, "")
	if e != nil || len(got.AIRequests) != 2 || got.AIRequests[1].State != "completed" || calls != 6 {
		t.Fatal("API task completion", e, calls)
	}
	if len(got.Runs) != 1 || got.Runs[0].AIRequestID != got.AIRequests[1].ID {
		t.Fatal("provider task did not settle through the shared run engine")
	}
	got = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: got.ProfileID, ID: got.AIRequests[0].ID, Source: "answer", Text: "Reviewed provider answer"})
	if len(got.Memories) != 1 || !strings.Contains(got.Memories[0].Source, got.AIRequests[0].ID) {
		t.Fatal("provider answer lost memory provenance")
	}
	s.provider.apiCredentials = false
	view, e := s.AIConnection()
	if e != nil || !view.Locked || len(s.provider.key) != 0 || view.TaskAccess != nil {
		t.Fatal("closed policy retained credential", e)
	}
}
