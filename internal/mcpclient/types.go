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
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
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
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
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
		if ip.IsLoopback() {
			if !allowLocalhost {
				return "", errors.New("loopback MCP endpoint requires allowLocalhost=true")
			}
			return u.String(), nil
		}
		if !isPublicUnicastIP(ip) {
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

func isMutatingToolName(toolName string) bool {
	low := strings.ToLower(strings.TrimSpace(toolName))
	for _, prefix := range []string{
		"delete_", "execute_", "sign_", "transfer_", "withdraw_",
		"submit_order", "place_order", "send_tx", "broadcast_", "write_", "mutate_",
	} {
		if strings.HasPrefix(low, prefix) || strings.Contains(low, prefix) {
			return true
		}
	}
	return false
}
