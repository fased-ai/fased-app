package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/signer"
)

type coreHyperliquidMockTransport struct {
	mu           sync.Mutex
	marketsRaw   []byte
	nextOid      uint64
	orderStates  map[uint64]string
	orderByCloid map[string]uint64
	cloidByOid   map[uint64]string
	appliedLev   map[string]struct {
		leverage uint32
		isCross  bool
	}
	exchangeActions []string
	rejectLeverage  bool
	driftLeverage   bool
	dropOrderSubmit bool
	lastSubmitted   map[string]any
}

func (m *coreHyperliquidMockTransport) ControlledFixture() bool { return true }

func (m *coreHyperliquidMockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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
			btcLevType := "cross"
			var btcLevVal uint32 = 5
			m.mu.Lock()
			if !m.driftLeverage {
				if al, ok := m.appliedLev["BTC"]; ok {
					btcLevVal = al.leverage
					if al.isCross {
						btcLevType = "cross"
					} else {
						btcLevType = "isolated"
					}
				}
			}
			m.mu.Unlock()
			respBody, _ = json.Marshal(map[string]any{
				"marginSummary": map[string]any{
					"accountValue":    "12500.00",
					"totalMarginUsed": "962.50",
				},
				"withdrawable": "11537.50",
				"assetPositions": []any{
					map[string]any{
						"position": map[string]any{
							"coin":          "BTC",
							"szi":           "0.05",
							"entryPx":       "95100.0",
							"positionValue": "4812.50",
							"unrealizedPnl": "57.50",
							"liquidationPx": "78400.0",
							"leverage": map[string]any{
								"type":  btcLevType,
								"value": btcLevVal,
							},
						},
					},
				},
			})
		case "openOrders":
			respBody, _ = json.Marshal([]any{})
		case "orderStatus":
			var oid uint64
			var cloid string
			m.mu.Lock()
			switch v := payload["oid"].(type) {
			case float64:
				oid = uint64(v)
				cloid = m.cloidByOid[oid]
			case string:
				cloid = strings.ToLower(v)
				oid = m.orderByCloid[cloid]
			}
			st := m.orderStates[oid]
			if oid != 0 && st == "" {
				st = "filled"
				m.orderStates[oid] = st
			}
			m.mu.Unlock()
			if oid == 0 {
				respBody, _ = json.Marshal(map[string]any{"status": "unknownOid"})
			} else {
				respBody, _ = json.Marshal(map[string]any{
					"status": "order",
					"order": map[string]any{
						"status": st,
						"order": map[string]any{
							"oid":     oid,
							"cloid":   cloid,
							"sz":      "0.5",
							"limitPx": "3200.5",
						},
					},
				})
			}
		default:
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
	case "/exchange":
		act, _ := payload["action"].(map[string]any)
		actType, _ := act["type"].(string)
		m.mu.Lock()
		m.lastSubmitted = payload
		m.exchangeActions = append(m.exchangeActions, actType)
		switch actType {
		case "updateLeverage":
			reject := m.rejectLeverage
			if !reject {
				assetIdxFloat, _ := act["asset"].(float64)
				isCross, _ := act["isCross"].(bool)
				levFloat, _ := act["leverage"].(float64)
				coinMap := map[uint32]string{0: "BTC", 1: "ETH", 2: "SOL"}
				if coin, ok := coinMap[uint32(assetIdxFloat)]; ok {
					m.appliedLev[coin] = struct {
						leverage uint32
						isCross  bool
					}{leverage: uint32(levFloat), isCross: isCross}
				}
			}
			m.mu.Unlock()
			if reject {
				respBody, _ = json.Marshal(map[string]any{
					"status":   "err",
					"response": "leverage rejected by mock exchange",
				})
			} else {
				respBody, _ = json.Marshal(map[string]any{
					"status":   "ok",
					"response": map[string]any{"type": "default"},
				})
			}
		case "cancel":
			cancels, _ := act["cancels"].([]any)
			if len(cancels) > 0 {
				if first, ok := cancels[0].(map[string]any); ok {
					if o, ok := first["o"].(float64); ok {
						m.orderStates[uint64(o)] = "canceled"
					}
				}
			}
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
					if c, ok := first["c"].(string); ok {
						cloid = strings.ToLower(c)
					}
				}
			}
			if cloid != "" {
				m.orderByCloid[cloid] = oid
				m.cloidByOid[oid] = cloid
			}
			drop := m.dropOrderSubmit
			if drop {
				m.orderStates[oid] = "filled"
				m.mu.Unlock()
				return nil, errors.New("simulated network drop after order acceptance")
			}
			m.orderStates[oid] = "open"
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

func setupHyperliquidControlledDeps(t testing.TB, dir string) (*hyperliquid.Client, *signer.Vault, signer.HyperliquidPolicy, *coreHyperliquidMockTransport) {
	t.Helper()
	cassette, err := os.ReadFile(filepath.Join("..", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		cassette, err = os.ReadFile(filepath.Join("internal", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	vaultDir, err := os.MkdirTemp(dir, "hl-signer-")
	if err != nil {
		t.Fatal(err)
	}
	v, err := signer.Open(vaultDir, "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	pass := []byte("hyperliquid-core-passphrase-2026")
	if err := v.Create(pass); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	if err := v.ConfigureHyperliquidControlled(true); err != nil {
		_ = v.Close()
		t.Fatal(err)
	}
	rt := &coreHyperliquidMockTransport{
		marketsRaw:   cassette,
		nextOid:      7000,
		orderStates:  map[uint64]string{},
		orderByCloid: map[string]uint64{},
		cloidByOid:   map[uint64]string{},
		appliedLev: map[string]struct {
			leverage uint32
			isCross  bool
		}{},
	}
	client, err := hyperliquid.NewControlledClient("testnet", rt)
	if err != nil {
		_ = v.ConfigureHyperliquidControlled(false)
		_ = v.Close()
		t.Fatal(err)
	}
	policy := signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH", "SOL"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       10_000_000_000, // $10,000
		MaxOrderPriceBpsDeviation: 500,            // 5%
		ExpiresAt:                 time.Now().Unix() + 3600,
	}
	return client, v, policy, rt
}

func TestHyperliquidOrdinaryFailClosed(t *testing.T) {
	dir := fixtureDir(t)
	server, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(c)
	}()
	claimFixture(t, server.Store)

	v, err := server.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Hyperliquid != nil || v.FinancialExecution {
		t.Fatalf("ordinary core exposed Hyperliquid or financialExecution: %+v", v)
	}
	for _, action := range []string{"hyperliquid-refresh", "hyperliquid-review", "hyperliquid-submit", "hyperliquid-reconcile", "hyperliquid-cancel"} {
		if _, err := server.Store.ChangeAgents(t.Context(), AgentCommand{Action: action, Revision: v.Revision}); !errors.Is(err, signer.ErrUnavailable) {
			t.Fatalf("ordinary action %s got %v, want ErrUnavailable", action, err)
		}
	}

	// Non-controlled client cannot be registered.
	liveClient, err := hyperliquid.NewClient("testnet")
	if err != nil {
		t.Fatal(err)
	}
	_, vault, policy, _ := setupHyperliquidControlledDeps(t, fixtureDir(t))
	defer func() {
		_ = vault.ConfigureHyperliquidControlled(false)
		_ = vault.Close()
	}()
	if err := server.ConfigureHyperliquidControlled(liveClient, vault, policy); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatalf("accepted live non-controlled client: %v", err)
	}
}

func TestHyperliquidControlledCoreLifecycle(t *testing.T) {
	dir := fixtureDir(t)
	server, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(c)
	}()
	claimFixture(t, server.Store)

	client, vault, policy, rt := setupHyperliquidControlledDeps(t, fixtureDir(t))
	defer func() {
		_ = vault.ConfigureHyperliquidControlled(false)
		_ = vault.Close()
	}()
	if err := server.ConfigureHyperliquidControlled(client, vault, policy); err != nil {
		t.Fatal(err)
	}

	view, err := server.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Hyperliquid == nil || !view.Hyperliquid.Available || view.Hyperliquid.ActivationEnabled || view.Hyperliquid.LiveExecution {
		t.Fatalf("unexpected HyperliquidView flags: %+v", view.Hyperliquid)
	}
	if len(view.Hyperliquid.Markets) != 3 {
		t.Fatalf("expected 3 markets, got %d", len(view.Hyperliquid.Markets))
	}

	// Review an ETH limit order.
	revView, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: view.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "ETH",
				IsBuy:       true,
				LimitPrice:  "3200.5",
				Size:        "0.5",
				Leverage:    5,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatalf("hyperliquid-review failed: %v", err)
	}
	if revView.Hyperliquid == nil || revView.Hyperliquid.Review == nil || revView.Hyperliquid.Review.Digest == "" {
		t.Fatalf("missing HyperliquidReview: %+v", revView.Hyperliquid)
	}
	digest := revView.Hyperliquid.Review.Digest

	// Bogus digest rejected.
	if _, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: "deadbeef"},
	}); err == nil {
		t.Fatal("accepted unreviewed digest")
	}

	// Submit with reviewed digest succeeds and verifies signature against SignerAddress.
	subView, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: digest},
	})
	if err != nil {
		t.Fatalf("hyperliquid-submit failed: %v", err)
	}
	if len(subView.Hyperliquid.Orders) != 1 {
		t.Fatalf("expected 1 order record, got %d", len(subView.Hyperliquid.Orders))
	}
	ord := subView.Hyperliquid.Orders[0]
	if ord.State != "resting" || ord.OrderID != 7001 || ord.SignerAddress != view.Hyperliquid.SignerAddress {
		t.Fatalf("unexpected submitted order: %+v", ord)
	}
	if !strings.HasPrefix(ord.ClientOrderID, "0x") || len(ord.ClientOrderID) != 34 {
		t.Fatalf("expected auto-generated 16-byte hex cloid, got %q", ord.ClientOrderID)
	}
	sig, err := signer.ParseECDSASignature(ord.SignatureHex)
	if err != nil {
		t.Fatal(err)
	}
	dBytes, err := hex.DecodeString(strings.TrimPrefix(ord.TypedDataDigest, "0x"))
	if err != nil || len(dBytes) != 32 {
		t.Fatalf("invalid TypedDataDigest %q", ord.TypedDataDigest)
	}
	var d32 [32]byte
	copy(d32[:], dBytes)
	if err := signer.VerifySecp256k1Signature(ord.SignerAddress, d32, sig); err != nil {
		t.Fatalf("order ecrecover failed: %v", err)
	}

	// Single-use digest replay must fail.
	if _, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: digest},
	}); err == nil {
		t.Fatal("accepted replayed hyperliquid-submit digest")
	}

	// Transition order on mock exchange to filled and reconcile.
	rt.mu.Lock()
	rt.orderStates[ord.OrderID] = "filled"
	rt.mu.Unlock()
	recView, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-reconcile",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{OrderID: ord.OrderID},
	})
	if err != nil {
		t.Fatalf("hyperliquid-reconcile failed: %v", err)
	}
	if recView.Hyperliquid.Orders[0].State != "filled" {
		t.Fatalf("expected filled state after reconcile, got %s", recView.Hyperliquid.Orders[0].State)
	}

	// Cancel order and verify state becomes cancelled.
	canView, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-cancel",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{OrderID: ord.OrderID},
	})
	if err != nil {
		t.Fatalf("hyperliquid-cancel failed: %v", err)
	}
	if canView.Hyperliquid.Orders[0].State != "cancelled" {
		t.Fatalf("expected cancelled state, got %s", canView.Hyperliquid.Orders[0].State)
	}
	rt.mu.Lock()
	actions := slices.Clone(rt.exchangeActions)
	rt.mu.Unlock()
	if !slices.Equal(actions, []string{"updateLeverage", "order", "cancel"}) {
		t.Fatalf("expected exchange action sequence [updateLeverage order cancel], got %v", actions)
	}
}

func TestHyperliquidUpdateLeverageFailureAndDriftAbortOrder(t *testing.T) {
	dir := fixtureDir(t)
	server, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(c)
	}()
	claimFixture(t, server.Store)

	client, vault, policy, rt := setupHyperliquidControlledDeps(t, fixtureDir(t))
	defer func() {
		_ = vault.ConfigureHyperliquidControlled(false)
		_ = vault.Close()
	}()
	if err := server.ConfigureHyperliquidControlled(client, vault, policy); err != nil {
		t.Fatal(err)
	}

	view, err := server.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Exchange rejects updateLeverage -> submit aborts before sending order.
	rt.mu.Lock()
	rt.rejectLeverage = true
	rt.mu.Unlock()
	revView, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: view.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "ETH",
				IsBuy:       true,
				LimitPrice:  "3200.5",
				Size:        "0.5",
				Leverage:    5,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: revView.Hyperliquid.Review.Digest},
	}); err == nil {
		t.Fatal("expected hyperliquid-submit to fail when updateLeverage is rejected")
	}

	// 2. Post-updateLeverage FetchAccountState shows leverage drift on BTC position -> aborts before order.
	rt.mu.Lock()
	rt.rejectLeverage = false
	rt.driftLeverage = true
	rt.mu.Unlock()
	btcRevView, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: view.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "BTC",
				IsBuy:       true,
				LimitPrice:  "96250",
				Size:        "0.05",
				Leverage:    7,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: btcRevView.Hyperliquid.Review.Digest},
	}); err == nil {
		t.Fatal("expected hyperliquid-submit to abort on leverage/margin-mode drift")
	}

	rt.mu.Lock()
	actions := slices.Clone(rt.exchangeActions)
	rt.mu.Unlock()
	if slices.Contains(actions, "order") {
		t.Fatalf("order action leaked after updateLeverage failure/drift: %v", actions)
	}
}

func TestHyperliquidOrderPersistenceAndCloidReconcileAcrossRestart(t *testing.T) {
	dir := fixtureDir(t)
	vaultParent := fixtureDir(t)
	cassette, err := os.ReadFile(filepath.Join("..", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		cassette, err = os.ReadFile(filepath.Join("internal", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	pass := []byte("hyperliquid-restart-pass-2026")
	vaultID := "0123456789abcdef0123456789abcdef"

	v1, err := signer.Open(vaultParent, vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if err := v1.Create(pass); err != nil {
		t.Fatal(err)
	}
	if err := v1.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := v1.ConfigureHyperliquidControlled(true); err != nil {
		t.Fatal(err)
	}

	rt := &coreHyperliquidMockTransport{
		marketsRaw:   cassette,
		nextOid:      7000,
		orderStates:  map[uint64]string{},
		orderByCloid: map[string]uint64{},
		cloidByOid:   map[uint64]string{},
		appliedLev: map[string]struct {
			leverage uint32
			isCross  bool
		}{},
		dropOrderSubmit: true,
	}
	policy := signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH", "SOL"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       10_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 time.Now().Unix() + 3600,
	}

	s1, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	claimFixture(t, s1.Store)
	client1, err := hyperliquid.NewControlledClient("testnet", rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.ConfigureHyperliquidControlled(client1, v1, policy); err != nil {
		t.Fatal(err)
	}
	view1, err := s1.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	rev1, err := s1.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: view1.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "ETH",
				IsBuy:       true,
				LimitPrice:  "3200.5",
				Size:        "0.5",
				Leverage:    5,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sub1, err := s1.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view1.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: rev1.Hyperliquid.Review.Digest},
	})
	if err != nil {
		t.Fatalf("hyperliquid-submit on dropped network failed: %v", err)
	}
	if len(sub1.Hyperliquid.Orders) != 1 {
		t.Fatalf("expected 1 recorded unknown order, got %d", len(sub1.Hyperliquid.Orders))
	}
	droppedOrd := sub1.Hyperliquid.Orders[0]
	if droppedOrd.State != "unknown" || droppedOrd.OrderID != 0 || droppedOrd.ClientOrderID == "" {
		t.Fatalf("expected unknown order with cloid and oid=0, got %+v", droppedOrd)
	}
	if _, err := os.Stat(filepath.Join(dir, "hyperliquid", "orders.sqlite")); err != nil {
		t.Fatalf("orders.sqlite missing on disk: %v", err)
	}

	// Shut down first server and vault.
	closeCtx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s1.Close(closeCtx1)
	cancel1()
	_ = v1.ConfigureHyperliquidControlled(false)
	_ = v1.Close()

	// Restart server2 + reopened vault against same data directory.
	v2, err := signer.Open(vaultParent, vaultID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = v2.ConfigureHyperliquidControlled(false)
		_ = v2.Close()
	}()
	if err := v2.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := v2.ConfigureHyperliquidControlled(true); err != nil {
		t.Fatal(err)
	}
	s2, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_ = s2.Close(closeCtx2)
	}()
	client2, err := hyperliquid.NewControlledClient("testnet", rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.ConfigureHyperliquidControlled(client2, v2, policy); err != nil {
		t.Fatal(err)
	}

	view2, err := s2.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view2.Hyperliquid.Orders) != 1 || view2.Hyperliquid.Orders[0].ClientOrderID != droppedOrd.ClientOrderID || view2.Hyperliquid.Orders[0].State != "unknown" {
		t.Fatalf("persisted unknown order lost across restart: %+v", view2.Hyperliquid.Orders)
	}

	// Nonce allocation after restart must stay strictly above persisted last_nonce.
	rev2, err := s2.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-review",
		Revision: view2.Revision,
		Hyperliquid: &HyperliquidCommand{
			Request: &hyperliquid.OrderDraft{
				Asset:       "ETH",
				IsBuy:       true,
				LimitPrice:  "3200.5",
				Size:        "0.5",
				Leverage:    5,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev2.Hyperliquid.Review.LeverageIntent.Nonce <= droppedOrd.Nonce || rev2.Hyperliquid.Review.Preview.Intent.Nonce <= rev2.Hyperliquid.Review.LeverageIntent.Nonce {
		t.Fatalf("nonce did not remain strictly monotonic across restart: dropped=%d lev=%d ord=%d",
			droppedOrd.Nonce, rev2.Hyperliquid.Review.LeverageIntent.Nonce, rev2.Hyperliquid.Review.Preview.Intent.Nonce)
	}

	// Reconcile the dropped order exclusively by clientOrderId (OrderID == 0).
	recView, err := s2.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:   "hyperliquid-reconcile",
		Revision: view2.Revision,
		Hyperliquid: &HyperliquidCommand{
			ClientOrderID: droppedOrd.ClientOrderID,
		},
	})
	if err != nil {
		t.Fatalf("hyperliquid-reconcile by cloid failed: %v", err)
	}
	reconciled := recView.Hyperliquid.Orders[0]
	if reconciled.State != "filled" || reconciled.OrderID != 7001 || reconciled.ClientOrderID != droppedOrd.ClientOrderID {
		t.Fatalf("unexpected cloid-reconciled order: %+v", reconciled)
	}
}

// TestHyperliquidBrowserServer is an explicit Playwright driver for controlled Hyperliquid Perps E2E.
func TestHyperliquidBrowserServer(t *testing.T) {
	dir := os.Getenv("FASED_HYPERLIQUID_BROWSER_DIR")
	if dir == "" {
		t.Skip("explicit browser driver only")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, err := Start(ctx, dir, "127.0.0.1:0", "", fasedapp.UI())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(c)
	}()

	vaultParent, err := os.MkdirTemp("", "hl-browser-signer-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(vaultParent) }()
	client, vault, policy, rt := setupHyperliquidControlledDeps(t, vaultParent)
	defer func() {
		_ = vault.ConfigureHyperliquidControlled(false)
		_ = vault.Close()
	}()
	_ = rt
	if err := server.ConfigureHyperliquidControlled(client, vault, policy); err != nil {
		t.Fatal(err)
	}
	claimed, err := server.Store.Claimed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		claimFixture(t, server.Store)
	}

	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	ready := map[string]any{
		"event":         "listening",
		"url":           server.URL,
		"instanceId":    server.Store.instanceID,
		"ownerPassword": testPassword,
	}
	b, _ := json.Marshal(ready)
	fmt.Println(string(b))
	select {
	case <-ctx.Done():
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	}
}
