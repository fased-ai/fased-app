package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func completeSignInFixture(t *testing.T, s *Store, remote bool) AIConnectionView {
	t.Helper()
	v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: remote, Name: "ChatGPT"})
	if err != nil || v.SignIn == nil || !v.SignInAvailable {
		t.Fatal("start sign-in", err)
	}
	command := AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Passphrase: testVaultPassword}
	if remote {
		command.Callback = v.SignIn.URL
	} else {
		response, err := http.Get(v.SignIn.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("callback", response.StatusCode)
		}
		state, err := s.AIConnection()
		if err != nil || state.SignIn == nil || state.SignIn.State != "callback-ready" {
			t.Fatal("callback not captured", err)
		}
	}
	v, err = s.ChangeAIConnection(t.Context(), command)
	if err != nil || v.Ref == "" || v.Locked || v.AuthMethod != "sign-in" || v.CredentialMode != "openai-signin" || !v.TestOnly || v.LiveCallsEnabled || !v.PlanUse {
		t.Fatal("complete sign-in", err, v.AuthMethod, v.Catalog.State)
	}
	return v
}

func TestProviderSignInLocalRemoteStorageAndNoGeneration(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "Local", true: "Remote"}[remote], func(t *testing.T) {
			s := fixtureStore(t)
			old := saveTestConnection(t, s)
			oldEnvelope, _ := s.readAIVault(old.Ref)
			oldBytes, _ := json.Marshal(oldEnvelope)
			agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Preserve this conversation"})
			before, _, _ := readAgents(t.Context(), s.db)
			beforeBytes, _ := json.Marshal(before)
			v := completeSignInFixture(t, s, remote)
			if v.Ref == old.Ref {
				t.Fatal("account reference reused")
			}
			current, _ := s.readAIVault(old.Ref)
			currentBytes, _ := json.Marshal(current)
			if !bytes.Equal(oldBytes, currentBytes) {
				t.Fatal("existing API account changed")
			}
			env, err := s.readAIVault(v.Ref)
			if err != nil || env.Version != 5 || !env.TestOnly || env.Mode != "openai-signin" {
				t.Fatal("distinct vault format missing")
			}
			plain, err := s.openAIVault(env, testVaultPassword)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(plain)
			credential, ok := parseProviderSignInCredential(plain)
			if !ok {
				t.Fatal("credential invalid")
			}
			stored, _ := os.ReadFile(s.aiVaultPath())
			viewBytes, _ := json.Marshal(v)
			for _, secret := range []string{credential.AccessToken, credential.RefreshToken, credential.IDToken, credential.ClientID, testVaultPassword} {
				if bytes.Contains(stored, []byte(secret)) || bytes.Contains(viewBytes, []byte(secret)) {
					t.Fatal("credential disclosed")
				}
			}
			selected := false
			for _, model := range v.Catalog.Models {
				if model.ID == admittedOpenAIModel && model.Selectable && model.Method == "sign-in" {
					selected = true
				}
			}
			if !selected {
				t.Fatal("account-specific current sign-in model unavailable", v.Catalog.State)
			}
			if err = s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) {
				t.Error("setup dispatched generation")
				return nil, errors.New("forbidden")
			}), true); err != nil {
				t.Fatal(err)
			}
			after, _, _ := readAgents(t.Context(), s.db)
			afterBytes, _ := json.Marshal(after)
			if !bytes.Equal(beforeBytes, afterBytes) {
				t.Fatal("authentication changed conversations/tasks/grants")
			}
			if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: v.Ref, Provider: "openai", Key: testAPIKey, Passphrase: testVaultPassword}); err == nil {
				t.Fatal("sign-in silently became API billing")
			}
		})
	}
}

func TestProviderSignInRefreshRestartAndDisconnect(t *testing.T) {
	s := fixtureStore(t)
	v := completeSignInFixture(t, s, true)
	readCredential := func() providerSignInCredential {
		t.Helper()
		env, err := s.readAIVault(v.Ref)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.openAIVault(env, testVaultPassword)
		if err != nil {
			t.Fatal(err)
		}
		defer clear(raw)
		c, ok := parseProviderSignInCredential(raw)
		if !ok {
			t.Fatal("invalid saved credential")
		}
		return c
	}
	before := readCredential()
	s.provider.mu.Lock()
	epoch := s.credentialLocked(v.Ref).epoch
	s.provider.mu.Unlock()
	refreshed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-refresh", Ref: v.Ref, Passphrase: testVaultPassword})
	if err != nil || refreshed.Ref != v.Ref || refreshed.Locked {
		t.Fatal("refresh", err)
	}
	after := readCredential()
	if before.ClientID != after.ClientID || before.Subject != after.Subject || before.RefreshToken == after.RefreshToken {
		t.Fatal("refresh failed identity/rotation")
	}
	s.provider.mu.Lock()
	if s.credentialLocked(v.Ref).epoch <= epoch || s.credentialLocked(v.Ref).taskAccess != nil {
		t.Fatal("refresh retained old grants")
	}
	s.provider.mu.Unlock()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.providerFixture = true
	locked, err := n.AIConnection(v.Ref)
	if err != nil || !locked.Locked || locked.SignIn != nil || locked.AuthMethod != "sign-in" {
		t.Fatal("restart retained auth session", err)
	}
	if _, err = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: "incorrect-long-password"}); err == nil {
		t.Fatal("wrong password unlocked sign-in")
	}
	if _, err = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	disconnected, err := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-disconnect", Ref: v.Ref, Passphrase: testVaultPassword})
	if err != nil || !disconnected.Locked || disconnected.PlanUse || disconnected.Ref != v.Ref {
		t.Fatal("disconnect", err)
	}
	env, _ := n.readAIVault(v.Ref)
	raw, _ := n.openAIVault(env, testVaultPassword)
	retained, ok := parseProviderSignInCredential(raw)
	clear(raw)
	if !ok || !retained.SignedOut || retained.ClientID != before.ClientID || retained.Subject != before.Subject || retained.AccessToken != "" || retained.RefreshToken != "" {
		t.Fatal("disconnect lost registration or retained tokens")
	}
	if _, err = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	start, err := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Ref: v.Ref, Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: start.SignIn.URL, Passphrase: testVaultPassword})
	if err != nil || finished.Ref != v.Ref || !finished.PlanUse {
		t.Fatal("registration reauthorization", err)
	}
}

func TestProviderSignInRefusesCallbackReplayExpiryAndDowngrade(t *testing.T) {
	for _, kind := range []string{"state", "host", "duplicate", "missing-client", "expiry", "cancel", "logout"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true})
			if err != nil {
				t.Fatal(err)
			}
			callback, _ := url.Parse(v.SignIn.URL)
			q := callback.Query()
			switch kind {
			case "state":
				q.Set("state", "wrong")
			case "host":
				callback.Host = "attacker.invalid"
			case "duplicate":
				q.Add("code", "second")
			case "missing-client":
				q.Del("client_id")
			case "expiry":
				now := s.now()
				s.now = func() time.Time { return now.Add(6 * time.Minute) }
			case "cancel":
				_, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-cancel", SignInID: v.SignIn.ID})
				if err != nil {
					t.Fatal(err)
				}
			case "logout":
				s.lockAIConnection()
			}
			callback.RawQuery = q.Encode()
			_, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Callback: callback.String(), Passphrase: testVaultPassword})
			if err == nil {
				t.Fatal("invalid callback/session accepted")
			}
			if _, err = os.Stat(s.aiVaultPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused sign-in created credential")
			}
		})
	}
	s := fixtureStore(t)
	v := completeSignInFixture(t, s, true)
	env, _ := s.readAIVault(v.Ref)
	env.Mode = "openai"
	if validAIVaultMode(env) {
		t.Fatal("v5 downgraded to API mode")
	}
	env.Mode = "openai-signin"
	env.TestOnly = false
	if _, err := s.openAIVault(env, testVaultPassword); err == nil {
		t.Fatal("fixture/live marker not authenticated")
	}
	s.providerFixture = false
	status, err := s.AIConnection(v.Ref)
	if err != nil || !status.Locked || status.SignInAvailable {
		t.Fatal("ordinary runtime admitted fixture sign-in", err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai"}); err == nil {
		t.Fatal("ordinary live auth enabled")
	}
}

// The official origin is retained at the interface; this test-only transport
// rewrites the physical destination to a trusted HTTPS fixture, never runtime.
func TestProviderSignInControlledHTTPSAndTokenScope(t *testing.T) {
	s := fixtureStore(t)
	v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	a := s.provider.signIn
	inert := a.wire
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "auth.openai.com" {
			t.Error("official auth origin lost")
		}
		copy := r.Clone(r.Context())
		target := *r.URL
		target.Scheme, target.Host = "https", "auth.openai.com"
		copy.URL = &target
		response, err := inert.RoundTrip(copy)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	a.wire = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if !signInEndpoint(r.URL.String()) {
			return nil, errors.New("untrusted endpoint")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		copy.URL = &u
		copy.Host = "auth.openai.com"
		return server.Client().Transport.RoundTrip(copy)
	})
	v, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Callback: v.SignIn.URL, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal("HTTPS auth", err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), "GET", "https://api.openai.com/v1/models", nil)
	calls := 0
	rt := authenticatedAITransport{store: s, ref: v.Ref, modelsOnly: true, next: aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fased-test-signin-access-") {
			t.Error("wrong sign-in bearer")
		}
		return (fixtureAITransport{}).RoundTrip(r)
	})}
	response, err := rt.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls != 1 {
		t.Fatal("missing discovery")
	}
	s.provider.mu.Lock()
	state := s.credentialLocked(v.Ref)
	credential, _ := parseProviderSignInCredential(state.key)
	credential.ExpiresAt = s.now().Unix()
	state.key, _ = json.Marshal(credential)
	s.provider.mu.Unlock()
	if _, err = rt.RoundTrip(request); err == nil || calls != 1 {
		t.Fatal("expired access token used")
	}
}

func TestProviderSignInIdentityAndPlanPermissionRefusals(t *testing.T) {
	for _, kind := range []string{"nonce", "subject", "plan"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true})
			if err != nil {
				t.Fatal(err)
			}
			f := s.provider.signIn.wire.(*providerSignInFixture)
			if kind == "nonce" {
				f.nonce = "wrong"
			} else if kind == "subject" {
				f.subject = "invalid subject"
			} else {
				f.scope = "openid profile email offline_access"
			}
			v, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Callback: v.SignIn.URL, Passphrase: testVaultPassword})
			if kind != "plan" {
				if err == nil {
					t.Fatal("unverified identity accepted")
				}
				return
			}
			if err != nil || v.PlanUse {
				t.Fatal("identity-only sign-in", err)
			}
			for _, m := range v.Catalog.Models {
				if m.Selectable {
					t.Fatal("identity alone enabled inference")
				}
			}
		})
	}
}

func TestProviderSignInVaultMaximumAccountsAndOversizePreservation(t *testing.T) {
	s := fixtureStore(t)
	credential := providerSignInCredential{Version: 1, Issuer: signInIssuer, Subject: "disposable-max-user", ClientID: "oaiapp_disposable_max", AccessToken: strings.Repeat("a", 4096), RefreshToken: strings.Repeat("r", 4096), IDToken: "i", Scope: signInScope, ExpiresAt: s.now().Add(time.Hour).Unix()}
	raw, _ := json.Marshal(credential)
	credential.IDToken = strings.Repeat("i", 1+16000-len(raw))
	raw, _ = json.Marshal(credential)
	defer clear(raw)
	if len(raw) != 16000 {
		t.Fatal("not testing plaintext bound", len(raw))
	}
	if _, ok := parseProviderSignInCredential(raw); !ok {
		t.Fatal("maximum credential refused")
	}
	salt := bytes.Repeat([]byte{0x42}, 16)
	key := passwordHash(testVaultPassword, salt)
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	col := aiVaultCollection{Version: 3}
	for i := 0; i < 8; i++ {
		ref, err := randomToken()
		if err != nil {
			t.Fatal(err)
		}
		env := aiVaultEnvelope{Version: 5, Provider: "openai", Mode: "openai-signin", TestOnly: true, Ref: ref, Name: "Disposable maximum account", Salt: append([]byte(nil), salt...), Nonce: make([]byte, 12)}
		if _, err = rand.Read(env.Nonce); err != nil {
			t.Fatal(err)
		}
		env.Ciphertext = gcm.Seal(nil, env.Nonce, raw, s.aiVaultAAD(env))
		col.Accounts = append(col.Accounts, env)
	}
	col.Default = col.Accounts[0].Ref
	if err := s.writeAIVaultCollection(col); err != nil {
		t.Fatal("maximum collection write", err)
	}
	original, err := os.ReadFile(s.aiVaultPath())
	if err != nil || len(original) <= 131072 {
		t.Fatal("not exercising former file limit", len(original), err)
	}
	read, err := s.readAIVaultCollection()
	if err != nil || len(read.Accounts) != 8 {
		t.Fatal("writer produced unreadable maximum collection", err)
	}
	for _, env := range read.Accounts {
		plain, err := s.openAIVault(env, testVaultPassword)
		if err != nil || !bytes.Equal(plain, raw) {
			clear(plain)
			t.Fatal("maximum credential did not round trip", err)
		}
		clear(plain)
	}
	assertPreserved := func(candidate aiVaultCollection) {
		t.Helper()
		if err := s.writeAIVaultCollection(candidate); err == nil {
			t.Fatal("invalid/oversized replacement accepted")
		}
		after, err := os.ReadFile(s.aiVaultPath())
		if err != nil || !bytes.Equal(original, after) {
			t.Fatal("rejected replacement changed original vault", err)
		}
	}
	clone := func() aiVaultCollection {
		copy := col
		copy.Accounts = append([]aiVaultEnvelope(nil), col.Accounts...)
		return copy
	}
	ninth := clone()
	extra := col.Accounts[0]
	extra.Ref, _ = randomToken()
	ninth.Accounts = append(ninth.Accounts, extra)
	assertPreserved(ninth)
	oversize := clone()
	oversize.Accounts[0].Ciphertext = append(append([]byte(nil), col.Accounts[0].Ciphertext...), 0)
	assertPreserved(oversize)
	tooLong := clone()
	tooLong.Accounts[0].Name = strings.Repeat("x", 262144)
	assertPreserved(tooLong)
	credential.IDToken += "i"
	oversizedPlain, _ := json.Marshal(credential)
	defer clear(oversizedPlain)
	if _, ok := parseProviderSignInCredential(oversizedPlain); ok {
		t.Fatal("oversized plaintext admitted")
	}
}
