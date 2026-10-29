package core

import (
	"github.com/fased-ai/fased-app/internal/localprovider"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLocalNamedServerReviewedModels(t *testing.T) {
	for _, id := range []string{"google/gemma-4-e2b", "google/gemma-4-e4b", "google/gemma-4-E2B-it", "google/gemma-4-E4B-it"} {
		p := localModelPolicy(id)
		if p.Model != id || p.Provider != "local" || p.WireVersion != localWire || p.InputMicrousdPerMillion != 0 {
			t.Fatalf("named server model is unavailable: %s", id)
		}
	}
}

func TestLocalNamedServerSetupAndScopes(t *testing.T) {
	s := fixtureStore(t)
	original := saveTestConnection(t, s)
	before, _ := s.readAIVault(original.Ref)
	if len(localServerPresets()) != 3 {
		t.Fatal("missing server choices")
	}
	root, _ := s.AIConnection()
	found := false
	for _, p := range root.Catalog.Providers {
		if p.ID == "local" {
			found = len(p.Servers) == 3
		}
	}
	if !found {
		t.Fatal("UI/CLI catalog missing named local choices")
	}
	for _, server := range localServerPresets() {
		account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: "local", Server: server.ID, Passphrase: testVaultPassword})
		if err != nil || account.Endpoint != server.Endpoint || account.Name != server.Name || account.Provider != "local" || account.LiveCallsEnabled {
			t.Fatal("setup failed", server.ID, err)
		}
	}
	after, _ := s.readAIVault(original.Ref)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("original credentials changed")
	}
	for _, command := range []AIConnectionCommand{
		{Action: "add", Provider: "openai", Server: "ollama"},
		{Action: "lock", Provider: "local", Server: "vllm"},
		{Action: "sign-in-start", Provider: "local", Server: "lmstudio"},
		{Action: "add", Provider: "local", Server: "unknown"},
		{Action: "add", Provider: "local", Server: "vllm", Endpoint: "http://0.0.0.0:8000/v1", Passphrase: testVaultPassword},
	} {
		if _, err := s.ChangeAIConnection(t.Context(), command); err == nil {
			t.Fatal("preset bypassed setup scope", command.Action)
		}
	}
	view, _, _ := readAgents(t.Context(), s.db)
	if len(view.AIRequests) != 0 || len(view.Tasks) != 0 {
		t.Fatal("setup generated work")
	}
	for _, model := range []string{"gemma4:e2b", "google/gemma-4-e2b", "google/gemma-4-E2B-it"} {
		m, _ := currentProviderModel(model)
		if !newerProviderRecord(m, discoveredModel{ID: "google/gemma-5-E2B-it"}, nil) || newerProviderRecord(m, discoveredModel{ID: "google/gemma-3-E2B-it"}, nil) {
			t.Fatal("local latest-family mismatch", model)
		}
	}
}

func TestLocalNamedServersControlledHTTP(t *testing.T) {
	for server, entry := range map[string]struct{ model, owner string }{"ollama": {"gemma4:e2b", "library"}, "lmstudio": {"google/gemma-4-e2b", "organization_owner"}, "vllm": {"google/gemma-4-E2B-it", "vllm"}} {
		t.Run(server, func(t *testing.T) {
			s := fixtureStore(t)
			calls := 0
			key := ""
			if server != "ollama" {
				key = "fased-test-disposable-local-server-key"
			}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				expected := ""
				if key != "" {
					expected = "Bearer " + key
				}
				if r.Header.Get("Authorization") != expected {
					t.Error("wrong bound local key")
				}
				if r.Method == "GET" && r.URL.Path == "/v1/models" {
					admissionReply(w, map[string]any{"object": "list", "data": []map[string]any{{"id": entry.model, "object": "model", "owned_by": entry.owner, "created": 0}}})
					return
				}
				if r.Method != "POST" || r.URL.Path != "/v1/responses" {
					t.Error("unexpected local route")
					w.WriteHeader(400)
					return
				}
				copy := r.Clone(r.Context())
				u := *r.URL
				u.Scheme, u.Host = "https", "local.fased.invalid"
				copy.URL = &u
				res, err := localFixtureResponse(copy)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer res.Body.Close()
				w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
				io.Copy(w, res.Body)
			}))
			defer endpoint.Close()
			a, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: "local", Server: server, Endpoint: endpoint.URL + "/v1", Key: key, Passphrase: testVaultPassword})
			if err != nil {
				t.Fatal(err)
			}
			wire := http.DefaultTransport.(*http.Transport).Clone()
			wire.Proxy = nil
			defer wire.CloseIdleConnections()
			transport := &localprovider.Transport{Enabled: true, Wire: wire}
			a, err = s.refreshProviderModels(t.Context(), a.Ref, transport)
			if err != nil || !modelChoice(a, entry.model).Selectable {
				t.Fatal("server-native model unavailable", err)
			}
			v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Named local", Provider: "local", Model: entry.model, AccountRef: &a.Ref})
			v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Synthetic local Chat"})
			session := v.Sessions[len(v.Sessions)-1].ID
			v = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: v.ProfileID, ID: session, UseAgentDefaults: true, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 1, AuthorizationSeconds: 900})
			permission := *v.ChatReview
			v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: v.ProfileID, ID: session, ChatPermission: &permission, ConsentDigest: permission.ID})
			v = chatSend(t, s, v, permission, "Synthetic local server note")
			if err = s.advanceAgentAI(t.Context(), transport, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[len(v.AIRequests)-1]
			if q.State != "completed" || q.Model != entry.model || q.ConnectionRef != a.Ref || q.SpentMicrousd != 0 || q.CostBasis != localUsageCostBasis || calls != 3 {
				t.Fatal("controlled server completion", q.State, q.Failure, calls)
			}
		})
	}
}
