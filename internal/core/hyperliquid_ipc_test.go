package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/signer"
)

func appSignerRelay(v *signer.Vault) func(context.Context, signer.Request) (signer.Response, error) {
	return func(_ context.Context, r signer.Request) (signer.Response, error) {
		if !signer.AppActionAllowed(r.Action) {
			return signer.Response{}, errors.New("owner administration denied on app channel")
		}
		switch r.Action {
		case "hyperliquid-status":
			st, err := v.HyperliquidStatus()
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{HyperliquidStatus: st}, nil
		case "hyperliquid-sign-leverage":
			if r.HyperliquidLeverage == nil {
				return signer.Response{}, errors.New("leverage intent required")
			}
			signed, err := v.SignHyperliquidUpdateLeverageIPC(*r.HyperliquidLeverage, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{HyperliquidSigned: signed}, nil
		case "hyperliquid-sign-order":
			if r.HyperliquidOrder == nil {
				return signer.Response{}, errors.New("order intent required")
			}
			signed, err := v.SignHyperliquidOrderIPC(*r.HyperliquidOrder, r.AgentVaultID)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{HyperliquidSigned: signed}, nil
		case "hyperliquid-sign-cancel":
			if r.HyperliquidCancel == nil {
				return signer.Response{}, errors.New("cancel intent required")
			}
			signed, err := v.SignHyperliquidCancelIPC(*r.HyperliquidCancel)
			if err != nil {
				return signer.Response{}, err
			}
			return signer.Response{HyperliquidSigned: signed}, nil
		default:
			return signer.Response{}, signer.ErrUnavailable
		}
	}
}

func TestHyperliquidCoreOverSignerIPCWithAgentVault(t *testing.T) {
	dir := fixtureDir(t)
	signerDir := fixtureDir(t)
	cassette, err := os.ReadFile(filepath.Join("..", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	if err != nil {
		cassette, err = os.ReadFile(filepath.Join("internal", "hyperliquid", "testdata", "meta_and_asset_ctxs.json"))
	}
	if err != nil {
		t.Fatal(err)
	}

	signerID := "0123456789abcdef0123456789abcdef"
	pass := []byte("hl-core-ipc-passphrase-2026")
	vaultID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	v1, err := signer.Open(signerDir, signerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := v1.Create(pass); err != nil {
		_ = v1.Close()
		t.Fatal(err)
	}
	if err := v1.Unlock(pass, 10*time.Minute); err != nil {
		_ = v1.Close()
		t.Fatal(err)
	}
	if err := v1.ConfigureHyperliquidControlled(true); err != nil {
		_ = v1.Close()
		t.Fatal(err)
	}

	now := time.Now().Unix()
	policy := signer.HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       10_000_000_000,
		MaxOrderPriceBpsDeviation: 500,
		ExpiresAt:                 now + 3600,
	}
	if err := v1.SetHyperliquidPolicy(policy); err != nil {
		t.Fatalf("SetHyperliquidPolicy: %v", err)
	}
	vaultPolicy := signer.AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "desk-agent-1",
		AllowedVenues:      []string{"hyperliquid"},
		AllowedAssets:      []string{"ETH"},
		MaxOrderMicroUSD:   2_000_000_000, // $2,000
		DailySpendMicroUSD: 2_500_000_000, // $2,500
		DailyLossMicroUSD:  500_000_000,   // $500
		MaxLeverage:        5,
		ExpiresAt:          now + 3600,
	}
	if err := v1.SetAgentVaultPolicy(vaultPolicy); err != nil {
		t.Fatalf("SetAgentVaultPolicy: %v", err)
	}

	rt := &coreHyperliquidMockTransport{
		marketsRaw:   cassette,
		nextOid:      8000,
		orderStates:  map[uint64]string{},
		orderByCloid: map[string]uint64{},
		cloidByOid:   map[uint64]string{},
		appliedLev: map[string]struct {
			leverage uint32
			isCross  bool
		}{},
	}
	client1, err := hyperliquid.NewControlledClient("testnet", rt)
	if err != nil {
		t.Fatal(err)
	}

	s1, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	claimFixture(t, s1.Store)
	if err := s1.ConfigureHyperliquidIPC(client1, appSignerRelay(v1), vaultID); err != nil {
		t.Fatalf("ConfigureHyperliquidIPC: %v", err)
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
				Size:        "0.5", // $1,600.25 <= $2,000 maxOrder & $2,500 daily
				Leverage:    5,
				TimeInForce: "Gtc",
			},
		},
	})
	if err != nil {
		t.Fatalf("hyperliquid-review #1: %v", err)
	}
	sub1, err := s1.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view1.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: rev1.Hyperliquid.Review.Digest},
	})
	if err != nil {
		t.Fatalf("hyperliquid-submit #1 over IPC failed: %v", err)
	}
	if len(sub1.Hyperliquid.Orders) != 1 || sub1.Hyperliquid.Orders[0].OrderID != 8001 || sub1.Hyperliquid.Orders[0].ClientOrderID == "" {
		t.Fatalf("unexpected submitted order #1 over IPC: %+v", sub1.Hyperliquid.Orders)
	}
	ord1Nonce := sub1.Hyperliquid.Orders[0].Nonce

	// Second $1,600.25 order brings cumulative spend to $3,200.50 (> $2,500 cap) -> signer rejects BEFORE order submission!
	rev2, err := s1.Store.ChangeAgents(t.Context(), AgentCommand{
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
	if _, err := s1.Store.ChangeAgents(t.Context(), AgentCommand{
		Action:      "hyperliquid-submit",
		Revision:    view1.Revision,
		Hyperliquid: &HyperliquidCommand{Digest: rev2.Hyperliquid.Review.Digest},
	}); err == nil {
		t.Fatal("expected signer IPC agent vault daily spend cap to reject second order")
	}

	rt.mu.Lock()
	orderCount := 0
	for _, act := range rt.exchangeActions {
		if act == "order" {
			orderCount++
		}
	}
	rt.mu.Unlock()
	if orderCount != 1 {
		t.Fatalf("expected exactly 1 /exchange order after second order rejected by signer IPC, got %d (%v)", orderCount, rt.exchangeActions)
	}

	// Shut down BOTH Core and Signer.
	closeCtx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s1.Close(closeCtx1)
	cancel1()
	_ = v1.ConfigureHyperliquidControlled(false)
	_ = v1.Close()

	// Reopen brand-new Signer + brand-new Core against persisted state directories.
	v2, err := signer.Open(signerDir, signerID)
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
	st2, err := v2.HyperliquidStatus()
	if err != nil || st2.Policy == nil || st2.LastNonce < ord1Nonce || len(st2.Vaults) != 1 || st2.Vaults[0].DailySpentMicroUSD != 1_600_250_000 {
		t.Fatalf("signer restart lost policy/nonce/vault spend: %+v err=%v", st2, err)
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
	if err := s2.ConfigureHyperliquidIPC(client2, appSignerRelay(v2), vaultID); err != nil {
		t.Fatalf("s2.ConfigureHyperliquidIPC: %v", err)
	}
	view2, err := s2.Store.Agents(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view2.Hyperliquid.Orders) != 1 || view2.Hyperliquid.Orders[0].OrderID != 8001 || view2.Hyperliquid.Orders[0].ClientOrderID != sub1.Hyperliquid.Orders[0].ClientOrderID {
		t.Fatalf("Core restart lost persisted SQLite order: %+v", view2.Hyperliquid.Orders)
	}
	_ = slices.Contains(rt.exchangeActions, "order")
}
