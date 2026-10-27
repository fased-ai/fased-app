package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAIAccountsPreserveLegacyAssignmentAndIsolation(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	env, err := s.readAIVault()
	if err != nil {
		t.Fatal(err)
	}
	// A genuine old envelope remains readable, unchanged when adding an account.
	if err = WritePrivateJSON(s.aiVaultPath(), env); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(env)
	b, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Name: "Research", Key: "fased-test-second-disposable-key", Passphrase: testVaultPassword})
	if err != nil || b.Ref == a.Ref || b.Locked {
		t.Fatal(b, err)
	}
	got, err := s.readAIVault(a.Ref)
	if err != nil {
		t.Fatal(err)
	}
	retained, _ := json.Marshal(got)
	if !bytes.Equal(original, retained) {
		t.Fatal("legacy envelope changed")
	}
	view, err := s.AIConnection()
	if err != nil || view.Ref != a.Ref || len(view.Connections) != 2 {
		t.Fatal("default changed", view, err)
	}
	raw, _ := os.ReadFile(s.aiVaultPath())
	if bytes.Contains(raw, []byte(testAPIKey)) || bytes.Contains(raw, []byte("fased-test-second-disposable-key")) {
		t.Fatal("plaintext key")
	}
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Account binding"})
	session := v.Sessions[len(v.Sessions)-1].ID
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "openai", Model: admittedOpenAIModel, AccountRef: &b.Ref})
	limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 512, DeadlineSeconds: 3, BudgetMicrousd: 30000}
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: session, UseAgentDefaults: true, Limits: limits, Text: "Keep account identity"})
	draft := v.AIRequests[len(v.AIRequests)-1]
	if draft.ConnectionRef != b.Ref {
		t.Fatal("wrong account")
	}
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "openai", Model: admittedOpenAIModel, AccountRef: &a.Ref})
	if v.AIRequests[len(v.AIRequests)-1].ConnectionRef != b.Ref {
		t.Fatal("draft retargeted")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: b.Ref}); err != nil {
		t.Fatal(err)
	}
	if !s.aiConnectionValid(a.Ref) || s.aiConnectionValid(b.Ref) {
		t.Fatal("lock crossed accounts")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: b.Ref, Passphrase: "incorrect-password-long"}); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	if !s.aiConnectionValid(a.Ref) {
		t.Fatal("failed unlock changed other account")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: b.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	if s.aiConnectionValid(b.Ref) || !s.aiConnectionValid(a.Ref) {
		t.Fatal("removed account fell back")
	}
	s.lockAIConnection()
	if s.aiConnectionValid(a.Ref) {
		t.Fatal("global lock missed account")
	}
}

func TestAIAccountsMigrationPreservesVaultAndHolds(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	v := agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: "trading-analyst", Name: "Analyst", Provider: "openai", Model: admittedOpenAIModel})
	before, _ := os.ReadFile(s.aiVaultPath())
	if _, err := s.db.Exec("PRAGMA user_version=22"); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	s.Close()
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, _ := os.ReadFile(reopened.aiVaultPath())
	if !bytes.Equal(before, after) {
		t.Fatal("migration rewrote vault")
	}
	d, revision, err := readAgents(t.Context(), reopened.db)
	if err != nil || revision != v.Revision {
		t.Fatal("migration changed revision", err)
	}
	if d.Profiles[agentProfile(d, "trading-analyst")].ConnectionRef != a.Ref {
		t.Fatal("legacy assignment not pinned")
	}
	got, err := reopened.AIConnection(a.Ref)
	if err != nil || !got.Locked {
		t.Fatal("restart unlocked", err)
	}
}

func TestAIAccountsNondefaultTaskGrantAndTransport(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	const key = "fased-test-task-second-disposable-key"
	b, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Name: "Tasks", Key: key, Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	v := connectedTask(t, s, b.Ref)
	task := v.Tasks[len(v.Tasks)-1]
	reviewed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: b.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400})
	if err != nil {
		t.Fatal(err)
	}
	grant := reviewed.AccessReview
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: b.Ref, Allowance: grant, ConsentDigest: grant.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: a.Ref}); err != nil {
		t.Fatal(err)
	}
	later := s.now().Add(61 * time.Minute)
	s.now = func() time.Time { return later }
	if err = s.runAgentTasks(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err = s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("cross-account credential")
		}
		return (fixtureAITransport{}).RoundTrip(r)
	}), true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if calls != 1 || v.AIRequests[len(v.AIRequests)-1].State != "completed" {
		t.Fatal("nondefault task failed", calls)
	}
	status, err := s.AIConnection(b.Ref)
	if err != nil || status.TaskAccess == nil || status.TaskAccess.UsedRequests != 1 {
		t.Fatal("wrong grant charged", err)
	}
	first, _ := s.AIConnection(a.Ref)
	if first.TaskAccess != nil {
		t.Fatal("grant leaked")
	}
}

func TestAIAccountsChatRevocationIsScoped(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	_, permission := chatPermission(t, s, a.Ref)
	b, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Key: "fased-test-another-disposable-key", Passphrase: testVaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	if !s.chatAccessCurrent(&permission) {
		t.Fatal("adding account revoked other chat")
	}
	for _, action := range []string{"lock", "remove"} {
		c := AIConnectionCommand{Action: action, Ref: b.Ref}
		if action == "remove" {
			c.Passphrase = testVaultPassword
		}
		if _, err = s.ChangeAIConnection(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		if !s.chatAccessCurrent(&permission) {
			t.Fatal("other account revoked chat", action)
		}
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: a.Ref}); err != nil {
		t.Fatal(err)
	}
	if s.chatAccessCurrent(&permission) {
		t.Fatal("same account lock kept consent")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: a.Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	if s.chatAccessCurrent(&permission) {
		t.Fatal("unlock resurrected old consent")
	}
}

func TestAIAccountsSchema22ReaderRefuses(t *testing.T) {
	reader := os.Getenv("FASED_SCHEMA22_READER")
	if reader == "" {
		t.Skip("previous reader supplied by local acceptance")
	}
	s := fixtureStore(t)
	saveTestConnection(t, s)
	dir := s.dir
	s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, reader, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "schema is newer") {
		t.Fatalf("older reader did not refuse: %s %v", out, err)
	}
}
func TestAIAccountsPausedMigrationRefuses(t *testing.T) {
	s := fixtureStore(t)
	if _, err := s.db.Exec("PRAGMA user_version=22"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	s.Close()
	next, err := OpenStore(dir)
	if next != nil {
		next.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "finish schema 22 recovery") {
		t.Fatal("paused migration accepted", err)
	}
}
