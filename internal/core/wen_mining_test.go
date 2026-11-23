package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenpm1"
)

type controlledWENVenue struct {
	mu              sync.Mutex
	submissions     int
	submitUncertain bool
	submitError     error
	reconcileState  string
	reconcileCalls  int
	submittedOps    map[string]bool
	dir             string
	instanceID      string
	salts           map[string]map[uint8]wenpm1.Key
}

func newControlledWENVenue() *controlledWENVenue {
	return &controlledWENVenue{
		reconcileState: "confirmed",
		submittedOps:   map[string]bool{},
		salts:          map[string]map[uint8]wenpm1.Key{},
	}
}

func newControlledWENVenueForStore(s *Store) *controlledWENVenue {
	return &controlledWENVenue{
		reconcileState: "confirmed",
		submittedOps:   map[string]bool{},
		dir:            s.dir,
		instanceID:     s.instanceID,
		salts:          map[string]map[uint8]wenpm1.Key{},
	}
}

func (v *controlledWENVenue) Prepare(_ context.Context, op WENMiningOperation) (WENMiningPrepared, uint64, string, error) {
	prepared, salts, slot, err := BuildWENMiningPreparedWithSalts(op)
	if err != nil {
		return WENMiningPrepared{}, 0, "", err
	}
	if v.dir != "" {
		if err := saveWENMiningSaltsAt(v.dir, v.instanceID, op.ID, salts); err != nil {
			return WENMiningPrepared{}, 0, "", err
		}
	} else {
		v.mu.Lock()
		if v.salts == nil {
			v.salts = map[string]map[uint8]wenpm1.Key{}
		}
		v.salts[op.ID] = salts
		v.mu.Unlock()
	}
	return prepared, slot, "5000", nil
}

func (v *controlledWENVenue) Submit(_ context.Context, op WENMiningOperation, _ WENMiningPrepared) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.submissions++
	v.submittedOps[op.ID] = true
	if v.submitUncertain {
		return true, errors.New("ambiguous controlled submission result")
	}
	if v.submitError != nil {
		return false, v.submitError
	}
	return true, nil
}

func (v *controlledWENVenue) Reconcile(_ context.Context, op WENMiningOperation, prepared WENMiningPrepared) (WENMiningObservation, error) {
	v.mu.Lock()
	v.reconcileCalls++
	state := v.reconcileState
	dir := v.dir
	instanceID := v.instanceID
	salts := v.salts[op.ID]
	v.mu.Unlock()
	switch state {
	case "unknown":
		return WENMiningObservation{State: "unknown", Slot: op.Slot}, nil
	case "failed":
		return WENMiningObservation{State: "failed", Slot: op.Slot}, nil
	default:
		if dir != "" {
			loaded, err := loadWENMiningSaltsAt(dir, instanceID, op.ID)
			if err != nil {
				return WENMiningObservation{}, err
			}
			salts = loaded
		}
		return ProjectWENMiningReceiptWithSalts(op, prepared, salts)
	}
}

func wenMiningTestStrategy(perRound, totalTurnover, maxUnsettled string) string {
	return fmt.Sprintf(`{
  "schema": "wen-strategy/1",
  "name": "Devnet Range Mining",
  "description": "Controlled WEN mining contract for engineering acceptance",
  "market": {
    "networkGenesisHash": "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG",
    "programId": "11111111111111111111111111111111",
    "economyId": "wen-devnet-pm1-v1",
    "assetMint": "So11111111111111111111111111111111111111112",
    "termsHash": "%s",
    "interfaceVersion": "pm1-v1"
  },
  "research": {
    "thesis": "custom",
    "horizonMinutes": 60,
    "conditions": [],
    "outlook": null
  },
  "allocation": {
    "preset": "center",
    "weightsBps": [500, 1500, 6000, 1500, 500],
    "mode": "fixed",
    "rules": []
  },
  "schedule": {
    "startsAt": "2026-10-22T00:00:00Z",
    "expiresAt": "2026-10-22T04:00:00Z",
    "everyNthRound": 1,
    "anchorRound": "0",
    "utcWindows": [],
    "selectedRoundIds": [],
    "skipRoundIds": [],
    "maxRoundsTotal": 12,
    "maxRoundsPerUtcDay": 12,
    "cooldownRounds": 0,
    "randomSelection": null
  },
  "limits": {
    "perRoundLamports": %q,
    "totalTurnoverLamports": %q,
    "dailyTurnoverLamports": %q,
    "maxUnsettledLamports": %q,
    "maxUserFeesLamports": "1000000",
    "maxUserFeesPerRoundLamports": "50000",
    "maxLossLamports": %q
  },
  "execution": {
    "mode": "review",
    "runner": "fased",
    "mandateId": null
  }
}`, strings.Repeat("a", 64), perRound, totalTurnover, totalTurnover, maxUnsettled, totalTurnover)
}

type wenMiningHarness struct {
	dir            string
	store          *Store
	venue          *controlledWENVenue
	account        Account
	ref            signer.AccountRef
	strategyDigest string
	now            time.Time
	recoveryHeld   bool
	updateHeld     bool
}

func setupWENMiningHarness(t *testing.T, perRound, totalTurnover, maxUnsettled string) *wenMiningHarness {
	t.Helper()
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	claimFixture(t, s)

	ref := testRef()
	ref.Version = 2
	ref.Network = "devnet"
	h := &wenMiningHarness{
		dir:   dir,
		store: s,
		venue: newControlledWENVenueForStore(s),
		ref:   ref,
		now:   now,
	}
	s.rpcAdmission = true
	s.custodyNetwork = "devnet"
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "status" {
			cur := h.ref
			return signer.Response{Status: signer.Status{
				FixtureOnly:      true,
				Locked:           true,
				RecoveryRequired: h.recoveryHeld,
				UpdateHeld:       h.updateHeld,
				Account:          &cur,
			}}, nil
		}
		return signer.Response{}, errors.New("unexpected signer action")
	}
	accountsView, err := s.SetupAccount(t.Context(), AccountSetupCommand{
		Action:    "attach",
		Name:      "Devnet Miner",
		Network:   "devnet",
		Reference: &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	account := accountsView.Record.Accounts[0]
	accountsView, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: account.ID,
		Revision:  accountsView.Record.Revision,
		Endpoint:  "https://devnet.helius-rpc.com",
		Network:   "devnet",
	})
	if err != nil {
		t.Fatal(err)
	}
	h.account = accountsView.Record.Accounts[0]

	lib, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{
		Revision: 0,
		Profile:  "wen-operator",
		Action:   "save",
		Raw:      wenMiningTestStrategy(perRound, totalTurnover, maxUnsettled),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.strategyDigest = lib.Versions[0].Digest
	if err := s.ConfigureControlledWENMining(func(Account) (ControlledWENMiningVenue, error) {
		return h.venue, nil
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *wenMiningHarness) reviewAndSubmit(t *testing.T, amount, walletBudget, maxOp string) (AgentView, string) {
	t.Helper()
	agents, err := h.store.Agents(t.Context(), "wen-operator", "")
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{
			Request: &WENMiningRequest{
				AccountID:            h.account.ID,
				StrategyDigest:       h.strategyDigest,
				AmountLamports:       amount,
				WalletBudgetLamports: walletBudget,
				MaxOperationLamports: maxOp,
				PolicyExpiresAt:      h.now.Unix() + 3600,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.WENMining == nil || reviewed.WENMining.Review == nil {
		t.Fatal("missing review")
	}
	rev := reviewed.WENMining.Review
	submitted, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  reviewed.Revision,
		Action:    "wen-mining-submit",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{
			ID:     rev.Operation.ID,
			Digest: rev.Digest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return submitted, wenMiningPrefix + rev.Operation.ID
}

func TestWENMiningDisabledByDefault(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	claimFixture(t, s)
	v, err := s.Agents(t.Context(), "wen-operator", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.WENMining != nil {
		t.Fatal("ordinary core advertised WEN mining")
	}
	snap, err := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(snap.State.Features, wenMiningFeature) {
		t.Fatal("empty core emitted wen-mining feature")
	}
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{
		Revision:  v.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{}},
	}); err == nil {
		t.Fatal("disabled mining accepted review")
	}
}

func TestWENMiningReviewedAuthorityOneOperationReceiptAndNoDuplicate(t *testing.T) {
	t.Parallel()
	h := setupWENMiningHarness(t, "500000000", "1000000000", "500000000")
	submitted, taskID := h.reviewAndSubmit(t, "200000000", "1000000000", "500000000")

	if submitted.WENMining.MiningActivation || submitted.WENMining.LiveExecution {
		t.Fatal("mining activation or live execution enabled")
	}
	// Duplicate submit of the same review must be refused.
	opID := strings.TrimPrefix(taskID, wenMiningPrefix)
	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  submitted.Revision,
		Action:    "wen-mining-submit",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: opID, Digest: strings.Repeat("0", 64)},
	}); err == nil {
		t.Fatal("duplicate submit accepted")
	}

	// Worker advances queued -> confirmed with verified PM1 v1 receipt.
	if err := h.store.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Repeated worker ticks must NEVER execute a second time.
	if err := h.store.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.venue.submissions != 1 {
		t.Fatalf("expected 1 submission, got %d", h.venue.submissions)
	}

	view, err := h.store.Agents(t.Context(), "wen-operator", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.WENMining.Tasks) != 1 || len(view.WENMining.Runs) != 1 {
		t.Fatalf("unexpected tasks/runs: %d/%d", len(view.WENMining.Tasks), len(view.WENMining.Runs))
	}
	run := view.WENMining.Runs[0].WENMining
	if run.State != "confirmed" || !run.Attempted || run.Observation == nil || run.Observation.Receipt == nil {
		t.Fatalf("expected confirmed PM1 receipt, got %+v", run)
	}
	if run.Observation.Receipt.Amount != 120000000 || len(run.Observation.Receipts) != 5 || view.WENMining.Tasks[0].State != "completed" {
		t.Fatalf("receipt mismatch: receipt=%+v count=%d", run.Observation.Receipt, len(run.Observation.Receipts))
	}
	var receiptSum uint64
	for _, rec := range run.Observation.Receipts {
		receiptSum += rec.Amount
	}
	if receiptSum != 200000000 {
		t.Fatalf("expected 5-range receipt sum 200000000, got %d", receiptSum)
	}
	if len(view.Notices) != 1 || view.Notices[0].RunID != taskID+":1" {
		t.Fatalf("expected one notice for confirmed mining run, got %+v", view.Notices)
	}
	// Conditional feature guard is emitted once a WENMining run exists.
	snap, err := h.store.UpdateSnapshot(t.Context(), update.NewProbe())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snap.State.Features, wenMiningFeature) {
		t.Fatal("missing wen-mining feature guard in snapshot")
	}
}

func TestWENMiningLimitsAndAuthorityRefusals(t *testing.T) {
	t.Parallel()
	h := setupWENMiningHarness(t, "500000000", "600000000", "500000000")
	agents, _ := h.store.Agents(t.Context(), "wen-operator", "")

	// 1. Exceeds wallet per-operation limit
	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{
			AccountID:            h.account.ID,
			StrategyDigest:       h.strategyDigest,
			AmountLamports:       "400000000",
			WalletBudgetLamports: "1000000000",
			MaxOperationLamports: "300000000",
			PolicyExpiresAt:      h.now.Unix() + 3600,
		}},
	}); err == nil {
		t.Fatal("operation exceeding wallet per-operation limit accepted")
	}

	// 2. Exceeds strategy perRoundLamports (500M)
	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{
			AccountID:            h.account.ID,
			StrategyDigest:       h.strategyDigest,
			AmountLamports:       "600000000",
			WalletBudgetLamports: "1000000000",
			MaxOperationLamports: "1000000000",
			PolicyExpiresAt:      h.now.Unix() + 3600,
		}},
	}); err == nil {
		t.Fatal("operation exceeding strategy perRoundLamports accepted")
	}

	// 3. Cumulative wallet lifetime budget enforcement across operations
	_, _ = h.reviewAndSubmit(t, "400000000", "500000000", "400000000")
	if err := h.store.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	agents, _ = h.store.Agents(t.Context(), "wen-operator", "")
	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{
			AccountID:            h.account.ID,
			StrategyDigest:       h.strategyDigest,
			AmountLamports:       "200000000",
			WalletBudgetLamports: "500000000",
			MaxOperationLamports: "400000000",
			PolicyExpiresAt:      h.now.Unix() + 3600,
		}},
	}); err == nil {
		t.Fatal("second operation exceeding cumulative wallet lifetime budget accepted")
	}

	// 4. Signer epoch rotation between submit and worker execution holds without submitting
	h2 := setupWENMiningHarness(t, "500000000", "1000000000", "500000000")
	_, taskID := h2.reviewAndSubmit(t, "200000000", "1000000000", "500000000")
	h2.ref.Epoch = strings.Repeat("9", 32)
	if err := h2.store.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h2.venue.submissions != 0 {
		t.Fatalf("stale epoch dispatched %d times", h2.venue.submissions)
	}
	v2, _ := h2.store.Agents(t.Context(), "wen-operator", "")
	if v2.WENMining.Runs[0].WENMining.State != "binding-held" {
		t.Fatalf("expected binding-held, got %s", v2.WENMining.Runs[0].WENMining.State)
	}
	// Held unattempted op can be explicitly cancelled.
	if _, err := h2.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  v2.Revision,
		Action:    "wen-mining-cancel",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: taskID},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWENMiningUncertainReconciliationAndRestartWithoutDuplicate(t *testing.T) {
	t.Parallel()
	h := setupWENMiningHarness(t, "500000000", "1000000000", "500000000")
	h.venue.submitUncertain = true
	h.venue.reconcileState = "unknown"

	_, taskID := h.reviewAndSubmit(t, "200000000", "1000000000", "500000000")
	if err := h.store.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.venue.submissions != 1 {
		t.Fatalf("expected 1 submission, got %d", h.venue.submissions)
	}
	v, _ := h.store.Agents(t.Context(), "wen-operator", "")
	if v.WENMining.Runs[0].WENMining.State != "unknown" || !v.WENMining.Runs[0].WENMining.Attempted {
		t.Fatalf("expected unknown attempted state, got %+v", v.WENMining.Runs[0].WENMining)
	}
	// Cancel MUST be refused once attempted.
	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  v.Revision,
		Action:    "wen-mining-cancel",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: taskID},
	}); err == nil {
		t.Fatal("attempted unknown operation was cancelled")
	}

	// Simulate full core restart (new Store + new bootID).
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.now = func() time.Time { return h.now.Add(10 * time.Second) }
	restarted.rpcAdmission = true
	restarted.custodyNetwork = "devnet"
	restarted.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		cur := h.ref
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, Account: &cur}}, nil
	}
	if err := restarted.ConfigureControlledWENMining(func(Account) (ControlledWENMiningVenue, error) {
		return h.venue, nil
	}); err != nil {
		t.Fatal(err)
	}
	// Advancing worker after restart MUST NOT resubmit!
	if err := restarted.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.venue.submissions != 1 {
		t.Fatalf("restart caused duplicate submission: %d", h.venue.submissions)
	}

	// Explicit reconcile while venue still uncertain keeps unknown.
	v, _ = restarted.Agents(t.Context(), "wen-operator", "")
	v, err = restarted.ChangeAgents(t.Context(), AgentCommand{
		Revision:  v.Revision,
		Action:    "wen-mining-reconcile",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: taskID},
	})
	if err != nil || v.WENMining.Runs[0].WENMining.State != "unknown" {
		t.Fatalf("expected unknown during uncertain reconcile, got %v %+v", err, v.WENMining)
	}

	// Venue now finalizes receipt -> explicit reconcile transitions unknown -> confirmed!
	h.venue.reconcileState = "confirmed"
	v, err = restarted.ChangeAgents(t.Context(), AgentCommand{
		Revision:  v.Revision,
		Action:    "wen-mining-reconcile",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: taskID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.venue.submissions != 1 {
		t.Fatalf("reconcile triggered extra submission: %d", h.venue.submissions)
	}
	run := v.WENMining.Runs[0].WENMining
	if run.State != "confirmed" || run.Observation == nil || run.Observation.Receipt == nil || v.WENMining.Tasks[0].State != "completed" {
		t.Fatalf("expected reconciled confirmed state, got %+v", run)
	}
}

func TestWENMiningRestartRecoverInFlightQueuedAndSending(t *testing.T) {
	t.Parallel()
	// Case A: restart while queued -> expires cleanly, zero submissions.
	h := setupWENMiningHarness(t, "500000000", "1000000000", "500000000")
	_, _ = h.reviewAndSubmit(t, "200000000", "1000000000", "500000000")
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ConfigureControlledWENMining(func(Account) (ControlledWENMiningVenue, error) {
		return h.venue, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ := restarted.Agents(t.Context(), "wen-operator", "")
	if v.WENMining.Runs[0].WENMining.State != "expired" || h.venue.submissions != 0 {
		t.Fatalf("queued task did not expire safely on restart: state=%s subs=%d", v.WENMining.Runs[0].WENMining.State, h.venue.submissions)
	}

	// Case B: restart while sending (crash after Attempted=true) -> recovers to unknown, never re-submits, reconciles cleanly!
	h2 := setupWENMiningHarness(t, "500000000", "1000000000", "500000000")
	_, taskID2 := h2.reviewAndSubmit(t, "200000000", "1000000000", "500000000")
	if err := h2.store.writeWENMining(t.Context(), "wen-operator", taskID2, func(_ *AgentTask, r *AgentRun) error {
		wenMiningEvent(r.WENMining, "validating", h2.now.Unix())
		wenMiningEvent(r.WENMining, "prepared", h2.now.Unix())
		r.WENMining.Attempted = true
		wenMiningEvent(r.WENMining, "sending", h2.now.Unix())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h2.store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted2, err := OpenStore(h2.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted2.Close()
	if err := restarted2.ConfigureControlledWENMining(func(Account) (ControlledWENMiningVenue, error) {
		return h2.venue, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := restarted2.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	v2, _ := restarted2.Agents(t.Context(), "wen-operator", "")
	if v2.WENMining.Runs[0].WENMining.State != "unknown" || h2.venue.submissions != 0 {
		t.Fatalf("mid-send crash did not hold as unknown: state=%s subs=%d", v2.WENMining.Runs[0].WENMining.State, h2.venue.submissions)
	}
	v2, err = restarted2.ChangeAgents(t.Context(), AgentCommand{
		Revision:  v2.Revision,
		Action:    "wen-mining-reconcile",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: taskID2},
	})
	if err != nil || v2.WENMining.Runs[0].WENMining.State != "confirmed" || h2.venue.submissions != 0 {
		t.Fatalf("post-crash reconcile failed: err=%v state=%s subs=%d", err, v2.WENMining.Runs[0].WENMining.State, h2.venue.submissions)
	}
}

// TestWENMiningBrowserServer is an explicit disposable Playwright driver for
// joined UI + CLI controlled WEN mining verification.
func TestWENMiningBrowserServer(t *testing.T) {
	dir := os.Getenv("FASED_WEN_MINING_BROWSER_DIR")
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
	ref := testRef()
	ref.Version = 2
	ref.Network = "devnet"
	server.Store.rpcAdmission = true
	server.Store.custodyNetwork = "devnet"
	server.Store.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		cur := ref
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, Account: &cur}}, nil
	}
	venue := newControlledWENVenueForStore(server.Store)
	modeFile := filepath.Join(dir, "venue-mode")
	readMode := func() string {
		b, _ := os.ReadFile(modeFile)
		return strings.TrimSpace(string(b))
	}
	dynamicVenue := &dynamicWENVenue{inner: venue, mode: readMode}
	if err = server.ConfigureControlledWENMining(func(Account) (ControlledWENMiningVenue, error) {
		return dynamicVenue, nil
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := server.Store.Claimed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		claimFixture(t, server.Store)
		accountsView, err := server.Store.SetupAccount(ctx, AccountSetupCommand{
			Action:    "attach",
			Name:      "Devnet Miner",
			Network:   "devnet",
			Reference: &ref,
		})
		if err != nil {
			t.Fatal(err)
		}
		a := accountsView.Record.Accounts[0]
		if _, err = server.Store.ConfigureAccountRPC(ctx, RPCConfigInput{
			AccountID: a.ID,
			Revision:  accountsView.Record.Revision,
			Endpoint:  "https://devnet.helius-rpc.com",
			Network:   "devnet",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = server.Store.ChangeWENResearch(ctx, WENResearchCommand{
			Revision: 0,
			Profile:  "wen-operator",
			Action:   "save",
			Raw:      wenMiningTestStrategy("500000000", "1000000000", "500000000"),
		}); err != nil {
			t.Fatal(err)
		}
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

type dynamicWENVenue struct {
	inner *controlledWENVenue
	mode  func() string
}

func (d *dynamicWENVenue) Prepare(ctx context.Context, op WENMiningOperation) (WENMiningPrepared, uint64, string, error) {
	return d.inner.Prepare(ctx, op)
}
func (d *dynamicWENVenue) Submit(ctx context.Context, op WENMiningOperation, p WENMiningPrepared) (bool, error) {
	d.inner.mu.Lock()
	m := d.mode()
	d.inner.submitUncertain = m == "uncertain"
	d.inner.mu.Unlock()
	return d.inner.Submit(ctx, op, p)
}
func (d *dynamicWENVenue) Reconcile(ctx context.Context, op WENMiningOperation, p WENMiningPrepared) (WENMiningObservation, error) {
	d.inner.mu.Lock()
	m := d.mode()
	if m == "uncertain" {
		d.inner.reconcileState = "unknown"
	} else {
		d.inner.reconcileState = "confirmed"
	}
	d.inner.mu.Unlock()
	return d.inner.Reconcile(ctx, op, p)
}

func TestWENMiningRandomCommitmentsAndEncryptedSaltStorage(t *testing.T) {
	t.Parallel()
	h := setupWENMiningHarness(t, "500000000", "2000000000", "500000000")

	reviewOnce := func() *WENMiningReview {
		agents, err := h.store.Agents(t.Context(), "wen-operator", "")
		if err != nil {
			t.Fatal(err)
		}
		v, err := h.store.ChangeAgents(t.Context(), AgentCommand{
			Revision:  agents.Revision,
			Action:    "wen-mining-review",
			ProfileID: "wen-operator",
			WENMining: &WENMiningCommand{Request: &WENMiningRequest{
				AccountID:            h.account.ID,
				StrategyDigest:       h.strategyDigest,
				AmountLamports:       "200000000",
				WalletBudgetLamports: "1000000000",
				MaxOperationLamports: "500000000",
				PolicyExpiresAt:      h.now.Unix() + 3600,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return v.WENMining.Review
	}

	r1 := reviewOnce()
	r2 := reviewOnce()
	if r1.Prepared.Commitment == r2.Prepared.Commitment || r1.Prepared.Nonce == r2.Prepared.Nonce {
		t.Fatalf("expected non-deterministic commitments/nonces across reviews: r1=%+v r2=%+v", r1.Prepared, r2.Prepared)
	}

	salts1, err := h.store.LoadWENMiningSalts(r1.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(salts1) != 5 {
		t.Fatalf("expected 5 salts persisted, got %d", len(salts1))
	}

	// Verify key and encrypted salt files use 0600 permissions and bind AAD to instanceID.
	for _, name := range []string{"wen-mining-salt.key", "wen-mining-salts.json"} {
		st, err := os.Stat(filepath.Join(h.dir, name))
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if perm := st.Mode().Perm(); perm != 0600 {
			t.Fatalf("%s permissions=%#o, want 0600", name, perm)
		}
	}
	if _, err := loadWENMiningSaltsAt(h.dir, "wrong-instance", r1.Operation.ID); err == nil {
		t.Fatal("expected AAD failure when decrypting salt with wrong instanceID")
	}

	// Verify ProjectWENMiningReceipt refuses projection without persisted random salts.
	if _, err := ProjectWENMiningReceipt(r1.Operation, r1.Prepared); err == nil {
		t.Fatal("ProjectWENMiningReceipt unexpectedly succeeded without salts")
	}

	// Submit r1 and confirm plaintext salts never leak into SQLite or public view JSON.
	agents, _ := h.store.Agents(t.Context(), "wen-operator", "")
	submitted, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-submit",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: r1.Operation.ID, Digest: r1.Digest},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewJSON, _ := json.Marshal(submitted)
	saltFileBytes, _ := os.ReadFile(filepath.Join(h.dir, "wen-mining-salts.json"))
	sqliteBytes, _ := os.ReadFile(filepath.Join(h.dir, "core.sqlite"))
	for idx, salt := range salts1 {
		saltHex := hex.EncodeToString(salt[:])
		if bytes.Contains(viewJSON, []byte(saltHex)) || bytes.Contains(saltFileBytes, []byte(saltHex)) || bytes.Contains(sqliteBytes, []byte(saltHex)) {
			t.Fatalf("plaintext salt for range %d leaked to disk/view", idx)
		}
	}

	// Cancel unattempted task and verify salt entry is deleted.
	taskID := wenMiningPrefix + r1.Operation.ID
	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  submitted.Revision,
		Action:    "wen-mining-cancel",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: taskID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.LoadWENMiningSalts(r1.Operation.ID); err == nil {
		t.Fatal("expected salt deletion after cancelling operation")
	}
}

func TestWENMiningFiveRangeCommitmentsAndReceipts(t *testing.T) {
	t.Parallel()
	h := setupWENMiningHarness(t, "500000000", "1000000000", "500000000")

	agents, _ := h.store.Agents(t.Context(), "wen-operator", "")
	revView, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{
			AccountID:            h.account.ID,
			StrategyDigest:       h.strategyDigest,
			AmountLamports:       "200000000",
			WalletBudgetLamports: "1000000000",
			MaxOperationLamports: "500000000",
			PolicyExpiresAt:      h.now.Unix() + 3600,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rev := revView.WENMining.Review
	if len(rev.Prepared.Entries) != 5 {
		t.Fatalf("expected 5 prepared range entries, got %d", len(rev.Prepared.Entries))
	}
	// 1 Open + 5 Commit + 5 Reveal = 11 instructions.
	if len(rev.Prepared.Instructions) != 11 {
		t.Fatalf("expected 11 PM1 instructions (Open + 5 Commit + 5 Reveal), got %d", len(rev.Prepared.Instructions))
	}
	wantAllocs := []string{"10000000", "30000000", "120000000", "30000000", "10000000"}
	for i, want := range wantAllocs {
		if rev.Prepared.Entries[i].AmountLamports != want || rev.Operation.AllocationsLamports[i] != want {
			t.Fatalf("range %d allocation=%s/%s want %s", i, rev.Prepared.Entries[i].AmountLamports, rev.Operation.AllocationsLamports[i], want)
		}
	}

	if _, err := h.store.ChangeAgents(t.Context(), AgentCommand{
		Revision:  revView.Revision,
		Action:    "wen-mining-submit",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{ID: rev.Operation.ID, Digest: rev.Digest},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := h.store.Agents(t.Context(), "wen-operator", "")
	run := after.WENMining.Runs[0].WENMining
	if run.Observation == nil || len(run.Observation.Receipts) != 5 || run.Observation.Receipt == nil {
		t.Fatalf("expected 5-range observation receipts, got %+v", run.Observation)
	}
	expectedOwner := wenMiningOwnerKey(rev.Operation, wenpm1.Key{})
	var sum uint64
	for i, rec := range run.Observation.Receipts {
		sum += rec.Amount
		if rec.Owner != expectedOwner {
			t.Fatalf("receipt %d owner=%s want %s", i, rec.Owner.String(), expectedOwner.String())
		}
		if rec.Selection == nil || *rec.Selection != uint8(i) {
			t.Fatalf("receipt %d selection=%v want %d", i, rec.Selection, i)
		}
	}
	if sum != 200000000 || run.Observation.Receipt.Amount != 120000000 {
		t.Fatalf("unexpected 5-range totals: sum=%d primary=%d", sum, run.Observation.Receipt.Amount)
	}

	// Negative check: verifyWENMiningObservation rejects missing or tampered receipts.
	tampered := *run.Observation
	tampered.Receipts = append([]wenpm1.Receipt{}, run.Observation.Receipts[:4]...)
	if err := verifyWENMiningObservation(rev.Operation, rev.Prepared, tampered); err == nil {
		t.Fatal("verifyWENMiningObservation accepted incomplete 4/5 receipt list")
	}
}

func TestWENMiningUTCDayRolloverAndHoldsAccounting(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	curNow := time.Date(2026, 11, 20, 23, 55, 0, 0, time.UTC)
	s.now = func() time.Time { return curNow }
	claimFixture(t, s)

	ref := testRef()
	ref.Version = 2
	ref.Network = "devnet"
	venue := newControlledWENVenueForStore(s)
	venue.submitUncertain = true
	venue.reconcileState = "unknown"
	s.rpcAdmission = true
	s.custodyNetwork = "devnet"
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		cur := ref
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, Account: &cur}}, nil
	}
	accountsView, err := s.SetupAccount(t.Context(), AccountSetupCommand{
		Action:    "attach",
		Name:      "Devnet Miner",
		Network:   "devnet",
		Reference: &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	account := accountsView.Record.Accounts[0]
	accountsView, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: account.ID,
		Revision:  accountsView.Record.Revision,
		Endpoint:  "https://devnet.helius-rpc.com",
		Network:   "devnet",
	})
	if err != nil {
		t.Fatal(err)
	}
	account = accountsView.Record.Accounts[0]

	raw := strings.Replace(wenMiningTestStrategy("500000000", "1000000000", "1000000000"), `"dailyTurnoverLamports": "1000000000"`, `"dailyTurnoverLamports": "500000000"`, 1)
	lib, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{
		Revision: 0,
		Profile:  "wen-operator",
		Action:   "save",
		Raw:      raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	strategyDigest := lib.Versions[0].Digest
	if err := s.ConfigureControlledWENMining(func(Account) (ControlledWENMiningVenue, error) {
		return venue, nil
	}); err != nil {
		t.Fatal(err)
	}

	h := &wenMiningHarness{
		dir:            dir,
		store:          s,
		venue:          venue,
		account:        account,
		ref:            ref,
		strategyDigest: strategyDigest,
		now:            curNow,
	}
	// Submit 400M on 2026-11-20T23:55:00Z and leave it unsettled (unknown).
	_, _ = h.reviewAndSubmit(t, "400000000", "1000000000", "500000000")
	if err := s.advanceWENMining(t.Context()); err != nil {
		t.Fatal(err)
	}

	d, _, err := s.swapSnapshot(t.Context(), "wen-operator")
	if err != nil {
		t.Fatal(err)
	}
	spent, unsettled, dailyTurnover, feesPaid, feeHolds := wenMiningWalletLedger(d, account.ID, "", curNow.Unix())
	if spent != 0 || unsettled != 400000000 || dailyTurnover != 400000000 || feesPaid != 0 || feeHolds != 5000 {
		t.Fatalf("unexpected day-1 ledger: spent=%d unsettled=%d daily=%d feesPaid=%d feeHolds=%d", spent, unsettled, dailyTurnover, feesPaid, feeHolds)
	}

	// Same UTC day: 200M exceeds dailyTurnoverLamports (400M + 200M > 500M).
	agents, _ := s.Agents(t.Context(), "wen-operator", "")
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{
			AccountID:            account.ID,
			StrategyDigest:       strategyDigest,
			AmountLamports:       "200000000",
			WalletBudgetLamports: "1000000000",
			MaxOperationLamports: "500000000",
			PolicyExpiresAt:      curNow.Unix() + 3600,
		}},
	}); err == nil {
		t.Fatal("operation exceeding dailyTurnoverLamports accepted on same UTC day")
	}

	// Advance clock across 00:00Z -> DailyTurnoverLamports resets to 0 while TotalTurnover retains 400M.
	curNow = curNow.Add(10 * time.Minute)
	h.now = curNow
	spent, unsettled, dailyTurnover, feesPaid, feeHolds = wenMiningWalletLedger(d, account.ID, "", curNow.Unix())
	if spent != 0 || unsettled != 400000000 || dailyTurnover != 0 || feesPaid != 0 || feeHolds != 5000 {
		t.Fatalf("unexpected rollover ledger: spent=%d unsettled=%d daily=%d feesPaid=%d feeHolds=%d", spent, unsettled, dailyTurnover, feesPaid, feeHolds)
	}
	// New 400M fits within lifetime budget (400M + 400M <= 1B) AND fresh daily cap (400M <= 500M).
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{
		Revision:  agents.Revision,
		Action:    "wen-mining-review",
		ProfileID: "wen-operator",
		WENMining: &WENMiningCommand{Request: &WENMiningRequest{
			AccountID:            account.ID,
			StrategyDigest:       strategyDigest,
			AmountLamports:       "400000000",
			WalletBudgetLamports: "1000000000",
			MaxOperationLamports: "500000000",
			PolicyExpiresAt:      curNow.Unix() + 3600,
		}},
	}); err != nil {
		t.Fatalf("expected review allowed after UTC midnight rollover, got: %v", err)
	}
}
