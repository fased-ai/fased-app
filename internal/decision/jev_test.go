package decision

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestJevEncryptedStorageFallbackAndMockedRemote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	var seenAuth string
	svc, err := NewService(Config{
		DataDir:     dir,
		FixtureMode: false,
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			seenAuth = r.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"model":"jev-1.13.0",
					"answers":{
						"urgent":{"type":"noul","noul":0.95},
						"bias":{"type":"choice","choice":"bullish","probabilities":{"bullish":0.82,"bearish":0.18},"confidence":0.82},
						"conviction":{"type":"score","score":4,"legend":"High","probabilities":{"4":0.7},"confidence":0.7}
					},
					"usage":{"input_tokens":120,"output_tokens":18}
				}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 1. Unconfigured fallback works deterministically in <5ms across all 3 question types.
	fb, err := svc.Evaluate(ctx, EvaluateRequest{
		State: "BTC broke resistance on urgent volume surge, bullish ETF inflows verified",
		Questions: map[string]QuestionSpec{
			"urgent":     {Type: TypeNoul, Instructions: "Conveys urgency"},
			"bias":       {Type: TypeChoice, Choices: []string{"bullish", "bearish", "neutral"}},
			"conviction": {Type: TypeScore, Rubric: []string{"Weak", "Mixed", "Solid", "High"}},
		},
	})
	if err != nil {
		t.Fatalf("fallback Evaluate failed: %v", err)
	}
	if !fb.Fallback || fb.Answers["urgent"].Noul == nil || fb.Answers["bias"].Choice == "" || fb.Answers["conviction"].Score == nil {
		t.Fatalf("unexpected fallback result: %+v", fb)
	}

	// 2. Save secret key & verify plaintext is NEVER present in decision.sqlite on disk.
	secret := "ts-super-secret-jev-token-999999"
	st, err := svc.SaveConfig(ctx, ConfigUpdate{APIKey: secret, Model: "jev-latest"})
	if err != nil || !st.Configured {
		t.Fatalf("SaveConfig failed: st=%+v err=%v", st, err)
	}
	dbBytes, err := os.ReadFile(filepath.Join(dir, integrationsDirName, decisionDBName))
	if err != nil {
		t.Fatalf("read decision.sqlite failed: %v", err)
	}
	if bytes.Contains(dbBytes, []byte(secret)) {
		t.Fatal("plaintext Jev API key leaked into decision.sqlite")
	}

	// 3. Configured live evaluation sends Authorization Bearer header and decodes JSON.
	live, err := svc.Evaluate(ctx, EvaluateRequest{
		State: "test state",
		Questions: map[string]QuestionSpec{
			"urgent": {Type: TypeNoul, Instructions: "Urgent?"},
		},
	})
	if err != nil {
		t.Fatalf("live Evaluate failed: %v", err)
	}
	if live.Fallback || seenAuth != "Bearer "+secret || *live.Answers["urgent"].Noul != 0.95 {
		t.Fatalf("unexpected live Jev output: auth=%q res=%+v", seenAuth, live)
	}

	// 4. Clearing key returns to unconfigured status & deterministic fallback.
	stCleared, err := svc.SaveConfig(ctx, ConfigUpdate{ClearKey: true})
	if err != nil || stCleared.Configured {
		t.Fatalf("expected cleared config, got %+v err=%v", stCleared, err)
	}
}

func TestJevFixtureModeZeroNetworkAndValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var calls atomic.Int32
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.SaveConfig(ctx, ConfigUpdate{APIKey: "ts-fixture-key"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	res, err := svc.Evaluate(ctx, EvaluateRequest{
		State:     "macro event",
		Questions: map[string]QuestionSpec{"urgent": {Type: TypeNoul, Instructions: "Urgent"}},
	})
	if err != nil || !res.Fallback {
		t.Fatalf("expected offline FixtureMode fallback, got %+v err=%v", res, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("FixtureMode issued %d HTTP calls, expected 0", calls.Load())
	}

	// SSRF private/CGNAT check.
	if isPublicUnicastIP(net.ParseIP("100.118.152.85")) || isPublicUnicastIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("non-public IP accepted")
	}

	// Validation errors.
	if _, err := svc.Evaluate(ctx, EvaluateRequest{State: "", Questions: map[string]QuestionSpec{"q": {Type: TypeNoul, Instructions: "x"}}}); err == nil {
		t.Fatal("expected empty state error")
	}
	if _, err := svc.Evaluate(ctx, EvaluateRequest{State: "ok", Questions: map[string]QuestionSpec{"q": {Type: TypeChoice, Choices: []string{"onlyone"}}}}); err == nil {
		t.Fatal("expected single-choice error")
	}

	// Verify deterministic lexicon sentiment scoring (no hash coin-flip on neutral/earthquake headlines).
	now := time.Unix(1760000000, 0).UTC()
	sentQ := map[string]QuestionSpec{
		"sentiment": {Type: TypeChoice, Choices: []string{"bullish", "bearish", "neutral"}},
	}
	quakeResp, err := EvaluateFallback(EvaluateRequest{State: "M 5.4 Earthquake near Tokyo Japan", Questions: sentQ}, now)
	if err != nil || quakeResp.Answers["sentiment"].Choice != "bearish" {
		t.Fatalf("expected earthquake headline to score bearish, got %+v err=%v", quakeResp.Answers, err)
	}
	inflowResp, err := EvaluateFallback(EvaluateRequest{State: "Spot Bitcoin ETF inflow surge drives breakout", Questions: sentQ}, now)
	if err != nil || inflowResp.Answers["sentiment"].Choice != "bullish" {
		t.Fatalf("expected inflow surge headline to score bullish, got %+v err=%v", inflowResp.Answers, err)
	}
	neutralResp, err := EvaluateFallback(EvaluateRequest{State: "Quarterly statistical schedule released on Tuesday", Questions: sentQ}, now)
	if err != nil || neutralResp.Answers["sentiment"].Choice != "neutral" {
		t.Fatalf("expected non-directional headline to score neutral, got %+v err=%v", neutralResp.Answers, err)
	}
}
