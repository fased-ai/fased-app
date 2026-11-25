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
						"cloid":   res.ClientOrderID,
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
		switch actType {
		case "updateLeverage":
			m.mu.Unlock()
			respBody, _ = json.Marshal(map[string]any{
				"status":   "ok",
				"response": map[string]any{"type": "default"},
			})
		case "cancel":
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
		default:
			m.nextOid++
			oid := m.nextOid
			cloid := ""
			if orders, ok := act["orders"].([]any); ok && len(orders) > 0 {
				if first, ok := orders[0].(map[string]any); ok {
					cloid, _ = first["c"].(string)
				}
			}
			m.reconcileRes = ExchangeSubmissionResult{
				State:         "filled",
				OrderID:       oid,
				ClientOrderID: cloid,
				FilledSize:    "0.5",
				AvgPrice:      "3200.5",
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
	if _, err := liveClient.SubmitSignedUpdateLeverage(t.Context(), signer.HyperliquidUpdateLeverageIntent{Network: "testnet", Leverage: 5}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("ordinary client SubmitSignedUpdateLeverage got %v, want ErrExecutionDisabled", err)
	}
	if _, err := liveClient.SubmitSignedCancel(t.Context(), signer.HyperliquidCancelIntent{Network: "testnet"}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("ordinary client SubmitSignedCancel got %v, want ErrExecutionDisabled", err)
	}

	// Testnet execution client must pin strictly to testnet and fail closed on mainnet.
	execClient, err := NewTestnetExecutionClient()
	if err != nil {
		t.Fatal(err)
	}
	if execClient.Network() != "testnet" || execClient.Origin() != TestnetAPIOrigin || !execClient.Controlled() {
		t.Fatalf("unexpected TestnetExecutionClient identity: network=%q origin=%q controlled=%v", execClient.Network(), execClient.Origin(), execClient.Controlled())
	}
	if _, err := execClient.SubmitSignedOrder(t.Context(), signer.HyperliquidOrderIntent{Network: "mainnet"}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("testnet exec client must reject mainnet SubmitSignedOrder, got %v", err)
	}
	if _, err := execClient.SubmitSignedUpdateLeverage(t.Context(), signer.HyperliquidUpdateLeverageIntent{Network: "mainnet", Leverage: 5}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("testnet exec client must reject mainnet SubmitSignedUpdateLeverage, got %v", err)
	}
	if _, err := execClient.SubmitSignedCancel(t.Context(), signer.HyperliquidCancelIntent{Network: "mainnet"}, &signer.HyperliquidSignedAction{}, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("testnet exec client must reject mainnet SubmitSignedCancel, got %v", err)
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
	levIntent := signer.HyperliquidUpdateLeverageIntent{
		Network:    "testnet",
		Asset:      "ETH",
		AssetIndex: 1,
		Leverage:   5,
		IsCross:    false,
		Nonce:      1000,
		ExpiresAt:  now + 300,
	}
	signedLev, err := v.SignHyperliquidUpdateLeverage(levIntent, policy)
	if err != nil {
		t.Fatalf("SignHyperliquidUpdateLeverage: %v", err)
	}
	levRes, err := client.SubmitSignedUpdateLeverage(ctx, levIntent, signedLev, policy.VaultAddress)
	if err != nil || levRes.State != "ok" {
		t.Fatalf("SubmitSignedUpdateLeverage got %+v, err=%v", levRes, err)
	}

	preview, intent, err := PreviewOrder(snap, policy, OrderDraft{
		Asset:         "ETH",
		IsBuy:         true,
		LimitPrice:    "3200.50",
		Size:          "0.500",
		Leverage:      5,
		TimeInForce:   "Gtc",
		ClientOrderID: "0x11111111111111111111111111111111",
	}, 1000, 1001, now)
	if err != nil {
		t.Fatalf("PreviewOrder: %v", err)
	}
	if intent.IsCross {
		t.Fatal("expected PreviewOrder to default IsCross to false")
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

	cloidRes, err := client.ReconcileOrderByCloid(ctx, signerAddr, intent.ClientOrderID)
	if err != nil || cloidRes.State != "filled" || cloidRes.OrderID != 5001 || cloidRes.ClientOrderID != intent.ClientOrderID {
		t.Fatalf("ReconcileOrderByCloid got %+v, err=%v", cloidRes, err)
	}
	if _, err := client.ReconcileOrderByCloid(ctx, signerAddr, "not-a-cloid"); err == nil {
		t.Fatal("accepted malformed cloid")
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

func TestPreviewOrderMarginModeAndOnlyIsolated(t *testing.T) {
	now := time.Now().Unix()
	snap := MarketSnapshot{
		Network: "testnet",
		Markets: []MarketMeta{
			{AssetIndex: 1, Symbol: "ETH", SzDecimals: 4, MaxLeverage: 20, OnlyIsolated: false, MarkPrice: "3200"},
			{AssetIndex: 2, Symbol: "MEME", SzDecimals: 1, MaxLeverage: 10, OnlyIsolated: true, MarkPrice: "1.5"},
		},
	}
	policy := signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"ETH", "MEME"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       5_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}
	trueVal := true
	_, crossIntent, err := PreviewOrder(snap, policy, OrderDraft{
		Asset:       "ETH",
		IsBuy:       true,
		IsCross:     &trueVal,
		LimitPrice:  "3200",
		Size:        "0.5",
		Leverage:    5,
		TimeInForce: "Gtc",
	}, 0, 100, now)
	if err != nil || !crossIntent.IsCross {
		t.Fatalf("expected IsCross=true on non-onlyIsolated market, got isCross=%v err=%v", crossIntent.IsCross, err)
	}

	if _, _, err := PreviewOrder(snap, policy, OrderDraft{
		Asset:       "MEME",
		IsBuy:       true,
		IsCross:     &trueVal,
		LimitPrice:  "1.5",
		Size:        "10",
		Leverage:    5,
		TimeInForce: "Gtc",
	}, 0, 101, now); err == nil {
		t.Fatal("accepted cross margin order on onlyIsolated market")
	}
}

func TestOrderStorePersistenceAndMonotonicNonce(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenOrderStore(dir)
	if err != nil {
		t.Fatalf("OpenOrderStore: %v", err)
	}
	ctx := t.Context()
	rec := OrderRecord{
		ID:              "hl-1001",
		OrderID:         7001,
		ClientOrderID:   "0xABCDEF0123456789ABCDEF0123456789",
		Asset:           "ETH",
		IsBuy:           true,
		IsCross:         true,
		LimitPrice:      "3200.5",
		Size:            "0.5",
		Leverage:        5,
		ReduceOnly:      false,
		TimeInForce:     "Gtc",
		State:           "resting",
		Nonce:           180,
		SignerAddress:   "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23",
		ConnectionID:    "0x01",
		TypedDataDigest: "0x02",
		SignatureHex:    "0x03",
		UpdatedAt:       1700000000,
	}
	if err := store.UpsertOrder(ctx, rec); err != nil {
		t.Fatalf("UpsertOrder: %v", err)
	}
	if err := store.SaveLastNonce(ctx, 200); err != nil {
		t.Fatalf("SaveLastNonce(200): %v", err)
	}
	// Regression nonce must be ignored monotonically.
	if err := store.SaveLastNonce(ctx, 150); err != nil {
		t.Fatalf("SaveLastNonce(150): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	reopened, err := OpenOrderStore(dir)
	if err != nil {
		t.Fatalf("reopen OrderStore: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	orders, lastNonce, err := reopened.LoadState(ctx, 64)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if lastNonce != 200 {
		t.Fatalf("expected monotonic lastNonce 200, got %d", lastNonce)
	}
	if len(orders) != 1 || orders[0].ID != rec.ID || orders[0].ClientOrderID != strings.ToLower(rec.ClientOrderID) || !orders[0].IsCross || orders[0].OrderID != 7001 {
		t.Fatalf("unexpected persisted orders after reopen: %+v", orders)
	}
}

func TestParseExchangeStatusesTopLevelStringError(t *testing.T) {
	t.Parallel()
	res, err := parseExchangeStatuses([]byte(`{"status":"err","response":"User or API Wallet 0x123 does not exist."}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.State != "rejected" || res.Message != "User or API Wallet 0x123 does not exist." {
		t.Fatalf("unexpected parsed result: %+v", res)
	}
}
