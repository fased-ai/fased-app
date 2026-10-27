package core

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// This transport implements the official origin only. Production activation is
// deliberately absent: neither environment, CLI nor IPC can enable dispatch.
// Controlled tests exercise the same transport with a test-only TLS dialer.
type openAITransport struct {
	wire    *http.Transport
	enabled bool
}

func newOpenAITransport() *openAITransport {
	return &openAITransport{enabled: providerActivationEnabled, wire: &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 8192,
		DisableKeepAlives:      true,
		DisableCompression:     true,
	}}
}

func (t *openAITransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t == nil || !t.enabled || t.wire == nil {
		return nil, errors.New("provider execution disabled")
	}
	if r == nil || r.URL == nil || !openAIRequestAllowed(r) || r.URL.Opaque != "" || r.URL.RawPath != "" || (r.Host != "" && r.Host != "api.openai.com") {
		return nil, errors.New("provider origin refused")
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") || len(auth[0]) <= 7 || len(auth[0]) > 4096 || strings.ContainsAny(auth[0][7:], " \t\r\n") {
		return nil, errors.New("provider authentication required")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	copy := r.Clone(ctx)
	// Do not inherit cookies, routing headers, idempotency keys or ambient auth.
	copy.Header = make(http.Header)
	copy.Header.Set("Authorization", auth[0])
	copy.Header.Set("Content-Type", "application/json")
	copy.Header.Set("Accept", "application/json, text/event-stream")
	copy.GetBody = nil
	response, err := t.wire.RoundTrip(copy)
	copy.Header.Del("Authorization")
	if err != nil {
		cancel()
		return nil, errors.New("provider transport unavailable")
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		response.Body.Close()
		cancel()
		return nil, errors.New("provider redirect refused")
	}
	response.Body = &openAIResponseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type openAIResponseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *openAIResponseBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}
