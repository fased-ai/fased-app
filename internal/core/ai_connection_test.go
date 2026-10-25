package core

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testAPIKey = "fased-test-disposable-openai-connection-key"
const testVaultPassword = "disposable-vault-password"

func TestAIConnectionEncryptedStorage(t *testing.T) {
	s := fixtureStore(t)
	v, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Key: testAPIKey, Passphrase: testVaultPassword})
	if e != nil || v.Ref == "" || v.Locked || v.LiveCallsEnabled {
		t.Fatal("save", v, e)
	}
	b, e := os.ReadFile(filepath.Join(s.dir, "ai-credentials.json"))
	if e != nil || strings.Contains(string(b), testAPIKey) || strings.Contains(string(b), testVaultPassword) {
		t.Fatal("plaintext credential")
	}
	s.lockAIConnection()
	if _, e = s.ChangeAIConnection(context.Background(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: "wrong-password"}); e == nil {
		t.Fatal("wrong password unlocked vault")
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	token, e := s.NewSession(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(t.Context(), token); e != nil {
		t.Fatal(e)
	}
	if s.aiConnectionValid(v.Ref) {
		t.Fatal("logout left credentials unlocked")
	}
}

func saveTestConnection(t *testing.T, s *Store) AIConnectionView {
	t.Helper()
	v, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Key: testAPIKey, Passphrase: testVaultPassword})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func prepareConnected(t *testing.T, s *Store, ref string) AgentView {
	t.Helper()
	v := aiPrepare(t, s)
	q := v.AIRequests[0]
	return agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: q.SessionID, Text: "Connected paper analysis", Limits: &q.Limits, ConnectionRef: ref})
}
func httpsFixture(t *testing.T, handler http.HandlerFunc) http.RoundTripper {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	rt, e := newOpenAIHTTPSFixture(server.URL, roots)
	if e != nil {
		t.Fatal(e)
	}
	return rt
}
func TestAIConnectionRestartExpiryRotationAndRemoval(t *testing.T) {
	s := fixtureStore(t)
	v := saveTestConnection(t, s)
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(s.aiVaultPath())
		if info.Mode().Perm() != 0600 {
			t.Fatal("vault permissions")
		}
	}
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: v.Ref, Key: "sk-not-a-real-key", Passphrase: testVaultPassword}); e == nil {
		t.Fatal("nonfixture key admitted")
	}
	old := s.now
	s.now = func() time.Time { return old().Add(16 * time.Minute) }
	if s.aiConnectionValid(v.Ref) {
		t.Fatal("unlock never expires")
	}
	s.now = old
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	got, e := n.AIConnection()
	if e != nil || !got.Locked || got.Ref != v.Ref {
		t.Fatal("restart unlocked", e)
	}
	if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	rotated, e := n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: v.Ref, Key: testAPIKey + "-replacement", Passphrase: testVaultPassword})
	if e != nil || rotated.Ref == v.Ref || n.aiConnectionValid(v.Ref) {
		t.Fatal("rotation", e)
	}
	if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: rotated.Ref, Passphrase: "wrong-password"}); e == nil {
		t.Fatal("removed without unlock")
	}
	if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "remove", Ref: rotated.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(n.aiVaultPath()); !os.IsNotExist(e) {
		t.Fatal("vault remains")
	}
}
func TestAIConnectionTamperInstanceAndPause(t *testing.T) {
	s := fixtureStore(t)
	v := saveTestConnection(t, s)
	original, _ := os.ReadFile(s.aiVaultPath())
	var env aiVaultEnvelope
	_ = json.Unmarshal(original, &env)
	env.Ciphertext[0] ^= 1
	if e := WritePrivateJSON(s.aiVaultPath(), env); e != nil {
		t.Fatal(e)
	}
	if s.aiConnectionValid(v.Ref) {
		t.Fatal("changed ciphertext remains unlocked")
	}
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); e == nil {
		t.Fatal("tamper decrypted")
	}
	if e := os.WriteFile(s.aiVaultPath(), original, 0600); e != nil {
		t.Fatal(e)
	}
	other := fixtureStore(t)
	if e := os.WriteFile(other.aiVaultPath(), original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := other.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); e == nil {
		t.Fatal("instance binding missing")
	}
	_, _ = s.db.Exec("UPDATE update_pause SET phase='paused'")
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: v.Ref, Key: testAPIKey, Passphrase: testVaultPassword}); e == nil {
		t.Fatal("vault changed during pause")
	}
	after, _ := os.ReadFile(s.aiVaultPath())
	if string(after) != string(original) {
		t.Fatal("paused write touched vault")
	}
}
func TestAIConnectionHTTPSConsentAndNoReplay(t *testing.T) {
	s := fixtureStore(t)
	connection := saveTestConnection(t, s)
	v := prepareConnected(t, s, connection.Ref)
	q := v.AIRequests[1]
	if strings.Contains(q.Context, testAPIKey) || strings.Contains(q.Disclosure, testAPIKey) {
		t.Fatal("key disclosed")
	}
	copy := q
	copy.ConnectionRef = "other"
	if aiDigest(copy) == q.Digest {
		t.Fatal("unbound connection")
	}
	var calls atomic.Int32
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey || r.URL.Path != "/v1/responses" || r.Method != "POST" {
			t.Error("auth or origin")
		}
		canonical := r.Clone(r.Context())
		target := *r.URL
		target.Scheme, target.Host = "https", "api.openai.com"
		canonical.URL = &target
		res, e := (fixtureAITransport{}).RoundTrip(canonical)
		if e != nil {
			t.Error(e)
			return
		}
		defer res.Body.Close()
		_, _ = io.Copy(w, res.Body)
	})
	v = aiStart(t, s, v)
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[1]
	if calls.Load() != 1 || q.State != "completed" || q.HeldMicrousd != 0 || q.SpentMicrousd <= 0 {
		t.Fatal("HTTPS completion", q.State, calls.Load())
	}
	for _, f := range []string{"core.sqlite", "core.sqlite-wal"} {
		b, _ := os.ReadFile(filepath.Join(s.dir, f))
		if strings.Contains(string(b), testAPIKey) || strings.Contains(string(b), testVaultPassword) {
			t.Fatal("secret persisted in metadata")
		}
	}
}
func TestAIConnectionStaleAndLockedConsent(t *testing.T) {
	s := fixtureStore(t)
	connection := saveTestConnection(t, s)
	v := prepareConnected(t, s, connection.Ref)
	s.lockAIConnection()
	q := v.AIRequests[1]
	c := AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}
	if _, e := s.changeAgents(t.Context(), c, true); e == nil {
		t.Fatal("locked consent")
	}
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: connection.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	v = aiStart(t, s, v)
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: connection.Ref, Key: testAPIKey + "-replacement", Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	if e := s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) }), true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[1]
	if calls != 0 || q.State != "cancelled" || q.HeldMicrousd != 0 {
		t.Fatal("rotated queue dispatched")
	}
}
func TestAIConnectionHTTPSFailuresHoldAndDoNotLeak(t *testing.T) {
	for _, mode := range []string{"401", "429", "redirect", "usage", "echo", "timeout", "lock"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			connection := saveTestConnection(t, s)
			v := aiStart(t, s, prepareConnected(t, s, connection.Ref))
			var calls atomic.Int32
			started := make(chan struct{})
			release := make(chan struct{})
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "401":
					w.WriteHeader(401)
					_, _ = io.WriteString(w, testAPIKey)
				case "429":
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(429)
				case "redirect":
					w.Header().Set("Location", "https://api.openai.com/v1/responses")
					w.WriteHeader(307)
				case "usage":
					_, _ = io.WriteString(w, `{"model":"fixture-openai-v1","usage":null}`)
				case "echo":
					_, _ = io.WriteString(w, testAPIKey)
				case "timeout":
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(2 * time.Second):
					}
				case "lock":
					close(started)
					<-release
					_, _ = io.WriteString(w, `{}`)
				}
			})
			if mode == "lock" {
				go func() { <-started; s.lockAIConnection(); close(release) }()
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if e := s.advanceAgentAI(ctx, rt, true); e != nil {
				t.Fatal(e)
			}
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[1]
			b, _ := json.Marshal(v)
			if calls.Load() != 1 || q.State != "unknown" || q.HeldMicrousd == 0 || strings.Contains(string(b), testAPIKey) {
				t.Fatal("failure released/replayed/leaked", mode, q.State, calls.Load())
			}
		})
	}
}
func TestAIConnectionTLSAdmissionAndOwnerAPI(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:1", "https://api.openai.com", "https://127.0.0.1:1/redirect", "https://user@127.0.0.1:1", "https://localhost:1"} {
		if _, e := newOpenAIHTTPSFixture(u, x509.NewCertPool()); e == nil {
			t.Fatal("unsafe fixture URL", u)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS reached handler") }))
	defer server.Close()
	rt, _ := newOpenAIHTTPSFixture(server.URL, x509.NewCertPool())
	req, _ := http.NewRequestWithContext(t.Context(), "POST", "https://api.openai.com/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	if res, e := rt.RoundTrip(req); e == nil {
		res.Body.Close()
		t.Fatal("untrusted TLS accepted")
	}
	s := fixtureStore(t)
	token, e := s.NewSession(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		auth, csrf   bool
		origin, body string
		status       int
	}{
		{false, false, "http://127.0.0.1:7331", `{}`, 401},
		{true, false, "http://127.0.0.1:7331", `{}`, 403},
		{true, true, "https://foreign.invalid", `{}`, 403},
		{true, true, "http://127.0.0.1:7331", `{"action":"save","key":"fased-test-secret-never-echo","unknown":"private"}`, 400},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7331/api/v1/ai/connection", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		if tc.auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if tc.csrf {
			r.Header.Set("X-CSRF-Token", csrfToken(token))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "fased-test-secret") {
			t.Fatal("owner boundary", w.Code, w.Body.String())
		}
	}
}
func TestAIConnectionSchema6MigrationGuard(t *testing.T) {
	for _, paused := range []bool{false, true} {
		s := fixtureStore(t)
		dropEmptyHistoryFixture(t, s)
		_, _ = s.db.Exec("PRAGMA user_version=6")
		if paused {
			_, _ = s.db.Exec("UPDATE update_pause SET phase='paused'")
		}
		_ = s.Close()
		n, e := OpenStore(s.dir)
		if paused {
			if e == nil {
				n.Close()
				t.Fatal("migrated paused schema6")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		n.Close()
	}
}

func TestAIConnectionBoundTaskAndActiveCancellation(t *testing.T) {
	t.Run("scheduled", func(t *testing.T) {
		s := fixtureStore(t)
		connection := saveTestConnection(t, s)
		v := taskAIPlan(t, s, 1)
		a := v.Tasks[0].AI
		v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: a.SessionID, Name: "Connected reminder", Text: "Review fixed paper inputs", IntervalSeconds: 60, MaxRuns: 1, Limits: &a.Limits, TotalBudgetMicrousd: 30000, AuthorizationSeconds: 3600, ConnectionRef: connection.Ref})
		v = taskAIRun(t, s, taskAIAuthorize(t, s, v))
		if len(v.AIRequests) != 1 || v.AIRequests[0].ConnectionRef != connection.Ref {
			t.Fatal("task lost credential binding")
		}
		rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
				t.Error("task auth")
			}
			canonical := r.Clone(r.Context())
			target := *r.URL
			target.Scheme, target.Host = "https", "api.openai.com"
			canonical.URL = &target
			res, e := (fixtureAITransport{}).RoundTrip(canonical)
			if e != nil {
				t.Error(e)
				return
			}
			defer res.Body.Close()
			_, _ = io.Copy(w, res.Body)
		})
		if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
			t.Fatal(e)
		}
		v, _ = s.Agents(t.Context(), v.ProfileID, "")
		if v.Tasks[1].State != "completed" || len(v.Runs) != 1 || len(v.Notices) != 1 {
			t.Fatal("dependent commit", v.Tasks[1].State)
		}
	})
	for _, action := range []string{"lock", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s := fixtureStore(t)
			connection := saveTestConnection(t, s)
			v := aiStart(t, s, prepareConnected(t, s, connection.Ref))
			started := make(chan struct{})
			var cancelled atomic.Bool
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(started)
				select {
				case <-r.Context().Done():
					cancelled.Store(true)
				case <-time.After(2 * time.Second):
				}
			})
			done := make(chan error, 1)
			go func() { done <- s.advanceAgentAI(t.Context(), rt, true) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("no dispatch")
			}
			if action == "lock" {
				s.lockAIConnection()
			} else {
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: v.AIRequests[1].ID})
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("active call not cancelled")
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[1]
			if q.HeldMicrousd == 0 || (action == "lock" && q.State != "unknown") || (action == "cancel" && q.State != "cancelled") {
				t.Fatal("dispatched hold lost", q.State)
			}
			// The handler may observe the close just after client settlement.
			for deadline := time.Now().Add(time.Second); !cancelled.Load() && time.Now().Before(deadline); {
				time.Sleep(time.Millisecond)
			}
			if !cancelled.Load() {
				t.Fatal("HTTPS request did not observe cancellation")
			}
		})
	}
}
