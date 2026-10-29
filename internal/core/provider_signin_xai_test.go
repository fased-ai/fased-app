package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestXaiSignInNeedsSupportedClientAndInferenceContract(t *testing.T) {
	s := fixtureStore(t)
	account := addNativeAccount(t, s, "xai", false)
	agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Preserve this xAI conversation"})
	before, _ := ReadPrivateFile(s.aiVaultPath())
	doc, _, _ := readAgents(t.Context(), s.db)
	records, _ := json.Marshal(doc)
	s.provider.signInTransport = aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("unsupported sign-in contacted an authentication service")
		return nil, errors.New("no exchange allowed")
	})
	// Merely supplying a client identifier does not establish an approved Fased
	// registration, proxy/API scope, model/tariff or unattended-use contract.
	t.Setenv("FASED_XAI_OAUTH_CLIENT_ID", "disposable-unverified-client-id")
	for _, remote := range []bool{false, true} {
		_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "xai", Ref: account.Ref, Name: "xAI", Remote: remote})
		if err == nil || !strings.Contains(err.Error(), "Fased") || !strings.Contains(err.Error(), "inference") || !strings.Contains(err.Error(), "API key") {
			t.Fatalf("missing supported-client explanation: %v", err)
		}
		if s.provider.signIn != nil {
			t.Fatal("unsupported sign-in created an attempt")
		}
	}
	after, _ := ReadPrivateFile(s.aiVaultPath())
	docAfter, _, _ := readAgents(t.Context(), s.db)
	afterRecords, _ := json.Marshal(docAfter)
	if !bytes.Equal(before, after) || !bytes.Equal(records, afterRecords) {
		t.Fatal("refusal altered encrypted accounts or work records")
	}
	current, err := s.AIConnection(account.Ref)
	if err != nil || current.AuthMethod != "api-key" || current.PlanUse || current.LiveCallsEnabled || !modelChoice(current, "grok-4.7").Selectable {
		t.Fatal("supported xAI API route changed", err)
	}
	for _, p := range current.Catalog.Providers {
		if p.ID == "xai" && (len(p.Methods) != 1 || p.Methods[0].ID != "api-key") {
			t.Fatal("unavailable login advertised as implemented")
		}
	}
	pending := &providerSignInAttempt{view: ProviderSignInView{ID: "preserve-pending-google", Provider: "google", ExpiresAt: s.now().Unix() + 60}}
	s.provider.signIn = pending
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "xai"}); err == nil || s.provider.signIn != pending {
		t.Fatal("xAI refusal changed an unrelated pending login")
	}
	s.provider.mu.Lock()
	s.cancelProviderSignInLocked()
	s.provider.mu.Unlock()
}
