package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	Endpoint Endpoint
	HTTP     *http.Client
	Token    string
	CSRF     string
}
type SavedSession struct {
	InstanceID string `json:"instanceId"`
	Token      string `json:"token"`
}

func NewClient(ctx context.Context, dir string) (*Client, error) {
	endpointPath := filepath.Join(dir, "endpoint.json")
	data, err := readPrivateFile(endpointPath)
	if err != nil {
		return nil, errors.New("core is not running; start the engine first")
	}
	var endpoint Endpoint
	if json.Unmarshal(data, &endpoint) != nil {
		return nil, errors.New("invalid core endpoint; restart the engine")
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.Scheme != "http" || !isLoopbackHost(parsed.Hostname()) || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("CLI endpoint must be a verified local core")
	}
	if err = validateIPC(dir, endpoint); err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialIPC(ctx, endpoint.Network, endpoint.Address)
	}}
	c := &Client{Endpoint: endpoint, HTTP: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirects are not allowed") }}}
	var health Health
	if err = c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); err != nil {
		c.Close()
		return nil, err
	}
	if health.InstanceID != endpoint.InstanceID || health.APIVersion != APIVersion {
		c.Close()
		return nil, errors.New("core instance or API version mismatch")
	}
	return c, nil
}
func (c *Client) Close() { c.HTTP.CloseIdleConnections() }
func (c *Client) Do(ctx context.Context, method, route string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint.URL+route, body)
	if err != nil {
		return err
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", c.Endpoint.URL)
		req.Header.Set("Content-Type", "application/json")
		if c.CSRF != "" {
			req.Header.Set("X-CSRF-Token", c.CSRF)
		}
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("cannot reach local core")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&message)
		if message.Error == "" {
			message.Error = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("core request failed (%d): %s", response.StatusCode, message.Error)
	}
	if output != nil {
		limit := int64(32 * 1024)
		if route == "/api/v1/agents" || strings.HasPrefix(route, "/api/v1/agents?") {
			limit = agentDocumentLimit + 32*1024
		}
		if err = json.NewDecoder(io.LimitReader(response.Body, limit)).Decode(output); err != nil {
			return errors.New("invalid core response")
		}
	}
	return nil
}
func (c *Client) Login(ctx context.Context, password string) error {
	return c.authenticate(ctx, "/api/v1/auth/login", map[string]string{"password": password})
}
func (c *Client) Claim(ctx context.Context, code, password string) error {
	return c.authenticate(ctx, "/api/v1/auth/claim", map[string]string{"code": code, "password": password})
}
func (c *Client) authenticate(ctx context.Context, route string, credentials map[string]string) error {
	request, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint.URL+route, bytes.NewReader(request))
	if err != nil {
		return err
	}
	req.Header.Set("Origin", c.Endpoint.URL)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("cannot reach local core")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrUnauthorized
	}
	var result struct {
		CSRFToken    string `json:"csrfToken"`
		SessionToken string `json:"sessionToken"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return err
	}
	c.Token = result.SessionToken
	if c.Token == "" || result.CSRFToken == "" {
		return errors.New("core did not create a session")
	}
	c.CSRF = result.CSRFToken
	return nil
}
func (c *Client) LoadSession(ctx context.Context, dir string) error {
	file := filepath.Join(dir, "cli-session.json")
	data, err := readPrivateFile(file)
	if err != nil {
		return errors.New("CLI login required")
	}
	var saved SavedSession
	if json.Unmarshal(data, &saved) != nil || saved.InstanceID != c.Endpoint.InstanceID {
		return errors.New("CLI login required")
	}
	c.Token = saved.Token
	var result struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrfToken"`
	}
	if err = c.Do(ctx, http.MethodGet, "/api/v1/auth/state", nil, &result); err != nil {
		return err
	}
	if !result.Authenticated {
		return errors.New("CLI session expired; log in again")
	}
	c.CSRF = result.CSRFToken
	return nil
}
func (c *Client) SaveSession(dir string) error {
	return WritePrivateJSON(filepath.Join(dir, "cli-session.json"), SavedSession{InstanceID: c.Endpoint.InstanceID, Token: c.Token})
}
