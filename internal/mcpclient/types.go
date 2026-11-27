package mcpclient

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const (
	protocolVersion  = "2025-03-26"
	maxResponseBytes = 512 * 1024 // 512 KiB.
	maxServerCount   = 32
)

var (
	idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	cgnatCIDR = mustParseCIDR("100.64.0.0/10")
)

func mustParseCIDR(raw string) *net.IPNet {
	_, parsed, err := net.ParseCIDR(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

// RemoteTool describes a discovered tool advertised by an external MCP server.
type RemoteTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	SchemaSHA256 string         `json:"schemaSha256,omitempty"`
	SchemaDrift  bool           `json:"schemaDrift,omitempty"`
}

// RemoteResource describes a discovered resource advertised by an external MCP server.
type RemoteResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ServerConfig persists an external MCP server configuration and cached capabilities.
type ServerConfig struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Endpoint       string           `json:"endpoint"`
	AllowLocalhost bool             `json:"allowLocalhost"`
	Enabled        bool             `json:"enabled"`
	ReadOnly       bool             `json:"readOnly"`
	HasAuthToken   bool             `json:"hasAuthToken"`
	AllowedTools   []string         `json:"allowedTools,omitempty"`
	LastSyncAt     int64            `json:"lastSyncAt,omitempty"`
	LastError      string           `json:"lastError,omitempty"`
	ToolCount      int              `json:"toolCount"`
	ResourceCount  int              `json:"resourceCount"`
	Tools          []RemoteTool     `json:"tools,omitempty"`
	Resources      []RemoteResource `json:"resources,omitempty"`
}

// ServerInput mutates a ServerConfig without echoing AuthToken back in reads.
type ServerInput struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Endpoint       string   `json:"endpoint"`
	AllowLocalhost bool     `json:"allowLocalhost"`
	Enabled        *bool    `json:"enabled,omitempty"`
	ReadOnly       *bool    `json:"readOnly,omitempty"`
	AuthToken      string   `json:"authToken,omitempty"`
	ClearAuthToken bool     `json:"clearAuthToken,omitempty"`
	AllowedTools   []string `json:"allowedTools,omitempty"`
}

// CallResult wraps external MCP tool output explicitly flagged as untrusted.
type CallResult struct {
	ServerID  string           `json:"serverId"`
	Tool      string           `json:"tool"`
	IsError   bool             `json:"isError"`
	Untrusted bool             `json:"untrusted"`
	Content   []map[string]any `json:"content"`
}

func isLoopbackHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "127.0.0.1" || h == "::1" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isPublicUnicastIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if cgnatCIDR.Contains(ip) || (ip[0] == 198 && (ip[1] == 18 || ip[1] == 19)) {
			return false
		}
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	return ip.IsGlobalUnicast()
}

// ValidateEndpoint enforces strict SSRF rules on external MCP server URLs.
// Non-loopback private/CGNAT/link-local IPs are always blocked; plaintext HTTP
// is permitted strictly for localhost/127.0.0.1/::1 when allowLocalhost is true.
func ValidateEndpoint(rawURL string, allowLocalhost bool) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || len(trimmed) > 1024 {
		return "", errors.New("MCP endpoint must be 1-1024 characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid MCP endpoint: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("MCP endpoint requires a hostname")
	}
	if u.User != nil {
		return "", errors.New("MCP endpoint must not embed URL credentials")
	}

	loopback := isLoopbackHostname(host)
	if loopback && !allowLocalhost {
		return "", errors.New("loopback MCP endpoint requires allowLocalhost=true")
	}
	if scheme == "http" {
		if !allowLocalhost || !loopback {
			return "", errors.New("plaintext http:// MCP endpoint requires allowLocalhost=true and loopback host")
		}
		return u.String(), nil
	}
	if scheme != "https" {
		return "", fmt.Errorf("unsupported MCP scheme %q", u.Scheme)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !loopback && !isPublicUnicastIP(ip) {
			return "", fmt.Errorf("blocked non-public MCP IP %s", ip)
		}
	}
	return u.String(), nil
}

// NormalizeServerInput validates fields of a ServerInput request.
func NormalizeServerInput(in ServerInput) (ServerInput, error) {
	id := strings.TrimSpace(in.ID)
	if !idPattern.MatchString(id) {
		return ServerInput{}, errors.New("server id must match [A-Za-z0-9._-] (1-64 chars)")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		return ServerInput{}, errors.New("server name must be 1-80 characters")
	}
	ep, err := ValidateEndpoint(in.Endpoint, in.AllowLocalhost)
	if err != nil {
		return ServerInput{}, err
	}
	enabledVal := true
	if in.Enabled != nil {
		enabledVal = *in.Enabled
	}
	readOnlyVal := true
	if in.ReadOnly != nil {
		readOnlyVal = *in.ReadOnly
	}
	toolsAllow := make([]string, 0, len(in.AllowedTools))
	for _, t := range in.AllowedTools {
		trimmed := strings.TrimSpace(t)
		if trimmed != "" && len(trimmed) <= 64 {
			toolsAllow = append(toolsAllow, trimmed)
		}
		if len(toolsAllow) >= 64 {
			break
		}
	}
	if len(strings.TrimSpace(in.AuthToken)) > 1024 {
		return ServerInput{}, errors.New("authToken exceeds 1024 characters")
	}
	return ServerInput{
		ID:             id,
		Name:           name,
		Endpoint:       ep,
		AllowLocalhost: in.AllowLocalhost,
		Enabled:        &enabledVal,
		ReadOnly:       &readOnlyVal,
		AuthToken:      strings.TrimSpace(in.AuthToken),
		ClearAuthToken: in.ClearAuthToken,
		AllowedTools:   toolsAllow,
	}, nil
}

func splitToolNameTokens(toolName string) []string {
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(strings.TrimSpace(toolName))
	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' || r == ':' || r == '/' || r == ' ' {
			flush()
			continue
		}
		// Split camelCase / PascalCase boundaries (e.g. sendTransaction -> send, transaction).
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			if prev >= 'a' && prev <= 'z' {
				flush()
			} else if prev >= 'A' && prev <= 'Z' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return tokens
}

var mcpHardMutatingTokens = map[string]bool{
	"delete": true, "remove": true, "rm": true, "drop": true, "purge": true, "destroy": true, "clear": true,
	"execute": true, "exec": true, "run": true, "eval": true, "shell": true, "bash": true, "cmd": true, "command": true,
	"sign": true, "transfer": true, "withdraw": true, "deposit": true, "pay": true, "mint": true, "burn": true, "stake": true, "unstake": true,
	"place": true, "submit": true, "cancel": true, "close": true,
	"send": true, "broadcast": true, "publish": true, "post": true, "put": true, "patch": true, "push": true, "commit": true,
	"write": true, "create": true, "update": true, "modify": true, "mutate": true, "edit": true, "set": true, "add": true, "insert": true,
	"approve": true, "revoke": true, "grant": true, "deploy": true, "install": true, "uninstall": true, "start": true, "stop": true, "kill": true,
	"webhook": true, "email": true, "sql": true,
}

var mcpTradeActionTokens = map[string]bool{
	"swap": true, "buy": true, "sell": true, "order": true, "trade": true,
}

var mcpReadOnlyLeadingTokens = map[string]bool{
	"get": true, "list": true, "read": true, "fetch": true, "quote": true, "search": true,
	"inspect": true, "describe": true, "show": true, "status": true, "check": true,
	"find": true, "query": true, "lookup": true, "ping": true, "health": true,
	"count": true, "stats": true, "view": true, "info": true, "resolve": true,
}

// isMutatingToolName implements a default-deny read-only guard over camelCase, PascalCase,
// snake_case, and kebab-case MCP tool names: any hard mutating verb blocks immediately,
// trade-action verbs block unless framed by a leading read-only verb (e.g. get_swap_quote),
// and unrecognized leading verbs fail closed under ReadOnly policy.
func isMutatingToolName(toolName string) bool {
	tokens := splitToolNameTokens(toolName)
	if len(tokens) == 0 {
		return true
	}
	for _, tok := range tokens {
		if mcpHardMutatingTokens[tok] {
			return true
		}
	}
	if mcpReadOnlyLeadingTokens[tokens[0]] {
		return false
	}
	// Fixture test helpers prefixed with "fixture_" are allowed only when explicitly read-scoped.
	if tokens[0] == "fixture" && len(tokens) >= 2 && mcpReadOnlyLeadingTokens[tokens[1]] {
		for _, tok := range tokens[1:] {
			if mcpTradeActionTokens[tok] {
				return true
			}
		}
		return false
	}
	for _, tok := range tokens {
		if mcpTradeActionTokens[tok] {
			return true
		}
	}
	return true
}
