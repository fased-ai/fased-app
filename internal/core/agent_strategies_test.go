package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func strategyFixture(t *testing.T) (*Store, StrategyPlan) {
	t.Helper()
	s := fixtureStore(t)
	s.now = func() time.Time { return time.Unix(1800000000, 0) }
	s.rpcAdmission = true
	accounts, e := s.Accounts(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	account := Account{Version: 2, ID: strings.Repeat("a", 32), Name: "Paper wallet", Mode: "watch", Network: "devnet", PublicKey: strings.Repeat("1", 64)}
	accounts.Record.Accounts = append(accounts.Record.Accounts, account)
	if _, e = s.UpdateAccounts(t.Context(), accounts.Record); e != nil {
		t.Fatal(e)
	}
	s.rpcAdmission = false
	return s, StrategyPlan{Version: strategyPlanVersion, AccountID: account.ID, Network: "devnet", Market: "SOL/USDC", QuantityAtoms: 100_000_000, LimitPriceMicros: 150_000_000, OrderBudgetMicros: 20_000_000, TotalBudgetMicros: 30_000_000}
}
func strategyApply(t *testing.T, s *Store, c AgentCommand) AgentView {
	t.Helper()
	if c.ProfileID == "" {
		c.ProfileID = "trading-analyst"
	}
	view, e := s.Agents(t.Context(), c.ProfileID, "")
	if e != nil {
		t.Fatal(e)
	}
	c.Revision = view.Revision
	view, e = s.ChangeAgents(t.Context(), c)
	if e != nil {
		t.Fatal(c.Action, e)
	}
	return view
}
func saveStrategyFixture(t *testing.T, s *Store, p StrategyPlan) AgentStrategy {
	t.Helper()
	v := strategyApply(t, s, AgentCommand{Action: "strategy-save", Name: "SOL accumulation", Strategy: &p})
	return v.Strategies[len(v.Strategies)-1]
}
func strategyReviewFixture(t *testing.T, s *Store, strategy AgentStrategy, runs int) *TaskReview {
	t.Helper()
	v := strategyApply(t, s, AgentCommand{Action: "task-review", Name: "Paper accumulation", StrategyID: strategy.ID, StrategyVersion: len(strategy.Versions), IntervalSeconds: 60, MaxRuns: runs})
	if v.TaskReview == nil {
		t.Fatal("review missing")
	}
	return v.TaskReview
}
func strategyTaskFixture(t *testing.T, s *Store, strategy AgentStrategy, runs int) AgentTask {
	t.Helper()
	r := strategyReviewFixture(t, s, strategy, runs)
	v := strategyApply(t, s, AgentCommand{Action: "task-reviewed-create", Name: r.Name, StrategyID: strategy.ID, StrategyVersion: len(strategy.Versions), IntervalSeconds: r.IntervalSeconds, MaxRuns: r.MaxRuns, ConsentDigest: r.Digest, ExpiresAt: r.ExpiresAt})
	return v.Tasks[len(v.Tasks)-1]
}
func TestStrategyReviewedTaskRunPauseBudgetAndDiscuss(t *testing.T) {
	s, p := strategyFixture(t)
	strategy := saveStrategyFixture(t, s, p)
	task := strategyTaskFixture(t, s, strategy, 4)
	v := strategyApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	if len(v.Runs) != 1 || v.Runs[0].Outcome != "paper-filled" || v.Runs[0].Paper.CostMicros != 12_500_000 || v.Strategies[0].Versions[0].SpentMicros != 12_500_000 || len(v.AIRequests) != 0 || v.FinancialExecution {
		t.Fatal("bounded result missing", v.Runs)
	}
	v = strategyApply(t, s, AgentCommand{Action: "task-pause", ID: task.ID})
	if v.Tasks[0].State != "paused" {
		t.Fatal("pause missing")
	}
	s.now = func() time.Time { return time.Unix(1800000120, 0) }
	if e := s.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, e := s.Agents(t.Context(), "trading-analyst", "")
	if e != nil || len(v.Runs) != 1 {
		t.Fatal("paused task ran", e)
	}
	strategyApply(t, s, AgentCommand{Action: "task-resume", ID: task.ID})
	v = strategyApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	v = strategyApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	if len(v.Runs) != 3 || v.Runs[2].Outcome != "budget-held" || v.Tasks[0].State != "held" || v.Strategies[0].Versions[0].SpentMicros != 25_000_000 {
		t.Fatal("allocation exceeded", v.Runs)
	}
	v = strategyApply(t, s, AgentCommand{Action: "task-run-explain", ID: v.Runs[0].ID})
	if !strings.Contains(v.Messages[len(v.Messages)-1].Text, strategy.ID) || !strings.Contains(v.Messages[len(v.Messages)-1].Text, "12500000") {
		t.Fatal("paper provenance absent from discussion")
	}
}
func TestStrategyVersionsRevokeOldTaskAndConsent(t *testing.T) {
	s, p := strategyFixture(t)
	strategy := saveStrategyFixture(t, s, p)
	task := strategyTaskFixture(t, s, strategy, 2)
	old := strategyReviewFixture(t, s, strategy, 1)
	p.LimitPriceMicros = 140_000_000
	v := strategyApply(t, s, AgentCommand{Action: "strategy-save", ID: strategy.ID, Name: "Edited accumulation", Strategy: &p})
	if len(v.Strategies[0].Versions) != 2 || v.Strategies[0].Versions[0].Digest != strategy.Versions[0].Digest || v.Tasks[0].State != "held" {
		t.Fatal("edit rewrote old approval")
	}
	for _, c := range []AgentCommand{
		{Action: "task-run", ID: task.ID}, {Action: "task-resume", ID: task.ID},
		{Action: "task-review", Name: "old", StrategyID: strategy.ID, StrategyVersion: 1, IntervalSeconds: 60, MaxRuns: 1},
		{Action: "task-reviewed-create", Name: old.Name, StrategyID: strategy.ID, StrategyVersion: 2, IntervalSeconds: old.IntervalSeconds, MaxRuns: old.MaxRuns, ConsentDigest: old.Digest, ExpiresAt: old.ExpiresAt},
		{Action: "task-review", ProfileID: "news-researcher", Name: "cross", StrategyID: strategy.ID, StrategyVersion: 2, IntervalSeconds: 60, MaxRuns: 1},
	} {
		profile := c.ProfileID
		if profile == "" {
			profile = "trading-analyst"
		}
		v, e := s.Agents(t.Context(), profile, "")
		if e != nil {
			t.Fatal(e)
		}
		c.ProfileID = profile
		c.Revision = v.Revision
		if _, e = s.ChangeAgents(t.Context(), c); e == nil {
			t.Fatal("stale/cross-profile authority accepted", c.Action)
		}
	}
}
func TestStrategyPriceCapAndOrdinaryAdmission(t *testing.T) {
	for _, mode := range []string{"price", "cap", "disabled", "wallet", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			s, p := strategyFixture(t)
			if mode == "price" {
				p.LimitPriceMicros = 100_000_000
			}
			if mode == "cap" {
				p.OrderBudgetMicros = 1_000_000
			}
			strategy := saveStrategyFixture(t, s, p)
			if mode == "disabled" {
				s.providerFixture = false
				r := strategyReviewFixture(t, s, strategy, 1)
				v, e := s.Agents(t.Context(), "trading-analyst", "")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "task-reviewed-create", ProfileID: v.ProfileID, Revision: v.Revision, Name: r.Name, StrategyID: strategy.ID, StrategyVersion: 1, IntervalSeconds: 60, MaxRuns: 1, ExpiresAt: r.ExpiresAt, ConsentDigest: r.Digest}); e == nil {
					t.Fatal("ordinary paper admission opened")
				}
				s.providerFixture = true
			}
			task := strategyTaskFixture(t, s, strategy, 2)
			if mode == "disabled" {
				s.providerFixture = false
			}
			if mode == "wallet" {
				a, e := s.Accounts(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				a.Record.Accounts = nil
				if _, e = s.UpdateAccounts(t.Context(), a.Record); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "expiry" {
				s.now = func() time.Time { return time.Unix(task.StrategyPlan.ExpiresAt, 0) }
			}
			v := strategyApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
			expected := map[string]string{"price": "price-skipped", "cap": "order-cap-held", "disabled": "paper-unavailable", "wallet": "binding-held", "expiry": "expired-held"}[mode]
			if v.Runs[0].Outcome != expected || v.Strategies[0].Versions[0].SpentMicros != 0 || v.Runs[0].Paper.CostMicros != 0 || len(v.AIRequests) != 0 {
				t.Fatal("incorrect refusal/skip", v.Runs)
			}
		})
	}
}
func TestStrategySharedAllocationConcurrentTicksAndRestart(t *testing.T) {
	s, p := strategyFixture(t)
	p.TotalBudgetMicros = 20_000_000
	strategy := saveStrategyFixture(t, s, p)
	strategyTaskFixture(t, s, strategy, 1)
	strategyTaskFixture(t, s, strategy, 1)
	strategyApply(t, s, AgentCommand{Action: "task-create", Name: "Independent reminder", Text: "Still runs", IntervalSeconds: 60, MaxRuns: 1})
	s.now = func() time.Time { return time.Unix(1800000060, 0) }
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.RunAgentTasks(context.Background()); e != nil {
				mu.Lock()
				errors = append(errors, e)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errors) > 0 {
		t.Fatal(errors)
	}
	v, e := s.Agents(t.Context(), "trading-analyst", "")
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Runs) != 3 || v.Strategies[0].Versions[0].SpentMicros != 12_500_000 {
		t.Fatal("duplicate dispatch or spend", v.Runs)
	}
	doc, rev, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(doc)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	after, gotRev, e := readAgents(t.Context(), restored.db)
	raw, _ := json.Marshal(after)
	if e != nil || rev != gotRev || string(before) != string(raw) {
		t.Fatal("restart rewrote records", e)
	}
	if e = restored.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	after, _, e = readAgents(t.Context(), restored.db)
	if e != nil || len(after.Runs) != 3 {
		t.Fatal("restart replayed", e)
	}
}
func TestStrategyStoragePreservesLedgerAndCannotBecomeReminder(t *testing.T) {
	s, p := strategyFixture(t)
	strategy := saveStrategyFixture(t, s, p)
	task := strategyTaskFixture(t, s, strategy, 1)
	strategyApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	d, _, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = savedPlainReminder(d, "trading-analyst", task.ID); e == nil || archiveEligible(d.Tasks[0]) {
		t.Fatal("strategy treated as plain reminder")
	}
	raw, _ := json.Marshal(d)
	var altered agentDocument
	json.Unmarshal(raw, &altered)
	altered.Strategies[0].Versions[0].SpentMicros = 0
	bad, _ := json.Marshal(altered)
	if validateStrategyStorage(bad, altered) == nil {
		t.Fatal("erased spend accepted")
	}
	json.Unmarshal(raw, &altered)
	altered.Runs[0].Paper.BudgetBeforeMicros--
	altered.Runs[0].Paper.BudgetAfterMicros--
	bad, _ = json.Marshal(altered)
	if validateStrategyStorage(bad, altered) == nil {
		t.Fatal("false remaining allocation accepted")
	}
}
func TestStrategyMigrationPreserves29AndRefusesUnprotectedState(t *testing.T) {
	for _, mode := range []string{"normal", "unprotected", "paused"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := strategyFixture(t)
			strategyApply(t, s, AgentCommand{Action: "session-create", Name: "Keep this draft"})
			strategyApply(t, s, AgentCommand{Action: "memory-put", Text: "Existing preference", Source: "owner"})
			var before string
			var revision int64
			if e := s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&before, &revision); e != nil {
				t.Fatal(e)
			}
			if mode == "unprotected" {
				before = strings.TrimSuffix(before, "}") + `,"strategies":[]}`
				if _, e := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", before); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "paused" {
				if _, e := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.db.Exec("PRAGMA user_version=29"); e != nil {
				t.Fatal(e)
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			next, e := OpenStore(s.dir)
			if mode != "normal" {
				if e == nil {
					next.Close()
					t.Fatal("unsafe migration accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			var after string
			var got int64
			var schema int
			if e = next.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&after, &got); e != nil {
				t.Fatal(e)
			}
			next.db.QueryRow("PRAGMA user_version").Scan(&schema)
			if schema != 30 || before != after || revision != got {
				t.Fatal("format-only migration rewrote old records")
			}
			// No authoritative originals are stored under an ephemeral test path.
			if _, e = os.Stat(filepath.Join(s.dir, "core.sqlite")); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestStrategyQuoteProvenanceAndInputLimits(t *testing.T) {
	for _, mode := range []string{"stale", "future", "network", "market", "source", "price"} {
		t.Run(mode, func(t *testing.T) {
			s, p := strategyFixture(t)
			strategy := saveStrategyFixture(t, s, p)
			task := strategyTaskFixture(t, s, strategy, 1)
			d, _, e := readAgents(t.Context(), s.db)
			if e != nil {
				t.Fatal(e)
			}
			accounts, e := s.accountsRecord(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			now := s.now().Unix()
			q := compiledStrategyQuote(*task.StrategyPlan, now)
			switch mode {
			case "stale":
				q.ObservedAt = now - 61
			case "future":
				q.ObservedAt = now + 1
			case "network":
				q.Network = "mainnet-beta"
			case "market":
				q.Market = "OTHER/USDC"
			case "source":
				q.Source = "untrusted"
			case "price":
				q.PriceMicros = -1
			}
			if e = runStrategyTask(&d, 0, now, true, accounts, q); e != nil {
				t.Fatal(e)
			}
			if d.Runs[0].Outcome != "price-held" || d.Strategies[0].Versions[0].SpentMicros != 0 {
				t.Fatal("invalid price evaluated")
			}
		})
	}
	s, p := strategyFixture(t)
	for _, bad := range []StrategyPlan{func() StrategyPlan { x := p; x.Network = "other"; return x }(), func() StrategyPlan { x := p; x.QuantityAtoms = 0; return x }(), func() StrategyPlan { x := p; x.OrderBudgetMicros = x.TotalBudgetMicros + 1; return x }()} {
		v, e := s.Agents(t.Context(), "trading-analyst", "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "strategy-save", ProfileID: v.ProfileID, Revision: v.Revision, Name: "bad", Strategy: &bad}); e == nil {
			t.Fatal("invalid strategy admitted")
		}
	}
}

// Reuse the real public-record writer for an isolated browser/CLI journey. This
// entry exists only in a Go test binary, never in the installed executable.
func TestStrategyBrowserSeed(t *testing.T) {
	dir := os.Getenv("FASED_STRATEGY_SEED_DIR")
	if dir == "" {
		t.Skip("explicit disposable browser seed only")
	}
	root, e := filepath.Abs(".evidence/b06-strategies")
	if e != nil {
		t.Fatal(e)
	}
	shortFixture := filepath.Dir(dir) == filepath.Clean(os.TempDir()) && strings.HasPrefix(filepath.Base(dir), "fased-b06-")
	if os.Getuid() <= 0 || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || (!strings.HasPrefix(dir, root+string(os.PathSeparator)) && !shortFixture) {
		t.Fatal("private task fixture directory required")
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.rpcAdmission = true
	accounts, e := s.Accounts(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if len(accounts.Record.Accounts) != 0 {
		t.Fatal("refuse to seed existing accounts")
	}
	accounts.Record.Accounts = []Account{{Version: 2, ID: strings.Repeat("a", 32), Name: "Paper wallet", Mode: "watch", Network: "devnet", PublicKey: strings.Repeat("1", 64)}}
	if _, e = s.UpdateAccounts(t.Context(), accounts.Record); e != nil {
		t.Fatal(e)
	}
}

func TestStrategyPolicyErasureRefusesFallbackAndPreservesRaw(t *testing.T) {
	s, p := strategyFixture(t)
	strategy := saveStrategyFixture(t, s, p)
	task := strategyTaskFixture(t, s, strategy, 1)
	var raw string
	if e := s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var doc map[string]json.RawMessage
	json.Unmarshal([]byte(raw), &doc)
	var tasks []map[string]json.RawMessage
	json.Unmarshal(doc["tasks"], &tasks)
	delete(tasks[0], "strategyPlan")
	doc["tasks"], _ = json.Marshal(tasks)
	erased, _ := json.Marshal(doc)
	var data agentDocument
	json.Unmarshal(erased, &data)
	if validateStrategyStorage(erased, data) == nil {
		t.Fatal("erased unrun strategy policy admitted")
	}
	if runAgentTask(&data, 0, s.now().Unix()) == nil {
		t.Fatal("strategy fell through to reminder evaluator")
	}
	if _, e := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(erased)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Agents(t.Context(), "trading-analyst", ""); e == nil {
		t.Fatal("corrupt workspace loaded")
	}
	var after string
	s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&after)
	if after != string(erased) {
		t.Fatal("refusal rewrote raw records")
	}
	if !strings.HasPrefix(task.ID, strategyTaskPrefix) {
		t.Fatal("strategy task is not tagged")
	}
	if strategyFields(erased, true) == nil {
		t.Fatal("erased type migrated without schema protection")
	}
}

func TestStrategyAliasedContainerRefusesAndKeepsSchema(t *testing.T) {
	s, p := strategyFixture(t)
	strategy := saveStrategyFixture(t, s, p)
	strategyTaskFixture(t, s, strategy, 1)
	var raw string
	s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw)
	var doc map[string]json.RawMessage
	json.Unmarshal([]byte(raw), &doc)
	var tasks []map[string]json.RawMessage
	json.Unmarshal(doc["tasks"], &tasks)
	var policy map[string]json.RawMessage
	json.Unmarshal(tasks[0]["strategyPlan"], &policy)
	policy["hiddenAction"] = json.RawMessage(`true`)
	tasks[0]["strategyPlan"], _ = json.Marshal(policy)
	doc["Tasks"], _ = json.Marshal(tasks)
	delete(doc, "tasks")
	altered, _ := json.Marshal(doc)
	var decoded agentDocument
	json.Unmarshal(altered, &decoded)
	if validateStrategyStorage(altered, decoded) == nil {
		t.Fatal("aliased strategy container accepted")
	}
	if _, e := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(altered)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("PRAGMA user_version=29"); e != nil {
		t.Fatal(e)
	}
	if e := migrateStrategies(t.Context(), s.db); e == nil {
		t.Fatal("aliased unprotected state migrated")
	}
	var after string
	var schema int
	s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&after)
	s.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if after != string(altered) || schema != 29 {
		t.Fatal("migration refusal changed source")
	}
}
