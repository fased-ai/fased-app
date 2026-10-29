package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestQwenRetiredPortalSignInPreservesAccountsAndWork(t *testing.T) {
	s := fixtureStore(t)
	account := addNativeAccount(t, s, "qwen", false)
	agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Keep this Qwen draft"})
	vaultBefore, _ := ReadPrivateFile(s.aiVaultPath())
	documentBefore, _, _ := readAgents(t.Context(), s.db)
	recordsBefore, _ := json.Marshal(documentBefore)
	// Refusal happens before the browser/code-exchange controller and never turns
	// an API account into legacy unmetered portal access.
	s.provider.signInTransport = aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("retired sign-in performed network exchange")
		return nil, errors.New("no exchange")
	})
	for _, remote := range []bool{false, true} {
		_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "qwen", Ref: account.Ref, Name: "Qwen", Remote: remote})
		if err == nil || !strings.Contains(err.Error(), "discontinued") || !strings.Contains(err.Error(), "API key") {
			t.Fatalf("retired portal needs a precise supported alternative: %v", err)
		}
		if s.provider.signIn != nil {
			t.Fatal("retired portal created an attempt")
		}
	}
	vaultAfter, _ := ReadPrivateFile(s.aiVaultPath())
	if !bytes.Equal(vaultBefore, vaultAfter) {
		t.Fatal("refused sign-in altered credentials")
	}
	documentAfter, _, _ := readAgents(t.Context(), s.db)
	recordsAfter, _ := json.Marshal(documentAfter)
	if !bytes.Equal(recordsBefore, recordsAfter) {
		t.Fatal("refused sign-in altered records or drafts")
	}
	current, err := s.AIConnection(account.Ref)
	if err != nil || current.AuthMethod != "api-key" || current.PlanUse || current.Ref != account.Ref {
		t.Fatal("Qwen account switched auth route", err)
	}
	for _, provider := range current.Catalog.Providers {
		if provider.ID == "qwen" {
			if len(provider.Methods) != 1 || provider.Methods[0].ID != "api-key" {
				t.Fatal("retired sign-in exposed in Providers")
			}
		}
	}
	if len(documentAfter.AIRequests) != 0 || len(documentAfter.Tasks) != 0 {
		t.Fatal("sign-in setup dispatched work")
	}

	pending := &providerSignInAttempt{view: ProviderSignInView{ID: "another-pending-login", Provider: "openrouter", ExpiresAt: s.now().Unix() + 60}}
	s.provider.signIn = pending
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "qwen"}); err == nil || s.provider.signIn != pending {
		t.Fatal("Qwen refusal affected another pending login")
	}
	s.provider.mu.Lock()
	s.cancelProviderSignInLocked()
	s.provider.mu.Unlock()
}
