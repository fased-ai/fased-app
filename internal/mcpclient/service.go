package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config parameterizes the External MCP Client Service.
type Config struct {
	DataDir       string
	FixtureMode   bool
	Now           func() time.Time
	HTTPTransport http.RoundTripper // Tests only.
}

// Service manages external MCP server definitions, encrypted Bearer tokens,
// schema sync, and read/allowlist-enforced tool invocation.
type Service struct {
	store         *store
	fixtureMode   bool
	now           func() time.Time
	httpTransport http.RoundTripper
}

// NewService opens isolated <dataDir>/integrations/mcpclient.sqlite storage.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	st, err := openStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Service{
		store:         st,
		fixtureMode:   cfg.FixtureMode,
		now:           nowFn,
		httpTransport: cfg.HTTPTransport,
	}, nil
}

// Close closes SQLite handles and zeroes cached secret key bytes.
func (s *Service) Close() error {
	return s.store.close()
}

// ListServers lists saved external MCP server configurations and discovered tools.
func (s *Service) ListServers(ctx context.Context) ([]ServerConfig, error) {
	return s.store.listServers(ctx)
}

// SaveServer creates or updates a validated external MCP configuration.
func (s *Service) SaveServer(ctx context.Context, in ServerInput) (ServerConfig, error) {
	return s.store.saveServer(ctx, in)
}

// DeleteServer removes a configured external MCP server by ID.
func (s *Service) DeleteServer(ctx context.Context, id string) (bool, error) {
	return s.store.deleteServer(ctx, id)
}

func isFixtureEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".invalid") || host == "fixture.local"
}

// SyncServer executes initialize + notifications/initialized + tools/list + resources/list
// against the configured MCP endpoint and persists discovered schemas.
func (s *Service) SyncServer(ctx context.Context, id string) (ServerConfig, error) {
	cfg, bearer, err := s.store.getServer(ctx, id)
	if err != nil {
		return ServerConfig{}, err
	}
	nowSec := s.now().UTC().Unix()

	if s.fixtureMode && (isFixtureEndpoint(cfg.Endpoint) || (!cfg.AllowLocalhost && s.httpTransport == nil)) {
		tools := []RemoteTool{
			{
				Name:        "fixture_quote_lookup",
				Description: "Fixture external MCP tool (offline preview)",
				InputSchema: map[string]any{"type": "object"},
			},
		}
		resources := []RemoteResource{
			{
				URI:      "fixture://external/status",
				Name:     "Fixture external status",
				MimeType: "application/json",
			},
		}
		return s.store.updateCapabilities(ctx, id, tools, resources, nowSec, "")
	}

	if _, err := callRPC(ctx, s.httpTransport, cfg, bearer, 1, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "fased-mcp-client", "version": "1.0"},
	}, false); err != nil {
		_, _ = s.store.updateCapabilities(ctx, id, cfg.Tools, cfg.Resources, nowSec, err.Error())
		return ServerConfig{}, err
	}
	_, _ = callRPC(ctx, s.httpTransport, cfg, bearer, 0, "notifications/initialized", nil, true)

	toolsRaw, err := callRPC(ctx, s.httpTransport, cfg, bearer, 2, "tools/list", nil, false)
	if err != nil {
		_, _ = s.store.updateCapabilities(ctx, id, cfg.Tools, cfg.Resources, nowSec, err.Error())
		return ServerConfig{}, err
	}
	var toolsResp struct {
		Tools []RemoteTool `json:"tools"`
	}
	if err := json.Unmarshal(toolsRaw, &toolsResp); err != nil {
		return ServerConfig{}, fmt.Errorf("decode tools/list: %w", err)
	}

	var resourcesResp struct {
		Resources []RemoteResource `json:"resources"`
	}
	if resRaw, rErr := callRPC(ctx, s.httpTransport, cfg, bearer, 3, "resources/list", nil, false); rErr == nil {
		_ = json.Unmarshal(resRaw, &resourcesResp)
	}

	return s.store.updateCapabilities(ctx, id, toolsResp.Tools, resourcesResp.Resources, nowSec, "")
}

// CallTool invokes a named tool on a configured external MCP server after enforcing
// enabled state, read-only guard, and optional tool allowlist.
func (s *Service) CallTool(ctx context.Context, serverID, toolName string, rawArgs json.RawMessage) (CallResult, error) {
	cfg, bearer, err := s.store.getServer(ctx, serverID)
	if err != nil {
		return CallResult{}, err
	}
	if !cfg.Enabled {
		return CallResult{}, fmt.Errorf("external MCP server %q is disabled", serverID)
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return CallResult{}, errors.New("toolName is required")
	}
	if cfg.ReadOnly && isMutatingToolName(name) {
		return CallResult{}, fmt.Errorf("tool %q blocked by read-only MCP client policy", name)
	}
	if len(cfg.AllowedTools) > 0 {
		allowed := false
		for _, a := range cfg.AllowedTools {
			if a == name {
				allowed = true
				break
			}
		}
		if !allowed {
			return CallResult{}, fmt.Errorf("tool %q not in allowedTools for server %q", name, serverID)
		}
	}

	if s.fixtureMode && (isFixtureEndpoint(cfg.Endpoint) || (!cfg.AllowLocalhost && s.httpTransport == nil)) {
		return CallResult{
			ServerID:  serverID,
			Tool:      name,
			IsError:   false,
			Untrusted: true,
			Content: []map[string]any{
				{"type": "text", "text": fmt.Sprintf(`{"fixture":true,"serverId":%q,"tool":%q}`, serverID, name)},
			},
		}, nil
	}

	var parsedArgs any = map[string]any{}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &parsedArgs); err != nil {
			return CallResult{}, fmt.Errorf("invalid tool arguments JSON: %w", err)
		}
	}

	resRaw, err := callRPC(ctx, s.httpTransport, cfg, bearer, 10, "tools/call", map[string]any{
		"name":      name,
		"arguments": parsedArgs,
	}, false)
	if err != nil {
		return CallResult{}, err
	}
	var rpcRes struct {
		IsError bool             `json:"isError"`
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(resRaw, &rpcRes); err != nil {
		return CallResult{}, fmt.Errorf("decode tools/call result: %w", err)
	}
	return CallResult{
		ServerID:  serverID,
		Tool:      name,
		IsError:   rpcRes.IsError,
		Untrusted: true,
		Content:   rpcRes.Content,
	}, nil
}
