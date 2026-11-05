package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/providersecrets"
)

type testTaskSecrets struct {
	key         []byte
	unavailable bool
}

func (k *testTaskSecrets) Key(context.Context, string, bool) ([]byte, error) {
	if k.unavailable {
		return nil, providersecrets.ErrUnavailable
	}
	return append([]byte(nil), k.key...), nil
}

func durableTask(t *testing.T) (*Store, AIConnectionView, AgentView, *testTaskSecrets) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Native unattended credential persistence currently targets Linux")
	}
	s := fixtureStore(t)
	key := &testTaskSecrets{key: bytes.Repeat([]byte{37}, 32)}
	s.taskSecrets = key
	c := saveTestConnection(t, s)
	v := connectedTask(t, s, c.Ref)
	task := v.Tasks[len(v.Tasks)-1]
	r, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: c.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400, ContinueOnSignOut: true, ContinueAfterRestart: true})
	if e != nil {
		t.Fatal(e)
	}
	a := r.AccessReview
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: c.Ref, Allowance: a, ConsentDigest: a.ID, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	return s, c, v, key
}

func restartDurable(t *testing.T, s *Store, key *testTaskSecrets) *Store {
	t.Helper()
	dir := s.dir
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := openStoreWithTaskSecrets(dir, key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = n.Close() })
	n.providerFixture = true
	return n
}

func TestDurableTaskAccessRestartPreservesLimits(t *testing.T) {
	s, c, v, key := durableTask(t)
	original, e := s.AIConnection(c.Ref)
	if e != nil {
		t.Fatal(e)
	}
	s = restartDurable(t, s, key)
	view, e := s.AIConnection(c.Ref)
	if e != nil || !view.Locked || view.TaskAccess == nil || view.TaskAccess.ID != original.TaskAccess.ID || s.aiConnectionValid(c.Ref) {
		t.Fatal("restart granted Chat or lost exact task grant", e)
	}
	v = taskAIRun(t, s, v)
	calls := 0
	if e = s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) }), true); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("actual bounded task did not dispatch", calls)
	}
	view, e = s.AIConnection(c.Ref)
	if e != nil || view.TaskAccess.UsedRequests != 1 {
		t.Fatal("charge missing", e)
	}
	charged := view.TaskAccess.AllocatedMicrousd
	s = restartDurable(t, s, key)
	view, e = s.AIConnection(c.Ref)
	if e != nil || !view.Locked || view.TaskAccess == nil || view.TaskAccess.UsedRequests != 1 || view.TaskAccess.AllocatedMicrousd != charged {
		t.Fatal("restart refilled allowance", e)
	}
	raw, e := os.ReadFile(s.aiVaultPath())
	if e != nil || bytes.Contains(raw, []byte(testAPIKey)) || bytes.Contains(raw, key.key) {
		t.Fatal("raw provider key or OS key persisted in application vault")
	}
	var old aiVaultEnvelope
	if json.Unmarshal(raw, &old) != nil || validAIVaultMode(old) {
		t.Fatal("older single-envelope reader accepted durable collection")
	}
}

func TestDurableTaskAccessRecoveryRefusesUnsafeState(t *testing.T) {
	for _, fault := range []string{"locked-os", "different-os-key", "tamper-budget", "revoked", "provider-lock", "changed-credential", "expired", "uncertain", "dispatch-reservation", "grant-change"} {
		t.Run(fault, func(t *testing.T) {
			s, c, v, key := durableTask(t)
			switch fault {
			case "locked-os":
				key.unavailable = true
			case "different-os-key":
				key.key = bytes.Repeat([]byte{99}, 32)
			case "tamper-budget":
				col, e := s.readAIVaultCollection()
				if e != nil {
					t.Fatal(e)
				}
				col.TaskAccess[0].Allowance.MaxRequests++
				col.TaskAccess[0].Allowance.ID = taskAccessDigest(col.TaskAccess[0].Allowance)
				if e = WritePrivateJSON(s.aiVaultPath(), col); e != nil {
					t.Fatal(e)
				}
			case "revoked":
				if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-revoke", Ref: c.Ref}); e != nil {
					t.Fatal(e)
				}
			case "provider-lock":
				if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: c.Ref}); e != nil {
					t.Fatal(e)
				}
			case "changed-credential":
				if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: c.Ref, Key: "fased-test-rotated-background-credential", Passphrase: testVaultPassword}); e != nil {
					t.Fatal(e)
				}
			case "expired":
				col, e := s.readAIVaultCollection()
				if e != nil {
					t.Fatal(e)
				}
				a := col.TaskAccess[0].Allowance
				a.ExpiresAt = a.CreatedAt + 1
				a.CreatedAt -= 3600
				a.ID = taskAccessDigest(a)
				// Altered bounds are also authenticated, so this cannot unseal.
				col.TaskAccess[0].Allowance = a
				if e = WritePrivateJSON(s.aiVaultPath(), col); e != nil {
					t.Fatal(e)
				}
			case "uncertain", "dispatch-reservation":
				v = taskAIRun(t, s, v)
				if fault == "dispatch-reservation" {
					q := taskAIRequest(v.Tasks[len(v.Tasks)-1], s.now().Unix())
					q.TaskID = v.Tasks[len(v.Tasks)-1].ID
					q.TaskDigest = v.Tasks[len(v.Tasks)-1].AI.Digest
					if !s.reserveTaskAccess(&q) {
						t.Fatal("reservation failed")
					}
				} else {
					d, r, e := readAgents(t.Context(), s.db)
					if e != nil {
						t.Fatal(e)
					}
					d.AIRequests[len(d.AIRequests)-1].State = "unknown"
					tx, e := s.db.BeginTx(t.Context(), nil)
					if e != nil {
						t.Fatal(e)
					}
					if e = saveAgents(t.Context(), tx, d, r); e != nil {
						t.Fatal(e)
					}
					if e = tx.Commit(); e != nil {
						t.Fatal(e)
					}
				}
			case "grant-change":
				v = agentApply(t, s, AgentCommand{Action: "task-ai-cancel", ProfileID: v.ProfileID, ID: v.Tasks[len(v.Tasks)-1].ID})
			}
			s = restartDurable(t, s, key)
			view, e := s.AIConnection(c.Ref)
			if fault == "dispatch-reservation" {
				col, readErr := s.readAIVaultCollection()
				if e != nil || readErr != nil || len(col.TaskAccess) != 1 || col.TaskAccess[0].Allowance.UsedRequests != 1 || view.TaskAccess != nil {
					t.Fatal("uncommitted dispatch refilled reserved budget", e)
				}
				return
			}
			if e != nil { // Authenticated tampering may refuse the entire vault, never unlock it.
				if fault != "tamper-budget" && fault != "expired" {
					t.Fatal(e)
				}
				return
			}
			if !view.Locked || view.TaskAccess != nil {
				t.Fatal("unsafe grant restored", fault)
			}
		})
	}
}

func TestDurableTaskAccessExpiryWithoutMutation(t *testing.T) {
	s, c, _, _ := durableTask(t)
	later := s.now().Add(2 * time.Hour)
	s.now = func() time.Time { return later }
	view, e := s.AIConnection(c.Ref)
	if e != nil || view.TaskAccess != nil || !view.Locked {
		t.Fatal("expired grant retained", e)
	}
}

func TestDurableRestoreRetiresTerminalTime(t *testing.T) {
	for _, condition := range []string{"expiry", "rollback"} {
		t.Run(condition, func(t *testing.T) {
			s, c, _, key := durableTask(t)
			col, err := s.readAIVaultCollection()
			if err != nil {
				t.Fatal(err)
			}
			a := col.TaskAccess[0].Allowance
			observed := a.ExpiresAt
			if condition == "rollback" {
				observed = a.CreatedAt - 1
			}
			s.now = func() time.Time { return time.Unix(observed, 0) }
			if err = s.restoreTaskAccess(t.Context()); err != nil {
				t.Fatal(err)
			}
			col, err = s.readAIVaultCollection()
			if err != nil || len(col.TaskAccess) != 0 {
				t.Fatal("observed terminal time left a recoverable grant", err)
			}
			// Move back into the original window and restart the actual store.
			// A retired permission must not become eligible again.
			s = restartDurable(t, s, key)
			view, err := s.AIConnection(c.Ref)
			if err != nil || !view.Locked || view.TaskAccess != nil {
				t.Fatal("retired permission returned after restart", err)
			}
		})
	}
}

func TestDurableRestoreRefusesRetirementWriteFailure(t *testing.T) {
	s, c, _, _ := durableTask(t)
	col, err := s.readAIVaultCollection()
	if err != nil {
		t.Fatal(err)
	}
	expired := col.TaskAccess[0].Allowance.ExpiresAt
	s.now = func() time.Time { return time.Unix(expired, 0) }
	s.providerVaultFault = func() error { return errors.New("controlled retirement write failure") }
	if err = s.restoreTaskAccess(t.Context()); err == nil {
		t.Fatal("startup may not ignore a failed durable retirement")
	}
	state := s.credentialLocked(c.Ref)
	if len(state.key) != 0 || state.taskAccess != nil {
		t.Fatal("failed retirement retained authority")
	}
}
