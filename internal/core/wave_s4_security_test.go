package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fased-ai/fased-app/internal/automation"
	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/mcpclient"
	"github.com/fased-ai/fased-app/internal/providersecrets"
)

type waveS4MemSecretSource struct {
	key []byte
	err error
}

func (m *waveS4MemSecretSource) Key(_ context.Context, name string, _ bool) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	if name != providersecrets.CredentialName {
		return nil, providersecrets.ErrMissing
	}
	out := make([]byte, len(m.key))
	copy(out, m.key)
	return out, nil
}

func TestWaveS4HKDFSealsAutomationAndIntegrationsKeys(t *testing.T) {
	dir := t.TempDir()
	master := bytes.Repeat([]byte{0x42}, 32)
	src := &waveS4MemSecretSource{key: master}

	// 1. Create automation service with OS secret source -> automation.key MUST be sealed on disk!
	autoSvc, err := automation.NewService(automation.Config{
		DataDir:      dir,
		FixtureMode:  true,
		SecretSource: src,
	})
	if err != nil {
		t.Fatalf("automation.NewService failed: %v", err)
	}
	if _, err := autoSvc.SaveWebhookConfig(t.Context(), automation.WebhookInput{
		Enabled:  true,
		Endpoint: "https://hooks.example.com/a",
		Secret:   "top-secret-webhook-tok",
	}); err != nil {
		t.Fatalf("SaveWebhookConfig failed: %v", err)
	}
	_ = autoSvc.Close()

	autoKeyBytes, err := os.ReadFile(filepath.Join(dir, "automation", "automation.key"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(autoKeyBytes, []byte("FASED-SEALED-KEY:v1\n")) {
		t.Fatalf("expected automation.key to be HKDF-sealed, got %d raw bytes", len(autoKeyBytes))
	}

	// Without OS KEK, reopening a sealed automation.key must FAIL CLOSED.
	if _, err := automation.NewService(automation.Config{
		DataDir:      dir,
		FixtureMode:  true,
		SecretSource: &waveS4MemSecretSource{err: providersecrets.ErrUnavailable},
	}); err == nil {
		t.Fatal("expected sealed automation.key without KEK to fail closed")
	}

	// Reopening with the same OS KEK succeeds and decrypts stored webhook secret.
	autoSvc2, err := automation.NewService(automation.Config{
		DataDir:      dir,
		FixtureMode:  true,
		SecretSource: src,
	})
	if err != nil {
		t.Fatalf("reopen automation.NewService failed: %v", err)
	}
	defer autoSvc2.Close()

	// 2. Transparent migration: write legacy 32-byte plaintext integrations.key, open mcpclient + decision with src -> migrated to sealed!
	intDir := filepath.Join(dir, "integrations")
	if err := os.MkdirAll(intDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyKey := bytes.Repeat([]byte{0x99}, 32)
	if err := os.WriteFile(filepath.Join(intDir, "integrations.key"), legacyKey, 0o600); err != nil {
		t.Fatal(err)
	}

	mcpSvc, err := mcpclient.NewService(mcpclient.Config{
		DataDir:      dir,
		FixtureMode:  true,
		SecretSource: src,
	})
	if err != nil {
		t.Fatalf("mcpclient.NewService migration failed: %v", err)
	}
	defer mcpSvc.Close()

	intKeyBytes, err := os.ReadFile(filepath.Join(intDir, "integrations.key"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(intKeyBytes, []byte("FASED-SEALED-KEY:v1\n")) {
		t.Fatalf("expected legacy integrations.key to migrate to HKDF-sealed envelope, got %d bytes", len(intKeyBytes))
	}

	// Decision service sharing integrations.key opens cleanly with the same HKDF purpose!
	decSvc, err := decision.NewService(decision.Config{
		DataDir:      dir,
		FixtureMode:  true,
		SecretSource: src,
	})
	if err != nil {
		t.Fatalf("decision.NewService failed with shared sealed integrations.key: %v", err)
	}
	defer decSvc.Close()
}

func TestWaveS4LogoutAndPasswordChangeCloseActiveWSAndSSE(t *testing.T) {
	ctx := t.Context()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := openStoreWithTaskSecrets(stateDir, fixtureTaskSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.providerFixture = true

	var ts *httptest.Server
	var handler *HTTPHandler
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer ts.Close()

	handler, err = NewHTTPHandler(store, ts.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	tok1, err := store.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := store.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Open WebSocket on tok1.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/stream"
	wsConn1, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{streamSubprotocol},
		HTTPHeader: http.Header{
			"Origin":        []string{ts.URL},
			"Authorization": []string{"Bearer " + tok1},
		},
	})
	if err != nil {
		t.Fatalf("WS dial tok1 failed: %v", err)
	}
	defer wsConn1.CloseNow()
	if _, _, err := wsConn1.Read(ctx); err != nil {
		t.Fatalf("expected initial WS ready frame: %v", err)
	}

	// 2. Open SSE (/api/v1/automation/stream) on tok1.
	sseReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/automation/stream", nil)
	sseReq.Header.Set("Authorization", "Bearer "+tok1)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil || sseResp.StatusCode != 200 {
		t.Fatalf("SSE open failed: resp=%v err=%v", sseResp, err)
	}
	defer sseResp.Body.Close()

	// 3. Logout tok1 -> both WS and SSE on tok1 MUST close promptly!
	logoutReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/auth/logout", strings.NewReader(`{}`))
	logoutReq.Header.Set("Origin", ts.URL)
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.Header.Set("Authorization", "Bearer "+tok1)
	logoutReq.Header.Set("X-CSRF-Token", csrfToken(tok1))
	logoutResp, err := http.DefaultClient.Do(logoutReq)
	if err != nil || logoutResp.StatusCode != 200 {
		t.Fatalf("logout failed: resp=%v err=%v", logoutResp, err)
	}
	logoutResp.Body.Close()

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	if _, _, err := wsConn1.Read(readCtx); err == nil {
		t.Fatal("expected WS connection to terminate immediately after logout")
	}

	sseDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, rErr := sseResp.Body.Read(buf); rErr != nil {
				close(sseDone)
				return
			}
		}
	}()
	select {
	case <-sseDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected SSE stream to terminate immediately after logout")
	}

	// 4. Open WS on tok2 and tok3, then set local owner password on tok2 -> tok3 WS MUST close while tok2 stays open!
	tok3, err := store.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wsConn3, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{streamSubprotocol},
		HTTPHeader: http.Header{
			"Origin":        []string{ts.URL},
			"Authorization": []string{"Bearer " + tok3},
		},
	})
	if err != nil {
		t.Fatalf("WS dial tok3 failed: %v", err)
	}
	defer wsConn3.CloseNow()
	if _, _, err := wsConn3.Read(ctx); err != nil {
		t.Fatalf("expected ready frame on tok3: %v", err)
	}

	pwBody, _ := json.Marshal(map[string]string{"password": "WaveS4-StrongPassword-2026!"})
	pwReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/auth/password", bytes.NewReader(pwBody))
	pwReq.Header.Set("Origin", ts.URL)
	pwReq.Header.Set("Content-Type", "application/json")
	pwReq.Header.Set("Authorization", "Bearer "+tok2)
	pwReq.Header.Set("X-CSRF-Token", csrfToken(tok2))
	pwResp, err := http.DefaultClient.Do(pwReq)
	if err != nil || pwResp.StatusCode != 200 {
		t.Fatalf("set owner password failed: resp=%v err=%v", pwResp, err)
	}
	pwResp.Body.Close()

	readCtx3, readCancel3 := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel3()
	if _, _, err := wsConn3.Read(readCtx3); err == nil {
		t.Fatal("expected WS connection of revoked passwordless session tok3 to close after SetLocalOwnerPassword")
	}
}
