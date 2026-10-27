package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func modelRows(ids ...string) map[string]any {
	rows := []map[string]any{}
	for _, id := range ids {
		rows = append(rows, map[string]any{"id": id, "object": "model", "owned_by": "openai", "created": 1744588800})
	}
	return map[string]any{"object": "list", "data": rows}
}
func modelChoice(v AIConnectionView, id string) ProviderModel {
	for _, m := range v.Catalog.Models {
		if m.ID == id {
			return m
		}
	}
	return ProviderModel{}
}
func TestProviderModelsAccountIsolationFreshnessAndFailure(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	const secondKey = "fased-test-discovery-second-account"
	b, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Name: "Other", Key: secondKey, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	transport := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Error("discovery did more than list", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") == "Bearer "+testAPIKey {
			admissionReply(w, modelRows(admittedOpenAIModel, "gpt-6.1-sol"))
		} else if r.Header.Get("Authorization") == "Bearer "+secondKey {
			admissionReply(w, modelRows("gpt-6-astra", legacyOpenAIModel))
		} else {
			t.Error("wrong credential")
		}
	})
	av, err := s.refreshProviderModels(t.Context(), a.Ref, transport)
	if err != nil {
		t.Fatal(err)
	}
	bv, err := s.refreshProviderModels(t.Context(), b.Ref, transport)
	if err != nil {
		t.Fatal(err)
	}
	if !modelChoice(av, admittedOpenAIModel).Selectable || modelChoice(av, "gpt-6-astra").Selectable || !modelChoice(bv, "gpt-6-astra").Selectable || modelChoice(bv, admittedOpenAIModel).Selectable || modelChoice(bv, legacyOpenAIModel).Selectable {
		t.Fatal("account catalogues crossed")
	}
	if av.Catalog.SHA256 == "" || av.Catalog.SHA256 == bv.Catalog.SHA256 || av.Catalog.Source != "https://api.openai.com/v1/models" {
		t.Fatal("missing provenance")
	}
	failed := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	av, err = s.refreshProviderModels(t.Context(), a.Ref, failed)
	if err != nil || av.Catalog.State != "failed" || modelChoice(av, admittedOpenAIModel).Selectable {
		t.Fatal("failure reused cache", err)
	}
	bv, _ = s.AIConnection(b.Ref)
	if !modelChoice(bv, "gpt-6-astra").Selectable {
		t.Fatal("other account invalidated")
	}
	// Keep interactive access while expiring only the account-list evidence.
	s.provider.mu.Lock()
	s.provider.accounts[b.Ref].expires += 3600
	s.provider.mu.Unlock()
	later := s.now().Add(16 * time.Minute)
	s.now = func() time.Time { return later }
	bv, _ = s.AIConnection(b.Ref)
	if modelChoice(bv, "gpt-6-astra").Availability != "stale" {
		t.Fatal("stale list selectable")
	}
}
func TestProviderModelsLateRefreshCannotResurrectAccount(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	started, release := make(chan struct{}), make(chan struct{})
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-release
		return fixtureAdmissionResponse(r, modelRows(admittedOpenAIModel)), nil
	})
	done := make(chan error, 1)
	go func() { _, err := s.refreshProviderModels(t.Context(), a.Ref, rt); done <- err }()
	<-started
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: a.Ref}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if !errors.Is(<-done, ErrConflict) {
		t.Fatal("late refresh accepted")
	}
	v, _ := s.AIConnection(a.Ref)
	if !v.Locked || modelChoice(v, admittedOpenAIModel).Selectable {
		t.Fatal("locked account resurrected")
	}
}
func TestProviderModelsMalformedAndForeignEvidenceRefused(t *testing.T) {
	for _, kind := range []string{"duplicate", "wrong-object", "retired", "foreign-owner", "redirect", "secret"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			a := saveTestConnection(t, s)
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if kind == "redirect" {
					http.Redirect(w, r, "https://foreign.invalid", 302)
					return
				}
				if kind == "secret" {
					admissionReply(w, map[string]any{"secret": testAPIKey})
					return
				}
				body := modelRows(admittedOpenAIModel)
				rows := body["data"].([]map[string]any)
				switch kind {
				case "duplicate":
					body["data"] = append(rows, rows[0])
				case "wrong-object":
					body["object"] = "response"
				case "retired":
					rows[0]["shutdown_date"] = "2026-10-23"
				case "foreign-owner":
					rows[0]["owned_by"] = "foreign"
				}
				admissionReply(w, body)
			})
			v, err := s.refreshProviderModels(t.Context(), a.Ref, rt)
			if err != nil {
				t.Fatal(err)
			}
			if modelChoice(v, admittedOpenAIModel).Selectable {
				t.Fatal("untrusted catalogue selected")
			}
		})
	}
}
func TestProviderModelsLatestOnlyAndLegacyInspection(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Latest"})
	legacy := legacyOpenAIModelPolicy()
	q := AgentAIRequest{Model: legacy.Model, Policy: &legacy, ConnectionRef: a.Ref, Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}}
	if s.providerRequestAllowed(q, true) || s.providerRequestAllowed(q, false) {
		t.Fatal("old model executes")
	}
	before := v.Revision
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{Revision: v.Revision, Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "Retain", ConnectionRef: a.Ref, Model: legacy.Model, Limits: &q.Limits}); err == nil {
		t.Fatal("old model prepared")
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if v.Revision != before {
		t.Fatal("refusal mutated data")
	}
	p := ChatPermission{ID: "", ProfileID: v.ProfileID, SessionID: v.Sessions[0].ID, ConnectionRef: a.Ref, Model: legacy.Model, MemoryMode: "none", PersonaDigest: strings.Repeat("a", 64), BootID: strings.Repeat("b", 32), Revision: 0, CreatedAt: legacy.ReviewedAt, ExpiresAt: legacy.ReviewedAt + 900, Limits: q.Limits, MaxRequests: 1, BudgetMicrousd: 30000}
	p.ID = chatDigest(p)
	if !validChatPermission(p, true) || validChatPermission(p) {
		t.Fatal("old receipt not inspectable or became executable")
	}
	current := openAIModelPolicy("gpt-6.1-sol")
	q.Model = current.Model
	q.Policy = &current
	raw, _ := aiWire(q)
	var wire map[string]json.RawMessage
	json.Unmarshal(raw, &wire)
	if string(wire["prompt_cache_options"]) != `{"mode":"explicit"}` || string(wire["reasoning"]) != `{"effort":"low"}` {
		t.Fatal("unreviewed cache or reasoning tariff", string(raw))
	}
}
func TestProviderModelsSchema23ReaderAndPausedMigration(t *testing.T) {
	s := fixtureStore(t)
	dir := s.dir
	s.Close()
	reader := os.Getenv("FASED_SCHEMA23_READER")
	if reader != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, reader, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "schema is newer") {
			t.Fatalf("old reader accepted: %s %v", out, err)
		}
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=23"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE update_pause SET phase='paused'"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if s != nil {
		s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "finish schema 23 recovery") {
		t.Fatal("paused migration accepted", err)
	}
}

func TestProviderModelsReasoningStreamAndEveryCurrentModel(t *testing.T) {
	for _, model := range currentModels.Models {
		t.Run(model.ID, func(t *testing.T) {
			s := fixtureStore(t)
			a := saveTestConnection(t, s)
			v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Current"})
			v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "Compare the supplied note", ConnectionRef: a.Ref, Model: model.ID, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 500000}})
			v = aiStart(t, s, v)
			if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[0]
			if q.State != "completed" || q.Model != model.ID || q.Admission == nil || !q.Admission.GenerationAttempted || q.SpentMicrousd > 500000 {
				t.Fatal("current model admission failed", q.State, q.Failure)
			}
		})
	}
	const model = "gpt-6.1-sol"
	text := `{"answer":"Fixture answer","cards":[]}`
	frame := func(kind string, seq int, extra map[string]any) string {
		m := map[string]any{"type": kind, "sequence_number": seq}
		for k, v := range extra {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return "event: " + kind + "\ndata: " + string(b) + "\n\n"
	}
	final := map[string]any{"id": "r", "model": model, "status": "completed", "output": []any{map[string]any{"type": "reasoning"}, map[string]any{"type": "message", "content": []any{map[string]any{"text": text}}}}}
	stream := frame("response.created", 0, map[string]any{"response": map[string]string{"id": "r", "model": model}}) + frame("response.output_text.delta", 1, map[string]any{"output_index": 1, "content_index": 0, "delta": text}) + frame("response.output_text.done", 2, map[string]any{"output_index": 1, "content_index": 0, "text": text}) + frame("response.completed", 3, map[string]any{"response": final})
	if _, err := readAIStream(strings.NewReader(stream), model, nil); err != nil {
		t.Fatal("reasoning companion rejected", err)
	}
	if _, err := readAIStream(strings.NewReader(strings.Replace(stream, `"output_index":1`, `"output_index":9`, 1)), model, nil); err == nil {
		t.Fatal("foreign output index accepted")
	}
}
func TestProviderModelsNewGenerationRemovesOldSelection(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		admissionReply(w, modelRows(admittedOpenAIModel, "gpt-7-terra"))
	})
	v, err := s.refreshProviderModels(t.Context(), a.Ref, rt)
	if err != nil {
		t.Fatal(err)
	}
	if m := modelChoice(v, admittedOpenAIModel); m.Selectable || m.Availability != "superseded" {
		t.Fatal("old generation selectable")
	}
	p := openAIModelPolicy()
	if s.providerRequestAllowed(AgentAIRequest{ConnectionRef: a.Ref, Model: p.Model, Policy: &p}, true) {
		t.Fatal("known older generation could dispatch")
	}
	if !newerProviderGeneration("gpt-6.10-sol", "gpt-6.2-sol") || newerProviderGeneration("gpt-7-sol", "gpt-6-astra") {
		t.Fatal("generation comparison")
	}
}

func TestProviderModelsFrozenDraftSurvivesRegistryRemoval(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Frozen"})
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, ConnectionRef: a.Ref, Model: "gpt-6.1-sol", Text: "Preserve this draft", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 100000}})
	q := v.AIRequests[0]
	before, _ := aiWire(q)
	saved := currentModels
	defer func() { currentModels = saved }()
	currentModels.Models = append([]currentModel(nil), saved.Models[0], saved.Models[2])
	after, _ := aiWire(q)
	if string(before) != string(after) {
		t.Fatal("registry changed historical wire")
	}
	draft, err := s.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
	if err != nil || draft.Model != q.Model || draft.Limits != q.Limits {
		t.Fatal("old draft not inspectable", err)
	}
	if s.providerRequestAllowed(q, true) {
		t.Fatal("removed model can execute")
	}
}

func TestProviderModelsSupersessionSurvivesFailedRefreshAndUnlock(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	p := openAIModelPolicy()
	q := AgentAIRequest{ConnectionRef: a.Ref, Model: p.Model, Policy: &p}
	newer := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) { admissionReply(w, modelRows(p.Model, "gpt-7-terra")) })
	if _, err := s.refreshProviderModels(t.Context(), a.Ref, newer); err != nil {
		t.Fatal(err)
	}
	failed := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	if _, err := s.refreshProviderModels(t.Context(), a.Ref, failed); err != nil {
		t.Fatal(err)
	}
	if s.providerRequestAllowed(q, true) {
		t.Fatal("failure forgot newer generation")
	}
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: a.Ref}); err != nil {
		t.Fatal(err)
	}
	v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: a.Ref, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	if s.providerRequestAllowed(q, true) || modelChoice(v, p.Model).Selectable {
		t.Fatal("unlock restored superseded model")
	}
}
