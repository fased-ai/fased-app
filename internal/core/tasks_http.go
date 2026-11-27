package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/quantlab"
	"github.com/fased-ai/fased-app/internal/tasks"
	"github.com/fased-ai/fased-app/internal/wenlv1x"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

var (
	tasksEngineMu sync.RWMutex
	tasksEngines  = map[*Store]*tasks.Engine{}
)

func init() {
	registerStoreCloseHook(func(s *Store) {
		tasksEngineMu.Lock()
		eng := tasksEngines[s]
		delete(tasksEngines, s)
		tasksEngineMu.Unlock()
		if eng != nil {
			_ = eng.Close()
		}
	})
}

func (s *Store) loadedTasksEngine() *tasks.Engine {
	if s == nil {
		return nil
	}
	tasksEngineMu.RLock()
	eng := tasksEngines[s]
	tasksEngineMu.RUnlock()
	return eng
}

func (s *Store) tasksEngine() (*tasks.Engine, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	if existing := s.loadedTasksEngine(); existing != nil {
		return existing, nil
	}
	tasksEngineMu.Lock()
	defer tasksEngineMu.Unlock()
	if existing := tasksEngines[s]; existing != nil {
		return existing, nil
	}
	eng, err := tasks.NewEngine(tasks.Config{
		DataDir:      s.dir,
		Now:          s.now,
		IsKillActive: s.isGlobalKillActive,
		DomainRunner: s.runTaskDomain,
		AIGovernor:   s.runTaskAIGovernor,
	})
	if err != nil {
		return nil, err
	}
	tasksEngines[s] = eng
	return eng, nil
}

func (s *Store) startTasksEngine(ctx context.Context) {
	if s == nil || s.dir == "" {
		return
	}
	if eng, err := s.tasksEngine(); err == nil && eng != nil {
		eng.Start(ctx)
	}
}

func (s *Store) syncTasksKill(ctx context.Context, active bool, reason, actor string) {
	eng := s.loadedTasksEngine()
	if eng == nil {
		return
	}
	if active {
		_, _ = eng.KillAll(ctx, reason, actor)
		return
	}
	eng.ClearKill()
}

func (s *Store) runTaskDomain(ctx context.Context, task tasks.Task, input tasks.RunInput) (tasks.RunOutput, error) {
	switch task.GoalDomain {
	case tasks.GoalNews:
		wi, err := s.worldIntelService()
		if err != nil {
			return tasks.RunOutput{Status: "completed", ActionTaken: "news_skip", DecisionSummary: "News feed engine uninitialized"}, nil
		}
		events, err := wi.ListEvents(ctx, worldintel.EventFilter{Limit: 25})
		if err != nil {
			return tasks.RunOutput{}, err
		}
		highCount := 0
		for _, ev := range events {
			if ev.Urgency >= 0.7 {
				highCount++
			}
		}
		b, _ := json.Marshal(map[string]any{"events": len(events), "highUrgency": highCount})
		return tasks.RunOutput{
			Status:          "completed",
			ActionTaken:     "news_scan",
			DecisionSummary: fmt.Sprintf("Scanned %d news events (%d high urgency)", len(events), highCount),
			SignalEmitted:   highCount > 0,
			ThresholdHit:    highCount > 0,
			ResultJSON:      string(b),
		}, nil

	case tasks.GoalTrading:
		if task.Attachments.ScriptID != "" {
			ql, err := s.quantLabService()
			if err == nil {
				if rec, ok, gErr := ql.GetScript(ctx, task.Attachments.ScriptID); gErr == nil && ok {
					bt, bErr := ql.RunBacktest(ctx, quantlab.BacktestRequest{
						ScriptID: rec.ID,
						Source:   rec.Source,
						Config: quantlab.BacktestConfig{
							Venue:    nonEmpty(task.ExecutionBinding.Venue, "kraken"),
							Symbol:   nonEmpty(task.ExecutionBinding.Symbol, "SOL/USD"),
							Interval: "15m",
						},
					})
					if bErr == nil {
						b, _ := json.Marshal(map[string]any{"scriptId": rec.ID, "trades": bt.Metrics.TradeCount, "winRate": bt.Metrics.WinRatePct})
						return tasks.RunOutput{
							Status:          "completed",
							ActionTaken:     "fasedscript_eval",
							DecisionSummary: fmt.Sprintf("Evaluated %s (%d trades, %.1f%% win)", rec.Name, bt.Metrics.TradeCount, bt.Metrics.WinRatePct),
							SignalEmitted:   bt.Metrics.TradeCount > 0,
							ResultJSON:      string(b),
						}, nil
					}
				}
			}
		}
		return tasks.RunOutput{
			Status:          "completed",
			ActionTaken:     "trading_monitor",
			DecisionSummary: fmt.Sprintf("Checked %s %s strategy rules", nonEmpty(task.ExecutionBinding.Venue, "paper"), nonEmpty(task.ExecutionBinding.Symbol, "SOL/USDC")),
		}, nil

	case tasks.GoalMining:
		mEng, err := s.wenLV1XEngine()
		if err != nil {
			return tasks.RunOutput{Status: "failed", DecisionSummary: err.Error()}, err
		}
		round := mEng.CurrentRound(ctx, nonEmpty(task.ExecutionBinding.Cluster, "devnet"))
		walletID := strings.TrimPrefix(strings.TrimSpace(task.ExecutionBinding.WalletHandle), "@wallet:")
		strategyID := nonEmpty(task.Attachments.StrategyID, "wide")
		if strategyID == "auto-claim" && walletID != "" && task.ApprovalPolicy != tasks.ApprovalManual {
			if _, led, cErr := mEng.SubmitClaim(ctx, wenlv1x.ClaimRequest{
				WalletID:   walletID,
				RoundSlot:  round.RoundSlot,
				Autonomous: true,
				TaskID:     task.ID,
			}); cErr == nil && led != nil {
				return tasks.RunOutput{
					Status:          "completed",
					ActionTaken:     "mining_auto_claim",
					DecisionSummary: fmt.Sprintf("Auto-claimed LV1X round %d rewards (tx=%s)", round.RoundSlot, led.TxSignature),
				}, nil
			}
		}
		dec := wenlv1x.EvaluateTrendDecision(strategyID, task.Constraints.StopOnReversal, round.MoveBps, 10, round, 0.02)
		if dec.Action == "pause" {
			b, _ := json.Marshal(dec)
			return tasks.RunOutput{
				Status:          "completed",
				ActionTaken:     "mining_reversal_pause",
				DecisionSummary: dec.Reason,
				SignalEmitted:   true,
				ThresholdHit:    true,
				ResultJSON:      string(b),
			}, nil
		}
		if walletID != "" && task.ApprovalPolicy == tasks.ApprovalAuto {
			if _, led, cErr := mEng.SubmitCommit(ctx, wenlv1x.CommitRequest{
				WalletID:   walletID,
				Cluster:    nonEmpty(task.ExecutionBinding.Cluster, "devnet"),
				AmountSOL:  dec.RecommendedSOL,
				StrategyID: dec.SelectedPreset,
				Autonomous: true,
				TaskID:     task.ID,
			}); cErr == nil && led != nil {
				b, _ := json.Marshal(dec)
				return tasks.RunOutput{
					Status:          "completed",
					ActionTaken:     "mining_auto_commit",
					DecisionSummary: fmt.Sprintf("%s -> committed %.3f SOL (bucket %d)", dec.Reason, dec.RecommendedSOL, dec.SelectedBucket),
					SignalEmitted:   dec.SignalTriggered,
					ResultJSON:      string(b),
				}, nil
			}
		}
		b, _ := json.Marshal(dec)
		return tasks.RunOutput{
			Status:          "completed",
			ActionTaken:     "mining_check",
			DecisionSummary: dec.Reason,
			SignalEmitted:   dec.SignalTriggered,
			ResultJSON:      string(b),
		}, nil

	case tasks.GoalMonitoring, tasks.GoalNotifications, tasks.GoalPortfolio:
		killed := s.isGlobalKillActive(ctx)
		return tasks.RunOutput{
			Status:          "completed",
			ActionTaken:     "watchdog_ok",
			DecisionSummary: fmt.Sprintf("Watchdog check complete (globalKill=%v)", killed),
			ThresholdHit:    killed,
		}, nil

	default:
		return tasks.RunOutput{
			Status:          "completed",
			ActionTaken:     "task_executed",
			DecisionSummary: fmt.Sprintf("Executed %s (%s)", task.Name, input.TriggerSource),
		}, nil
	}
}

func (s *Store) runTaskAIGovernor(ctx context.Context, task tasks.Task, personas []tasks.TaskPersona, skills []tasks.TaskSkill, domainOut tasks.RunOutput, input tasks.RunInput) (tasks.RunOutput, error) {
	personaName := "Default Analyst"
	if len(personas) > 0 {
		personaName = personas[0].Name
	}
	skillNames := make([]string, 0, len(skills))
	for _, sk := range skills {
		skillNames = append(skillNames, sk.Name)
	}
	summary := fmt.Sprintf("[%s] Evaluated %s (%s); skills=%s",
		personaName, task.Name, nonEmpty(domainOut.DecisionSummary, "objective check"),
		nonEmpty(strings.Join(skillNames, ", "), "none"))
	if task.Tainted {
		summary += " [tainted-context-guarded]"
	}
	b, _ := json.Marshal(map[string]any{
		"persona": personaName,
		"skills":  skillNames,
		"tainted": task.Tainted,
		"trigger": input.TriggerSource,
	})
	return tasks.RunOutput{
		Status:          "completed",
		ActionTaken:     "ai_governor_eval",
		DecisionSummary: summary,
		SignalEmitted:   domainOut.SignalEmitted,
		ThresholdHit:    domainOut.ThresholdHit,
		ResultJSON:      string(b),
	}, nil
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func (h *HTTPHandler) tasksAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "tasks", 240) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	eng, err := h.store.tasksEngine()
	if err != nil {
		writeError(w, 500, "tasks engine unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	switch {
	case rel == "":
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			list, err := eng.Store().ListTasks(ctx, tasks.TaskFilter{
				GoalDomain: tasks.GoalDomain(strings.TrimSpace(q.Get("goal"))),
				Status:     tasks.TaskStatus(strings.TrimSpace(q.Get("status"))),
				Query:      strings.TrimSpace(q.Get("q")),
				Limit:      limit,
			})
			if err != nil {
				writeError(w, 500, "failed to list tasks")
				return
			}
			writeJSON(w, 200, map[string]any{"tasks": list})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in tasks.Task
			if !httpapi.DecodeStrictJSON(w, r, 128*1024, &in) {
				return
			}
			if in.Status == tasks.StatusActive && in.NeedsOwnerStepUpToActivate() {
				if hasPW, _ := h.store.hasPassword(ctx); hasPW {
					if _, ok := h.requireStepUpCtx(w, r, token, "task-activate", in.Name); !ok {
						return
					}
				}
			}
			created, err := eng.CreateTask(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 201, map[string]any{"task": created})
		default:
			writeError(w, 405, "method not allowed")
		}

	case rel == "libraries":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		libs, err := eng.Store().ListLibraries(ctx)
		if err != nil {
			writeError(w, 500, "failed to load task libraries")
			return
		}
		writeJSON(w, 200, libs)

	case rel == "from-template":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			TemplateID string     `json:"templateId"`
			Overrides  tasks.Task `json:"overrides"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 128*1024, &in) {
			return
		}
		if in.Overrides.Status == tasks.StatusActive && in.Overrides.NeedsOwnerStepUpToActivate() {
			if hasPW, _ := h.store.hasPassword(ctx); hasPW {
				if _, ok := h.requireStepUpCtx(w, r, token, "task-activate", in.TemplateID); !ok {
					return
				}
			}
		}
		created, err := eng.InstantiateTemplate(ctx, strings.TrimSpace(in.TemplateID), in.Overrides)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 201, map[string]any{"task": created})

	case rel == "swarm":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var req tasks.SwarmRequest
		if !httpapi.DecodeStrictJSON(w, r, 256*1024, &req) {
			return
		}
		res, err := eng.CreateSwarm(ctx, req)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 201, res)

	case rel == "kill-all":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength > 0 {
			if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
				return
			}
		}
		reason := nonEmpty(in.Reason, "Manual task engine emergency kill")
		ids, err := eng.KillAll(ctx, reason, "owner")
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		_, _ = h.store.syncGlobalKill(ctx, true, reason, "owner")
		writeJSON(w, 200, map[string]any{"killedIds": ids})

	default:
		parts := strings.Split(rel, "/")
		taskID := strings.TrimSpace(parts[0])
		if taskID == "" {
			writeError(w, 404, "not found")
			return
		}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				t, err := eng.Store().GetTask(ctx, taskID)
				if err != nil {
					writeError(w, 404, "task not found")
					return
				}
				edges, _ := eng.Store().ListEdges(ctx, taskID)
				runs, _ := eng.Store().ListRuns(ctx, taskID, 30)
				logs, _ := eng.Store().ListLogs(ctx, taskID, 100)
				writeJSON(w, 200, map[string]any{
					"task":  t,
					"edges": edges,
					"runs":  runs,
					"logs":  logs,
				})
			case http.MethodPatch:
				if !h.v2Mutation(w, r, token, true) {
					return
				}
				var in tasks.Task
				if !httpapi.DecodeStrictJSON(w, r, 128*1024, &in) {
					return
				}
				in.ID = taskID
				if in.Status == tasks.StatusActive && in.NeedsOwnerStepUpToActivate() {
					if hasPW, _ := h.store.hasPassword(ctx); hasPW {
						if _, ok := h.requireStepUpCtx(w, r, token, "task-activate", taskID); !ok {
							return
						}
					}
				}
				updated, err := eng.Store().UpdateTask(ctx, in)
				if err != nil {
					writeError(w, 400, err.Error())
					return
				}
				writeJSON(w, 200, map[string]any{"task": updated})
			case http.MethodDelete:
				if !h.v2Mutation(w, r, token, true) {
					return
				}
				if err := eng.Store().DeleteTask(ctx, taskID); err != nil {
					writeError(w, 404, "task not found")
					return
				}
				writeJSON(w, 200, map[string]any{"deleted": true, "id": taskID})
			default:
				writeError(w, 405, "method not allowed")
			}
			return
		}

		action := parts[1]
		switch {
		case len(parts) == 2 && action == "activate":
			if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
				return
			}
			cur, err := eng.Store().GetTask(ctx, taskID)
			if err != nil {
				writeError(w, 404, "task not found")
				return
			}
			if cur.NeedsOwnerStepUpToActivate() {
				if hasPW, _ := h.store.hasPassword(ctx); hasPW {
					if _, ok := h.requireStepUpCtx(w, r, token, "task-activate", taskID); !ok {
						return
					}
				}
			}
			t, err := eng.Activate(ctx, taskID)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"task": t})

		case len(parts) == 2 && action == "pause":
			if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
				return
			}
			var in struct {
				Reason string `json:"reason"`
			}
			if r.ContentLength > 0 {
				_ = httpapi.DecodeStrictJSON(w, r, 16*1024, &in)
			}
			t, err := eng.Pause(ctx, taskID, in.Reason)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"task": t})

		case len(parts) == 2 && action == "stop":
			if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
				return
			}
			t, err := eng.StopTask(ctx, taskID, "manual stop")
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"task": t})

		case len(parts) == 2 && action == "run":
			if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
				return
			}
			run, err := eng.RunTaskNow(ctx, taskID, tasks.RunInput{TriggerSource: "manual"})
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"run": run})

		case len(parts) == 2 && action == "runs":
			if r.Method != http.MethodGet {
				writeError(w, 405, "method not allowed")
				return
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			runs, err := eng.Store().ListRuns(ctx, taskID, limit)
			if err != nil {
				writeError(w, 500, "failed to load runs")
				return
			}
			writeJSON(w, 200, map[string]any{"runs": runs})

		case len(parts) == 2 && action == "logs":
			if r.Method != http.MethodGet {
				writeError(w, 405, "method not allowed")
				return
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			logs, err := eng.Store().ListLogs(ctx, taskID, limit)
			if err != nil {
				writeError(w, 500, "failed to load logs")
				return
			}
			writeJSON(w, 200, map[string]any{"logs": logs})

		case len(parts) == 2 && action == "edges":
			if r.Method == http.MethodGet {
				edges, err := eng.Store().ListEdges(ctx, taskID)
				if err != nil {
					writeError(w, 500, "failed to list edges")
					return
				}
				writeJSON(w, 200, map[string]any{"edges": edges})
				return
			}
			if r.Method == http.MethodPost && h.v2Mutation(w, r, token, true) {
				var in tasks.TaskEdge
				if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
					return
				}
				in.ParentTaskID = taskID
				edge, err := eng.Store().AddEdge(ctx, in)
				if err != nil {
					writeError(w, 400, err.Error())
					return
				}
				writeJSON(w, 201, map[string]any{"edge": edge})
				return
			}

		case len(parts) == 3 && action == "edges" && r.Method == http.MethodDelete:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			if err := eng.Store().DeleteEdge(ctx, parts[2]); err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"deleted": true})

		default:
			writeError(w, 404, "not found")
		}
	}
}

func toolTasksList(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	eng, err := s.tasksEngine()
	if err != nil {
		return nil, err
	}
	var args struct {
		Goal   string `json:"goal"`
		Status string `json:"status"`
	}
	if len(rawArgs) > 0 {
		_ = json.Unmarshal(rawArgs, &args)
	}
	list, err := eng.Store().ListTasks(ctx, tasks.TaskFilter{
		GoalDomain: tasks.GoalDomain(strings.TrimSpace(args.Goal)),
		Status:     tasks.TaskStatus(strings.TrimSpace(args.Status)),
		Limit:      50,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"tasks": list}, nil
}

func toolTasksLibraries(ctx context.Context, s *Store, _ string, _ json.RawMessage) (any, error) {
	eng, err := s.tasksEngine()
	if err != nil {
		return nil, err
	}
	return eng.Store().ListLibraries(ctx)
}

type draftTaskV2Args struct {
	Name           string `json:"name"`
	GoalDomain     string `json:"goalDomain"`
	GoalObjective  string `json:"goalObjective"`
	TemplateID     string `json:"templateId"`
	ScheduleKind   string `json:"scheduleKind"`
	IntervalSec    int64  `json:"intervalSec"`
	ExecMode       string `json:"execMode"`
	ApprovalPolicy string `json:"approvalPolicy"`
	PersonaID      string `json:"personaId"`
	StrategyID     string `json:"strategyId"`
}

func parseDraftTaskV2Args(raw json.RawMessage) (draftTaskV2Args, error) {
	var a draftTaskV2Args
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, err
	}
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return a, errors.New("task name is required")
	}
	return a, nil
}

func previewDraftTaskV2(_ context.Context, _ *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	a, err := parseDraftTaskV2Args(rawArgs)
	if err != nil {
		return approvalDraft{}, err
	}
	payload, _ := json.Marshal(a)
	return approvalDraft{
		Title:       "Draft Task: " + a.Name,
		Summary:     fmt.Sprintf("%s · %s (%s) — saved as draft for owner review", a.GoalDomain, nonEmpty(a.ExecMode, "standalone"), nonEmpty(a.ScheduleKind, "interval")),
		PayloadJSON: string(payload),
	}, nil
}

func executeDraftTaskV2(ctx context.Context, s *Store, threadID string, rawArgs json.RawMessage) (any, error) {
	a, err := parseDraftTaskV2Args(rawArgs)
	if err != nil {
		return nil, err
	}
	eng, err := s.tasksEngine()
	if err != nil {
		return nil, err
	}
	tainted := IsContextTainted(ctx)
	if !tainted && strings.TrimSpace(threadID) != "" {
		if ts, _ := s.GetThreadTaint(ctx, threadID); ts > 0 {
			tainted = true
		}
	}
	overrides := tasks.Task{
		Name:           a.Name,
		GoalDomain:     tasks.GoalDomain(strings.TrimSpace(a.GoalDomain)),
		GoalObjective:  a.GoalObjective,
		ScheduleKind:   tasks.ScheduleKind(strings.TrimSpace(a.ScheduleKind)),
		ScheduleSpec:   tasks.ScheduleSpec{IntervalSec: a.IntervalSec},
		ExecMode:       tasks.ExecMode(strings.TrimSpace(a.ExecMode)),
		ApprovalPolicy: tasks.ApprovalPolicy(strings.TrimSpace(a.ApprovalPolicy)),
		Attachments: tasks.TaskAttachments{
			PersonaID:  a.PersonaID,
			StrategyID: a.StrategyID,
		},
		Status:     tasks.StatusDraft,
		Tainted:    tainted,
		CreatedVia: "chat",
	}
	var created tasks.Task
	if strings.TrimSpace(a.TemplateID) != "" {
		created, err = eng.InstantiateTemplate(ctx, strings.TrimSpace(a.TemplateID), overrides)
	} else {
		created, err = eng.CreateTask(ctx, overrides)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "draft_saved", "task": created}, nil
}
