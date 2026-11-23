package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type failingReader struct {
	chunks [][]byte
	idx    int
	err    error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.idx < len(f.chunks) {
		n := copy(p, f.chunks[f.idx])
		f.chunks[f.idx] = f.chunks[f.idx][n:]
		if len(f.chunks[f.idx]) == 0 {
			f.idx++
		}
		return n, nil
	}
	if f.err != nil {
		return 0, f.err
	}
	return 0, io.EOF
}

func TestNetworkChaosSSEAndHTTPGuards(t *testing.T) {
	t.Parallel()

	t.Run("mid-stream EOF and TCP reset refuse incomplete SSE output", func(t *testing.T) {
		prefix := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_chaos\",\"model\":\"gpt-5.4\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"output_index\":0,\"content_index\":0,\"delta\":\"partial"
		for _, streamErr := range []error{io.EOF, io.ErrUnexpectedEOF, errors.New("connection reset by peer")} {
			r := &failingReader{chunks: [][]byte{[]byte(prefix)}, err: streamErr}
			completed, err := readAIStream(r, "gpt-5.4", nil, true)
			if err == nil || completed != nil {
				t.Fatalf("expected error on truncated stream (%v), got completed=%q err=%v", streamErr, completed, err)
			}
		}
	})

	t.Run("signInHTTP and googleOAuthHTTP refuse HTML challenges gzip redirects and wrong content types", func(t *testing.T) {
		var mode atomic.Int32
		rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch mode.Load() {
			case 0:
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"Content-Type": []string{"text/html"}, "CF-Ray": []string{"ray-cf-403"}},
					Body:       io.NopCloser(strings.NewReader("<html><body>cf-turnstile-secret-challenge</body></html>")),
					Request:    r,
				}, nil
			case 1:
				var buf bytes.Buffer
				gz := gzip.NewWriter(&buf)
				_, _ = gz.Write([]byte(`{"issuer":"https://auth.openai.com"}`))
				_ = gz.Close()
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}, "Content-Encoding": []string{"gzip"}},
					Body:       io.NopCloser(&buf),
					Request:    r,
				}, nil
			case 2:
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"https://evil.example/redirect"}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    r,
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/plain"}},
					Body:       io.NopCloser(strings.NewReader(`{"issuer":"https://auth.openai.com","jwks_uri":"https://auth.openai.com/.well-known/jwks.json","revocation_endpoint":"https://auth.openai.com/oauth/revoke"}`)),
					Request:    r,
				}, nil
			}
		})

		for m := int32(0); m < 4; m++ {
			mode.Store(m)
			var dst signInDiscovery
			err := signInHTTP(t.Context(), rt, http.MethodGet, signInIssuer+"/.well-known/openid-configuration", nil, &dst)
			if err == nil || strings.Contains(err.Error(), "turnstile-secret") {
				t.Fatalf("signInHTTP mode=%d unexpected result err=%v", m, err)
			}
			var gdst map[string]any
			gerr := googleOAuthHTTP(t.Context(), rt, http.MethodGet, googleCertsURL, nil, &gdst)
			if gerr == nil || strings.Contains(gerr.Error(), "turnstile-secret") {
				t.Fatalf("googleOAuthHTTP mode=%d unexpected result err=%v", m, gerr)
			}
		}

		// Google endpoint also rejects POST redirects on token exchange cleanly.
		mode.Store(2)
		var tok map[string]any
		if err := googleOAuthHTTP(t.Context(), rt, http.MethodPost, googleTokenURL, url.Values{"grant_type": {"refresh_token"}}, &tok); err == nil {
			t.Fatal("expected Google OAuth token POST redirect refusal")
		}
	})
}

func TestNetworkChaosSolanaRPCAndDialFallback(t *testing.T) {
	t.Parallel()

	t.Run("walletRPCCaller recovers from single 429 and rejects persistent 429 or EOF", func(t *testing.T) {
		var calls atomic.Int32
		rtRecover := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var req struct {
				ID int `json:"id"`
			}
			_ = jsonDecoderDecode(r.Body, &req)
			n := calls.Add(1)
			if n == 1 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body:       io.NopCloser(strings.NewReader("rate limited")),
					Request:    r,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":` + strconv.Itoa(req.ID) + `,"result":"5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"}`)),
				Request:    r,
			}, nil
		})
		caller := walletRPCCaller(t.Context(), "https://api.mainnet-beta.solana.com", rtRecover, func() error { return nil })
		var genesis string
		if err := caller("getGenesisHash", []any{}, &genesis); err != nil || genesis != solanaGenesis("mainnet-beta") {
			t.Fatalf("expected 429 recovery, got genesis=%q err=%v", genesis, err)
		}

		rtEOF := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(&failingReader{chunks: [][]byte{[]byte(`{"jsonrpc":"2.0","id":1`)}, err: io.ErrUnexpectedEOF}),
				Request:    r,
			}, nil
		})
		callerEOF := walletRPCCaller(t.Context(), "https://api.mainnet-beta.solana.com", rtEOF, func() error { return nil })
		if err := callerEOF("getGenesisHash", []any{}, &genesis); err == nil {
			t.Fatal("expected mid-stream EOF failure")
		}
	})

	t.Run("chunked gzipped public RPC response and slow-loris cancellation", func(t *testing.T) {
		var step atomic.Int32
		releaseSlow := make(chan struct{})
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if step.Load() == 1 {
				select {
				case <-r.Context().Done():
				case <-releaseSlow:
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			flusher, _ := w.(http.Flusher)
			var req struct {
				ID int `json:"id"`
			}
			_ = jsonDecoderDecode(r.Body, &req)
			res := solanaGenesis("devnet")
			if req.ID == 2 {
				res = "ok"
			}
			_, _ = gz.Write([]byte(`{"jsonrpc":"2.0","id":`))
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = gz.Write([]byte(strconv.Itoa(req.ID) + `,"result":"` + res + `"}`))
			_ = gz.Close()
		}))
		defer srv.Close()
		defer srv.CloseClientConnections()
		defer close(releaseSlow)

		rt := loopbackRPCTLSTransport(t, srv, "api.devnet.solana.com")
		res, err := checkPublicRPC(t.Context(), RPCCheckRequest{Endpoint: "https://api.devnet.solana.com", Network: "devnet"}, rt)
		if err != nil || !res.LiveRPC || res.GenesisHash != solanaGenesis("devnet") {
			t.Fatalf("chunked gzipped RPC check failed: res=%+v err=%v", res, err)
		}

		step.Store(1)
		slowCtx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
		defer cancel()
		if _, err := checkPublicRPC(slowCtx, RPCCheckRequest{Endpoint: "https://api.devnet.solana.com", Network: "devnet"}, rt); err == nil {
			t.Fatal("expected slow-loris context timeout failure")
		}
		rt.transport.CloseIdleConnections()
	})

	t.Run("dialPublicRPC blackhole ipv6 fallback and concurrent cancellation race check", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()

		conn, err := dialPublicRPC(t.Context(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("2606:4700:4700::1111"),
					netip.MustParseAddr("1.1.1.1"),
				}, nil
			},
			func(attemptCtx context.Context, _ string, addr string) (net.Conn, error) {
				if _, hasDeadline := attemptCtx.Deadline(); !hasDeadline {
					t.Fatal("expected bounded per-attempt deadline")
				}
				if strings.HasPrefix(addr, "[2606:") {
					return nil, context.DeadlineExceeded
				}
				return c1, nil
			},
		)
		if err != nil || conn != c1 {
			t.Fatalf("expected IPv4 fallback after blackholed IPv6 timeout, got conn=%v err=%v", conn, err)
		}

		// SSRF contamination: mixed public + metadata IP must fail before dialing anything.
		if _, err := dialPublicRPC(t.Context(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("1.1.1.1"),
					netip.MustParseAddr("169.254.169.254"),
				}, nil
			},
			func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("SSRF guard dialed contaminated IP set")
				return nil, nil
			},
		); err == nil {
			t.Fatal("expected error for contaminated lookup")
		}

		// Concurrent cancellation chaos under -race.
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				defer cancel()
				_, _ = dialPublicRPC(ctx, "tcp", "example.com:443",
					func(context.Context, string, string) ([]netip.Addr, error) {
						return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}, nil
					},
					func(aCtx context.Context, _ string, _ string) (net.Conn, error) {
						if idx%2 == 0 {
							<-aCtx.Done()
							return nil, aCtx.Err()
						}
						return nil, errors.New("chaos dial reset")
					},
				)
			}(i)
		}
		wg.Wait()
	})
}

func jsonDecoderDecode(r io.Reader, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, 8192))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}
