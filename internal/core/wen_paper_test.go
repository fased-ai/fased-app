package core

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestWENPaperJourney(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Range C rehearsal", WEN: &WENPaperInput{BudgetSOL: "0.25", Selection: "2", ValidForSeconds: 900}})
	task := v.Tasks[0]
	if task.State != "review" || task.WEN == nil || len(v.Runs) != 0 {
		t.Fatal("review required", v)
	}
	v = agentApply(t, s, AgentCommand{Action: "wen-paper-approve", ID: task.ID, ConsentDigest: task.WEN.Digest})
	v = agentApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	if len(v.Runs) != 1 || v.Runs[0].WEN == nil || v.Tasks[0].State != "completed" || v.ProviderCallsEnabled || v.FinancialExecution {
		t.Fatal(v)
	}
	b, _ := json.Marshal(v.Runs[0])
	if len(b) > 7000 {
		t.Fatal("result exceeds reserved completion bytes")
	}
	r := v.Runs[0].WEN
	if r.Receipt.Amount != 250000000 || r.Receipt.Settled || r.Receipt.SAT != 0 || r.Receipt.Principal != 0 || r.Receipt.Outcome != nil || len(r.Instructions) != 3 {
		t.Fatal(r)
	}
	if err := s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), "", "")
	if len(v.Runs) != 1 || len(v.Notices) != 1 {
		t.Fatal("replayed", v)
	}
	other, _ := s.Agents(t.Context(), "news-researcher", "")
	if len(other.Tasks) != 0 || len(other.Runs) != 0 || len(other.Notices) != 0 {
		t.Fatal("profile leak")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Agents(t.Context(), "", "")
	if err != nil || got.Runs[0].WEN.ReceiptSHA256 != r.ReceiptSHA256 {
		t.Fatal("restart lost receipt", err)
	}
}

func TestWENPaperRefusalsExpiryAndIsolation(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	prepare := func() AgentTask {
		v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Bounded paper", WEN: &WENPaperInput{BudgetSOL: "0.010000001", Selection: "0", ValidForSeconds: 300}})
		return v.Tasks[len(v.Tasks)-1]
	}
	refuse := func(c AgentCommand) {
		t.Helper()
		v, _ := s.Agents(t.Context(), c.ProfileID, "")
		c.Revision = v.Revision
		if _, err := s.ChangeAgents(t.Context(), c); err == nil {
			t.Fatal("accepted", c.Action)
		}
		after, _ := s.Agents(t.Context(), c.ProfileID, "")
		if after.Revision != v.Revision {
			t.Fatal("refusal mutated state")
		}
	}
	for _, budget := range []string{"", "0", "0.009999999", "1.000000001", "01", "1e-2", "-1", ".01", "0.1234567890", "0. 1", "NaN"} {
		refuse(AgentCommand{Action: "wen-paper-prepare", Name: "bad", WEN: &WENPaperInput{BudgetSOL: budget, Selection: "2", ValidForSeconds: 300}})
	}
	for _, selection := range []string{"", "5", "-1", "02"} {
		refuse(AgentCommand{Action: "wen-paper-prepare", Name: "bad", WEN: &WENPaperInput{BudgetSOL: "0.01", Selection: selection, ValidForSeconds: 300}})
	}
	a := prepare()
	for _, action := range []string{"task-run", "task-resume", "task-pause"} {
		refuse(AgentCommand{Action: action, ID: a.ID})
	}
	refuse(AgentCommand{Action: "wen-paper-approve", ID: a.ID, ConsentDigest: "wrong"})
	refuse(AgentCommand{Action: "wen-paper-approve", ProfileID: "news-researcher", ID: a.ID, ConsentDigest: a.WEN.Digest})
	agentApply(t, s, AgentCommand{Action: "wen-paper-cancel", ID: a.ID})
	for _, action := range []string{"task-resume", "task-run", "wen-paper-approve"} {
		refuse(AgentCommand{Action: action, ID: a.ID, ConsentDigest: a.WEN.Digest})
	}
	b := prepare()
	agentApply(t, s, AgentCommand{Action: "wen-paper-approve", ID: b.ID, ConsentDigest: b.WEN.Digest})
	agentApply(t, s, AgentCommand{Action: "task-pause", ID: b.ID})
	now = now.Add(301 * time.Second)
	refuse(AgentCommand{Action: "task-resume", ID: b.ID})
	c := prepare()
	agentApply(t, s, AgentCommand{Action: "wen-paper-approve", ID: c.ID, ConsentDigest: c.WEN.Digest})
	agentApply(t, s, AgentCommand{Action: "task-create", Name: "Independent", Text: "Still runs", IntervalSeconds: 60, MaxRuns: 1})
	now = now.Add(301 * time.Second)
	if err := s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Agents(t.Context(), "", "")
	if len(v.Runs) != 2 || v.Tasks[2].State != "held" || v.Runs[0].WEN != nil || v.Runs[0].Outcome != "paper-held" || v.Runs[1].Outcome != "reminded" {
		t.Fatal("expired or blocked independent work", v)
	}
	refuse(AgentCommand{Action: "task-resume", ID: c.ID})
}

func TestWENPaperConcurrentScheduledOnceAndTamper(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Scheduled", WEN: &WENPaperInput{BudgetSOL: "1", Selection: "4", ValidForSeconds: 300}})
	task := v.Tasks[0]
	agentApply(t, s, AgentCommand{Action: "wen-paper-approve", ID: task.ID, ConsentDigest: task.WEN.Digest})
	now = now.Add(61 * time.Second)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if err := s.RunAgentTasks(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	v, _ = s.Agents(t.Context(), "", "")
	if len(v.Runs) != 1 || len(v.Notices) != 1 || v.Runs[0].WEN.Receipt.Amount != 1000000000 {
		t.Fatal("scheduled replay", v)
	}
	task.WEN.Approved = true
	mutations := []func(*AgentTask){func(t *AgentTask) { t.Name = "changed" }, func(t *AgentTask) { t.ProfileID = "other" }, func(t *AgentTask) { t.WEN.BudgetSOL = "0.5" }, func(t *AgentTask) { t.WEN.FixtureSHA256 = "foreign" }, func(t *AgentTask) { t.BotID = "bot" }, func(t *AgentTask) { t.MaxRuns = 2 }, func(t *AgentTask) { t.WEN.Selection = "1" }}
	for _, mutate := range mutations {
		copy := task
		p := *task.WEN
		copy.WEN = &p
		mutate(&copy)
		if validateWEN(copy, now.Unix(), true) == nil {
			t.Fatal("changed plan accepted")
		}
	}
}

func TestWENPaperCapacityAndPausedMigration(t *testing.T) {
	s := fixtureStore(t)
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Retained", WEN: &WENPaperInput{BudgetSOL: "0.01", Selection: "2", ValidForSeconds: 300}})
	d, revision, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	d.Notices = make([]AgentNotice, 200)
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if saveAgents(t.Context(), tx, d, revision) == nil {
		t.Fatal("reserved inbox stolen")
	}
	tx.Rollback()
	if _, err = s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1; PRAGMA user_version=9"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := OpenStore(s.dir); err == nil {
		n.Close()
		t.Fatal("paused schema-9 migrated")
	}
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 9 {
		t.Fatal("refused migration changed schema")
	}
	if _, err = db.Exec("UPDATE update_pause SET phase='idle' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	after, err := n.Agents(t.Context(), "", "")
	if err != nil || after.Tasks[0].WEN.Digest != v.Tasks[0].WEN.Digest || after.Revision != v.Revision {
		t.Fatal("migration lost records", err)
	}
}

func TestWENPaperSnapshotBindsReviewAndReceipt(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	snapshot := func() update.CoreSnapshot {
		a, err := s.UpdateSnapshot(t.Context(), update.NewProbe())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := snapshot()
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Snapshot", WEN: &WENPaperInput{BudgetSOL: "0.01", Selection: "2", ValidForSeconds: 300}})
	b := snapshot()
	if a.State.Anchor == b.State.Anchor {
		t.Fatal("plan missing from snapshot")
	}
	task := v.Tasks[0]
	agentApply(t, s, AgentCommand{Action: "wen-paper-approve", ID: task.ID, ConsentDigest: task.WEN.Digest})
	agentApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	c := snapshot()
	if b.State.Anchor == c.State.Anchor {
		t.Fatal("receipt missing from snapshot")
	}
	if StateSchema != 12 {
		t.Fatal("reader compatibility boundary missing")
	}
}
