package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

type fakeRemoteSignInServer struct {
	mu           sync.Mutex
	signInURL    string
	signInID     string
	completeSeen core.AIConnectionCommand
	cancelSeen   core.AIConnectionCommand
	cancelCount  int
	startCalls   int
}

func (f *fakeRemoteSignInServer) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ai/connection", func(w http.ResponseWriter, r *http.Request) {
		var cmd core.AIConnectionCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch cmd.Action {
		case "sign-in-start":
			f.startCalls++
			_ = json.NewEncoder(w).Encode(core.AIConnectionView{
				OSVaultAvailable: true,
				SignIn: &core.ProviderSignInView{
					ID:       f.signInID,
					Provider: cmd.Provider,
					State:    "waiting",
					URL:      f.signInURL,
					Remote:   true,
				},
			})
		case "sign-in-complete":
			f.completeSeen = cmd
			_ = json.NewEncoder(w).Encode(core.AIConnectionView{
				Ref:              "conn-remote-1",
				Provider:         "openai",
				Name:             "Remote OpenAI",
				AuthMethod:       "sign-in",
				CredentialMode:   "openai-signin",
				OSVaultAvailable: true,
			})
		case "sign-in-cancel":
			f.cancelCount++
			f.cancelSeen = cmd
			_ = json.NewEncoder(w).Encode(core.AIConnectionView{OSVaultAvailable: true})
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	})
	return mux
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func newTestRemoteCoreClient(t *testing.T, srv *httptest.Server) *core.Client {
	t.Helper()
	return &core.Client{
		Endpoint: core.Endpoint{URL: srv.URL, InstanceID: "remote-instance-test-0123456789"},
		HTTP:     srv.Client(),
		CSRF:     "test-csrf-token",
	}
}

func TestRemoteSignInBridgeOpenAIAndGoogleHappyPath(t *testing.T) {
	for _, tc := range []struct {
		name          string
		provider      string
		googleProject string
	}{
		{name: "openai", provider: "openai"},
		{name: "google", provider: "google", googleProject: "my-gcp-project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := freeLoopbackAddr(t)
			redirectURI := "http://" + addr + "/auth/callback"
			authURL := "https://auth.example.com/authorize?" + url.Values{
				"client_id":    {"cid"},
				"redirect_uri": {redirectURI},
				"state":        {"xyz"},
			}.Encode()

			srvState := &fakeRemoteSignInServer{
				signInID:  "signin-" + tc.provider,
				signInURL: authURL,
			}
			srv := httptest.NewServer(srvState.handler(t))
			defer srv.Close()

			origRemote := aiConnectionRemoteClient
			origBrowser := aiConnectionOpenBrowser
			t.Cleanup(func() {
				aiConnectionRemoteClient = origRemote
				aiConnectionOpenBrowser = origBrowser
			})
			aiConnectionRemoteClient = func(context.Context, string) (*core.Client, error) {
				return newTestRemoteCoreClient(t, srv), nil
			}

			browserURLCh := make(chan string, 1)
			aiConnectionOpenBrowser = func(u string) error {
				browserURLCh <- u
				return nil
			}

			var stdout, stderr bytes.Buffer
			doneCh := make(chan error, 1)
			go func() {
				args := []string{"--data-dir", t.TempDir(), "--remote", "--sign-in", tc.provider, "--name", "My Remote"}
				if tc.googleProject != "" {
					args = append(args, "--google-project", tc.googleProject)
				}
				doneCh <- aiConnectionCommand(args, nil, &stdout, &stderr)
			}()

			var opened string
			select {
			case opened = <-browserURLCh:
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for local browser URL")
			}
			if opened != authURL {
				t.Fatalf("unexpected opened URL: %s", opened)
			}

			// Simulate OAuth callback hitting local loopback bridge
			cbReqURL := redirectURI + "?code=abc&state=xyz&client_id=cid"
			resp, err := http.Get(cbReqURL)
			if err != nil {
				t.Fatalf("callback GET failed: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 OK from bridge callback, got %d", resp.StatusCode)
			}

			select {
			case err := <-doneCh:
				if err != nil {
					t.Fatalf("aiConnectionCommand failed: %v (stderr=%s)", err, stderr.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for aiConnectionCommand return")
			}

			srvState.mu.Lock()
			gotComplete := srvState.completeSeen
			srvState.mu.Unlock()
			if gotComplete.Action != "sign-in-complete" || gotComplete.SignInID != "signin-"+tc.provider || gotComplete.Callback != cbReqURL {
				t.Fatalf("unexpected sign-in-complete payload: %+v", gotComplete)
			}
			var outView core.AIConnectionView
			if err := json.Unmarshal(stdout.Bytes(), &outView); err != nil || outView.Ref != "conn-remote-1" {
				t.Fatalf("unexpected CLI JSON output: %q (%v)", stdout.String(), err)
			}

			// Verify local loopback listener closed after completion
			ln, err := net.Listen("tcp4", addr)
			if err != nil {
				t.Fatalf("expected loopback port %s released after bridge completion: %v", addr, err)
			}
			_ = ln.Close()
		})
	}
}

func TestRemoteSignInBridgeRejectsNonLoopbackAndCancelsOnTimeout(t *testing.T) {
	// 1. Non-loopback redirect_uri security rejection
	srvState := &fakeRemoteSignInServer{
		signInID:  "signin-bad",
		signInURL: "https://auth.example.com/authorize?redirect_uri=" + url.QueryEscape("https://evil.example/auth/callback"),
	}
	srv := httptest.NewServer(srvState.handler(t))
	defer srv.Close()

	client := newTestRemoteCoreClient(t, srv)
	var out, errOut bytes.Buffer
	err := runRemoteSignInBridge(t.Context(), client, remoteSignInBridgeOpts{
		provider:  "openai",
		noBrowser: true,
	}, nil, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected non-loopback redirect rejection, got: %v", err)
	}
	srvState.mu.Lock()
	cancels := srvState.cancelCount
	cancelID := srvState.cancelSeen.SignInID
	srvState.mu.Unlock()
	if cancels != 1 || cancelID != "signin-bad" {
		t.Fatalf("expected sign-in-cancel for signin-bad, got count=%d id=%q", cancels, cancelID)
	}

	// 2. Context cancellation sends sign-in-cancel cleanly
	addr := freeLoopbackAddr(t)
	srvTimeoutState := &fakeRemoteSignInServer{
		signInID:  "signin-timeout",
		signInURL: "https://auth.example.com/authorize?redirect_uri=" + url.QueryEscape("http://"+addr+"/auth/callback"),
	}
	srv2 := httptest.NewServer(srvTimeoutState.handler(t))
	defer srv2.Close()

	client2 := newTestRemoteCoreClient(t, srv2)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	err = runRemoteSignInBridge(ctx, client2, remoteSignInBridgeOpts{
		provider:  "openai",
		noBrowser: true,
	}, nil, &out, &errOut)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got: %v", err)
	}
	srvTimeoutState.mu.Lock()
	c2 := srvTimeoutState.cancelCount
	id2 := srvTimeoutState.cancelSeen.SignInID
	srvTimeoutState.mu.Unlock()
	if c2 != 1 || id2 != "signin-timeout" {
		t.Fatalf("expected sign-in-cancel on timeout, got count=%d id=%q", c2, id2)
	}
}
