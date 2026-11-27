package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/tasks"
	"github.com/fased-ai/fased-app/internal/trade"
)

func TestStage4TradeRouterCEXAndEquitiesExecutionProfilesAndKillSwitch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := fixtureStore(t)
	s.providerFixture = true

	// Attach a Task Engine listener to verify sub-second "trade.fill" emission on live orders.
	eng, err := s.tasksEngine()
	if err != nil {
		t.Fatalf("tasksEngine: %v", err)
	}
	subCh, unsub := eng.Subscribe()
	defer unsub()

	fillTask, err := eng.CreateTask(ctx, tasks.Task{
		Name:           "Trade Fill Watcher",
		GoalDomain:     tasks.GoalTrading,
		GoalObjective:  "Log every live venue fill",
		ScheduleKind:   tasks.ScheduleEvent,
		ScheduleSpec:   tasks.ScheduleSpec{EventFilter: "trade.fill"},
		ExecMode:       tasks.ExecStandalone,
		ApprovalPolicy: tasks.ApprovalSemiAuto,
		Status:         tasks.StatusActive,
	})
	if err != nil {
		t.Fatalf("CreateTask fill watcher: %v", err)
	}

	// 1. Execution profile OFF must fail closed for both CEX and equities live routes.
	if _, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "kraken", Symbol: "SOL", Side: "buy", Quantity: 1, Strategy: trade.StrategyLimit, EntryPrice: 50, Mode: trade.ModeManual, AccountID: "plugin:kraken",
	}, 50); err == nil || !strings.Contains(err.Error(), "execution-profile=off") {
		t.Fatalf("expected execution-profile=off error for kraken, got %v", err)
	}

	// 2. Testnet profile routes kraken (CEX) and alpaca (equities) through sandbox/paper paths cleanly.
	testnetSigner := func(_ context.Context, req signer.Request) (signer.Response, error) {
		if req.Action == "hyperliquid-status" {
			return signer.Response{}, signer.ErrUnavailable
		}
		return signer.Response{Status: signer.Status{
			TestnetExecution: true,
			Account:          &signer.AccountRef{Network: "devnet", PublicKey: "11111111111111111111111111111111"},
		}}, nil
	}
	if err := s.ConfigureTestnetExecution(ctx, TestnetExecutionConfig{
		Profile:      signer.ExecutionProfileTestnet,
		AgentVaultID: "0123456789abcdef0123456789abcdef",
		SignerCall:   testnetSigner,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution(testnet): %v", err)
	}

	recKrak, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "kraken", Symbol: "SOL", Side: "buy", Quantity: 1, Strategy: trade.StrategyLimit, EntryPrice: 50, Mode: trade.ModeManual, AccountID: "plugin:kraken",
	}, 50)
	if err != nil || !strings.HasPrefix(recKrak.VenueOrderID, "cex-") {
		t.Fatalf("expected testnet kraken cex receipt, got rec=%+v err=%v", recKrak, err)
	}
	recAlp, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: "buy", Quantity: 1, Strategy: trade.StrategyLimit, EntryPrice: 120, Mode: trade.ModeManual, AccountID: "plugin:alpaca",
	}, 120)
	if err != nil || !strings.HasPrefix(recAlp.VenueOrderID, "eq-") {
		t.Fatalf("expected testnet alpaca equities receipt, got rec=%+v err=%v", recAlp, err)
	}

	// Verify trade.fill triggered the Fill Watcher task asynchronously.
	deadline := time.After(2 * time.Second)
	foundFillRun := false
	for !foundFillRun {
		select {
		case ev := <-subCh:
			if ev.TaskID == fillTask.ID && ev.Run != nil && strings.Contains(ev.Run.TriggerSource, "trade.fill") {
				foundFillRun = true
			}
		case <-deadline:
			runs, _ := eng.Store().ListRuns(ctx, fillTask.ID, 5)
			for _, r := range runs {
				if strings.Contains(r.TriggerSource, "trade.fill") {
					foundFillRun = true
					break
				}
			}
			if !foundFillRun {
				t.Fatalf("expected trade.fill push run on %s", fillTask.ID)
			}
		}
	}

	// 3. Mainnet-capped allows orders <= MaxOrderMicroUSD and blocks orders > cap.
	mainnetSigner := func(_ context.Context, req signer.Request) (signer.Response, error) {
		if req.Action == "hyperliquid-status" {
			return signer.Response{}, signer.ErrUnavailable
		}
		return signer.Response{Status: signer.Status{
			MainnetCapped:           true,
			MainnetMaxOrderMicroUSD: 100_000_000,
			Account:                 &signer.AccountRef{Network: "mainnet-beta", PublicKey: "11111111111111111111111111111111"},
		}}, nil
	}
	if err := s.ConfigureTestnetExecution(ctx, TestnetExecutionConfig{
		Profile: signer.ExecutionProfileMainnetCapped,
		MainnetPolicy: signer.MainnetCappedPolicy{
			Confirmed:        true,
			MaxOrderMicroUSD: 100_000_000,
			MaxDailyMicroUSD: 250_000_000,
		},
		AgentVaultID: "0123456789abcdef0123456789abcdef",
		SignerCall:   mainnetSigner,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution(mainnet-capped): %v", err)
	}
	defer s.clearStage4Runtime()

	recCapOK, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "kraken", Symbol: "BTC-USD", Side: "buy", Quantity: 1, Strategy: trade.StrategyLimit, EntryPrice: 80, Mode: trade.ModeManual, AccountID: "plugin:kraken",
	}, 80)
	if err != nil || !strings.HasPrefix(recCapOK.VenueOrderID, "cex-") {
		t.Fatalf("expected mainnet-capped kraken order <= $100 to succeed, got rec=%+v err=%v", recCapOK, err)
	}
	if _, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "kraken", Symbol: "BTC-USD", Side: "buy", Quantity: 2, Strategy: trade.StrategyLimit, EntryPrice: 80, Mode: trade.ModeManual, AccountID: "plugin:kraken",
	}, 80); err == nil || !strings.Contains(err.Error(), "exceeds mainnet-capped limit") {
		t.Fatalf("expected mainnet-capped notional cap violation for kraken, got %v", err)
	}
	if _, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "alpaca", Symbol: "SPY", Side: "buy", Quantity: 1, Strategy: trade.StrategyLimit, EntryPrice: 200, Mode: trade.ModeManual, AccountID: "plugin:alpaca",
	}, 200); err == nil || !strings.Contains(err.Error(), "exceeds mainnet-capped limit") {
		t.Fatalf("expected mainnet-capped notional cap violation for alpaca, got %v", err)
	}

	// 4. Global kill switch immediately blocks CEX/equities live routing.
	tsSvc, err := s.tradeService()
	if err != nil {
		t.Fatalf("tradeService: %v", err)
	}
	if _, err := tsSvc.SetKillSwitch(ctx, true, "cert halt", "owner"); err != nil {
		t.Fatalf("SetKillSwitch: %v", err)
	}
	if _, err := s.executeLiveTradeRoute(ctx, trade.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: "buy", Quantity: 1, Strategy: trade.StrategyLimit, EntryPrice: 50, Mode: trade.ModeManual, AccountID: "plugin:alpaca",
	}, 50); err == nil {
		t.Fatal("expected kill switch to block equities live routing")
	}
}
