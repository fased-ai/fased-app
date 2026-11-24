package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

func TestValidateEndpointSSRF(t *testing.T) {
	t.Parallel()

	badCases := []struct {
		url            string
		allowLocalhost bool
	}{
		{"", false},
		{"file:///etc/passwd", true},
		{"ftp://example.com/mcp", false},
		{"https://user:pass@example.com/mcp", false},
		{"http://example.com/mcp", false},
		{"http://example.com/mcp", true},
		{"http://127.0.0.1:8080/mcp", false},
		{"https://10.0.0.5/mcp", true},
		{"https://172.16.0.10/mcp", true},
		{"https://192.168.1.50/mcp", true},
		{"https://100.64.0.1/mcp", true},
		{"https://169.254.169.254/latest/meta-data", true},
	}
	for _, tc := range badCases {
		if _, err := ValidateEndpoint(tc.url, tc.allowLocalhost); err == nil {
			t.Fatalf("expected SSRF/validation rejection for %q (allowLocalhost=%v)", tc.url, tc.allowLocalhost)
		}
	}

	goodCases := []struct {
		url            string
		allowLocalhost bool
	}{
		{"https://mcp.example.com/v1", false},
		{"https://1.1.1.1/mcp", false},
		{"http://127.0.0.1:9999/mcp", true},
		{"http://localhost:9999/mcp", true},
		{"https://localhost:8443/mcp", true},
	}
	for _, tc := range goodCases {
		if _, err := ValidateEndpoint(tc.url, tc.allowLocalhost); err != nil {
			t.Fatalf("unexpected error for %q (allowLocalhost=%v): %v", tc.url, tc.allowLocalhost, err)
		}
	}
}

func TestBearerSealingAndClearOnDisk(t *testing.T) {
	t.Setenv("FASED_FAST_TEST_KDF", "1")
	dir := t.TempDir()
	svc, err := NewService(Config{DataDir: dir})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer func() { _ = svc.Close() }()

	secret := "mcp-secret-token-xyz-987654321"
	saved, err := svc.SaveServer(context.Background(), ServerInput{
		ID:        "alpha",
		Name:      "Alpha MCP",
		Endpoint:  "https://alpha.example.com/mcp",
		AuthToken: secret,
	})
	if err != nil {
		t.Fatalf("SaveServer: %v", err)
	}
	if !saved.HasAuthToken {
		t.Fatalf("expected HasAuthToken=true, got %+v", saved)
	}
	rawJSON, _ := json.Marshal(saved)
	if strings.Contains(string(rawJSON), secret) {
		t.Fatalf("secret leaked in ServerConfig JSON")
	}

	dbBytes, err := os.ReadFile(filepath.Join(dir, "integrations", "mcpclient.sqlite"))
	if err != nil {
		t.Fatalf("read sqlite: %v", err)
	}
	if bytes.Contains(dbBytes, []byte(secret)) {
		t.Fatalf("plaintext secret leaked into mcpclient.sqlite")
	}

	cleared, err := svc.SaveServer(context.Background(), ServerInput{
		ID:             "alpha",
		Name:           "Alpha MCP",
		Endpoint:       "https://alpha.example.com/mcp",
		ClearAuthToken: true,
	})
	if err != nil {
		t.Fatalf("SaveServer clear: %v", err)
	}
	if cleared.HasAuthToken {
		t.Fatalf("expected HasAuthToken=false after ClearAuthToken")
	}
}

func TestSyncAndCallToolPolicies(t *testing.T) {
	t.Setenv("FASED_FAST_TEST_KDF", "1")
	dir := t.TempDir()

	var seenMethods []string
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		seenMethods = append(seenMethods, req.Method)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"quote_lookup","description":"Read quote"},{"name":"submit_order","description":"Write order"}]}}`))
		case "resources/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":3,"result":{"resources":[{"uri":"mcp://alpha/feed","name":"Alpha Feed","mimeType":"application/json"}]}}`))
		case "tools/call":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":10,"result":{"isError":false,"content":[{"type":"text","text":"{\"price\":42.5}"}]}}`))
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	svc, err := NewService(Config{
		DataDir: dir,
		Now:     func() time.Time { return time.Unix(1730000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer func() { _ = svc.Close() }()

	_, err = svc.SaveServer(context.Background(), ServerInput{
		ID:             "local-alpha",
		Name:           "Local Alpha",
		Endpoint:       srv.URL,
		AllowLocalhost: true,
		AuthToken:      "bearer-local-1",
		AllowedTools:   []string{"quote_lookup", "submit_order"},
	})
	if err != nil {
		t.Fatalf("SaveServer: %v", err)
	}

	synced, err := svc.SyncServer(context.Background(), "local-alpha")
	if err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if sawAuth != "Bearer bearer-local-1" {
		t.Fatalf("expected bearer header, got %q", sawAuth)
	}
	if synced.ToolCount != 2 || synced.ResourceCount != 1 || synced.LastSyncAt != 1730000000 {
		t.Fatalf("unexpected synced metadata: %+v", synced)
	}
	if strings.Join(seenMethods, ",") != "initialize,notifications/initialized,tools/list,resources/list" {
		t.Fatalf("unexpected RPC sequence: %v", seenMethods)
	}

	// ReadOnly guard blocks mutating names.
	if _, err := svc.CallTool(context.Background(), "local-alpha", "submit_order", nil); err == nil {
		t.Fatalf("expected read-only guard to block submit_order")
	}
	// AllowedTools blocks unlisted names even if read-like.
	if _, err := svc.CallTool(context.Background(), "local-alpha", "other_read_tool", nil); err == nil {
		t.Fatalf("expected allowlist to block unlisted tool")
	}

	// Permitted read tool succeeds and marks output Untrusted: true.
	callRes, err := svc.CallTool(context.Background(), "local-alpha", "quote_lookup", json.RawMessage(`{"symbol":"SOL"}`))
	if err != nil {
		t.Fatalf("CallTool permitted: %v", err)
	}
	if !callRes.Untrusted || callRes.IsError || callRes.Tool != "quote_lookup" || len(callRes.Content) != 1 {
		t.Fatalf("unexpected CallResult: %+v", callRes)
	}

	// Disabled server rejects tool calls.
	disabled := false
	if _, err := svc.SaveServer(context.Background(), ServerInput{
		ID:             "local-alpha",
		Name:           "Local Alpha",
		Endpoint:       srv.URL,
		AllowLocalhost: true,
		Enabled:        &disabled,
	}); err != nil {
		t.Fatalf("disable SaveServer: %v", err)
	}
	if _, err := svc.CallTool(context.Background(), "local-alpha", "quote_lookup", nil); err == nil {
		t.Fatalf("expected disabled server error")
	}

	deleted, err := svc.DeleteServer(context.Background(), "local-alpha")
	if err != nil || !deleted {
		t.Fatalf("DeleteServer failed: deleted=%v err=%v", deleted, err)
	}
}

func TestFixtureModeZeroNetwork(t *testing.T) {
	t.Setenv("FASED_FAST_TEST_KDF", "1")
	dir := t.TempDir()
	called := false
	svc, err := NewService(Config{
		DataDir:     dir,
		FixtureMode: true,
		HTTPTransport: rtFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return jsonResponse(http.StatusInternalServerError, map[string]string{"error": "must not hit network"}), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.SaveServer(context.Background(), ServerInput{
		ID:       "fixture-srv",
		Name:     "Fixture Server",
		Endpoint: "https://fixture.mcp.invalid/v1",
	}); err != nil {
		t.Fatalf("SaveServer: %v", err)
	}
	synced, err := svc.SyncServer(context.Background(), "fixture-srv")
	if err != nil {
		t.Fatalf("SyncServer fixture: %v", err)
	}
	if synced.ToolCount != 1 || synced.ResourceCount != 1 {
		t.Fatalf("unexpected fixture sync result: %+v", synced)
	}
	res, err := svc.CallTool(context.Background(), "fixture-srv", "fixture_quote_lookup", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool fixture: %v", err)
	}
	if !res.Untrusted || called {
		t.Fatalf("expected untrusted fixture result with zero network calls, called=%v res=%+v", called, res)
	}
}
