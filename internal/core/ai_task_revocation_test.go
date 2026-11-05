package core

import (
	"bytes"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This fixture seeds a sealed record to isolate refresh/revocation ordering;
// complete real task/worker/restart coverage lives in ai_task_persistence_test.
func sealedSignInRecord(t *testing.T, provider string) (*Store, AIConnectionView) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("protected restart credentials are Linux-only")
	}
	s := fixtureStore(t)
	s.taskSecrets = &testTaskSecrets{key: bytes.Repeat([]byte{43}, 32)}
	var c AIConnectionView
	if provider == "openai" {
		c = completeSignInFixture(t, s, true)
	} else {
		start, e := s.ChangeAIConnection(t.Context(), googleStartCommand(t, false))
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.Get(start.SignIn.URL)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		c, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Passphrase: testVaultPassword})
		if e != nil {
			t.Fatal(e)
		}
	}
	env, e := s.readAIVault(c.Ref)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := s.openAIVault(env, testVaultPassword)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	now := s.now().Unix()
	a := TaskAccessAllowance{ConnectionRef: c.Ref, ProfileID: "trading-analyst", Model: c.Model, BootID: s.bootID, CreatedAt: now, ExpiresAt: now + 3600, Tasks: []TaskAccessBinding{{"isolated-ordering-record", strings.Repeat("a", 64)}}, MaxRequests: 1, ContinueOnSignOut: true, ContinueAfterRestart: true}
	a.ID = taskAccessDigest(a)
	if e = s.persistTaskAccess(t.Context(), a, plain, env, now); e != nil {
		t.Fatal(e)
	}
	return s, c
}

func TestDurableTokenOperationsRevokeBeforeIO(t *testing.T) {
	for _, provider := range []string{"openai", "google"} {
		for _, action := range []string{"sign-in-refresh", "sign-in-disconnect"} {
			for _, writeFailure := range []bool{false, true} {
				t.Run(provider+"/"+action+map[bool]string{true: "/write-failure", false: "/provider-failure"}[writeFailure], func(t *testing.T) {
					s, c := sealedSignInRecord(t, provider)
					calls := 0
					s.provider.signInTransport = aiTestTransport(func(*http.Request) (*http.Response, error) {
						calls++
						col, e := s.readAIVaultCollection()
						if e != nil || len(col.TaskAccess) != 0 {
							t.Error("external I/O before durable revocation", e)
						}
						return nil, errors.New("controlled token endpoint unavailable")
					})
					if writeFailure {
						s.providerVaultFault = func() error { return errors.New("controlled credential write failure") }
					}
					if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: action, Ref: c.Ref, Passphrase: testVaultPassword}); e == nil {
						t.Fatal("failed operation reported success")
					}
					if writeFailure && calls != 0 {
						t.Fatal("external I/O followed failed revocation")
					}
					if !writeFailure && calls == 0 {
						t.Fatal("controlled endpoint not exercised")
					}
					col, e := s.readAIVaultCollection()
					if e != nil {
						t.Fatal(e)
					}
					if !writeFailure && len(col.TaskAccess) != 0 {
						t.Fatal("failed remote operation retained background grant")
					}
				})
			}
		}
	}
}

func TestDurableReloadNeverKeepsAnOldKey(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed-key", true: "unavailable"}[unavailable], func(t *testing.T) {
			s, c, _, key := durableTask(t)
			old := s.credentialLocked(c.Ref).key
			key.unavailable = unavailable
			key.key = bytes.Repeat([]byte{97}, 32)
			s.restoreTaskAccess(t.Context())
			state := s.credentialLocked(c.Ref)
			if len(state.key) != 0 || state.taskAccess != nil || !bytes.Equal(old, make([]byte, len(old))) {
				t.Fatal("failed reload retained previous authority")
			}
		})
	}
}

func TestDurableInteractiveUnlockPreservesTaskScope(t *testing.T) {
	s, c, _, _ := durableTask(t)
	before, e := s.AIConnection(c.Ref)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(t.Context(), ""); e != nil {
		t.Fatal(e)
	}
	later := s.now().Add(5 * time.Minute)
	s.now = func() time.Time { return later }
	view, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: c.Ref, Passphrase: testVaultPassword})
	if e != nil || view.Locked || view.TaskAccess == nil || view.TaskAccess.ID != before.TaskAccess.ID {
		t.Fatal("opening Chat revoked or changed independent task permission", e)
	}
	if s.credentialLocked(c.Ref).taskAccessLast != later.Unix() {
		t.Fatal("interactive unlock rewound the observation fence")
	}
}
