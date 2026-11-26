package automation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/news"
)

const (
	webhookConfigKey = "webhook_config"
	webhookSecretKey = "webhook_secret"
)

type persistedWebhookConfig struct {
	Enabled    bool   `json:"enabled"`
	Endpoint   string `json:"endpoint"`
	LastStatus string `json:"lastStatus,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	LastSentAt int64  `json:"lastSentAt,omitempty"`
}

func dialSafePublic(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if parsed, err := netip.ParseAddr(host); err == nil {
		a := parsed.Unmap()
		if !news.PublicIP(a) {
			return nil, fmt.Errorf("blocked non-public IP %s", a)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host %q: %w", host, err)
	}
	var lastErr error
	for _, r := range resolved {
		parsed, ok := netip.AddrFromSlice(r.IP)
		if !ok {
			continue
		}
		a := parsed.Unmap()
		if !news.PublicIP(a) {
			continue
		}
		conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no public unicast IP resolved for host %q", host)
}

func newWebhookHTTPClient(override http.RoundTripper) *http.Client {
	transport := override
	if transport == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		transport = &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSafePublic(ctx, dialer, network, addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func signWebhookPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) deliverWebhook(ctx context.Context, n Notification) string {
	var cfg persistedWebhookConfig
	found, err := s.GetJSONValue(ctx, webhookConfigKey, &cfg)
	if err != nil || !found || !cfg.Enabled || strings.TrimSpace(cfg.Endpoint) == "" {
		return ""
	}
	_, status, _ := s.sendWebhookPayload(ctx, cfg, map[string]any{
		"source":       "fased-automation",
		"notification": n,
	})
	return status
}

// SendWebhookTest dispatches (or simulates in FixtureMode) a signed test webhook event.
func (s *Service) SendWebhookTest(ctx context.Context) (WebhookConfig, error) {
	var cfg persistedWebhookConfig
	found, err := s.GetJSONValue(ctx, webhookConfigKey, &cfg)
	if err != nil || !found || strings.TrimSpace(cfg.Endpoint) == "" {
		return WebhookConfig{}, errors.New("save a webhook HTTPS URL first")
	}
	_, _, sendErr := s.sendWebhookPayload(ctx, cfg, map[string]any{
		"source":    "fased-automation",
		"event":     "fased.webhook.test",
		"timestamp": s.now().UTC().Unix(),
		"message":   "Fased webhook test delivery",
	})
	view, viewErr := s.GetWebhookConfig(ctx)
	if sendErr != nil {
		return view, sendErr
	}
	return view, viewErr
}

func (s *Service) sendWebhookPayload(ctx context.Context, cfg persistedWebhookConfig, payload any) (persistedWebhookConfig, string, error) {
	endpoint, err := ValidateWebhookEndpoint(cfg.Endpoint)
	if err != nil {
		cfg.LastStatus = "invalid-endpoint"
		cfg.LastError = err.Error()
		_ = s.SetJSONValue(ctx, webhookConfigKey, cfg)
		return cfg, "webhook-error", err
	}

	nowSec := s.now().UTC().Unix()
	if s.fixtureMode {
		cfg.LastStatus = "fixture-delivered"
		cfg.LastError = ""
		cfg.LastSentAt = nowSec
		_ = s.SetJSONValue(ctx, webhookConfigKey, cfg)
		return cfg, "fixture-delivered", nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return cfg, "webhook-error", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return cfg, "webhook-error", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "fased-automation/1.0")
	if secret, ok, sErr := s.GetSealedValue(ctx, webhookSecretKey); sErr == nil && ok && secret != "" {
		req.Header.Set("X-Fased-Signature-256", signWebhookPayload(secret, payloadBytes))
	}

	resp, err := s.webhookClient.Do(req)
	if err != nil {
		cfg.LastStatus = "error"
		cfg.LastError = err.Error()
		cfg.LastSentAt = nowSec
		_ = s.SetJSONValue(ctx, webhookConfigKey, cfg)
		return cfg, "webhook-error", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxWebhookResponseBytes+1))

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		redirectErr := errors.New("webhook upstream redirect blocked")
		cfg.LastStatus = "error"
		cfg.LastError = redirectErr.Error()
		cfg.LastSentAt = nowSec
		_ = s.SetJSONValue(ctx, webhookConfigKey, cfg)
		return cfg, "webhook-error", redirectErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		httpErr := fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
		cfg.LastStatus = fmt.Sprintf("http-%d", resp.StatusCode)
		cfg.LastError = httpErr.Error()
		cfg.LastSentAt = nowSec
		_ = s.SetJSONValue(ctx, webhookConfigKey, cfg)
		return cfg, "webhook-error", httpErr
	}

	cfg.LastStatus = "delivered"
	cfg.LastError = ""
	cfg.LastSentAt = nowSec
	_ = s.SetJSONValue(ctx, webhookConfigKey, cfg)
	return cfg, "webhook-delivered", nil
}
