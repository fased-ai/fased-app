package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestRouterSignInLocalRemoteUsesExistingAPIAccounts(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "Local", true: "Remote"}[remote], func(t *testing.T) {
			s := fixtureStore(t)
			old := saveTestConnection(t, s)
			retained, _ := s.readAIVault(old.Ref)
			before, _ := json.Marshal(retained)
			agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Preserve"})
			v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openrouter", Name: "Router login", Remote: remote})
			if err != nil || v.SignIn == nil || v.SignIn.Provider != "openrouter" {
				t.Fatal("start", err)
			}
			command := AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Passphrase: testVaultPassword}
			if remote {
				command.Callback = "fixture-router-code"
			} else {
				res, err := http.Get(v.SignIn.URL)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatal("callback", res.StatusCode)
				}
			}
			account, err := s.ChangeAIConnection(t.Context(), command)
			if err != nil || account.Provider != "openrouter" || account.AuthMethod != "api-key" || account.PlanUse || account.LiveCallsEnabled || account.Ref == old.Ref || account.Locked || account.Catalog.State != "ready" {
				t.Fatal("complete", err, account)
			}
			records, _, _ := readAgents(t.Context(), s.db)
			if len(records.AIRequests) != 0 {
				t.Fatal("sign-in generated a reply")
			}
			raw, _ := ReadPrivateFile(s.aiVaultPath())
			if bytes.Contains(raw, []byte("fased-test-router-oauth")) {
				t.Fatal("plaintext key persisted")
			}
			original, _ := s.readAIVault(old.Ref)
			after, _ := json.Marshal(original)
			if !bytes.Equal(before, after) {
				t.Fatal("prior account changed")
			}
			if _, err = s.ChangeAIConnection(t.Context(), command); err == nil {
				t.Fatal("sign-in replay accepted")
			}
			view, permission := nativePermission(t, s, account, "google/gemini-3.8-flash")
			view = chatSend(t, s, view, permission, "Synthetic router note")
			if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
				t.Fatal(err)
			}
			view, _ = s.Agents(t.Context(), view.ProfileID, "")
			if view.AIRequests[len(view.AIRequests)-1].State != "completed" {
				t.Fatal("sign-in account not shared with Chat")
			}
			if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: account.Ref, Passphrase: testVaultPassword}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRouterSignInPKCECancelExpiryAndCallbackIsolation(t *testing.T) {
	for _, remote := range []bool{false, true} {
		s := fixtureStore(t)
		s.providerFixture = false
		s.provider.signInTransport = aiTestTransport(func(*http.Request) (*http.Response, error) {
			t.Error("setup performed token exchange")
			return nil, errors.New("no exchange")
		})
		v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openrouter", Remote: remote})
		if err != nil {
			t.Fatal(err)
		}
		a := s.provider.signIn
		u, _ := url.Parse(v.SignIn.URL)
		q := u.Query()
		challenge := sha256.Sum256([]byte(a.verifier))
		if u.Scheme != "https" || u.Host != "openrouter.ai" || u.Path != "/auth" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || q.Get("key_label") != "Fased" {
			t.Fatal("PKCE changed", u)
		}
		if remote {
			if q.Get("callback_url") != "" || q.Get("state") != "" || a.server != nil {
				t.Fatal("Remote requires a reachable callback")
			}
		} else {
			if q.Get("callback_url") != a.redirect || q.Get("state") != a.state {
				t.Fatal("callback not bound")
			}
			for _, callback := range []string{a.redirect + "?code=ok&state=wrong", a.redirect + "?code=ok&code=again&state=" + a.state, "http://foreign.invalid/auth/callback?code=ok&state=" + a.state} {
				if _, _, err = providerSignInCallback(a, callback); err == nil {
					t.Fatal("cross-attempt callback accepted")
				}
			}
		}
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-cancel", SignInID: v.SignIn.ID}); err != nil {
			t.Fatal(err)
		}
		if s.provider.signIn != nil || a.verifier != "" {
			t.Fatal("cancel retained secret")
		}
		v, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openrouter", Remote: remote})
		if err != nil {
			t.Fatal(err)
		}
		future := s.now().Add(6 * time.Minute)
		s.now = func() time.Time { return future }
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Callback: "fixture-router-code", Passphrase: testVaultPassword}); err == nil {
			t.Fatal("expired attempt accepted")
		}
		s.provider.mu.Lock()
		s.cancelProviderSignInLocked()
		s.provider.mu.Unlock()
	}
}
