package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
				s.now = func() time.Time { return now.Add(11 * time.Minute) }
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	s := fixtureStore(t)
	credential := providerSignInCredential{Version: 1, Issuer: signInIssuer, Subject: "disposable-max-user", ClientID: "oaiapp_disposable_max", AccessToken: strings.Repeat("a", 8192), RefreshToken: strings.Repeat("r", 8192), IDToken: "i", Scope: signInScope, ExpiresAt: s.now().Add(time.Hour).Unix()}
	raw, _ := json.Marshal(credential)
	credential.IDToken = strings.Repeat("i", 1+providerSignInMaxPlaintext-len(raw))
	raw, _ = json.Marshal(credential)
	defer clear(raw)
	if len(raw) != providerSignInMaxPlaintext {
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
	for i := 0; i < 16; i++ {
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
	if err != nil || len(read.Accounts) != 16 {
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
	overflow := clone()
	extra := col.Accounts[0]
	extra.Ref, _ = randomToken()
	overflow.Accounts = append(overflow.Accounts, extra)
	assertPreserved(overflow)
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

func TestProviderSignInLiveCallbackIssWhitespaceReconnectRefModelsAndOIDC(t *testing.T) {
	t.Parallel()
	t.Run("iss-and-whitespace-remote-and-reconnect-ref", func(t *testing.T) {
		s := fixtureStore(t)
		start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "Live SIWC"})
		if err != nil || start.SignIn == nil || start.SignIn.Ref != "" {
			t.Fatal("new sign-in ref should be empty", err, start.SignIn)
		}
		u, _ := url.Parse(start.SignIn.URL)
		q := u.Query()
		q.Set("iss", signInIssuer)
		q.Set("scope", "chatgpt.tokens.use.direct email offline_access openid profile resource.invoke")
		u.RawQuery = q.Encode()
		account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
			Action:     "sign-in-complete",
			SignInID:   start.SignIn.ID,
			Callback:   "  " + u.String() + " \n",
			Passphrase: testVaultPassword,
		})
		if err != nil || account.Ref == "" || account.AuthMethod != "sign-in" || !account.PlanUse {
			t.Fatal("live-style iss+whitespace callback failed", err)
		}
		recon, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Ref: account.Ref, Remote: true})
		if err != nil || recon.SignIn == nil || recon.SignIn.Ref != account.Ref {
			t.Fatal("reconnect sign-in ref missing", err, recon.SignIn)
		}
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-cancel", SignInID: recon.SignIn.ID}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("local-listener-iss-and-mismatched-iss", func(t *testing.T) {
		s := fixtureStore(t)
		start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: false})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(start.SignIn.URL)
		bad := *u
		bq := bad.Query()
		bq.Set("iss", "https://evil.invalid")
		bad.RawQuery = bq.Encode()
		resBad, err := http.Get(bad.String())
		if err != nil {
			t.Fatal(err)
		}
		resBad.Body.Close()
		if resBad.StatusCode != 400 {
			t.Fatal("mismatched iss callback accepted on loopback listener", resBad.StatusCode)
		}
		q := u.Query()
		q.Set("iss", signInIssuer)
		u.RawQuery = q.Encode()
		resGood, err := http.Get(u.String())
		if err != nil {
			t.Fatal(err)
		}
		resGood.Body.Close()
		if resGood.StatusCode != 200 {
			t.Fatal("valid iss callback rejected on loopback listener", resGood.StatusCode)
		}
		account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Passphrase: testVaultPassword})
		if err != nil || account.Ref == "" {
			t.Fatal("local iss completion failed", err)
		}

		// Direct callback rejection on Remote mismatched iss
		start2, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true})
		if err != nil {
			t.Fatal(err)
		}
		u2, _ := url.Parse(start2.SignIn.URL)
		q2 := u2.Query()
		q2.Set("iss", "https://evil.invalid")
		u2.RawQuery = q2.Encode()
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start2.SignIn.ID, Callback: u2.String(), Passphrase: testVaultPassword}); err == nil {
			t.Fatal("remote mismatched iss accepted")
		}
	})

	t.Run("parseProviderSignInModels-visibility", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"models": []any{
				map[string]string{"slug": admittedOpenAIModel, "display_name": "GPT-6.1 Terra", "visibility": "list"},
				map[string]string{"slug": "internal model!", "display_name": "Bad\nName", "visibility": "private"},
				map[string]string{"slug": "hidden-model", "display_name": "Hidden", "visibility": "none"},
				map[string]string{"slug": "legacy-hidden", "display_name": "Legacy", "visibility": "hide"},
			},
		})
		rows, err := parseProviderSignInModels(raw, 1795219200)
		if err != nil || len(rows) != 1 || rows[0].ID != admittedOpenAIModel {
			t.Fatal("failed skipping non-list visibility models", rows, err)
		}
		badControl, _ := json.Marshal(map[string]any{
			"models": []any{
				map[string]string{"slug": admittedOpenAIModel, "display_name": "Bad Vis", "visibility": "list\n"},
			},
		})
		if _, err = parseProviderSignInModels(badControl, 1795219200); err == nil {
			t.Fatal("control character visibility accepted")
		}
		badListEntry, _ := json.Marshal(map[string]any{
			"models": []any{
				map[string]string{"slug": "bad slug", "display_name": "Broken", "visibility": "list"},
			},
		})
		if _, err = parseProviderSignInModels(badListEntry, 1795219200); err == nil {
			t.Fatal("invalid listed model accepted")
		}
	})

	t.Run("validateOIDCIDToken-multi-audience-and-azp", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		now := int64(1795267200)
		client := "oaiapp_test_client"
		jwksJSON, _ := json.Marshal(map[string]any{
			"keys": []any{map[string]string{
				"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
			}},
		})
		fetch := func(_ context.Context, _ http.RoundTripper, _, _ string, _ url.Values, target any) error {
			return json.Unmarshal(jwksJSON, target)
		}
		signToken := func(claims map[string]any) string {
			h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
			c, _ := json.Marshal(claims)
			msg := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
			d := sha256.Sum256([]byte(msg))
			sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
			return msg + "." + base64.RawURLEncoding.EncodeToString(sig)
		}
		good := signToken(map[string]any{
			"iss": signInIssuer, "sub": "user-1", "nonce": "n1", "azp": client,
			"aud": []string{client, signInResource}, "iat": now, "exp": now + 3600,
		})
		if sub, err := validateOIDCIDToken(t.Context(), nil, signInIssuer+"/.well-known/jwks.json", good, client, "n1", now, signInIssuer, fetch); err != nil || sub != "user-1" {
			t.Fatal("multi-audience azp-matched id_token refused", sub, err)
		}
		missingAzp := signToken(map[string]any{
			"iss": signInIssuer, "sub": "user-1", "nonce": "n1",
			"aud": []string{client, signInResource}, "iat": now, "exp": now + 3600,
		})
		if sub, err := validateOIDCIDToken(t.Context(), nil, signInIssuer+"/.well-known/jwks.json", missingAzp, client, "n1", now, signInIssuer, fetch); err != nil || sub != "user-1" {
			t.Fatal("multi-audience without azp matching client_id refused", sub, err)
		}
		wrongAzp := signToken(map[string]any{
			"iss": signInIssuer, "sub": "user-1", "nonce": "n1", "azp": "oaiapp_other",
			"aud": client, "iat": now, "exp": now + 3600,
		})
		if _, err := validateOIDCIDToken(t.Context(), nil, signInIssuer+"/.well-known/jwks.json", wrongAzp, client, "n1", now, signInIssuer, fetch); err == nil {
			t.Fatal("mismatched azp accepted")
		}
	})
}

func TestProviderSignInOAuthToleranceAutoRefreshAndOSPersist(t *testing.T) {
	t.Parallel()

	t.Run("callback-extra-params-fragment-trailing-iss-retry-and-default", func(t *testing.T) {
		s := fixtureStore(t)
		start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "Flexible Callback"})
		if err != nil || start.SignIn == nil {
			t.Fatal(err)
		}
		// Wrong state must NOT cancel the pending sign-in attempt; user can retry with corrected paste.
		badURL, _ := url.Parse(start.SignIn.URL)
		bq := badURL.Query()
		bq.Set("state", "wrong-state")
		badURL.RawQuery = bq.Encode()
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: badURL.String(), Passphrase: testVaultPassword}); err == nil {
			t.Fatal("mismatched state accepted")
		}
		stillPending, _ := s.AIConnection()
		if stillPending.SignIn == nil || stillPending.SignIn.ID != start.SignIn.ID {
			t.Fatal("callback error destructively cancelled pending sign-in")
		}
		// Valid callback with extra unknown params, #fragment, and trailing / on iss succeeds.
		u, _ := url.Parse(start.SignIn.URL)
		q := u.Query()
		q.Set("iss", signInIssuer+"/")
		q.Set("session_state", "extra-session-state")
		q.Set("authuser", "0")
		u.RawQuery = q.Encode()
		u.Fragment = "_=done"
		done, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: u.String(), Passphrase: testVaultPassword})
		if err != nil || done.Ref == "" || !done.PlanUse {
			t.Fatalf("flexible callback completion failed: %+v err=%v", done, err)
		}
		// First sign-in save leaves legacy API-key default slot untouched while surfacing the connection in top-level Connections.
		col, err := s.readAIVaultCollection()
		if err != nil || col.Default != "" {
			t.Fatalf("sign-in save mutated legacy col.Default: %+v err=%v", col.Default, err)
		}
		top, err := s.AIConnection()
		if err != nil || len(top.Connections) != 1 || top.Connections[0].Ref != done.Ref || top.Connections[0].Locked || !top.Connections[0].PlanUse {
			t.Fatalf("top-level AIConnection Connections missing saved sign-in account: %+v err=%v", top, err)
		}
	})

	t.Run("omitted-scope-and-missing-refresh-token", func(t *testing.T) {
		s := fixtureStore(t)
		s.providerFixture = false
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		var currentNonce, issuedCode string
		clientID := "oaiapp_no_refresh"
		s.provider.signInTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			var payload any
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				payload = map[string]any{"issuer": signInIssuer, "jwks_uri": signInIssuer + "/.well-known/jwks.json", "revocation_endpoint": signInIssuer + "/api/accounts/oauth/revoke"}
			case "/.well-known/jwks.json":
				payload = map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "k1", "jwk": nil, "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}}
			case "/api/accounts/oauth/token":
				_ = r.ParseForm()
				if r.Form.Get("code") != issuedCode {
					return nil, errors.New("unexpected code")
				}
				now := s.now().Unix()
				hdr, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "jwk": nil})
				cls, _ := json.Marshal(map[string]any{
					"iss": signInIssuer, "sub": "user-no-refresh", "nonce": currentNonce,
					"aud": clientID, "iat": now + 250, "exp": now + 3600,
					"pad": strings.Repeat("x", 8500),
				})
				msg := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(cls)
				d := sha256.Sum256([]byte(msg))
				sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
				// RFC 6749 §5.1 omitted scope + optional omitted refresh_token.
				payload = map[string]any{
					"access_token": "tok-" + strings.Repeat("a", 50),
					"id_token":     msg + "." + base64.RawURLEncoding.EncodeToString(sig),
					"token_type":   "Bearer",
					"expires_in":   3600,
				}
			case "/v1/models":
				payload = map[string]any{"models": []any{map[string]any{"slug": admittedOpenAIModel, "display_name": "GPT Test", "visibility": "list"}}}
			default:
				return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			b, _ := json.Marshal(payload)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
		})
		start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "No Refresh"})
		if err != nil || start.SignIn == nil {
			t.Fatal(err)
		}
		s.provider.mu.Lock()
		currentNonce = s.provider.signIn.nonce
		csrfState := s.provider.signIn.state
		callbackURL := s.provider.signIn.redirect
		s.provider.mu.Unlock()
		issuedCode = "code-no-refresh"
		u, _ := url.Parse(callbackURL)
		q := url.Values{"state": {csrfState}, "code": {issuedCode}, "client_id": {clientID}}
		u.RawQuery = q.Encode()
		v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: u.String(), Passphrase: testVaultPassword})
		if err != nil || v.Ref == "" || !v.PlanUse {
			t.Fatalf("omitted scope/refresh sign-in failed: %+v err=%v", v, err)
		}
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-refresh", Ref: v.Ref, Passphrase: testVaultPassword}); err == nil {
			t.Fatal("manual refresh without refresh_token succeeded")
		}
		disconnected, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-disconnect", Ref: v.Ref, Passphrase: testVaultPassword})
		if err != nil || !disconnected.Locked {
			t.Fatalf("disconnect without refresh_token failed: %+v err=%v", disconnected, err)
		}
	})

	t.Run("validateOIDCIDToken-iat-clock-skew-and-jwks-optional-alg-use", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		now := int64(1795267200)
		client := "oaiapp_test_client"
		jwksJSON, _ := json.Marshal(map[string]any{
			"keys": []any{map[string]any{
				"kty": "RSA", "kid": "k1",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
			}},
		})
		fetch := func(_ context.Context, _ http.RoundTripper, _, _ string, _ url.Values, target any) error {
			return json.Unmarshal(jwksJSON, target)
		}
		signWithHeader := func(iat int64) string {
			h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "jwk": nil})
			c, _ := json.Marshal(map[string]any{"iss": signInIssuer, "sub": "user-skew", "nonce": "n1", "aud": client, "iat": iat, "exp": now + 3600})
			msg := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
			d := sha256.Sum256([]byte(msg))
			sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
			return msg + "." + base64.RawURLEncoding.EncodeToString(sig)
		}
		if sub, err := validateOIDCIDToken(t.Context(), nil, signInIssuer+"/.well-known/jwks.json", signWithHeader(now+300), client, "n1", now, signInIssuer, fetch); err != nil || sub != "user-skew" {
			t.Fatalf("iat+300s / null jwk / omitted alg+use refused: %v", err)
		}
		if _, err := validateOIDCIDToken(t.Context(), nil, signInIssuer+"/.well-known/jwks.json", signWithHeader(now+301), client, "n1", now, signInIssuer, fetch); err == nil {
			t.Fatal("iat+301s skew accepted")
		}
	})

	t.Run("os-protected-auto-refresh-persists-and-keeps-epoch", func(t *testing.T) {
		dir := fixtureDir(t)
		s, err := openStoreWithTaskSecrets(dir, &testTaskSecrets{key: bytes.Repeat([]byte{93}, 32)})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		s.providerFixture = true
		v := completeSignInFixtureOS(t, s)
		env, _ := s.readAIVault(v.Ref)
		if !env.OSProtected {
			t.Fatal("expected OS-protected sign-in vault")
		}
		s.provider.mu.Lock()
		f, _ := newProviderSignInRenewFixture(providerSignInCredential{ClientID: "ignored"}, s.now)
		rawOld, _ := json.Marshal(env)
		plainOld, _ := s.openAIVault(env, "")
		credOld, _ := parseProviderSignInCredential(plainOld)
		clear(plainOld)
		f.subject, f.clientID, f.refresh, f.scope = credOld.Subject, credOld.ClientID, credOld.RefreshToken, credOld.Scope
		s.provider.signInTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.Host == "auth.openai.com" {
				return f.RoundTrip(r)
			}
			if !strings.Contains(r.Header.Get("Authorization"), "fased-test-signin-access-") {
				return nil, errors.New("missing bearer")
			}
			return (fixtureAITransport{}).RoundTrip(r)
		})
		state := s.credentialLocked(v.Ref)
		beforeEpoch := state.epoch
		credOld.ExpiresAt = s.now().Unix() + 10
		state.key, _ = json.Marshal(credOld)
		_ = rawOld
		s.provider.mu.Unlock()

		req, _ := http.NewRequestWithContext(t.Context(), "GET", "https://api.openai.com/v1/models", nil)
		rt := authenticatedAITransport{store: s, ref: v.Ref, modelsOnly: true, next: s.provider.signInTransport}
		res, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("auto-refresh RoundTrip failed: %v", err)
		}
		res.Body.Close()

		s.provider.mu.Lock()
		afterEpoch := s.credentialLocked(v.Ref).epoch
		s.provider.mu.Unlock()
		if afterEpoch != beforeEpoch {
			t.Fatalf("auto-refresh mutated epoch %d -> %d", beforeEpoch, afterEpoch)
		}
		// Restore from disk into fresh map to verify rotated refresh_token was persisted to OS vault.
		s.lockAIConnection()
		s.restoreOSProtectedConnections(t.Context())
		s.provider.mu.Lock()
		reloaded, ok := parseProviderSignInCredential(s.credentialLocked(v.Ref).key)
		s.provider.mu.Unlock()
		if !ok || reloaded.RefreshToken == credOld.RefreshToken || reloaded.AccessToken == credOld.AccessToken {
			t.Fatalf("rotated OS-protected refresh token not persisted across reload: ok=%v", ok)
		}
	})

	t.Run("busy-reset-on-db-cancel", func(t *testing.T) {
		s := fixtureStore(t)
		s.providerFixture = false
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var currentNonce, issuedCode string
		clientID := "oaiapp_busy_reset"
		s.provider.signInTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			var payload any
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				payload = map[string]any{"issuer": signInIssuer, "jwks_uri": signInIssuer + "/.well-known/jwks.json", "revocation_endpoint": signInIssuer + "/api/accounts/oauth/revoke"}
			case "/.well-known/jwks.json":
				payload = map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}}
				b, _ := json.Marshal(payload)
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       &cancelOnCloseReader{Reader: bytes.NewReader(b), onClose: cancel},
					Request:    r,
				}, nil
			case "/api/accounts/oauth/token":
				_ = r.ParseForm()
				if r.Form.Get("code") != issuedCode {
					return nil, errors.New("unexpected code")
				}
				now := s.now().Unix()
				hdr, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1"})
				cls, _ := json.Marshal(map[string]any{"iss": signInIssuer, "sub": "user-busy-reset", "nonce": currentNonce, "aud": clientID, "iat": now, "exp": now + 3600})
				msg := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(cls)
				d := sha256.Sum256([]byte(msg))
				sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
				payload = map[string]any{
					"access_token": "tok-" + strings.Repeat("b", 50),
					"id_token":     msg + "." + base64.RawURLEncoding.EncodeToString(sig),
					"token_type":   "Bearer",
					"expires_in":   3600,
				}
			case "/v1/models":
				payload = map[string]any{"models": []any{map[string]any{"slug": admittedOpenAIModel, "display_name": "GPT Test", "visibility": "list"}}}
			default:
				return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			b, _ := json.Marshal(payload)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
		})
		start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "Busy Reset"})
		if err != nil || start.SignIn == nil {
			t.Fatal(err)
		}
		s.provider.mu.Lock()
		currentNonce = s.provider.signIn.nonce
		csrfState := s.provider.signIn.state
		callbackURL := s.provider.signIn.redirect
		s.provider.mu.Unlock()
		issuedCode = "code-busy-reset"
		u, _ := url.Parse(callbackURL)
		u.RawQuery = url.Values{"state": {csrfState}, "code": {issuedCode}, "client_id": {clientID}}.Encode()
		if _, err = s.ChangeAIConnection(ctx, AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: u.String(), Passphrase: testVaultPassword}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled from aborted DB transaction, got err=%v", err)
		}
		s.provider.mu.Lock()
		attempt := s.provider.signIn
		busy := attempt != nil && attempt.busy
		s.provider.mu.Unlock()
		if attempt == nil || busy {
			t.Fatalf("signIn busy remained wedged or cleared unexpectedly: attempt=%v busy=%v", attempt != nil, busy)
		}
		// Retry with a healthy context should now succeed instead of failing ErrConflict.
		done, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: u.String(), Passphrase: testVaultPassword})
		if err != nil || done.Ref == "" || !done.PlanUse {
			t.Fatalf("retry after busy reset failed: %+v err=%v", done, err)
		}
	})
}

type cancelOnCloseReader struct {
	io.Reader
	onClose func()
}

func (r *cancelOnCloseReader) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}

type lockedDiagBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedDiagBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedDiagBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func completeSignInFixtureOS(t *testing.T, s *Store) AIConnectionView {
	t.Helper()
	v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Name: "ChatGPT OS"})
	if err != nil || v.SignIn == nil {
		t.Fatal(err)
	}
	res, err := http.Get(v.SignIn.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	out, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID})
	if err != nil || !out.OSProtected || out.Locked {
		t.Fatalf("complete OS sign-in failed: %+v err=%v", out, err)
	}
	return out
}

func TestProviderSignInRedactedDiagnostics(t *testing.T) {
	var buf lockedDiagBuffer
	prevOut := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prevOut)

	s := fixtureStore(t)
	s.providerFixture = false
	secretToken := "sk-secret-do-not-leak-1234567890"
	s.provider.signInTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"error":"invalid_grant","error_description":"bad token Bearer ` + secretToken + `"}`
		hdr := http.Header{}
		hdr.Set("Content-Type", "application/json")
		hdr.Set("CF-Ray", "ray-999-dfw")
		hdr.Set("x-request-id", "req-abc")
		return &http.Response{
			StatusCode: 403,
			Header:     hdr,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})
	start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true})
	if err != nil || start.SignIn == nil {
		t.Fatal(err)
	}
	s.provider.mu.Lock()
	callbackBase := s.provider.signIn.redirect
	csrfState := s.provider.signIn.state
	s.provider.mu.Unlock()
	u, _ := url.Parse(callbackBase)
	q := url.Values{"state": {csrfState}, "code": {"secret-auth-code-999"}, "client_id": {"oaiapp_diag"}}
	u.RawQuery = q.Encode()
	_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: u.String(), Passphrase: testVaultPassword})
	logged := buf.String()
	for _, want := range []string{`"stage":"signin-http"`, `"status":403`, `"cfRay":"ray-999-dfw"`, `"requestId":"req-abc"`, `"oauthError":"invalid_grant"`, `[redacted]`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("diagnostic log missing %s: %s", want, logged)
		}
	}
	for _, secret := range []string{secretToken, "secret-auth-code-999", testVaultPassword, csrfState} {
		if strings.Contains(logged, secret) {
			t.Fatalf("diagnostic log leaked secret %q: %s", secret, logged)
		}
	}
}

func TestRemoteProviderSignInOpensNoVPSLoopbackSocket(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "Remote No Socket"})
	if err != nil || start.SignIn == nil {
		t.Fatal(err)
	}
	s.provider.mu.Lock()
	attempt := s.provider.signIn
	s.provider.mu.Unlock()
	if attempt == nil || attempt.server != nil {
		t.Fatalf("Remote OpenAI sign-in opened a loopback callback server: %+v", attempt)
	}
	u, err := url.Parse(attempt.redirect)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/auth/callback" {
		t.Fatalf("unexpected Remote redirect URI: %q err=%v", attempt.redirect, err)
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if res, getErr := client.Get(attempt.redirect); getErr == nil {
		res.Body.Close()
		t.Fatalf("unexpected listening callback socket on VPS in Remote mode: %d", res.StatusCode)
	}
}

func TestRemoteProviderSignInPersistsAccentColor(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, AccentColor: "not-a-color"}); err == nil {
		t.Fatal("invalid sign-in accentColor should be refused")
	}
	start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "Remote Accent", AccentColor: "#27C5DA"})
	if err != nil || start.SignIn == nil || !start.SignIn.Remote {
		t.Fatalf("remote sign-in-start failed: err=%v signIn=%+v", err, start.SignIn)
	}
	done, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: start.SignIn.URL, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatalf("sign-in-complete failed: %v", err)
	}
	if done.AccentColor != "#27C5DA" {
		t.Fatalf("expected persisted AccentColor #27C5DA, got %q", done.AccentColor)
	}
}
