//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

type onchainReadHook struct {
	io.Reader
	once bool
	hook func()
}

func TestRemoteCustodyOnchainCancellation(t *testing.T) {
	harness := newSwapHarness(t)
	s := harness.store
	id := tailnet.Identity{UserID: 1, NodeID: "owner-device", Login: "owner"}
	code, err := s.IssueRemoteClaim(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.remoteSession(t.Context(), id, code.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	s.custodyOwnerSupported, s.custodyNetwork = true, "mainnet-beta"
	proposal := harness.quote(t)
	harness.submit(t, proposal)
	harness.chain.SimulationFails = true
	if err = s.advanceOnchain(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.Agents(t.Context(), harness.profile, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Onchain.Tasks) != 1 || !current.Onchain.Recovery[0].Cancel {
		t.Fatal("expected cancellable original")
	}
	cmd := AgentCommand{Action: "swap-cancel", ProfileID: harness.profile, Revision: current.Revision, Swap: &SwapCommand{ID: current.Onchain.Tasks[0].ID}}
	body, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.instanceLocation, handler.instanceOwnerUID, handler.instanceOwnerKind = "remote", 1000, "custody"
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7331/api/v1/agents", bytes.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(custodyOnchainHeader, s.instanceID)
		w := httptest.NewRecorder()
		privateIPCHandler(handler).ServeHTTP(w, r)
		return w
	}
	if w := call(); w.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	proof, err := harness.vault.SwapReceipt(proposal.Proposal.Quote.ID)
	if err != nil || proof.State != "cancelled" || harness.chain.Submissions != 0 {
		t.Fatal("cancel lost original", err)
	}
	if w := call(); w.Code == http.StatusOK {
		t.Fatal("duplicate cancellation accepted")
	}
}

func (r *onchainReadHook) Read(p []byte) (int, error) {
	if !r.once {
		r.once = true
		r.hook()
	}
	return r.Reader.Read(p)
}

func TestRemoteCustodyOnchainScope(t *testing.T) {
	s := fixtureStore(t)
	id := tailnet.Identity{UserID: 1, NodeID: "owner-device", Login: "owner"}
	code, err := s.IssueRemoteClaim(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.remoteSession(t.Context(), id, code.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	doc, _, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	doc.Profiles[1].Persona, doc.Profiles[1].Model = "PRIVATE_INSTRUCTIONS", "PRIVATE_MODEL"
	doc.Sessions = append(doc.Sessions, AgentSession{ID: "private-session", ProfileID: "trading-analyst", Name: "PRIVATE_SESSION"})
	doc.Messages = append(doc.Messages, AgentMessage{ID: "private-message", ProfileID: "trading-analyst", Text: "PRIVATE_MESSAGE"})
	doc.Memories = append(doc.Memories, AgentMemory{ID: "private-memory", ProfileID: "trading-analyst", Text: "PRIVATE_MEMORY"})
	doc.Tasks = append(doc.Tasks, AgentTask{ID: "private-task", ProfileID: "trading-analyst", Name: "PRIVATE_TASK"})
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(data)); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation, h.instanceOwnerUID, h.instanceOwnerKind = "remote", 1000, "custody"
	s.custodyOwnerSupported, s.custodyNetwork = true, "devnet"
	call := func(private bool, method, route, instance, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7331"+route, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Fased-Custody-Onchain", instance)
		w := httptest.NewRecorder()
		if private {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w
	}
	for _, tc := range []struct{ method, route, body string }{
		{"GET", "/api/v1/agents?profile=trading-analyst", ""},
		{"POST", "/api/v1/agents", `{"action":"swap-view","profileId":"trading-analyst","swap":{}}`},
	} {
		w := call(true, tc.method, tc.route, s.instanceID, tc.body)
		if w.Code != http.StatusOK {
			t.Fatalf("owner view: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Fatal("unrelated agent data leaked", w.Body)
		}
		var projection map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &projection); err != nil {
			t.Fatal(err)
		}
		for key := range projection {
			if key != "onchain" && key != "revision" && key != "profileId" && key != "profiles" {
				t.Fatal("unexpected agent data", key)
			}
		}
	}
	for _, action := range []string{"swap-quote", "swap-review", "swap-submit", "swap-cancel", "swap-reconcile"} {
		body := `{"action":"` + action + `","profileId":"trading-analyst","swap":{}}`
		if w := call(true, "POST", "/api/v1/agents", s.instanceID, body); w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusOK {
			t.Fatalf("%s did not reach the existing validation: %d %s", action, w.Code, w.Body)
		}
	}
	for _, tc := range []struct{ method, route, body string }{
		{"GET", "/api/v1/accounts", ""},
		{"GET", "/api/v1/agents?profile=trading-analyst&memory-search=x", ""},
		{"GET", "/api/v1/agents?profile=one&profile=two", ""},
		{"POST", "/api/v1/agents", `{"action":"task-create","profileId":"trading-analyst","name":"unexpected"}`},
		{"POST", "/api/v1/agents?profile=trading-analyst", `{"action":"swap-view","swap":{}}`},
	} {
		if w := call(true, tc.method, tc.route, s.instanceID, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("scope escaped %s %s: %d %s", tc.method, tc.route, w.Code, w.Body)
		}
	}
	if w := call(true, "POST", "/api/v1/agents", s.instanceID, `{"action":"swap-view","profileId":"trading-analyst","swap":{},"extra":"refused"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected command input accepted: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		private  bool
		instance string
	}{{false, s.instanceID}, {true, "foreign"}, {true, ""}} {
		if w := call(tc.private, "GET", "/api/v1/agents", tc.instance, ""); w.Code == http.StatusOK {
			t.Fatal("foreign transport or instance admitted")
		}
	}
	for _, state := range []string{"local", "foreign-owner", "unsupported"} {
		switch state {
		case "local":
			h.instanceLocation = "local"
		case "foreign-owner":
			h.instanceLocation, h.instanceOwnerKind = "remote", "other"
		case "unsupported":
			h.instanceOwnerKind, s.custodyOwnerSupported = "custody", false
		}
		if w := call(true, "GET", "/api/v1/agents", s.instanceID, ""); w.Code == http.StatusOK {
			t.Fatal("foreign installation admitted", state)
		}
	}
	h.instanceLocation, h.instanceOwnerKind, s.custodyOwnerSupported = "remote", "custody", true
	// Revocation while the admitted body is being decoded must not convert the
	// scoped request into an unrestricted Agents command.
	r := httptest.NewRequest("POST", "http://127.0.0.1:7331/api/v1/agents", nil)
	r.Header.Set("Origin", "http://127.0.0.1:7331")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Fased-Custody-Onchain", s.instanceID)
	r.Body = io.NopCloser(&onchainReadHook{Reader: strings.NewReader(`{"action":"task-create","profileId":"trading-analyst","name":"unrelated"}`), hook: func() {
		if _, e := s.db.Exec("DELETE FROM remote_owner"); e != nil {
			t.Fatal(e)
		}
	}})
	w := httptest.NewRecorder()
	privateIPCHandler(h).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("revocation broadened request: %d %s", w.Code, w.Body)
	}
	if w := call(true, "GET", "/api/v1/agents", s.instanceID, ""); w.Code == http.StatusOK {
		t.Fatal("revoked owner admitted")
	}
}

func TestRemoteCustodyOnchainClientRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	var instance string
	for boot := 0; boot < 2; boot++ {
		server, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		s := server.Store
		if boot == 0 {
			id := tailnet.Identity{UserID: 1, NodeID: "owner-device", Login: "owner"}
			code, e := s.IssueRemoteClaim(t.Context(), id)
			if e != nil {
				t.Fatal(e)
			}
			token, e := s.remoteSession(t.Context(), id, code.Code, "")
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Logout(t.Context(), token); e != nil {
				t.Fatal(e)
			}
			instance = s.instanceID
		} else if instance != s.instanceID {
			t.Fatal("instance changed across restart")
		}
		h := server.HTTP.Handler.(*HTTPHandler)
		h.instanceLocation, h.instanceOwnerUID, h.instanceOwnerKind = "remote", 1000, "custody"
		s.custodyOwnerSupported, s.custodyNetwork = true, "devnet"
		serving := make(chan error, 1)
		go func() { serving <- server.Serve() }()
		c, err := NewClient(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.LoadSession(t.Context(), dir); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("expected no session", err)
		}
		if err = c.CustodyOnchain(t.Context()); err != nil {
			t.Fatal("private IPC admission", err)
		}
		var view AgentView
		if err = c.Do(t.Context(), "POST", "/api/v1/agents", AgentCommand{Action: "swap-view", ProfileID: "trading-analyst", Swap: &SwapCommand{}}, &view); err != nil {
			t.Fatal("private swap view", err)
		}
		if err = c.Do(t.Context(), "GET", "/api/v1/accounts", nil, nil); err == nil {
			t.Fatal("Onchain authority accessed Accounts")
		}
		if err = c.Do(t.Context(), "POST", "/api/v1/agents", AgentCommand{Action: "task-create", ProfileID: "trading-analyst"}, nil); err == nil {
			t.Fatal("Onchain authority created task")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err = c.Do(ctx, "GET", "/api/v1/agents", nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
		c.Close()
		if _, err = os.Stat(filepath.Join(dir, "cli-session.json")); !os.IsNotExist(err) {
			t.Fatal("created bearer session", err)
		}
		if err = server.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = <-serving; err != nil {
			t.Fatal(err)
		}
	}
}
