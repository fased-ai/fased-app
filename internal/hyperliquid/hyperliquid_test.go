package hyperliquid

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

type mockHyperliquidTransport struct {
	mu           sync.Mutex
	marketsRaw   []byte
	signerAddr   string
	reconcileRes ExchangeSubmissionResult
	nextOid      uint64
	exchangeHits int
	lastAction   string
}

func (m *mockHyperliquidTransport) ControlledFixture() bool { return true }

func (m *mockHyperliquidTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost {
		return nil, errors.New("method not allowed")
	}
	body, _ := io.ReadAll(req.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	var respBody []byte
	switch req.URL.Path {
	case "/info":
		switch payload["type"] {
		case "metaAndAssetCtxs":
			respBody = m.marketsRaw
		case "clearinghouseState":
			respBody, _ = json.Marshal(map[string]any{
				"marginSummary": map[string]any{
					"accountValue":    "10000.00",
					"totalMarginUsed": "640.00",
				},
				"withdrawable": "9360.00",
				"assetPositions": []any{
					map[string]any{
						"position": map[string]any{
							"coin":          "BTC",
							"szi":           "0.05",
							"entryPx":       "95000.0",
							"positionValue": "4812.50",
							"unrealizedPnl": "62.50",
							"liquidationPx": "79200.0",
							"leverage": map[string]any{
								"type":  "cross",
								"value": 5,
							},
						},
					},
				},
			})
		case "openOrders":
			respBody, _ = json.Marshal([]any{})
		case "orderStatus":
			m.mu.Lock()
			res := m.reconcileRes
			m.mu.Unlock()
			respBody, _ = json.Marshal(map[string]any{
				"status": "order",
				"order": map[string]any{
					"status": res.State,
					"order": map[string]any{
						"oid":     res.OrderID,
						"sz":      res.FilledSize,
						"limitPx": res.AvgPrice,
					},
				},
			})
		default:
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
	case "/exchange":
		m.mu.Lock()
		m.exchangeHits++
		act, _ := payload["action"].(map[string]any)
		actType, _ := act["type"].(string)
		m.lastAction = actType
		if actType == "cancel" {
			m.reconcileRes.State = "canceled"
			m.mu.Unlock()
			respBody, _ = json.Marshal(map[string]any{
				"status": "ok",
				"response": map[string]any{
					"type": "cancel",
					"data": map[string]any{
						"statuses": []any{map[string]any{}},
					},
				},
			})
		} else {
			m.nextOid++
			oid := m.nextOid
			m.reconcileRes = ExchangeSubmissionResult{
				State:      "filled",
				OrderID:    oid,
				FilledSize: "0.5",
				AvgPrice:   "3200.5",
			}
			m.mu.Unlock()
			respBody, _ = json.Marshal(map[string]any{
				"status": "ok",
				"response": map[string]any{
					"type": "order",
					"data": map[string]any{
						"statuses": []any{
							map[string]any{"resting": map[string]any{"oid": oid}},
						},
					},
				},
			})
		}
	default:
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}, nil
}

func TestValidateOriginAndSSRFGuards(t *testing.T) {
	if got, err := ValidateOrigin(MainnetAPIOrigin); err != nil || got != MainnetAPIOrigin {
		t.Fatalf("mainnet origin rejected: %q, %v", got, err)
	}
	if got, err := ValidateOrigin(TestnetAPIOrigin); err != nil || got != TestnetAPIOrigin {
		t.Fatalf("testnet origin rejected: %q, %v", got, err)
	}
	for _, bad := range []string{
		"http://api.hyperliquid.xyz",
		"https://evil.example",
		"https://api.hyperliquid.xyz/info",
		"https://api.hyperliquid.xyz?x=1",
		"https://user@api.hyperliquid.xyz",
	} {
		if _, err := ValidateOrigin(bad); err == nil {
			t.Fatalf("accepted invalid origin %q", bad)
		}
	}

	for _, privateIP := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1"} {
		ip := netip.MustParseAddr(privateIP)
		if _, err := interleavePublicIPs([]netip.Addr{ip}); err == nil {
			t.Fatalf("accepted non-public unicast IP %s", privateIP)
		}
	}

	// Standard non-controlled client must refuse SubmitSignedOrder fail-closed.
	liveClient, err := NewClient("testnet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := liveClient.SubmitSignedOrder(t.Context(), signer.HyperliquidOrderIntent{Network: "testnet"}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("ordinary client SubmitSignedOrder got %v, want ErrExecutionDisabled", err)
	}
	if _, err := liveClient.SubmitSignedCancel(t.Context(), signer.HyperliquidCancelIntent{Network: "testnet"}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("ordinary client SubmitSignedCancel got %v, want ErrExecutionDisabled", err)
	}
}

func TestControlledHyperliquidEndToEndFlow(t *testing.T) {
	cassette, err := os.ReadFile(filepath.Join("testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "hl-vault-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	v, err := signer.Open(dir, "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = v.ConfigureHyperliquidControlled(false)
		_ = v.Close()
	})
	pass := []byte("hyperliquid-test-passphrase-2026")
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := v.ConfigureHyperliquidControlled(true); err != nil {
		t.Fatal(err)
	}
	signerAddr, err := v.HyperliquidAddress()
	if err != nil {
		t.Fatal(err)
	}

	rt := &mockHyperliquidTransport{marketsRaw: cassette, signerAddr: signerAddr, nextOid: 5000}
	client, err := NewControlledClient("testnet", rt)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	snap, err := client.FetchMarkets(ctx)
	if err != nil || len(snap.Markets) != 3 {
		t.Fatalf("FetchMarkets got %d markets, err=%v", len(snap.Markets), err)
	}
	acct, err := client.FetchAccountState(ctx, signerAddr)
	if err != nil || acct.Withdrawable != "9360.00" || len(acct.Positions) != 1 {
		t.Fatalf("FetchAccountState unexpected: %+v, err=%v", acct, err)
	}

	now := time.Now().Unix()
	policy := signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH", "SOL"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       5_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}
	preview, intent, err := PreviewOrder(snap, policy, OrderDraft{
		Asset:       "ETH",
		IsBuy:       true,
		LimitPrice:  "3200.50",
		Size:        "0.500",
		Leverage:    5,
		TimeInForce: "Gtc",
	}, 0, 1001, now)
	if err != nil {
		t.Fatalf("PreviewOrder: %v", err)
	}
	if preview.NormalizedPrice != "3200.5" || preview.NormalizedSize != "0.5" {
		t.Fatalf("unexpected normalized preview: %+v", preview)
	}
	if preview.NotionalMicroUSD != 1_600_250_000 {
		t.Fatalf("unexpected notional: %d", preview.NotionalMicroUSD)
	}

	// Sign with vault and confirm preview digest matches signed typedDataDigest.
	signed, err := v.SignHyperliquidOrder(intent, policy)
	if err != nil {
		t.Fatalf("SignHyperliquidOrder: %v", err)
	}
	if signed.TypedDataDigest != preview.TypedDataDigest || signed.ConnectionID != preview.ConnectionID {
		t.Fatalf("preview vs signed digest mismatch: preview=%s signed=%s", preview.TypedDataDigest, signed.TypedDataDigest)
	}

	submitRes, err := client.SubmitSignedOrder(ctx, intent, signed, policy.VaultAddress)
	if err != nil || submitRes.State != "resting" || submitRes.OrderID != 5001 {
		t.Fatalf("SubmitSignedOrder got %+v, err=%v", submitRes, err)
	}

	reconcileRes, err := client.ReconcileOrder(ctx, signerAddr, submitRes.OrderID)
	if err != nil || reconcileRes.State != "filled" || reconcileRes.OrderID != 5001 {
		t.Fatalf("ReconcileOrder got %+v, err=%v", reconcileRes, err)
	}

	cancelIntent := signer.HyperliquidCancelIntent{
		Network:    "testnet",
		Asset:      "ETH",
		AssetIndex: 1,
		OrderID:    submitRes.OrderID,
		Nonce:      1002,
		ExpiresAt:  now + 300,
	}
	signedCancel, err := v.SignHyperliquidCancel(cancelIntent, policy)
	if err != nil {
		t.Fatalf("SignHyperliquidCancel: %v", err)
	}
	cancelRes, err := client.SubmitSignedCancel(ctx, cancelIntent, signedCancel, policy.VaultAddress)
	if err != nil || cancelRes.State != "cancelled" {
		t.Fatalf("SubmitSignedCancel got %+v, err=%v", cancelRes, err)
	}
}
