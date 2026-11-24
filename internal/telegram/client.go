package telegram

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/news"
)

const (
	maxAPIResponseBytes = 1 << 20 // 1 MiB.
	maxOutboundPerMin   = 20
)

// redactToken replaces every instance of a Telegram bot token with [REDACTED].
func redactToken(msg, token string) string {
	if msg == "" {
		return ""
	}
	trimmed := strings.TrimSpace(token)
	if trimmed != "" {
		msg = strings.ReplaceAll(msg, trimmed, "[REDACTED]")
	}
	return msg
}

func dialSafeTelegram(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
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

type sendRateLimiter struct {
	mu    sync.Mutex
	sends []time.Time
}

func (l *sendRateLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-60 * time.Second)
	kept := l.sends[:0]
	for _, ts := range l.sends {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	l.sends = kept
	if len(l.sends) >= maxOutboundPerMin {
		return false
	}
	l.sends = append(l.sends, now)
	return true
}

type botClient struct {
	httpClient *http.Client
	limiter    sendRateLimiter
	now        func() time.Time
}

func newBotClient(override http.RoundTripper, nowFn func() time.Time) *botClient {
	if nowFn == nil {
		nowFn = time.Now
	}
	transport := override
	if transport == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		transport = &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSafeTelegram(ctx, dialer, network, addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 35 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	return &botClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   35 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: nowFn,
	}
}

type apiEnvelope struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// ParseGetUpdatesResponse parses and validates a Bot API getUpdates JSON envelope.
func ParseGetUpdatesResponse(raw []byte) ([]Update, error) {
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode telegram response: %w", err)
	}
	if !env.OK {
		desc := strings.TrimSpace(env.Description)
		if desc == "" {
			desc = "telegram API rejected request"
		}
		return nil, errors.New(desc)
	}
	if len(env.Result) == 0 || string(bytes.TrimSpace(env.Result)) == "null" {
		return nil, nil
	}
	var updates []Update
	if err := json.Unmarshal(env.Result, &updates); err != nil {
		return nil, fmt.Errorf("decode telegram updates: %w", err)
	}
	return updates, nil
}

// ParseUpdateJSON parses a single Telegram Update payload cleanly.
func ParseUpdateJSON(raw []byte) (Update, error) {
	var u Update
	if err := json.Unmarshal(raw, &u); err != nil {
		return Update{}, fmt.Errorf("decode telegram update: %w", err)
	}
	return u, nil
}

func (c *botClient) postMethod(ctx context.Context, token, method string, payload any) ([]byte, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram bot token not configured")
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/bot%s/%s", TelegramAPIBase, token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, errors.New(redactToken(err.Error(), token))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.New(redactToken(err.Error(), token))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, errors.New("telegram redirect blocked")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
	if err != nil {
		return nil, errors.New(redactToken(err.Error(), token))
	}
	if len(raw) > maxAPIResponseBytes {
		return nil, errors.New("telegram response exceeded 1 MiB bound")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram returned HTTP %d", resp.StatusCode)
	}
	return raw, nil
}

func (c *botClient) getUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]Update, error) {
	if timeoutSec < 0 || timeoutSec > 25 {
		timeoutSec = 10
	}
	raw, err := c.postMethod(ctx, token, "getUpdates", map[string]any{
		"offset":          offset,
		"limit":           25,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "callback_query"},
	})
	if err != nil {
		return nil, err
	}
	return ParseGetUpdatesResponse(raw)
}

func (c *botClient) sendMessage(ctx context.Context, token string, chatID int64, text string, buttons [][]InlineKeyboardButton) error {
	if !c.limiter.allow(c.now()) {
		return errors.New("telegram outbound rate limit reached (20/min)")
	}
	if len(text) > maxReplyBytes {
		text = text[:maxReplyBytes] + "\n..."
	}
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if len(buttons) > 0 {
		payload["reply_markup"] = InlineKeyboardMarkup{InlineKeyboard: buttons}
	}
	raw, err := c.postMethod(ctx, token, "sendMessage", payload)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if json.Unmarshal(raw, &env) == nil && !env.OK {
		desc := strings.TrimSpace(env.Description)
		if desc == "" {
			desc = "telegram sendMessage rejected"
		}
		return errors.New(desc)
	}
	return nil
}

func (c *botClient) answerCallbackQuery(ctx context.Context, token, callbackID, text string) error {
	if strings.TrimSpace(callbackID) == "" {
		return nil
	}
	_, err := c.postMethod(ctx, token, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	})
	return err
}
