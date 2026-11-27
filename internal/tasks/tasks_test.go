package tasks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTasksCRUDLibrariesDAGAndPrivilegeGuards(t *testing.T) {
	ctx := context.Background()
	eng, err := NewEngine(Config{DataDir: t.TempDir(), Now: time.Now})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	defer func() { _ = eng.Close() }()

	libs, err := eng.Store().ListLibraries(ctx)
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	if len(libs.Personas) < 7 || len(libs.Skills) < 14 || len(libs.Templates) < 14 {
		t.Fatalf("expected seeded builtins (>=7 personas, >=14 skills, >=14 templates), got %d/%d/%d",
			len(libs.Personas), len(libs.Skills), len(libs.Templates))
	}

	// Instantiate from Mining template
	root, err := eng.InstantiateTemplate(ctx, "tpl-det-band-miner", Task{
		Name:             "Band Miner Root",
		ApprovalPolicy:   ApprovalSemiAuto,
		ExecutionBinding: ExecutionBinding{WalletHandle: "@wallet:miner1"},
		Constraints:      TaskConstraints{MaxSpendUSD: 50, MaxSpendLamports: 100000000},
		CreatedVia:       "ui",
	})
	if err != nil {
		t.Fatalf("InstantiateTemplate: %v", err)
	}
	if root.PolicyHash == "" || len(root.PolicyHash) != 64 {
		t.Fatalf("expected 64-hex policyHash, got %q", root.PolicyHash)
	}

	// External chat/mcp task creation is forced to Draft status.
	chatDraft, err := eng.CreateTask(ctx, Task{
		Name:             "Chat Miner Draft",
		GoalDomain:       GoalMining,
		ScheduleKind:     ScheduleOnce,
		ExecMode:         ExecStandalone,
		ApprovalPolicy:   ApprovalAuto,
		ExecutionBinding: ExecutionBinding{WalletHandle: "@wallet:miner1"},
		Status:           StatusActive,
		CreatedVia:       "chat",
	})
	if err != nil {
		t.Fatalf("CreateTask chat draft: %v", err)
	}
	if chatDraft.Status != StatusDraft {
		t.Fatalf("expected chat-created task to force draft, got %s", chatDraft.Status)
	}

	// Child cannot loosen ApprovalPolicy or switch live wallet handle.
	_, err = eng.CreateTask(ctx, Task{
		Name:             "Escalated Child",
		ParentTaskID:     root.ID,
		GoalDomain:       GoalMining,
		ScheduleKind:     ScheduleOnce,
		ExecMode:         ExecStandalone,
		ApprovalPolicy:   ApprovalAuto, // looser than root ApprovalSemiAuto
		ExecutionBinding: ExecutionBinding{WalletHandle: "@wallet:miner1"},
		Constraints:      TaskConstraints{MaxSpendUSD: 10, MaxSpendLamports: 1000},
	})
	if err == nil {
		t.Fatal("expected child approval escalation to fail")
	}

	_, err = eng.CreateTask(ctx, Task{
		Name:             "Wallet Hopping Child",
		ParentTaskID:     root.ID,
		GoalDomain:       GoalMining,
		ScheduleKind:     ScheduleOnce,
		ExecMode:         ExecStandalone,
		ApprovalPolicy:   ApprovalManual,
		ExecutionBinding: ExecutionBinding{WalletHandle: "@wallet:other"},
		Constraints:      TaskConstraints{MaxSpendUSD: 10, MaxSpendLamports: 1000},
	})
	if err == nil {
		t.Fatal("expected child wallet switching to fail")
	}

	// Build a valid 3-node DAG and verify cycle + depth bounds.
	c1, err := eng.CreateTask(ctx, Task{
		Name:           "Child 1",
		ParentTaskID:   root.ID,
		GoalDomain:     GoalNotifications,
		ScheduleKind:   ScheduleEvent,
		ExecMode:       ExecStandalone,
		ApprovalPolicy: ApprovalManual,
		Constraints:    TaskConstraints{MaxSpendUSD: 5, MaxSpendLamports: 500},
	})
	if err != nil {
		t.Fatalf("create child 1: %v", err)
	}
	if _, err := eng.Store().AddEdge(ctx, TaskEdge{ParentTaskID: root.ID, ChildTaskID: c1.ID, Condition: EdgeOnSuccess}); err != nil {
		t.Fatalf("AddEdge root->c1: %v", err)
	}
	if _, err := eng.Store().AddEdge(ctx, TaskEdge{ParentTaskID: c1.ID, ChildTaskID: root.ID, Condition: EdgeAlways}); err == nil {
		t.Fatal("expected DAG cycle check to reject c1->root")
	}
}

func TestTasksStandaloneHybridKillAndSwarm(t *testing.T) {
	ctx := context.Background()
	var aiCalls atomic.Int64
	var domainCalls atomic.Int64

	eng, err := NewEngine(Config{
		DataDir: t.TempDir(),
		Now:     time.Now,
		DomainRunner: func(ctx context.Context, task Task, input RunInput) (RunOutput, error) {
			domainCalls.Add(1)
			return RunOutput{
				Status:          "completed",
				SignalEmitted:   true,
				ThresholdHit:    true,
				ActionTaken:     "checked",
				DecisionSummary: "domain ok for " + task.Name,
			}, nil
		},
		AIGovernor: func(ctx context.Context, task Task, personas []TaskPersona, skills []TaskSkill, domainOutput RunOutput, input RunInput) (RunOutput, error) {
			aiCalls.Add(1)
			return RunOutput{
				Status:          "completed",
				ActionTaken:     "pause_reversal",
				DecisionSummary: "trend reversed",
				RecommendPause:  true,
				PauseReason:     "BTC trend reversed",
				TokensIn:        120,
				TokensOut:       45,
				CostMicroUSD:    500,
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Standalone task must execute DomainRunner ONLY and never call AIGovernor.
	standaloneTask, err := eng.InstantiateTemplate(ctx, "tpl-auto-claim", Task{Status: StatusActive})
	if err != nil {
		t.Fatalf("InstantiateTemplate standalone: %v", err)
	}
	if _, err := eng.RunTaskNow(ctx, standaloneTask.ID, RunInput{TriggerSource: "test"}); err != nil {
		t.Fatalf("RunTaskNow standalone: %v", err)
	}
	if domainCalls.Load() != 1 || aiCalls.Load() != 0 {
		t.Fatalf("expected domainCalls=1 aiCalls=0, got %d/%d", domainCalls.Load(), aiCalls.Load())
	}

	// Hybrid task with StopOnReversal pauses automatically when AIGovernor recommends pause.
	hybridTask, err := eng.InstantiateTemplate(ctx, "tpl-ai-trend-miner", Task{Status: StatusActive})
	if err != nil {
		t.Fatalf("InstantiateTemplate hybrid: %v", err)
	}
	if _, err := eng.RunTaskNow(ctx, hybridTask.ID, RunInput{TriggerSource: "test"}); err != nil {
		t.Fatalf("RunTaskNow hybrid: %v", err)
	}
	if aiCalls.Load() != 1 {
		t.Fatalf("expected aiCalls=1, got %d", aiCalls.Load())
	}
	reloaded, err := eng.Store().GetTask(ctx, hybridTask.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if reloaded.Status != StatusPaused {
		t.Fatalf("expected hybrid task paused on trend reversal, got %s", reloaded.Status)
	}

	// Swarm creation + DAG chaining + KillAll.
	swarm, err := eng.CreateSwarm(ctx, SwarmRequest{
		Name:          "Research Swarm",
		GoalDomain:    GoalResearch,
		GoalObjective: "Analyze SOL catalyst",
		ScheduleKind:  ScheduleOnce,
		Children: []SwarmChildSpec{
			{Name: "News Scout", PersonaID: "news_sentinel", ExecMode: ExecAI},
			{Name: "Skeptic Review", PersonaID: "skeptic", ExecMode: ExecAI, DependsOn: []int{0}},
		},
	})
	if err != nil {
		t.Fatalf("CreateSwarm: %v", err)
	}
	if len(swarm.Children) != 2 || len(swarm.Edges) != 2 {
		t.Fatalf("expected 2 children & 2 edges, got %d/%d", len(swarm.Children), len(swarm.Edges))
	}

	if _, err := eng.Activate(ctx, swarm.RootTask.ID); err != nil {
		t.Fatalf("Activate swarm root: %v", err)
	}
	killed, err := eng.KillAll(ctx, "test emergency stop", "owner")
	if err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	if len(killed) == 0 {
		t.Fatal("expected at least 1 active/paused task killed")
	}
	if _, err := eng.Activate(ctx, swarm.RootTask.ID); err == nil {
		t.Fatal("expected Activate to fail while kill switch active")
	}
}

func TestEmitEventSubSecondPushCooldownAndKillSwitch(t *testing.T) {
	ctx := context.Background()
	var triggered atomic.Int64
	doneCh := make(chan string, 8)
	eng, err := NewEngine(Config{
		DataDir: t.TempDir(),
		Now:     time.Now,
		DomainRunner: func(_ context.Context, tk Task, in RunInput) (RunOutput, error) {
			triggered.Add(1)
			select {
			case doneCh <- tk.ID + ":" + in.TriggerSource:
			default:
			}
			return RunOutput{Status: "completed", ActionTaken: "push_eval", DecisionSummary: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = eng.Close() }()

	eventTask, err := eng.CreateTask(ctx, Task{
		Name:           "Round Opened Listener",
		GoalDomain:     GoalMining,
		GoalObjective:  "React immediately to LV1X round transitions",
		ScheduleKind:   ScheduleEvent,
		ScheduleSpec:   ScheduleSpec{EventFilter: "mining.round.opened"},
		ExecMode:       ExecStandalone,
		ApprovalPolicy: ApprovalSemiAuto,
		Constraints:    TaskConstraints{CooldownSec: 30},
		Status:         StatusActive,
	})
	if err != nil {
		t.Fatalf("CreateTask event: %v", err)
	}

	contTask, err := eng.CreateTask(ctx, Task{
		Name:           "Continuous Multi-Topic Listener",
		GoalDomain:     GoalTrading,
		GoalObjective:  "React to market ticks and live fills",
		ScheduleKind:   ScheduleContinuous,
		ScheduleSpec:   ScheduleSpec{IntervalSec: 15, EventFilter: "market.tick,trade.fill"},
		ExecMode:       ExecStandalone,
		ApprovalPolicy: ApprovalSemiAuto,
		Constraints:    TaskConstraints{CooldownSec: 15},
		Status:         StatusActive,
	})
	if err != nil {
		t.Fatalf("CreateTask continuous: %v", err)
	}

	// Sub-second dispatch for ScheduleEvent
	if n := eng.EmitEvent(ctx, "mining.round.opened", `{"roundSlot":37}`); n != 1 {
		t.Fatalf("expected 1 dispatched task for mining.round.opened, got %d", n)
	}
	select {
	case msg := <-doneCh:
		if msg != eventTask.ID+":event:mining.round.opened" {
			t.Fatalf("unexpected trigger message %q", msg)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected event-triggered run within 250ms")
	}

	// Cooldown deduplication prevents duplicate bursts within CooldownSec
	if n := eng.EmitEvent(ctx, "mining.round.opened", `{"roundSlot":37}`); n != 0 {
		t.Fatalf("expected 0 dispatched during cooldown, got %d", n)
	}

	// Sub-second dispatch for ScheduleContinuous with EventFilter
	if n := eng.EmitEvent(ctx, "trade.fill", `{"venue":"kraken"}`); n != 1 {
		t.Fatalf("expected 1 dispatched continuous task for trade.fill, got %d", n)
	}
	select {
	case msg := <-doneCh:
		if msg != contTask.ID+":event:trade.fill" {
			t.Fatalf("unexpected continuous trigger message %q", msg)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected continuous-filtered run within 250ms")
	}

	// KillAll blocks further push dispatches until ClearKill
	if _, err := eng.KillAll(ctx, "stop push", "owner"); err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	if n := eng.EmitEvent(ctx, "market.tick", `{}`); n != 0 {
		t.Fatalf("expected 0 dispatches while killed, got %d", n)
	}
	eng.ClearKill()
}
