package core

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestProviderAdmissionRemainsClosedAndReportsCapabilities(t *testing.T) {
	s := fixtureStore(t)
	if providerActivationEnabled || s.provider.apiCredentials || newOpenAITransport().enabled {
		t.Fatal("ordinary runtime enabled live admission")
	}
	token, err := s.NewSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string, auth bool, result any) int {
		r := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/"+path, nil)
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), result) != nil {
			t.Fatal("invalid view")
		}
		return w.Code
	}
	for _, admitted := range []bool{false, true} {
		s.provider.apiCredentials = admitted // Test binary only; no live key or transport.
		var agents AgentView
		var connection AIConnectionView
		if read("agents", false, &agents) != 401 || read("ai/connection", false, &connection) != 401 {
			t.Fatal("owner authentication bypassed")
		}
		if read("agents", true, &agents) != 200 || read("ai/connection", true, &connection) != 200 {
			t.Fatal("owner views unavailable")
		}
		if agents.AIExecutionAvailable != admitted || agents.AIFixtureAvailable || agents.ProviderCallsEnabled || agents.FinancialExecution || connection.LiveCallsEnabled {
			t.Fatal("capability confused with fixture or live authority")
		}
		modes := []string{"test", "local"}
		if admitted {
			modes = []string{"openai", "anthropic", "xai", "moonshot", "moonshot-intl", "kimi-code", "ollama-cloud", "google", "openrouter", "qwen", "opencode", "local"}
		}
		if len(connection.CredentialModes) != len(modes) {
			t.Fatal("credential editor disagrees with runtime admission")
		}
		for i, mode := range modes {
			if connection.CredentialModes[i] != mode {
				t.Fatal("credential editor disagrees with runtime admission")
			}
		}

	}
}

func TestProviderAdmissionRejectsSyntheticAndResearchRequests(t *testing.T) {
	s := fixtureStore(t)
	connection := admitDisposableAPI(t, s, "")
	good := AgentAIRequest{ConnectionRef: connection.Ref, Model: admittedOpenAIModel, Policy: chatModelPolicy(admittedOpenAIModel)}
	if !s.providerRequestAllowed(good, false) {
		t.Fatal("admitted provider request unavailable")
	}
	for _, change := range []func(*AgentAIRequest){
		func(q *AgentAIRequest) { q.ConnectionRef = "" },
		func(q *AgentAIRequest) { q.ConnectionRef = "foreign" },
		func(q *AgentAIRequest) { q.Model = fixtureAIModel },
		func(q *AgentAIRequest) { q.Policy = nil },
		func(q *AgentAIRequest) { q.ResearchTurnID = "held-research" },
	} {
		bad := good
		change(&bad)
		if s.providerRequestAllowed(bad, false) {
			t.Fatal("unadmitted request gained ordinary execution")
		}
	}
	// A saved synthetic credential cannot become a production credential merely
	// because provider admission is available.
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: connection.Ref, Key: testAPIKey, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	view, _ := s.AIConnection()
	good.ConnectionRef = view.Ref
	if s.providerRequestAllowed(good, false) {
		t.Fatal("test credential admitted to provider path")
	}
}
