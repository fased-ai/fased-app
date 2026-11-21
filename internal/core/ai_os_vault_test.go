package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOSProtectedVaultLifecycleAndRestartUnseal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("OS-protected provider vault targets Linux")
	}
	dir := fixtureDir(t)
	osKey := &testTaskSecrets{key: bytes.Repeat([]byte{91}, 32)}
	localModels := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "gemma4:e2b", "object": "model", "owned_by": "local"}},
		})
	}))
	t.Cleanup(localModels.Close)

	s, err := openStoreWithTaskSecrets(dir, osKey)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.AIConnection()
	if err != nil || !v.OSVaultAvailable || !v.PersistentTaskAccessAvailable || !v.LiveCallsEnabled {
		_ = s.Close()
		t.Fatalf("unexpected OS vault view: %+v err=%v", v, err)
	}
	// Passwordless save when OS protection is available.
	added, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:         "add",
		Provider:       "local",
		Name:           "Passwordless Local",
		Endpoint:       localModels.URL + "/v1",
		CredentialMode: "local",
	})
	if err != nil || added.Locked || !added.OSProtected || added.Ref == "" {
		_ = s.Close()
		t.Fatalf("passwordless save failed: %+v err=%v", added, err)
	}
	raw, err := ReadPrivateFile(s.aiVaultPath())
	if err != nil || bytes.Contains(raw, []byte(localModels.URL)) {
		_ = s.Close()
		t.Fatal("plaintext leaked in vault", err)
	}
	env, err := s.readAIVault(added.Ref)
	if err != nil || !env.OSProtected {
		_ = s.Close()
		t.Fatal("envelope missing osProtected", err)
	}
	if _, err = s.openAIVault(env, testVaultPassword); err == nil {
		_ = s.Close()
		t.Fatal("OS-protected envelope accepted explicit passphrase")
	}
	// Verify tampering with AAD fields fails closed.
	for _, mutate := range []func(*aiVaultEnvelope){
		func(e *aiVaultEnvelope) { e.OSProtected = false },
		func(e *aiVaultEnvelope) { e.Provider = "openai" },
		func(e *aiVaultEnvelope) { e.Ciphertext = append([]byte(nil), e.Ciphertext...); e.Ciphertext[0] ^= 1 },
	} {
		bad := env
		mutate(&bad)
		if plain, err := s.openAIVault(bad, ""); err == nil {
			clear(plain)
			_ = s.Close()
			t.Fatal("tampered OS-protected envelope decrypted")
		}
	}
	wrongKeyStore, err := openStoreWithTaskSecrets(dir, &testTaskSecrets{key: bytes.Repeat([]byte{92}, 32)})
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	if locked, err := wrongKeyStore.AIConnection(added.Ref); err != nil || !locked.Locked {
		_ = wrongKeyStore.Close()
		_ = s.Close()
		t.Fatal("wrong OS key unlocked account", locked, err)
	}
	_ = wrongKeyStore.Close()

	// Time past 15m does NOT re-lock OS-protected interactive access.
	origNow := s.now
	s.now = func() time.Time { return origNow().Add(2 * time.Hour) }
	if !s.aiConnectionValid(added.Ref) {
		_ = s.Close()
		t.Fatal("OS-protected account expired after 15m")
	}
	s.now = origNow
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: OS-protected account auto-unseals cleanly without passphrase.
	n, err := openStoreWithTaskSecrets(dir, osKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	restored, err := n.AIConnection(added.Ref)
	if err != nil || restored.Locked || !restored.OSProtected || !n.aiConnectionValid(added.Ref) {
		t.Fatalf("restart did not auto-unseal OS-protected account: %+v err=%v", restored, err)
	}

	// Explicit lock holds across NewSession; 1-click passwordless unlock restores.
	if locked, err := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: added.Ref}); err != nil || !locked.Locked {
		t.Fatal("explicit lock failed", err)
	}
	token, err := n.NewSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if locked, err := n.AIConnection(added.Ref); err != nil || !locked.Locked {
		t.Fatal("NewSession bypassed explicit manual lock", err)
	}
	if unlocked, err := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: added.Ref}); err != nil || unlocked.Locked {
		t.Fatal("passwordless unlock failed", err)
	}
	// Logout revokes interactive access while signed out; NewSession auto-restores.
	if err = n.Logout(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if afterLogout, err := n.AIConnection(added.Ref); err != nil || !afterLogout.Locked {
		t.Fatal("logout failed to lock OS-protected account", err)
	}
	if _, err = n.NewSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	if afterLogin, err := n.AIConnection(added.Ref); err != nil || afterLogin.Locked {
		t.Fatal("NewSession failed to restore OS-protected account after logout", afterLogin, err)
	}
	// Passwordless remove clears the account completely.
	if _, err = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: added.Ref}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(n.aiVaultPath()); !os.IsNotExist(err) {
		t.Fatal("remove left credential vault")
	}
}

func TestOSProtectedVaultSignInOAuthAndTaskAccessWithoutPassphrase(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("OS-protected provider vault targets Linux")
	}
	osKey := &testTaskSecrets{key: bytes.Repeat([]byte{93}, 32)}
	for _, provider := range []string{"openai", "openrouter", "google"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			s.taskSecrets = osKey // Non-fixture taskSecrets unlocks OSVaultAvailable.
			startCmd := AIConnectionCommand{Action: "sign-in-start", Provider: provider, Name: provider + " OS"}
			if provider == "google" {
				startCmd = googleStartCommand(t, false)
			}
			v, err := s.ChangeAIConnection(t.Context(), startCmd)
			if err != nil || v.SignIn == nil || !v.OSVaultAvailable {
				t.Fatalf("sign-in start failed: %+v err=%v", v, err)
			}
			resp, err := http.Get(v.SignIn.URL)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatal("callback status", resp.StatusCode)
			}
			account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
				Action:   "sign-in-complete",
				SignInID: v.SignIn.ID,
			})
			if err != nil || account.Locked || !account.OSProtected {
				t.Fatalf("passwordless sign-in-complete failed: %+v err=%v", account, err)
			}
			if provider == "openai" || provider == "google" {
				refreshed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-refresh", Ref: account.Ref})
				if err != nil || refreshed.Locked || !refreshed.OSProtected {
					t.Fatalf("passwordless sign-in-refresh failed: %+v err=%v", refreshed, err)
				}
				disconnected, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-disconnect", Ref: account.Ref})
				if err != nil || !disconnected.Locked {
					t.Fatalf("passwordless sign-in-disconnect failed: %+v err=%v", disconnected, err)
				}
				// Reopening must keep signed-out OAuth accounts locked.
				s.provider.mu.Lock()
				s.provider.accounts = map[string]*providerCredential{}
				s.provider.mu.Unlock()
				s.restoreOSProtectedConnections(t.Context())
				stillLocked, err := s.AIConnection(account.Ref)
				if err != nil || !stillLocked.Locked {
					t.Fatalf("signed-out OS account auto-unlocked: %+v err=%v", stillLocked, err)
				}
				return
			}
			// OpenRouter issues an API key: verify passwordless task-access-allow & revoke keep Chat unlocked.
			taskView := connectedTask(t, s, account.Ref)
			task := taskView.Tasks[len(taskView.Tasks)-1]
			review, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
				Action:               "task-access-review",
				Ref:                  account.Ref,
				Revision:             taskView.Revision,
				ProfileID:            taskView.ProfileID,
				TaskIDs:              []string{task.ID},
				AuthorizationSeconds: 5400,
				ContinueOnSignOut:    true,
				ContinueAfterRestart: true,
			})
			if err != nil || review.AccessReview == nil {
				t.Fatal("task-access-review failed", err)
			}
			allowed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
				Action:        "task-access-allow",
				Ref:           account.Ref,
				Allowance:     review.AccessReview,
				ConsentDigest: review.AccessReview.ID,
			})
			if err != nil || allowed.Locked || allowed.TaskAccess == nil {
				t.Fatalf("passwordless task-access-allow failed: %+v err=%v", allowed, err)
			}
			revoked, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-revoke", Ref: account.Ref})
			if err != nil || revoked.Locked || revoked.TaskAccess != nil {
				t.Fatalf("task-access-revoke should keep OS-protected Chat unlocked: %+v err=%v", revoked, err)
			}
		})
	}
}

func TestOSProtectedVaultRefusesWithoutOSSecretsAndPreventsLegacyDowngrade(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("OS-protected provider vault targets Linux")
	}
	dir := fixtureDir(t)
	s, err := openStoreWithTaskSecrets(dir, &testTaskSecrets{unavailable: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	v, err := s.AIConnection()
	if err != nil || v.OSVaultAvailable {
		t.Fatalf("expected unavailable OS vault, got %+v err=%v", v, err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:         "add",
		Provider:       "openai",
		Name:           "Should refuse",
		CredentialMode: "openai",
		Key:            disposableAPIInput,
	}); err == nil {
		t.Fatal("empty passphrase accepted when OS secrets unavailable")
	}

	// Now inject OS secrets, save a legacy passphrase account explicitly, and confirm empty passphrase is refused on unlock/remove.
	s.taskSecrets = &testTaskSecrets{key: bytes.Repeat([]byte{94}, 32)}
	legacy, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:         "add",
		Provider:       "local",
		Name:           "Legacy passphrase",
		Endpoint:       "http://127.0.0.1:11434/v1",
		CredentialMode: "local",
		Passphrase:     testVaultPassword,
	})
	if err != nil || legacy.OSProtected {
		t.Fatalf("explicit passphrase unexpectedly marked osProtected: %+v err=%v", legacy, err)
	}
	s.lockAIConnection()
	for _, action := range []string{"unlock", "remove"} {
		if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: action, Ref: legacy.Ref}); err == nil {
			t.Fatalf("legacy passphrase account accepted empty passphrase for %s", action)
		}
	}
}

func TestNativeProviderRouterAndLongDeadline(t *testing.T) {
	s := fixtureStore(t)
	s.provider.apiCredentials = true
	// OpenAI transport must respect caller deadline > 5s instead of killing streams at 5s.
	tr := newOpenAITransport()
	var seenHeader string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	router := s.providerRuntimeTransport()
	if _, ok := router.(nativeProviderRouter); !ok {
		t.Fatalf("expected nativeProviderRouter, got %T", router)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, providerNameContextKey{}, "openai")
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+disposableAPIInput)
	var (
		seenDeadline    time.Time
		seenHasDeadline bool
	)
	tr.wire.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	tr.wire.TLSClientConfig.InsecureSkipVerify = true
	tr.wire.Proxy = func(pr *http.Request) (*url.URL, error) {
		seenDeadline, seenHasDeadline = pr.Context().Deadline()
		return nil, nil
	}
	tr.wire.DialContext = func(c context.Context, netw, addr string) (net.Conn, error) {
		if addr != "api.openai.com:443" {
			return nil, errors.New("unexpected origin: " + addr)
		}
		return (&net.Dialer{}).DialContext(c, netw, server.Listener.Addr().String())
	}
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("long-deadline RoundTrip failed: %v", err)
	}
	res.Body.Close()
	if !seenHasDeadline || time.Until(seenDeadline) < 40*time.Second || tr.wire.ResponseHeaderTimeout < 60*time.Second {
		t.Fatalf("request deadline truncated or timeout unsafe: has=%v until=%v headerTimeout=%v", seenHasDeadline, time.Until(seenDeadline), tr.wire.ResponseHeaderTimeout)
	}
	if res.StatusCode != http.StatusOK || seenHeader != "Bearer "+disposableAPIInput {
		t.Fatalf("unexpected response/header: status=%d auth=%q", res.StatusCode, seenHeader)
	}
	// Verify nativeProviderRouter rejects mismatched provider/origin before any network dial.
	badReq, _ := http.NewRequestWithContext(context.WithValue(t.Context(), providerNameContextKey{}, "anthropic"), "POST", "https://api.openai.com/v1/responses", strings.NewReader(`{}`))
	badReq.Header.Set("x-api-key", disposableAPIInput)
	badReq.Header.Set("anthropic-version", "2026-10-21")
	if _, err = router.RoundTrip(badReq); err == nil {
		t.Fatal("nativeProviderRouter routed anthropic request to openai origin")
	}
	// A testOnly credential must never acquire a live provider transport even on an enabled store.
	s.providerFixture = false
	testConn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:         "add",
		Provider:       "openai",
		Name:           "Test-only guard",
		CredentialMode: "test",
		Key:            testAPIKey,
		Passphrase:     testVaultPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rt := s.providerModelTransport(testConn.Ref); rt != nil {
		t.Fatalf("expected nil providerModelTransport for TestOnly account, got %T", rt)
	}
}
