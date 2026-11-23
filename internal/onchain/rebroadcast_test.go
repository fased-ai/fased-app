package onchain_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

type rebroadcastScriptedRT struct {
	base     *onchaintest.Chain
	mu       sync.Mutex
	sends    [][]byte
	heights  []uint64
	statuses []map[string]any
	sendErrs []error
	rpcCalls int
}

func (rt *rebroadcastScriptedRT) ControlledFixture() bool { return true }

func (rt *rebroadcastScriptedRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "rpc.fased-fixture.invalid" && r.Method == "POST" {
		body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			return nil, err
		}
		var call struct {
			ID     uint64            `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &call); err != nil {
			return nil, err
		}
		rt.mu.Lock()
		rt.rpcCalls++
		idxSend := len(rt.sends)
		switch call.Method {
		case "getBlockHeight":
			var h uint64
			if len(rt.heights) > 0 {
				h = rt.heights[0]
				if len(rt.heights) > 1 {
					rt.heights = rt.heights[1:]
				}
			}
			rt.mu.Unlock()
			if h > 0 {
				return jsonRPCResponse(r, call.ID, h), nil
			}
		case "sendTransaction":
			var b64 string
			_ = json.Unmarshal(call.Params[0], &b64)
			rawPkt, _ := base64.StdEncoding.Strict().DecodeString(b64)
			rt.sends = append(rt.sends, rawPkt)
			var injectedErr error
			if idxSend < len(rt.sendErrs) {
				injectedErr = rt.sendErrs[idxSend]
			}
			rt.mu.Unlock()
			if injectedErr != nil {
				return nil, injectedErr
			}
		case "getSignatureStatuses":
			var nextStatus map[string]any
			haveOverride := len(rt.statuses) > 0
			if haveOverride {
				nextStatus = rt.statuses[0]
				if len(rt.statuses) > 1 {
					rt.statuses = rt.statuses[1:]
				}
			}
			rt.mu.Unlock()
			if haveOverride {
				var value any
				if nextStatus != nil {
					value = nextStatus
				}
				return jsonRPCResponse(r, call.ID, map[string]any{
					"context": map[string]any{"slot": 10},
					"value":   []any{value},
				}), nil
			}
		default:
			rt.mu.Unlock()
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	return rt.base.RoundTrip(r)
}

func jsonRPCResponse(r *http.Request, id uint64, result any) *http.Response {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(b)),
		Request:    r,
	}
}

func prepareSignedSwap(t *testing.T) (*onchaintest.Chain, *rebroadcastScriptedRT, *onchain.Client, onchain.Quote, []byte) {
	t.Helper()
	chain, _, q, key := fixture(t)
	rt := &rebroadcastScriptedRT{base: chain}
	client, err := onchain.NewControlledClient(rt)
	if err != nil {
		t.Fatal(err)
	}
	q, err = client.Prepare(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := onchain.Message(q)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := onchain.Signed(q, ed25519.Sign(key, msg))
	if err != nil {
		t.Fatal(err)
	}
	return chain, rt, client, q, packet
}

func TestSendWithRebroadcast(t *testing.T) {
	t.Run("first-attempt-confirm", func(t *testing.T) {
		_, rt, client, q, packet := prepareSignedSwap(t)
		var sleeps []time.Duration
		res, err := client.SendWithRebroadcast(t.Context(), q, packet, onchain.RebroadcastConfig{
			Sleep: func(_ context.Context, d time.Duration) error {
				sleeps = append(sleeps, d)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Attempted || !res.Confirmed || res.Expired || res.Attempts != 1 || len(sleeps) != 1 || len(rt.sends) != 1 {
			t.Fatalf("unexpected result: %+v sleeps=%v sends=%d", res, sleeps, len(rt.sends))
		}
	})

	t.Run("dropped-packet-rebroadcasts-identical-bytes-and-confirms-on-second-attempt", func(t *testing.T) {
		_, rt, client, q, packet := prepareSignedSwap(t)
		rt.statuses = []map[string]any{
			nil, // post-attempt-1: leader dropped packet
			nil, // pre-attempt-2 check
			{"slot": 12, "err": nil, "confirmationStatus": "confirmed"}, // post-attempt-2
		}
		var sleeps []time.Duration
		res, err := client.SendWithRebroadcast(t.Context(), q, packet, onchain.RebroadcastConfig{
			MaxAttempts:  3,
			InitialDelay: 10 * time.Millisecond,
			MaxDelay:     15 * time.Millisecond,
			Sleep: func(_ context.Context, d time.Duration) error {
				sleeps = append(sleeps, d)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Attempted || !res.Confirmed || res.Attempts != 2 || len(rt.sends) != 2 {
			t.Fatalf("unexpected rebroadcast result: %+v sends=%d", res, len(rt.sends))
		}
		if !bytes.Equal(rt.sends[0], packet) || !bytes.Equal(rt.sends[1], packet) {
			t.Fatal("rebroadcast altered signed packet bytes")
		}
		if len(sleeps) != 2 || sleeps[0] != 10*time.Millisecond || sleeps[1] != 15*time.Millisecond {
			t.Fatalf("unexpected backoff schedule: %v", sleeps)
		}
	})

	t.Run("rpc-error-after-acceptance-short-circuits-before-second-send", func(t *testing.T) {
		_, rt, client, q, packet := prepareSignedSwap(t)
		rt.sendErrs = []error{errors.New("transient RPC timeout")}
		// Pre-attempt-2 status poll discovers attempt 1 actually landed.
		rt.statuses = []map[string]any{
			{"slot": 11, "err": nil, "confirmationStatus": "finalized"},
		}
		res, err := client.SendWithRebroadcast(t.Context(), q, packet, onchain.RebroadcastConfig{
			MaxAttempts: 3,
			Sleep:       func(context.Context, time.Duration) error { return nil },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Attempted || !res.Confirmed || res.Attempts != 1 || len(rt.sends) != 1 {
			t.Fatalf("expected short-circuit after 1 attempt, got %+v sends=%d", res, len(rt.sends))
		}
	})

	t.Run("expired-blockhash-stops-rebroadcast", func(t *testing.T) {
		_, rt, client, q, packet := prepareSignedSwap(t)
		rt.heights = []uint64{q.LastValidHeight + 1}
		res, err := client.SendWithRebroadcast(t.Context(), q, packet, onchain.RebroadcastConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		})
		if err == nil || !res.Expired || res.Attempted || res.Attempts != 0 {
			t.Fatalf("expected expiry stop, got res=%+v err=%v", res, err)
		}
	})

	t.Run("context-cancellation-during-backoff", func(t *testing.T) {
		_, rt, client, q, packet := prepareSignedSwap(t)
		rt.statuses = []map[string]any{nil}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		res, err := client.SendWithRebroadcast(ctx, q, packet, onchain.RebroadcastConfig{
			Sleep: func(c context.Context, _ time.Duration) error {
				cancel()
				return c.Err()
			},
		})
		if !errors.Is(err, context.Canceled) || res.Attempts != 1 {
			t.Fatalf("expected context.Canceled, got res=%+v err=%v", res, err)
		}
	})

	t.Run("rejects-excess-attempts-and-tampered-packet-zero-rpc", func(t *testing.T) {
		_, rt, client, q, packet := prepareSignedSwap(t)
		rt.rpcCalls = 0
		if _, err := client.SendWithRebroadcast(t.Context(), q, packet, onchain.RebroadcastConfig{
			MaxAttempts: onchain.MaxRebroadcastAttempts + 1,
		}); err == nil {
			t.Fatal("accepted MaxAttempts above bound")
		}
		tampered := append([]byte(nil), packet...)
		tampered[len(tampered)-1] ^= 0xff
		if _, err := client.SendWithRebroadcast(t.Context(), q, tampered, onchain.RebroadcastConfig{}); err == nil {
			t.Fatal("accepted tampered signed packet")
		}
		if rt.rpcCalls != 0 {
			t.Fatalf("expected 0 RPC calls on invalid config/packet, got %d", rt.rpcCalls)
		}
	})

	t.Run("client-send-remains-single-shot", func(t *testing.T) {
		chain, _, client, q, packet := prepareSignedSwap(t)
		chain.DropSubmission = true
		attempted, err := client.Send(t.Context(), q, packet)
		if !attempted || err == nil || chain.Submissions != 1 {
			t.Fatalf("expected strictly single-shot Send, got attempted=%v err=%v submissions=%d", attempted, err, chain.Submissions)
		}
	})
}
