// Package localprovider binds reviewed logical requests to explicit loopback
// credentials. It has no ambient endpoint, proxy, credential or activation source.
package localprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

const Origin = "https://local.fased.invalid"

const (
	maxCredential = 2048
	maxHeaders    = 16 << 10
	maxRequest    = 1 << 20
	maxResponse   = 8 << 20
)

type Credential struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"apiKey,omitempty"`
}

func NewCredential(endpoint, key string) ([]byte, error) {
	c := Credential{Version: 1, Endpoint: endpoint, APIKey: key}
	if !validCredential(c) {
		return nil, errors.New("local credential refused")
	}
	return json.Marshal(c)
}

func ParseCredential(b []byte) (Credential, error) {
	var c Credential
	if len(b) == 0 || len(b) > maxCredential || update.StrictJSON(b, &c) != nil {
		return Credential{}, errors.New("local credential refused")
	}
	// encoding/json accepts case aliases and null strings. Neither is part of
	// this versioned credential contract; StrictJSON already excludes duplicates.
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields["version"] == nil || fields["endpoint"] == nil {
		return Credential{}, errors.New("local credential refused")
	}
	for name, value := range fields {
		if name != "version" && name != "endpoint" && name != "apiKey" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Credential{}, errors.New("local credential refused")
		}
	}
	if !validCredential(c) {
		return Credential{}, errors.New("local credential refused")
	}
	return c, nil
}

func validCredential(c Credential) bool {
	if c.Version != 1 || len(c.Endpoint) == 0 || len(c.Endpoint) > 256 || len(c.APIKey) > 256 {
		return false
	}
	for _, v := range c.APIKey {
		if v < '!' || v > '~' {
			return false
		}
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil || u.Path != "/v1" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.String() != c.Endpoint {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || (host != "127.0.0.1" && host != "::1") || u.Host != net.JoinHostPort(host, port) {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == port
}

type credentialKey struct{}

// Bind returns a request copy; it never serializes the credential into headers.
func Bind(req *http.Request, c Credential) *http.Request {
	if req == nil {
		return nil
	}
	return req.WithContext(context.WithValue(req.Context(), credentialKey{}, c))
}

// Transport requires explicit activation and a trusted Go-side wire. Ordinary
// construction must supply a fixed no-proxy http.Transport; only controlled Go
// tests may inject a fixture wire or TLS trust. Model membership is core policy.
type Transport struct {
	Enabled bool
	Wire    http.RoundTripper
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	refuse := func(message string) (*http.Response, error) {
		if req != nil && req.Body != nil {
			req.Body.Close()
		}
		return nil, errors.New(message)
	}
	if t == nil || !t.Enabled || t.Wire == nil {
		return refuse("local provider execution disabled")
	}
	if !requestAllowed(req) || !headersBounded(req.Header) || len(req.Trailer) != 0 || req.ContentLength > maxRequest {
		return refuse("local provider request refused")
	}
	if wire, ok := t.Wire.(*http.Transport); ok && (wire == nil || wire.Proxy != nil) {
		return refuse("local provider routing refused")
	}
	c, ok := req.Context().Value(credentialKey{}).(Credential)
	if !ok || !validCredential(c) {
		return refuse("local credential required")
	}
	endpoint, _ := url.Parse(c.Endpoint)
	limit := 5 * time.Second
	if req.Method == http.MethodPost {
		limit = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(req.Context(), limit)
	out := req.Clone(ctx)
	mapped := *endpoint
	mapped.Path += strings.TrimPrefix(req.URL.Path, "/v1")
	out.URL = &mapped
	out.Host = mapped.Host
	out.RequestURI = ""
	out.Header = make(http.Header)
	out.Trailer = nil
	out.TransferEncoding = nil
	out.GetBody = nil
	out.Close = true
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Accept", "application/json, text/event-stream")
	if c.APIKey != "" {
		out.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if req.Body != nil {
		out.Body = &boundedBody{body: req.Body, remaining: maxRequest, ctx: ctx}
	}
	response, err := t.Wire.RoundTrip(out)
	out.Header.Del("Authorization")
	if err != nil {
		if out.Body != nil {
			out.Body.Close()
		}
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		cancel()
		return nil, errors.New("local provider transport unavailable")
	}
	if response == nil || response.Body == nil {
		cancel()
		return nil, errors.New("local provider response refused")
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 || !headersBounded(response.Header) || !headersBounded(response.Trailer) || response.ContentLength > maxResponse {
		response.Body.Close()
		cancel()
		return nil, errors.New("local provider response refused")
	}
	// Do not return the mapped endpoint or credential-bearing request to callers.
	response.Request = req.Clone(req.Context())
	response.Request.Header = make(http.Header)
	response.Body = &boundedBody{body: response.Body, remaining: maxResponse, ctx: ctx, cancel: cancel}
	return response, nil
}

func requestAllowed(req *http.Request) bool {
	if req == nil || req.URL == nil || req.RequestURI != "" {
		return false
	}
	u := req.URL
	if u.Scheme != "https" || u.Host != "local.fased.invalid" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" {
		return false
	}
	if req.Method == http.MethodPost {
		return u.Path == "/v1/responses"
	}
	if req.Method != http.MethodGet || req.Body != nil && req.Body != http.NoBody || req.ContentLength != 0 {
		return false
	}
	if u.Path == "/v1/models" {
		return true
	}
	id, ok := strings.CutPrefix(u.Path, "/v1/models/")
	if !ok || len(id) == 0 || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, v := range id {
		if !(v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || strings.ContainsRune("-_.:", v)) {
			return false
		}
	}
	return true
}

func headersBounded(h http.Header) bool {
	n := 0
	for key, values := range h {
		n += len(key) + 2
		if n > maxHeaders {
			return false
		}
		for _, value := range values {
			n += len(key) + len(value) + 4
			if n > maxHeaders {
				return false
			}
		}
	}
	return true
}

// boundedBody streams without buffering and keeps the operation deadline alive
// until Close. Its errors never include raw wire responses or local credentials.
type boundedBody struct {
	body      io.ReadCloser
	remaining int64
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	closeErr  error
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.body.Read(p)
	if int64(n) > b.remaining {
		n = int(b.remaining)
		b.remaining = 0
		return n, errors.New("local provider body limit")
	}
	b.remaining -= int64(n)
	if err != nil && err != io.EOF {
		if contextErr := b.ctx.Err(); contextErr != nil {
			return n, contextErr
		}
		return n, errors.New("local provider body unavailable")
	}
	return n, err
}

func (b *boundedBody) Close() error {
	b.once.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}
		if b.body.Close() != nil {
			b.closeErr = errors.New("local provider body unavailable")
		}
	})
	return b.closeErr
}
