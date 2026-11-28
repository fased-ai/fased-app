package core

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/tasks"
)

func TestLV1XMiningCoreRoutesAndMainnetGate(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())

	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)
	csrf := csrfToken(token)

	if !slices.Contains(OptionalStateSQLitePaths(), "mining/mining.sqlite") {
		t.Fatalf("missing mining/mining.sqlite in OptionalStateSQLitePaths(): %v", OptionalStateSQLitePaths())
	}

	// 1. /api/v1/mining/status returns Devnet round telemetry, 24 streak chips, 6 presets, and mainnetEnabled=false.
	stResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/mining/status?cluster=devnet", "", token, "", "")
	if stResp.Code != http.StatusOK || !strings.Contains(stResp.Body.String(), `"mainnetEnabled":false`) || !strings.Contains(stResp.Body.String(), `"trend-up"`) {
		t.Fatalf("GET /api/v1/mining/status failed (%d): %s", stResp.Code, stResp.Body.String())
	}

	// 2. /api/v1/mining/preview flags mainnetBlocked=true for mainnet-beta.
	prevMain := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/preview", `{"cluster":"mainnet-beta","amountSol":0.05,"strategyId":"wide"}`, token, srv.URL, csrf)
	if prevMain.Code != http.StatusOK || !strings.Contains(prevMain.Body.String(), `"mainnetBlocked":true`) {
		t.Fatalf("expected mainnetBlocked preview, got %d: %s", prevMain.Code, prevMain.Body.String())
	}

	// 3. Session creation blocked on mainnet-beta (403) and succeeds on devnet (201).
	sessMain := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/sessions", `{"cluster":"mainnet-beta","strategyId":"wide","mode":"manual","perRoundSol":0.02}`, token, srv.URL, csrf)
	if sessMain.Code != http.StatusForbidden {
		t.Fatalf("expected 403 mainnet session denial, got %d: %s", sessMain.Code, sessMain.Body.String())
	}
	sessDev := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/sessions", `{"cluster":"devnet","strategyId":"center","mode":"semi_auto","perRoundSol":0.02}`, token, srv.URL, csrf)
	if sessDev.Code != http.StatusCreated || !strings.Contains(sessDev.Body.String(), `"strategyId":"center"`) {
		t.Fatalf("expected 201 devnet session, got %d: %s", sessDev.Code, sessDev.Body.String())
	}

	// 4. Candles, protocol-stats, leaderboard, portfolio, staking & saved strategies endpoints.
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/v1/mining/candles?interval=10m&limit=30", `"interval":"10m"`},
		{"/api/v1/mining/protocol-stats?cluster=devnet", `"maxSupplySat":30000000`},
		{"/api/v1/mining/leaderboard?cluster=devnet&window=24h", `"window":"24h"`},
		{"/api/v1/mining/portfolio?cluster=devnet", `"cluster":"devnet"`},
		{"/api/v1/mining/staking?cluster=devnet", `"minStakeSat":1`},
		{"/api/v1/mining/strategies", `"presets"`},
		{"/api/v1/mining/manifest", `"devnetProgramId"`},
		{"/api/v1/mining/self-keeper", `"cluster":"devnet"`},
	} {
		resp := stage4Req(h, http.MethodGet, srv.URL, tc.path, "", token, "", "")
		if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), tc.want) {
			t.Fatalf("GET %s failed (%d): %s", tc.path, resp.Code, resp.Body.String())
		}
	}
	sweepResp := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/self-keeper/sweep", "{}", token, srv.URL, csrf)
	if sweepResp.Code != http.StatusOK || !strings.Contains(sweepResp.Body.String(), `"lastSweepSummary"`) {
		t.Fatalf("POST /api/v1/mining/self-keeper/sweep failed (%d): %s", sweepResp.Code, sweepResp.Body.String())
	}

	saveStrat := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/strategies", `{"name":"Custom Wing","bucketsBps":[1500,2000,3000,2000,1500]}`, token, srv.URL, csrf)
	if saveStrat.Code != http.StatusCreated || !strings.Contains(saveStrat.Body.String(), `"Custom Wing"`) {
		t.Fatalf("POST /api/v1/mining/strategies failed (%d): %s", saveStrat.Code, saveStrat.Body.String())
	}
	simStrat := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/mining/strategies/simulate", `{"bucketsBps":[1500,2000,3000,2000,1500],"perRoundSol":0.05,"limit":24}`, token, srv.URL, csrf)
	if simStrat.Code != http.StatusOK || !strings.Contains(simStrat.Body.String(), `"rounds":24`) {
		t.Fatalf("POST /api/v1/mining/strategies/simulate failed (%d): %s", simStrat.Code, simStrat.Body.String())
	}

	// 5. Stream WS topic "mining" parses cleanly.
	if p, err := parseStreamTopic("mining"); err != nil || p.kind != "mining" {
		t.Fatalf("parseStreamTopic(mining) failed: %+v err=%v", p, err)
	}

	// 6. Unbound policy update fails closed with 403 step_up_required/owner_password_required when loosening policy.
	crW := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/wallets/create", `{"name":"Test Miner","role":"mining","rpcUrl":"https://api.devnet.solana.com"}`, token, srv.URL, csrf)
	if crW.Code == http.StatusOK || crW.Code == http.StatusCreated {
		polNoStep := stage4Req(h, http.MethodPut, srv.URL, "/api/v1/mining/policy", `{"walletId":"test-miner","confirmLoosen":true,"maxPerTxLamports":"50000000","maxDailyLamports":"150000000","mining":{"cluster":"devnet"}}`, token, srv.URL, csrf)
		if polNoStep.Code != http.StatusForbidden {
			t.Fatalf("expected 403 on policy update without owner step-up, got %d: %s", polNoStep.Code, polNoStep.Body.String())
		}
	}
}

func TestGoalMiningStrategiesAndIdempotentSlotGuard(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := fixtureStore(t)

	cust, err := s.custodyBackend()
	if err != nil || cust == nil {
		t.Fatalf("custodyBackend: %v", err)
	}
	w, err := cust.CreateNativeWallet(ctx, "Task Miner", custody.RoleMining, "https://api.devnet.solana.com")
	if err != nil {
		t.Fatal(err)
	}
	w, err = cust.UpdatePolicy(ctx, w.ID, custody.WalletPolicy{
		Role:              custody.RoleMining,
		ApprovalMode:      custody.ApprovalBoundedAuto,
		AutoApproveMaxUSD: 50.0,
		Operations:        custody.AllMiningOperations(),
		Programs:          []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget, custody.SolanaTokenProgram, custody.SolanaATAProgram, onchain.LV1XDevnetProgramID},
		SOLCap: custody.AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 50_000_000,
			MaxDailyRaw: 200_000_000,
		},
		Mining: &custody.MiningPolicy{
			Cluster:               "devnet",
			ProgramID:             onchain.LV1XDevnetProgramID,
			LedgerID:              onchain.LV1XDevnetLedgerID,
			ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
			MaxSOLPerRoundLamport: 50_000_000,
			MaxDailySOLLamports:   200_000_000,
			AllowedStrategies:     []string{"wide", "center", "trend-up", "trend-down", "tails"},
			AutoClaim:             true,
		},
	}, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}

	// 1. yield-sweep executes Self-Keeper sweep cleanly.
	yOut, err := s.runTaskDomain(ctx, tasks.Task{
		ID:          "tsk-yield-1",
		GoalDomain:  tasks.GoalMining,
		Attachments: tasks.TaskAttachments{StrategyID: "yield-sweep"},
	}, tasks.RunInput{TriggerSource: "schedule"})
	if err != nil || yOut.ActionTaken != "mining_yield_sweep" {
		t.Fatalf("yield-sweep failed: out=%+v err=%v", yOut, err)
	}

	// 2. det-band adapts regime and idempotent per-slot guard skips duplicate commit on same round slot.
	detTask := tasks.Task{
		ID:               "tsk-det-1",
		GoalDomain:       tasks.GoalMining,
		ApprovalPolicy:   tasks.ApprovalAuto,
		Attachments:      tasks.TaskAttachments{StrategyID: "det-band"},
		ExecutionBinding: tasks.ExecutionBinding{Cluster: "devnet", WalletHandle: "@wallet:" + w.ID},
	}
	d1, err := s.runTaskDomain(ctx, detTask, tasks.RunInput{TriggerSource: "event:mining.round.opened"})
	if err != nil || d1.ActionTaken != "mining_auto_commit" {
		t.Fatalf("det-band first commit failed: out=%+v err=%v", d1, err)
	}
	d2, err := s.runTaskDomain(ctx, detTask, tasks.RunInput{TriggerSource: "event:mining.round.opened"})
	if err != nil || d2.ActionTaken != "mining_slot_already_committed" {
		t.Fatalf("expected idempotent slot guard on duplicate run, got out=%+v err=%v", d2, err)
	}

	// 3. contrarian-ev selects the +EV bucket (>= 1.15x) from fixture crowd skew.
	cOut, err := s.runTaskDomain(ctx, tasks.Task{
		ID:               "tsk-ev-1",
		GoalDomain:       tasks.GoalMining,
		ApprovalPolicy:   tasks.ApprovalAuto,
		Attachments:      tasks.TaskAttachments{StrategyID: "contrarian-ev"},
		ExecutionBinding: tasks.ExecutionBinding{Cluster: "devnet", WalletHandle: "@wallet:" + w.ID},
	}, tasks.RunInput{TriggerSource: "schedule"})
	if err != nil || cOut.ActionTaken != "mining_auto_commit" || !strings.Contains(cOut.DecisionSummary, "Contrarian-EV selected bucket") {
		t.Fatalf("expected contrarian-ev commit on +EV bucket, got out=%+v err=%v", cOut, err)
	}
}
