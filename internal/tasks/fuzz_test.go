package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func FuzzNormalizeAndValidateTask(f *testing.F) {
	f.Add(`{"name":"Miner","goalDomain":"mining","scheduleKind":"interval","execMode":"standalone","approvalPolicy":"manual"}`)
	f.Add(`{"name":"","goalDomain":"invalid"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		var task Task
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			return
		}
		_ = NormalizeAndValidate(&task)
	})
}

func FuzzDAGAntiEscalationAndCycleCheck(f *testing.F) {
	f.Add("Child", "mining", "auto", int64(50))
	f.Fuzz(func(t *testing.T, name, domain, approval string, maxUSD int64) {
		eng, err := NewEngine(Config{DataDir: t.TempDir(), Now: time.Now})
		if err != nil {
			t.Skip()
		}
		defer func() { _ = eng.Close() }()
		ctx := context.Background()
		root, err := eng.CreateTask(ctx, Task{
			Name:             "Root",
			GoalDomain:       GoalMining,
			ScheduleKind:     ScheduleOnce,
			ExecMode:         ExecStandalone,
			ApprovalPolicy:   ApprovalSemiAuto,
			ExecutionBinding: ExecutionBinding{WalletHandle: "@wallet:a"},
			Constraints:      TaskConstraints{MaxSpendUSD: 25},
		})
		if err != nil {
			return
		}
		child, err := eng.CreateTask(ctx, Task{
			Name:           name,
			GoalDomain:     GoalDomain(domain),
			ScheduleKind:   ScheduleOnce,
			ExecMode:       ExecStandalone,
			ApprovalPolicy: ApprovalPolicy(approval),
			Constraints:    TaskConstraints{MaxSpendUSD: float64(maxUSD)},
		})
		if err != nil {
			return
		}
		edge, err := eng.Store().AddEdge(ctx, TaskEdge{ParentTaskID: root.ID, ChildTaskID: child.ID})
		if err == nil {
			if checkErr := CheckChildPrivilege(root, child); checkErr != nil {
				t.Fatalf("edge %s bypassed privilege check: %v", edge.ID, checkErr)
			}
		}
	})
}
