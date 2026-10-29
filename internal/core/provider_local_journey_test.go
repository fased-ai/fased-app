package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func addLocalFixtureAccount(t *testing.T, s *Store) AIConnectionView {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"action": "add", "provider": "local", "credentialMode": "local", "name": "Local research", "endpoint": "http://127.0.0.1:11434/v1", "passphrase": testVaultPassword})
	var command AIConnectionCommand
	if err := json.Unmarshal(raw, &command); err != nil {
		t.Fatal(err)
	}
	account, err := s.ChangeAIConnection(t.Context(), command)
	if err != nil {
		t.Fatal("local provider cannot complete connection", err)
	}
	return account
}

func TestLocalProviderConnectionAndAssignment(t *testing.T) {
	s := fixtureStore(t)
	account := addLocalFixtureAccount(t, s)
	if account.Provider != "local" || account.Ref == "" || account.Locked || account.AuthMethod != "local" {
		t.Fatal("local connection not ready")
	}
	model := "gemma4:e2b"
	found := false
	for _, m := range account.Catalog.Models {
		if m.ID == model && m.Selectable {
			found = true
		}
	}
	if !found {
		t.Fatal("local current model not discovered")
	}
	view := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Local analyst", Provider: "local", AccountRef: &account.Ref, Model: model})
	p := view.Profiles[agentProfile(agentDocument{Profiles: view.Profiles}, view.ProfileID)]
	for _, action := range []string{"chat-review", "task-ai-prepare"} {
		command := AgentCommand{Action: action}
		if err := s.resolveAgentProvider(&command, p); err != nil || command.ConnectionRef != account.Ref || command.Model != model {
			t.Fatal("local agent selection disconnected", action, err)
		}
	}
	if len(view.AIRequests) != 0 || len(view.Tasks) != 0 {
		t.Fatal("setup dispatched work")
	}
}

func TestLocalProviderChatTaskAndRestart(t *testing.T) {
	for server, model := range map[string]string{"ollama": "gemma4:e2b", "lmstudio": "google/gemma-4-e2b", "vllm": "google/gemma-4-E2B-it"} {
		t.Run(server, func(t *testing.T) { localProviderJoinedJourney(t, server, model) })
	}
}
func localProviderJoinedJourney(t *testing.T, server, model string) {
	s := fixtureStore(t)
	existing := addNativeAccount(t, s, "openai", false)
	oldVault, err := s.readAIVault(existing.Ref)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: "local", Server: server, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.readAIVault(existing.Ref)
	if err != nil || !reflect.DeepEqual(current, oldVault) {
		t.Fatal("adding local replaced existing account", err)
	}
	view := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Local analyst", Provider: "local", AccountRef: &account.Ref, Model: model})
	profile := view.ProfileID
	view = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: profile, Name: "Local research"})
	session := view.Sessions[len(view.Sessions)-1].ID
	limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}
	view = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: profile, ID: session, UseAgentDefaults: true, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 2, AuthorizationSeconds: 900})
	permission := *view.ChatReview
	view = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: profile, ID: session, ChatPermission: &permission, ConsentDigest: permission.ID})
	view = chatSend(t, s, view, permission, "Summarize this fictional market note.")
	paths := []string{}
	transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Host+r.URL.Path)
		return (fixtureAITransport{}).RoundTrip(r)
	})
	if err := s.advanceAgentAI(t.Context(), transport, true); err != nil {
		t.Fatal(err)
	}
	view, _ = s.Agents(t.Context(), profile, "")
	response := view.AIRequests[len(view.AIRequests)-1]
	if response.State != "completed" || response.Answer == "" || response.ConnectionRef != account.Ref || response.CostBasis != reportedUsageBasis(response) || response.SpentMicrousd != 0 || response.HeldMicrousd != 0 {
		t.Fatal("local Chat incomplete", response.State, response.Failure, response.CostBasis)
	}
	view = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: profile, ID: session, UseAgentDefaults: true, Name: "Local digest", Text: "Summarize a fictional note", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 900, Limits: limits})
	task := view.Tasks[len(view.Tasks)-1]
	view = taskAIAuthorize(t, s, view)
	grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: profile, Revision: view.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 900})
	if err != nil || grant.AccessReview == nil {
		t.Fatal("local task access review", err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	view = taskAIRun(t, s, view)
	if err = s.advanceAgentAI(t.Context(), transport, true); err != nil {
		t.Fatal(err)
	}
	view, _ = s.Agents(t.Context(), profile, "")
	result := view.AIRequests[len(view.AIRequests)-1]
	if result.State != "completed" || result.TaskID != task.ID || result.ConnectionRef != account.Ref || result.CostBasis != reportedUsageBasis(result) || len(view.Runs) != 1 || view.Tasks[len(view.Tasks)-1].State != "completed" {
		t.Fatal("local task result incomplete", result.State, result.Failure)
	}
	for _, p := range paths {
		if !strings.Contains(p, "local.fased.invalid") || strings.Contains(p, "input_tokens") {
			t.Fatal("local request reached cloud/count route", p)
		}
	}
	view = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: profile, ID: view.Runs[0].ID})
	view = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: profile, Source: "message", ID: view.Messages[len(view.Messages)-1].ID, Text: "Use local evidence for this fictional market."})
	if len(view.Memories) != 1 {
		t.Fatal("local result lost memory linkage")
	}
	vault, err := os.ReadFile(s.aiVaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(vault, []byte("127.0.0.1")) {
		t.Fatal("local endpoint stored outside encryption")
	}
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	locked, err := reopened.AIConnection(account.Ref)
	if err != nil || !locked.Locked {
		t.Fatal("restart unlocked local credentials", err)
	}
	if _, err = reopened.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword}); err == nil {
		t.Fatal("ordinary build unlocked fixture local account")
	}
	reopened.providerFixture = true
	restored, err := reopened.Agents(t.Context(), profile, "")
	if err != nil || len(restored.Runs) != 1 || len(restored.Memories) != 1 {
		t.Fatal("restart lost local history", err)
	}
	after, err := os.ReadFile(reopened.aiVaultPath())
	if err != nil || !bytes.Equal(vault, after) {
		t.Fatal("restart rewrote encrypted accounts", err)
	}
}

func TestLocalProviderReplacementRequiresExplicitBinding(t *testing.T) {
	s := fixtureStore(t)
	account := addLocalFixtureAccount(t, s)
	raw, _ := json.Marshal(map[string]any{"version": 1, "endpoint": "http://127.0.0.1:11434/v1", "apiKey": "real-looking-key-must-never-enter-fixtures"})
	before, _ := os.ReadFile(s.aiVaultPath())
	_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: account.Ref, CredentialMode: "local", Key: string(raw), Passphrase: testVaultPassword})
	if err == nil {
		t.Fatal("serialized local credential bypassed provider/key validation")
	}
	after, _ := os.ReadFile(s.aiVaultPath())
	if !bytes.Equal(before, after) {
		t.Fatal("refused replacement changed encrypted accounts")
	}
}
func TestLocalProviderWireRequiresJSONEnvelope(t *testing.T) {
	policy := localModelPolicy("gemma4:e2b")
	q := AgentAIRequest{Model: policy.Model, Policy: &policy, Context: "synthetic note", Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}}
	raw, err := aiWire(q)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(raw, &body)
	var instruction string
	json.Unmarshal(body["instructions"], &instruction)
	if !strings.Contains(instruction, "Return exactly one JSON object") || !strings.Contains(instruction, `"answer"`) || !strings.Contains(instruction, `"cards"`) {
		t.Fatal("local disclosed wire omitted its JSON output contract")
	}
	if _, present := body["text"]; present {
		t.Fatal("local wire relies on unsupported structured-output field")
	}
}

func TestLocalProviderOutputDiagnosticIsLocal(t *testing.T) {
	p := localModelPolicy("gemma4:e2b")
	q := AgentAIRequest{Policy: &p, Model: p.Model, CostBasis: localUsageCostBasis, InputTokens: 100, OutputTokens: 2000}
	result := planOutputLimitFailure(q)
	if !strings.HasPrefix(result.Failure, "Local model request stopped.") || result.CostBasis != localUsageCostBasis || result.SpentMicrousd != 0 {
		t.Fatal("local over-limit receipt mislabelled or repriced")
	}
}

func TestLocalProviderUnknownUsageRemainsHeld(t *testing.T) {
	p := localModelPolicy("gemma4:e2b")
	q := AgentAIRequest{Policy: &p, Model: p.Model, State: "cancelled", DispatchBoot: "fixture-boot"}
	if !aiHasUsageHold(q) {
		t.Fatal("zero-priced local cancellation lost unverified dispatch hold")
	}
	q.CostBasis = planUsageCostBasis
	if !aiHasUsageHold(q) {
		t.Fatal("ChatGPT receipt settled a local dispatch")
	}
	q.CostBasis = localUsageCostBasis
	if aiHasUsageHold(q) {
		t.Fatal("verified local receipt did not settle its nonmonetary hold")
	}
}
func TestLocalProviderSchemaMigrationPreservesRecords(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprint(paused), func(t *testing.T) {
			s := fixtureStore(t)
			account := addNativeAccount(t, s, "openai", false)
			before, rev, err := readAgents(t.Context(), s.db)
			if err != nil {
				t.Fatal(err)
			}
			vault, _ := os.ReadFile(s.aiVaultPath())
			if _, err = s.db.Exec("PRAGMA user_version=26"); err != nil {
				t.Fatal(err)
			}
			if paused {
				if _, err = s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			}
			dir := s.dir
			s.Close()
			n, err := OpenStore(dir)
			if paused {
				if err == nil {
					n.Close()
					t.Fatal("paused schema26 migrated")
				}
				kept, _ := os.ReadFile(filepath.Join(dir, "ai-credentials.json"))
				if !bytes.Equal(kept, vault) {
					t.Fatal("held migration changed credentials")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			after, newRev, err := readAgents(t.Context(), n.db)
			if err != nil || rev != newRev || !reflect.DeepEqual(before, after) {
				t.Fatal("schema migration changed records", err)
			}
			kept, _ := os.ReadFile(n.aiVaultPath())
			if !bytes.Equal(kept, vault) {
				t.Fatal("schema migration rewrote credentials")
			}
			v, err := n.AIConnection(account.Ref)
			if err != nil || !v.Locked {
				t.Fatal("migration unlocked credentials", err)
			}
		})
	}
}

func TestLocalProviderSupersededModelStaysRefused(t *testing.T) {
	s := fixtureStore(t)
	account := addLocalFixtureAccount(t, s)
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{"object": "list", "data": []map[string]any{{"id": "gemma4:e2b", "object": "model", "owned_by": "local", "created": 1}, {"id": "gemma5:e2b", "object": "model", "owned_by": "local", "created": 1}}}), nil
	})
	v, err := s.refreshProviderModels(t.Context(), account.Ref, rt)
	if err != nil {
		t.Fatal(err)
	}
	if modelChoice(v, "gemma4:e2b").Selectable || modelChoice(v, "gemma4:e2b").Availability != "superseded" || modelChoice(v, "gemma5:e2b").Selectable {
		t.Fatal("older or unreviewed local model selected")
	}
	fail := aiTestTransport(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("controlled failure") })
	v, _ = s.refreshProviderModels(t.Context(), account.Ref, fail)
	if modelChoice(v, "gemma4:e2b").Selectable {
		t.Fatal("failed refresh revived superseded local model")
	}
}

func TestLocalProviderFreshAdmissionRejectsNewFamily(t *testing.T) {
	s := fixtureStore(t)
	account := addLocalFixtureAccount(t, s)
	view := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Fresh local analyst", Provider: "local", AccountRef: &account.Ref, Model: "gemma4:e2b"})
	profile := view.ProfileID
	view = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: profile, Name: "Fresh local note"})
	session := view.Sessions[len(view.Sessions)-1].ID
	view = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: profile, ID: session, UseAgentDefaults: true, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 1, AuthorizationSeconds: 900})
	permission := *view.ChatReview
	view = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: profile, ID: session, ChatPermission: &permission, ConsentDigest: permission.ID})
	_ = chatSend(t, s, view, permission, "Summarize a synthetic note.")
	generated := false
	transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			return fixtureAdmissionResponse(r, map[string]any{"object": "list", "data": []map[string]any{{"id": "gemma4:e2b", "object": "model", "owned_by": "local", "created": 1}, {"id": "gemma5:e2b", "object": "model", "owned_by": "local", "created": 1}}}), nil
		}
		generated = true
		return (fixtureAITransport{}).RoundTrip(r)
	})
	if err := s.advanceAgentAI(t.Context(), transport, true); err != nil {
		t.Fatal(err)
	}
	view, _ = s.Agents(t.Context(), profile, "")
	result := view.AIRequests[len(view.AIRequests)-1]
	if generated || result.State != "failed" || result.Admission == nil || result.Admission.GenerationAttempted {
		t.Fatal("fresh newer local family reached generation", result.State, generated)
	}
	paused, _ := s.AIConnection(account.Ref)
	if !paused.PlanPaused {
		t.Fatal("failed admission did not pause local account")
	}

}

func TestLocalProviderCompletionDiagnosticIsLocal(t *testing.T) {
	p := localModelPolicy("gemma4:e2b")
	q := AgentAIRequest{Policy: &p, Model: p.Model, State: "running", DispatchBoot: "fixture-boot"}
	rejectAICompletion(&q, "scope changed")
	if q.State != "unknown" || !strings.HasPrefix(q.Failure, "Local model request stopped.") {
		t.Fatal("local uncertain completion mislabelled", q.State, q.Failure)
	}
}

func TestLocalProviderFailurePausesAndRevokesAccess(t *testing.T) {
	s := fixtureStore(t)
	account := addLocalFixtureAccount(t, s)
	before, err := s.readAIVault(account.Ref)
	if err != nil {
		t.Fatal(err)
	}
	state := s.credentialLocked(account.Ref)
	epoch := state.epoch
	grant := &TaskAccessAllowance{ConnectionRef: account.Ref, BootID: s.bootID, CreatedAt: s.now().Unix(), ExpiresAt: s.now().Unix() + 900}
	grant.ID = taskAccessDigest(*grant)
	state.taskAccess = grant
	if !s.taskAccessCurrent(grant, state) {
		t.Fatal("fixture grant not current")
	}
	s.pauseSignInAccount(account.Ref, strings.Repeat("x", 2049))
	view, err := s.AIConnection(account.Ref)
	if err != nil || !view.PlanPaused || state.epoch <= epoch || state.taskAccess != nil || state.taskAccessLast != 0 || s.aiConnectionValid(account.Ref) || !strings.HasPrefix(view.PlanFailure, "Local model") {
		t.Fatal("local failure retained access", view.PlanPaused, view.PlanFailure, err)
	}
	if _, err = s.refreshProviderModels(t.Context(), account.Ref, fixtureAITransport{}); err != nil {
		t.Fatal(err)
	}
	view, _ = s.AIConnection(account.Ref)
	if !view.PlanPaused {
		t.Fatal("refresh cleared failure pause")
	}
	view, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword})
	if err != nil || view.PlanPaused || view.Locked || s.credentialLocked(account.Ref).taskAccess != nil {
		t.Fatal("explicit unlock did not require fresh task grant", err)
	}
	after, err := s.readAIVault(account.Ref)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("pause/unlock rewrote credentials", err)
	}
}
