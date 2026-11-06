//go:build linux

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/providersecrets"
)

func TestInstalledProviderFixtureNativeFailClosed(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "/not-a-systemd-credential-directory")
	dir := fixtureDir(t)
	s, err := StartInstalledProviderFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if _, ok := s.Store.taskSecrets.(providersecrets.Native); !ok || !s.agentFixture || !s.Store.providerOnlyFixture {
		t.Fatal("installed fixture replaced the Native source or selected a general fixture")
	}
	view, err := s.Store.Agents(t.Context(), "trading-analyst", "")
	if err != nil || view.PaperExecutionAvailable || s.Store.fixture != nil || s.Store.sessionFixture || s.Store.provider.apiCredentials || s.Store.probeTaskSecrets(t.Context()) {
		t.Fatal("provider fixture widened permission or fell back to a test key", err)
	}
	connection := saveTestConnection(t, s.Store)
	v := connectedTask(t, s.Store, connection.Ref)
	task := v.Tasks[len(v.Tasks)-1]
	if _, err = s.Store.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: connection.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400, ContinueOnSignOut: true, ContinueAfterRestart: true}); err == nil {
		t.Fatal("missing OS protection granted restart access")
	}
	if _, err = StartInstalledProviderFixture(t.Context(), fixtureDir(t), "127.0.0.1:0", "", nil, "signer.json"); err == nil {
		t.Fatal("provider-only fixture admitted a signer")
	}
	standalone, err := StartAgentFixture(t.Context(), fixtureDir(t), "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer standalone.Close(context.Background())
	if _, ok := standalone.Store.taskSecrets.(fixtureTaskSecrets); !ok || !standalone.Store.paperExecutionAvailable() || standalone.Store.providerOnlyFixture {
		t.Fatal("installed mode changed the existing standalone fixture")
	}
}

func installedProviderCredentialJourney(t *testing.T, start func(string) (*Server, error)) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var current *Server
	t.Cleanup(func() {
		if current != nil {
			_ = current.Close(context.Background())
		}
	})
	halt := func(s *Server) { s.taskCancel(); <-s.taskDone }
	restart := func() *Server {
		if current != nil {
			if err := current.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			current = nil
		}
		var err error
		current, err = start(dir)
		if err != nil {
			t.Fatal(err)
		}
		halt(current) // Freeze preparation before the real worker is started below.
		return current
	}
	s := restart()
	connection := saveTestConnection(t, s.Store)
	v := connectedTask(t, s.Store, connection.Ref)
	task := v.Tasks[len(v.Tasks)-1]
	review, err := s.Store.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: connection.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400, ContinueOnSignOut: true, ContinueAfterRestart: true})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := s.Store.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: connection.Ref, Allowance: review.AccessReview, ConsentDigest: review.AccessReview.ID, Passphrase: testVaultPassword})
	if err != nil || allowed.TaskAccess == nil {
		t.Fatal("protected grant", err)
	}
	grantID := allowed.TaskAccess.ID
	s.Store.logoutAIConnections()
	s = restart()
	view, err := s.Store.AIConnection(connection.Ref)
	if err != nil || !view.Locked || view.TaskAccess == nil || view.TaskAccess.ID != grantID || s.Store.aiConnectionValid(connection.Ref) {
		t.Fatal("restart unlocked Chat or lost the selected-task grant", err)
	}
	v, err = s.Store.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	v = taskAIRun(t, s.Store, v)
	s.startAgentTasks() // Actual shared worker and compiled transport, no test overlay.
	deadline := time.Now().Add(6 * time.Second)
	for {
		v, err = s.Store.Agents(t.Context(), v.ProfileID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(v.AIRequests) > 0 && v.AIRequests[len(v.AIRequests)-1].State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("installed provider worker did not complete the selected task")
		}
		time.Sleep(20 * time.Millisecond)
	}
	halt(s)
	view, err = s.Store.AIConnection(connection.Ref)
	if err != nil || view.TaskAccess == nil || view.TaskAccess.UsedRequests != 1 || view.TaskAccess.AllocatedMicrousd == 0 || v.Tasks[len(v.Tasks)-1].Runs != 1 {
		t.Fatal("task result or durable allocation missing", err)
	}
	allocated := view.TaskAccess.AllocatedMicrousd
	completed := len(v.AIRequests)
	s = restart()
	view, err = s.Store.AIConnection(connection.Ref)
	if err != nil || !view.Locked || view.TaskAccess == nil || view.TaskAccess.UsedRequests != 1 || view.TaskAccess.AllocatedMicrousd != allocated {
		t.Fatal("restart refilled permission or unlocked Chat", err)
	}
	if _, err = s.Store.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-revoke", Ref: connection.Ref}); err != nil {
		t.Fatal(err)
	}
	s = restart()
	view, err = s.Store.AIConnection(connection.Ref)
	if err != nil || !view.Locked || view.TaskAccess != nil {
		t.Fatal("revoked authority returned after restart", err)
	}
	v, err = s.Store.Agents(t.Context(), v.ProfileID, "")
	if err != nil || len(v.AIRequests) != completed || v.PaperExecutionAvailable || s.Store.provider.apiCredentials {
		t.Fatal("restart duplicated work or enabled non-provider execution", err)
	}
}

func TestInstalledProviderFixtureCredentialRestartWorker(t *testing.T) {
	key := &testTaskSecrets{key: bytes.Repeat([]byte{71}, 32)}
	installedProviderCredentialJourney(t, func(dir string) (*Server, error) {
		return startModesWithTaskSecrets(t.Context(), dir, "127.0.0.1:0", "", nil, true, false, key, true)
	})
}

// The existing disposable Ubuntu Native harness supplies a host-encrypted key.
// This exercises the production constructor, not a patched source selector.
func TestInstalledProviderFixtureNativeRuntime(t *testing.T) {
	expected := os.Getenv("FASED_NATIVE_EXPECTED_SHA256")
	if expected == "" {
		t.Skip("requires the existing protected Ubuntu credential harness")
	}
	key, err := (providersecrets.Native{}).Key(t.Context(), "disposable-installed-provider", false)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(key)
	clear(key)
	if hex.EncodeToString(h[:]) != expected {
		t.Fatal("unexpected Native credential identity")
	}
	installedProviderCredentialJourney(t, func(dir string) (*Server, error) {
		return StartInstalledProviderFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
	})
}
