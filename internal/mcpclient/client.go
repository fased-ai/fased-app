package mcpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// ParseJSONRPCResult validates a JSON-RPC 2.0 response envelope safely.
func ParseJSONRPCResult(raw []byte) (json.RawMessage, error) {
	var env rpcEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode MCP JSON-RPC response: %w", err)
	}
	if env.JSONRPC != "2.0" {
		return nil, errors.New("invalid JSON-RPC version")
	}
	if env.Error != nil {
		return nil, fmt.Errorf("MCP error (%d): %s", env.Error.Code, env.Error.Message)
	}
	return env.Result, nil
}

func dialSafeMCP(ctx context.Context, dialer *net.Dialer, network, addr string, allowLocalhost bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			if !allowLocalhost {
				return nil, fmt.Errorf("blocked loopback MCP target %s", ip)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		if !isPublicUnicastIP(ip) {
			return nil, fmt.Errorf("blocked non-public MCP IP %s", ip)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, r := range resolved {
		if r.IP.IsLoopback() {
			if !allowLocalhost {
				continue
			}
			if conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(r.IP.String(), port)); dErr == nil {
				return conn, nil
			}
			continue
		}
		if isPublicUnicastIP(r.IP) {
			if conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(r.IP.String(), port)); dErr == nil {
				return conn, nil
			}
		}
	}
	return nil, fmt.Errorf("no permitted IP resolved for MCP host %q", host)
}

func newHTTPClient(override http.RoundTripper, allowLocalhost bool) *http.Client {
	transport := override
	if transport == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSafeMCP(ctx, dialer, network, addr, allowLocalhost)
			},
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func closeHTTPClient(c *http.Client) {
	if c == nil {
		return
	}
	if tr, ok := c.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func parseSSERPCResult(raw []byte) (json.RawMessage, error) {
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	var cur []string
	var lastPayload string
	flush := func() {
		if len(cur) > 0 {
			joined := strings.TrimSpace(strings.Join(cur, "\n"))
			if joined != "" {
				lastPayload = joined
			}
			cur = nil
		}
	}
	for _, line := range lines {
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			cur = append(cur, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if strings.TrimSpace(lastPayload) == "" {
		return nil, errors.New("empty MCP SSE response")
	}
	return ParseJSONRPCResult([]byte(lastPayload))
}

func callRPC(ctx context.Context, client *http.Client, cfg ServerConfig, bearer, sessionID string, id int64, method string, params any, notification bool) (json.RawMessage, string, int, error) {
	ep, err := ValidateEndpoint(cfg.Endpoint, cfg.AllowLocalhost)
	if err != nil {
		return nil, "", 0, err
	}
	u, err := url.Parse(ep)
	if err != nil {
		return nil, "", 0, err
	}
	if !cfg.AllowLocalhost && isLoopbackHostname(u.Hostname()) {
		return nil, "", 0, errors.New("loopback target disabled")
	}

	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if strings.TrimSpace(sessionID) != "" {
		req.Header.Set("Mcp-Session-Id", strings.TrimSpace(sessionID))
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}

	if client == nil {
		client = newHTTPClient(nil, cfg.AllowLocalhost)
		defer closeHTTPClient(client)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()

	respSessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id"))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, respSessionID, resp.StatusCode, errors.New("MCP redirect blocked by security policy")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, respSessionID, resp.StatusCode, err
	}
	if len(raw) > maxResponseBytes {
		return nil, respSessionID, resp.StatusCode, errors.New("MCP response exceeded 512 KiB limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, respSessionID, resp.StatusCode, fmt.Errorf("MCP server returned HTTP %d", resp.StatusCode)
	}
	if notification && (resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(raw)) == 0) {
		return nil, respSessionID, resp.StatusCode, nil
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if mediaType, _, pErr := mime.ParseMediaType(ct); pErr == nil && mediaType == "text/event-stream" {
			res, sseErr := parseSSERPCResult(raw)
			return res, respSessionID, resp.StatusCode, sseErr
		}
	}
	res, decErr := ParseJSONRPCResult(raw)
	return res, respSessionID, resp.StatusCode, decErr
}
