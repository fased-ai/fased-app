package localprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type wireFunc func(*http.Request) (*http.Response, error)

func (f wireFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func request(t *testing.T, method, path string, c Credential) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, Origin+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Bind(r, c)
}

func credential(t *testing.T, endpoint, key string) Credential {
	t.Helper()
	b, err := NewCredential(endpoint, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCredential(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCredentialStrict(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:11434/v1", "https://[::1]:443/v1"} {
		c := credential(t, endpoint, "fixture-key")
		if c.Version != 1 || c.Endpoint != endpoint || c.APIKey != "fixture-key" {
			t.Fatal("credential round trip changed fields")
		}
		credential(t, endpoint, "")
	}
	for _, endpoint := range []string{
		"", "http://localhost:11434/v1", "http://127.0.0.2:11434/v1", "http://0.0.0.0:11434/v1",
		"http://[::ffff:127.0.0.1]:11434/v1", "http://[0:0:0:0:0:0:0:1]:11434/v1", "http://2130706433:11434/v1",
		"http://127.0.0.1/v1", "http://127.0.0.1:0/v1", "http://127.0.0.1:65536/v1", "http://127.0.0.1:011434/v1",
		"http://127.0.0.1:+123/v1", "http://127.0.0.1:/v1", "HTTP://127.0.0.1:123/v1", "ftp://127.0.0.1:123/v1",
		"http://user:secret@127.0.0.1:123/v1", "http://127.0.0.1:123/v1/", "http://127.0.0.1:123/v1?",
		"http://127.0.0.1:123/v1?q=x", "http://127.0.0.1:123/v1#", "http://127.0.0.1:123/v1#x", "http://127.0.0.1:123/%761",
		"http://127.0.0.1:123/a/../v1", "http://127.0.0.1:123/" + strings.Repeat("v", 257),
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewCredential(endpoint, ""); err == nil {
				t.Fatal("noncanonical endpoint admitted")
			}
		})
	}
	for _, key := range []string{" ", "x y", "x\ty", "x\ny", "x\ry", "\x00", "\x7f", "é", strings.Repeat("x", 257)} {
		if _, err := NewCredential("http://127.0.0.1:1/v1", key); err == nil {
			t.Fatal("invalid key admitted")
		}
	}
	credential(t, "http://127.0.0.1:1/v1", strings.Repeat("!", 256))
	for _, b := range []string{
		``, `null`, `[]`, `{}`, `{"version":2,"endpoint":"http://127.0.0.1:1/v1"}`,
		`{"version":1,"endpoint":"http://127.0.0.1:1/v1","extra":true}`,
		`{"version":1,"endpoint":"http://127.0.0.1:1/v1","endpoint":"http://127.0.0.1:2/v1"}`,
		`{"version":1,"endpoint":"http://127.0.0.1:1/v1","end\u0070oint":"http://127.0.0.1:2/v1"}`,
		`{"Version":1,"endpoint":"http://127.0.0.1:1/v1"}`,
		`{"version":1,"Endpoint":"http://127.0.0.1:1/v1"}`,
		`{"version":1,"endpoint":"http://127.0.0.1:1/v1","APIKey":"x"}`,
		`{"version":1,"endpoint":"http://127.0.0.1:1/v1","apiKey":null}`,
		`{"version":1,"endpoint":"http://127.0.0.1:1/v1"} {}`,
		strings.Repeat(" ", 2049), string([]byte{0xff}),
	} {
		if _, err := ParseCredential([]byte(b)); err == nil {
			t.Fatalf("ambiguous schema admitted: %q", b)
		}
	}
}

func TestDisabledAndInvalidRequestsNeverDispatch(t *testing.T) {
	var calls atomic.Int32
	wire := wireFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected dispatch")
	})
	c := credential(t, "http://127.0.0.1:1/v1", "")
	r := request(t, http.MethodGet, "/v1/models", c)
	for _, transport := range []*Transport{nil, {}, {Wire: wire}, {Enabled: true}, {Enabled: true, Wire: (*http.Transport)(nil)}} {
		if _, err := transport.RoundTrip(r); err == nil {
			t.Fatal("disabled transport admitted")
		}
	}
	transport := &Transport{Enabled: true, Wire: wire}
	for _, path := range []string{"/", "/v1/chat/completions", "/v1/models/", "/v1/models/..", "/v1/models/a/b", "/v1/models/a%2fb", "/v1/models/a%20b", "/v1/models/a?q=x", "/v1/models?", "/v1/models#x", "/v1/models/" + strings.Repeat("x", 129)} {
		if _, err := transport.RoundTrip(request(t, http.MethodGet, path, c)); err == nil {
			t.Fatalf("invalid route admitted: %s", path)
		}
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.URL.Host = "127.0.0.1:1" },
		func(r *http.Request) { r.URL.Host = "local.fased.invalid:443" },
		func(r *http.Request) { r.URL.Scheme = "http" },
		func(r *http.Request) { r.Method = http.MethodDelete },
		func(r *http.Request) { r.RequestURI = "/v1/models" },
		func(r *http.Request) { r.Header.Set("X-Large", strings.Repeat("x", maxHeaders)) },
		func(r *http.Request) { r.Trailer = http.Header{"X-Trailer": {"x"}} },
		func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("x")) },
	} {
		r := request(t, http.MethodGet, "/v1/models", c)
		mutate(r)
		if _, err := transport.RoundTrip(r); err == nil {
			t.Fatal("invalid request admitted")
		}
	}
	if _, err := transport.RoundTrip(request(t, http.MethodGet, "/v1/models", Credential{})); err == nil {
		t.Fatal("invalid bound credential admitted")
	}
	unbound, _ := http.NewRequest(http.MethodGet, Origin+"/v1/models", nil)
	if _, err := transport.RoundTrip(unbound); err == nil {
		t.Fatal("unbound request admitted")
	}
	if _, err := transport.RoundTrip(nil); err == nil {
		t.Fatal("nil request admitted")
	}
	proxyWire := &Transport{Enabled: true, Wire: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	if _, err := proxyWire.RoundTrip(r); err == nil {
		t.Fatal("ambient proxy admitted")
	}
	if calls.Load() != 0 {
		t.Fatal("refused request reached wire")
	}
}

func TestControlledHTTPAndTLSRouting(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "TLS"}[tls], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Host == "foreign.invalid" || r.Header.Get("Authorization") != "Bearer fixture-local-key" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Secret") != "" || r.Header.Get("Idempotency-Key") != "" {
					t.Error("routing or auth boundary leaked")
				}
				if r.Header.Get("Accept") != "application/json, text/event-stream" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("fixed protocol headers missing")
				}
				if r.URL.Path != "/v1/models" && r.URL.Path != "/v1/models/gemma4:e2b" && r.URL.Path != "/v1/responses" {
					t.Error("route changed")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method == http.MethodPost && string(body) != `{"input":"fixture"}` {
					t.Error("request body changed")
				}
				io.WriteString(w, "data: fixture\n\n")
			}))
			if tls {
				server.StartTLS()
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)
			wire := server.Client().Transport.(*http.Transport).Clone()
			wire.Proxy = nil
			t.Cleanup(wire.CloseIdleConnections)
			transport := &Transport{Enabled: true, Wire: wire}
			c := credential(t, server.URL+"/v1", "fixture-local-key")
			for _, path := range []string{"/v1/models", "/v1/models/gemma4:e2b", "/v1/responses"} {
				method := http.MethodGet
				if path == "/v1/responses" {
					method = http.MethodPost
				}
				r := request(t, method, path, c)
				r.Host = "foreign.invalid"
				for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Forwarded-Host", "X-Secret", "Idempotency-Key"} {
					r.Header.Set(header, "foreign-secret")
				}
				if method == http.MethodPost {
					r.Body = io.NopCloser(strings.NewReader(`{"input":"fixture"}`))
					r.ContentLength = int64(len(`{"input":"fixture"}`))
				}
				response, err := transport.RoundTrip(r)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || string(body) != "data: fixture\n\n" || response.Request.URL.String() != Origin+path || response.Request.Header.Get("Authorization") != "" {
					t.Fatal("logical response or streaming body changed")
				}
				if r.Header.Get("Authorization") != "foreign-secret" || r.URL.String() != Origin+path {
					t.Fatal("caller request mutated")
				}
			}
			if calls.Load() != 3 {
				t.Fatal("unexpected dispatch count")
			}
		})
	}
}

func TestRedirectRefusalAndSanitizedErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "http://127.0.0.1:1/stolen")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	c := credential(t, server.URL+"/v1", "fixture-secret")
	wire := server.Client().Transport.(*http.Transport).Clone()
	wire.Proxy = nil
	t.Cleanup(wire.CloseIdleConnections)
	client := &http.Client{Transport: &Transport{Enabled: true, Wire: wire}}
	if _, err := client.Do(request(t, http.MethodGet, "/v1/models", c)); err == nil || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), c.APIKey) {
		t.Fatal("redirect admitted or credential exposed")
	}
	if calls.Load() != 1 {
		t.Fatal("redirect followed")
	}
	transport := &Transport{Enabled: true, Wire: wireFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(server.URL + " " + c.APIKey + " raw response")
	})}
	if _, err := transport.RoundTrip(request(t, http.MethodGet, "/v1/models", c)); err == nil || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), c.APIKey) || strings.Contains(err.Error(), "raw response") {
		t.Fatal("wire error exposed")
	}
}

func TestBoundedStreamingAndCancellation(t *testing.T) {
	c := credential(t, "http://127.0.0.1:1/v1", "")
	var operation context.Context
	transport := &Transport{Enabled: true, Wire: wireFunc(func(r *http.Request) (*http.Response, error) {
		operation = r.Context()
		deadline, ok := operation.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) < 4*time.Second {
			t.Error("operation timeout is not bounded at five seconds")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("keyless credential added authorization")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponse+1)))}, nil
	})}
	response, err := transport.RoundTrip(request(t, http.MethodGet, "/v1/models", c))
	if err != nil || operation.Err() != nil {
		t.Fatal("operation cancelled before response consumption")
	}
	n, err := io.Copy(io.Discard, response.Body)
	if err == nil || n != maxResponse || operation.Err() != nil {
		t.Fatal("response bound or close lifetime violated")
	}
	response.Body.Close()
	response.Body.Close()
	if operation.Err() != context.Canceled {
		t.Fatal("response close did not cancel operation")
	}
	transport.Wire = wireFunc(func(r *http.Request) (*http.Response, error) {
		n, err := io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if n != maxRequest || err == nil {
			t.Error("request stream escaped bound")
		}
		return nil, errors.New("fixture body refused")
	})
	r := request(t, http.MethodPost, "/v1/responses", c)
	r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxRequest+1)))
	r.ContentLength = -1
	if _, err := transport.RoundTrip(r); err == nil {
		t.Fatal("oversized request stream admitted")
	}
	transport.Wire = wireFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Large": {strings.Repeat("x", maxHeaders)}}, Body: http.NoBody}, nil
	})
	if _, err := transport.RoundTrip(request(t, http.MethodGet, "/v1/models", c)); err == nil {
		t.Fatal("oversized response headers admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	transport.Wire = wireFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
	})
	response, err = transport.RoundTrip(request(t, http.MethodGet, "/v1/models", c).WithContext(context.WithValue(ctx, credentialKey{}, c)))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := response.Body.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal("parent cancellation lost")
	}
	response.Body.Close()
}

func TestOperationDeadline(t *testing.T) {
	c := credential(t, "http://127.0.0.1:1/v1", "")
	for _, test := range []struct {
		name   string
		method string
		path   string
		cap    time.Duration
		caller time.Duration
	}{
		{"models", http.MethodGet, "/v1/models", 5 * time.Second, 0},
		{"model", http.MethodGet, "/v1/models/gemma4:e2b", 5 * time.Second, 0},
		{"responses", http.MethodPost, "/v1/responses", 120 * time.Second, 0},
		{"shorter caller", http.MethodPost, "/v1/responses", 120 * time.Second, time.Second},
		{"longer caller", http.MethodPost, "/v1/responses", 120 * time.Second, 240 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.caller != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.caller)
				defer cancel()
			}
			var operation context.Context
			before := time.Now()
			transport := &Transport{Enabled: true, Wire: wireFunc(func(r *http.Request) (*http.Response, error) {
				operation = r.Context()
				deadline, ok := operation.Deadline()
				if !ok {
					t.Fatal("operation deadline missing")
				}
				if parent, ok := ctx.Deadline(); ok && test.caller < test.cap {
					if !deadline.Equal(parent) {
						t.Fatal("shorter caller deadline changed")
					}
				} else if deadline.Before(before.Add(test.cap)) || deadline.After(time.Now().Add(test.cap)) {
					t.Fatal("operation cap changed")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
			})}
			r, err := http.NewRequestWithContext(ctx, test.method, Origin+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(Bind(r, c))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if operation.Err() != context.Canceled {
				t.Fatal("operation not cancelled on immediate close")
			}
		})
	}
}

func TestControlledCancellation(t *testing.T) {
	observed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(observed)
	}))
	t.Cleanup(server.Close)
	wire := server.Client().Transport.(*http.Transport).Clone()
	wire.Proxy = nil
	t.Cleanup(wire.CloseIdleConnections)
	transport := &Transport{Enabled: true, Wire: wire}
	c := credential(t, server.URL+"/v1", "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := request(t, http.MethodGet, "/v1/models", c)
	response, err := transport.RoundTrip(r.WithContext(context.WithValue(ctx, credentialKey{}, c)))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("cancelled stream succeeded")
	}
	response.Body.Close()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("server operation outlived cancellation")
	}
}
