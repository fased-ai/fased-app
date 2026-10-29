package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func googleStartCommand(t *testing.T, remote bool) AIConnectionCommand {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"action": "sign-in-start", "provider": "google", "name": "Google project", "googleProject": "fased-fixture-project", "remote": remote})
	var c AIConnectionCommand
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGoogleAPIOAuthUsesSharedAccountAndModels(t *testing.T) {
	s := fixtureStore(t)
	old := addNativeAccount(t, s, "google", false)
	env, _ := s.readAIVault(old.Ref)
	before, _ := json.Marshal(env)
	v, err := s.ChangeAIConnection(t.Context(), googleStartCommand(t, false))
	if err != nil || v.SignIn == nil || v.SignIn.Provider != "google" {
		t.Fatal("Google API OAuth unavailable", err)
	}
	res, err := http.Get(v.SignIn.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("callback", res.StatusCode)
	}
	a, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Passphrase: testVaultPassword})
	if err != nil || a.Provider != "google" || a.AuthMethod != "oauth" || a.PlanUse || a.Catalog.State != "ready" || a.LiveCallsEnabled {
		t.Fatal("Google API account incomplete", err, a.AuthMethod)
	}
	if !modelChoice(a, "gemini-3.8-flash").Selectable {
		t.Fatal("signed-in account model missing")
	}
	retained, _ := s.readAIVault(old.Ref)
	after, _ := json.Marshal(retained)
	if !bytes.Equal(before, after) {
		t.Fatal("API key account changed")
	}
	doc, _, _ := readAgents(t.Context(), s.db)
	if len(doc.AIRequests) != 0 {
		t.Fatal("sign-in generated a reply")
	}
}

func connectGoogleFixture(t *testing.T, s *Store, remote bool) AIConnectionView {
	t.Helper()
	v, err := s.ChangeAIConnection(t.Context(), googleStartCommand(t, remote))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(v.SignIn.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("private loopback callback failed", response.StatusCode)
	}
	a, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestGoogleOAuthChatTaskRefreshResultMemoryAndRestart(t *testing.T) {
	s := fixtureStore(t)
	account := connectGoogleFixture(t, s, true)
	s.provider.apiCredentials = true
	v, p := nativePermission(t, s, account, "gemini-3.8-flash")
	posts := 0
	transport := nativeHTTPSFixture(t, "google", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fased-fixture-google-access-token" || r.Header.Get("X-Goog-User-Project") != "fased-fixture-project" || r.Header.Get("X-Api-Key") != "" {
			t.Error("Google principal/quota project changed")
		}
		if r.Method == "POST" {
			posts++
		}
		response, err := compatibleFixtureResponse(r, "google")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		io.Copy(w, response.Body)
	})
	v = chatSend(t, s, v, p, "Synthetic Google API OAuth note")
	if err := s.advanceAgentAI(t.Context(), transport, false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[len(v.AIRequests)-1]
	if q.State != "completed" || q.SpentMicrousd <= 0 || q.ConnectionRef != account.Ref || q.Policy.Provider != "google" || posts != 1 {
		t.Fatal("OAuth not joined to API Chat", q.State, q.Failure)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: p.SessionID, UseAgentDefaults: true, Name: "Google API task", Text: "Summarize synthetic input", IntervalSeconds: 3600, MaxRuns: 1, Limits: &p.Limits, AuthorizationSeconds: 7200, TotalBudgetMicrousd: p.Limits.BudgetMicrousd})
	task := v.Tasks[len(v.Tasks)-1]
	v = agentApply(t, s, AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, ID: task.ID, ConsentDigest: task.AI.Digest})
	grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	vault, _ := ReadPrivateFile(s.aiVaultPath())
	later := s.now().Add(61 * time.Minute)
	s.now = func() time.Time { return later }
	if err = s.runAgentTasks(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentAI(t.Context(), transport, false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	last := v.AIRequests[len(v.AIRequests)-1]
	if last.State != "completed" || last.TaskID != task.ID || last.ConnectionRef != account.Ref || posts != 2 {
		t.Fatal("bounded automatic OAuth refresh/task failed", last.State, last.Failure)
	}
	current, _ := s.AIConnection(account.Ref)
	if current.TaskAccess == nil || current.TaskAccess.UsedRequests != 1 || !current.Locked {
		t.Fatal("refresh extended interactive unlock or lost finite grant")
	}
	after, _ := ReadPrivateFile(s.aiVaultPath())
	if !bytes.Equal(vault, after) {
		t.Fatal("automatic access refresh rewrote vault")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: "message", ID: v.Messages[len(v.Messages)-1].ID, Text: "Keep this synthetic Google API result."})
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retained, err := reopened.Agents(t.Context(), v.ProfileID, "")
	if err != nil || len(retained.Memories) != 1 || len(retained.Runs) != 1 {
		t.Fatal("OAuth result/memory lost across restart", err)
	}
	a, _ := reopened.AIConnection(account.Ref)
	if !a.Locked || a.TaskAccess != nil || a.AuthMethod != "oauth" {
		t.Fatal("restart granted OAuth authority")
	}
}
func TestGoogleOAuthRefreshDisconnectAndEncryptedBinding(t *testing.T) {
	s := fixtureStore(t)
	a := connectGoogleFixture(t, s, false)
	env, _ := s.readAIVault(a.Ref)
	plain, err := s.openAIVault(env, testVaultPassword)
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := parseGoogleCredential(plain)
	clear(plain)
	if !ok {
		t.Fatal("invalid encrypted Google credential")
	}
	for _, mutate := range []func(*aiVaultEnvelope){func(e *aiVaultEnvelope) { e.Provider = "openai" }, func(e *aiVaultEnvelope) { e.Mode = "openai-signin" }, func(e *aiVaultEnvelope) { e.TestOnly = false }} {
		bad := env
		mutate(&bad)
		if _, err = s.openAIVault(bad, testVaultPassword); err == nil {
			t.Fatal("OAuth AAD/credential class tamper accepted")
		}
	}
	refreshed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-refresh", Ref: a.Ref, Passphrase: testVaultPassword})
	if err != nil || refreshed.Ref != a.Ref || refreshed.GoogleProject != credential.Project || refreshed.PlanUse {
		t.Fatal("protected Google refresh failed", err)
	}
	disconnected, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-disconnect", Ref: a.Ref, Passphrase: testVaultPassword})
	if err != nil || !disconnected.Locked || disconnected.TaskAccess != nil {
		t.Fatal("Google revocation failed", err)
	}
	env, _ = s.readAIVault(a.Ref)
	plain, err = s.openAIVault(env, testVaultPassword)
	signedOut, ok := parseGoogleCredential(plain)
	clear(plain)
	if err != nil || !ok || !signedOut.SignedOut {
		t.Fatal("revocation not retained")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: a.Ref, Provider: "google", Key: "fased-test-no-api-fallback", Passphrase: testVaultPassword}); err == nil {
		t.Fatal("OAuth silently replaced by API key")
	}
}
func TestGoogleOAuthReconnectAndExpiredAccessNeedsPermission(t *testing.T) {
	s := fixtureStore(t)
	a := connectGoogleFixture(t, s, false)
	_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-disconnect", Ref: a.Ref, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: a.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	c := googleStartCommand(t, false)
	c.Ref = a.Ref
	start, err := s.ChangeAIConnection(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(start.SignIn.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	reconnected, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Passphrase: testVaultPassword})
	if err != nil || reconnected.Ref != a.Ref || reconnected.GoogleProject != "fased-fixture-project" {
		t.Fatal("protected reconnect lost account identity", err)
	}
	later := s.now().Add(61 * time.Minute)
	s.now = func() time.Time { return later }
	called := false
	s.provider.signInTransport = aiTestTransport(func(*http.Request) (*http.Response, error) { called = true; return nil, nil })
	rt := authenticatedAITransport{store: s, ref: a.Ref, next: fixtureAITransport{}, modelsOnly: true}
	request, _ := http.NewRequest("GET", providerOrigin("google")+providerModelsPath("google"), nil)
	if _, err = rt.RoundTrip(request); err == nil || called {
		t.Fatal("expired account refreshed without unlock or current grant")
	}
}

func TestGoogleOAuthCallbackOriginScopeAndCancellation(t *testing.T) {
	for _, mode := range []string{"wrong-state", "foreign-origin", "raw-code", "cancel", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			old := addNativeAccount(t, s, "google", false)
			before, _ := ReadPrivateFile(s.aiVaultPath())
			v, err := s.ChangeAIConnection(t.Context(), googleStartCommand(t, true))
			if err != nil {
				t.Fatal(err)
			}
			callback := v.SignIn.URL
			switch mode {
			case "wrong-state":
				callback = strings.Replace(callback, "state=", "state=wrong-", 1)
			case "foreign-origin":
				u, _ := url.Parse(callback)
				u.Host = "foreign.invalid"
				callback = u.String()
			case "raw-code":
				callback = "fixture-google-code"
			case "cancel":
				_, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-cancel", SignInID: v.SignIn.ID})
				if err != nil {
					t.Fatal(err)
				}
			case "expired":
				later := s.now().Add(6 * time.Minute)
				s.now = func() time.Time { return later }
			}
			_, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: v.SignIn.ID, Callback: callback, Passphrase: testVaultPassword})
			if err == nil {
				t.Fatal("invalid OAuth completion accepted")
			}
			after, _ := ReadPrivateFile(s.aiVaultPath())
			if !bytes.Equal(before, after) {
				t.Fatal("refused callback changed prior account", old.Ref)
			}
		})
	}
	s := fixtureStore(t)
	pending, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openrouter"})
	if err != nil {
		t.Fatal(err)
	}
	bad := googleStartCommand(t, false)
	bad.GoogleProject = "invalid/project"
	if _, err = s.ChangeAIConnection(t.Context(), bad); err == nil {
		t.Fatal("invalid project accepted")
	}
	if s.provider.signIn == nil || s.provider.signIn.view.ID != pending.SignIn.ID {
		t.Fatal("Google altered unrelated login")
	}
}
