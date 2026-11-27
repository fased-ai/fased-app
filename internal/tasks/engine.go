package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunInput provides execution context to DomainRunner and AIGovernor.
type RunInput struct {
	TriggerSource    string `json:"triggerSource"`
	ParentRunID      string `json:"parentRunId,omitempty"`
	ParentOutputJSON string `json:"parentOutputJson,omitempty"`
	EventTopic       string `json:"eventTopic,omitempty"`
	EventPayloadJSON string `json:"eventPayloadJson,omitempty"`
	Depth            int    `json:"depth,omitempty"`
}

// RunOutput holds the structured result emitted by a task's DomainRunner or AIGovernor.
type RunOutput struct {
	Status          string `json:"status,omitempty"` // completed | pending_approval
	ActionTaken     string `json:"actionTaken,omitempty"`
	DecisionSummary string `json:"decisionSummary,omitempty"`
	SignalEmitted   bool   `json:"signalEmitted,omitempty"`
	ThresholdHit    bool   `json:"thresholdHit,omitempty"`
	RequestGovernor bool   `json:"requestGovernor,omitempty"`
	PnLMicroUSD     int64  `json:"pnlMicroUsd,omitempty"`
	SatEarnedAtoms  int64  `json:"satEarnedAtoms,omitempty"`
	TokensIn        int64  `json:"tokensIn,omitempty"`
	TokensOut       int64  `json:"tokensOut,omitempty"`
	CostMicroUSD    int64  `json:"costMicroUsd,omitempty"`
	ResultJSON      string `json:"resultJson,omitempty"`
	RecommendPause  bool   `json:"recommendPause,omitempty"`
	PauseReason     string `json:"pauseReason,omitempty"`
}

// DomainRunner executes deterministic or domain-specific work for a task.
type DomainRunner func(ctx context.Context, task Task, input RunInput) (RunOutput, error)

// AIGovernor evaluates strategy/regime/decisions when a task uses ExecAI or ExecHybrid.
type AIGovernor func(ctx context.Context, task Task, personas []TaskPersona, skills []TaskSkill, domainOutput RunOutput, input RunInput) (RunOutput, error)

// Config configures an authoritative Universal Task Engine instance.
type Config struct {
	DataDir      string
	Now          func() time.Time
	DomainRunner DomainRunner
	AIGovernor   AIGovernor
	IsKillActive func(ctx context.Context) bool
}

// Engine schedules, executes, chains, and monitors tasks across all Fased domains.
type Engine struct {
	store        *Store
	now          func() time.Time
	domainRunner DomainRunner
	aiGovernor   AIGovernor
	isKillActive func(ctx context.Context) bool

	killed atomic.Bool

	subMu sync.Mutex
	subs  map[uint64]chan TaskEvent
	subID uint64

	runMu    sync.Mutex
	inFlight map[string]context.CancelFunc
	lastEmit map[string]time.Time

	loopMu     sync.Mutex
	loopCancel context.CancelFunc
	wg         sync.WaitGroup
}

// NewEngine initializes the persistent Store and task execution Engine.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	store, err := NewStore(cfg.DataDir, cfg.Now)
	if err != nil {
		return nil, err
	}
	return &Engine{
		store:        store,
		now:          cfg.Now,
		domainRunner: cfg.DomainRunner,
		aiGovernor:   cfg.AIGovernor,
		isKillActive: cfg.IsKillActive,
		subs:         make(map[uint64]chan TaskEvent),
		inFlight:     make(map[string]context.CancelFunc),
		lastEmit:     make(map[string]time.Time),
	}, nil
}

// Store returns the backing SQLite Store.
func (e *Engine) Store() *Store {
	return e.store
}

// Close stops background loops, waits for in-flight runs, and closes SQLite cleanly.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.Stop()
	if e.store != nil {
		return e.store.Close()
	}
	return nil
}

// Start launches the 1s scheduler tick loop until Stop() or ctx cancellation.
func (e *Engine) Start(ctx context.Context) {
	e.loopMu.Lock()
	if e.loopCancel != nil {
		e.loopMu.Unlock()
		return
	}
	lCtx, cancel := context.WithCancel(ctx)
	e.loopCancel = cancel
	e.loopMu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-lCtx.Done():
				return
			case <-ticker.C:
				e.Tick(lCtx)
			}
		}
	}()
}

// Stop stops the scheduler loop and cancels any active runs cleanly.
func (e *Engine) Stop() {
	e.loopMu.Lock()
	cancel := e.loopCancel
	e.loopCancel = nil
	e.loopMu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.runMu.Lock()
	for id, fn := range e.inFlight {
		fn()
		delete(e.inFlight, id)
	}
	e.runMu.Unlock()
	e.wg.Wait()
}

// Subscribe registers a listener for live task lifecycle, run, and log events.
func (e *Engine) Subscribe() (<-chan TaskEvent, func()) {
	ch := make(chan TaskEvent, 64)
	e.subMu.Lock()
	e.subID++
	id := e.subID
	e.subs[id] = ch
	e.subMu.Unlock()
	return ch, func() {
		e.subMu.Lock()
		if cur, ok := e.subs[id]; ok {
			delete(e.subs, id)
			close(cur)
		}
		e.subMu.Unlock()
	}
}

func (e *Engine) publish(ev TaskEvent) {
	if ev.Timestamp == 0 {
		ev.Timestamp = e.now().Unix()
	}
	e.subMu.Lock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	e.subMu.Unlock()
}

// ClearKill resets the internal killed flag (for owner unkill workflows).
func (e *Engine) ClearKill() {
	e.killed.Store(false)
}

func (e *Engine) killBlocked(ctx context.Context) bool {
	if e.killed.Load() {
		return true
	}
	if e.isKillActive != nil && e.isKillActive(ctx) {
		return true
	}
	return false
}

// CreateTask creates a validated task (always Draft if createdVia is chat or mcp).
func (e *Engine) CreateTask(ctx context.Context, in Task) (Task, error) {
	if in.CreatedVia == "chat" || in.CreatedVia == "mcp" || in.CreatedVia == "telegram" {
		if in.HasLiveWalletBinding() || in.ApprovalPolicy == ApprovalAuto {
			in.Status = StatusDraft
		}
		if in.CreatedVia == "chat" || in.CreatedVia == "mcp" {
			in.Status = StatusDraft
		}
	}
	t, err := e.store.CreateTask(ctx, in)
	if err != nil {
		return Task{}, err
	}
	e.publish(TaskEvent{Event: "task.updated", TaskID: t.ID, Task: &t})
	return t, nil
}

// InstantiateTemplate creates a task from a seeded or user-defined template ID.
func (e *Engine) InstantiateTemplate(ctx context.Context, templateID string, overrides Task) (Task, error) {
	tpl, err := e.store.GetTemplate(ctx, templateID)
	if err != nil {
		return Task{}, err
	}
	def := tpl.DefaultTask
	def.TemplateID = tpl.ID
	if strings.TrimSpace(overrides.Name) != "" {
		def.Name = overrides.Name
	}
	if overrides.GoalObjective != "" {
		def.GoalObjective = overrides.GoalObjective
	}
	if overrides.ScheduleKind != "" {
		def.ScheduleKind = overrides.ScheduleKind
		def.ScheduleSpec = overrides.ScheduleSpec
	}
	if overrides.ExecMode != "" {
		def.ExecMode = overrides.ExecMode
	}
	if overrides.ApprovalPolicy != "" {
		def.ApprovalPolicy = overrides.ApprovalPolicy
	}
	if overrides.ExecutionBinding.WalletHandle != "" || overrides.ExecutionBinding.AccountID != "" || overrides.ExecutionBinding.Venue != "" || overrides.ExecutionBinding.Symbol != "" {
		def.ExecutionBinding = overrides.ExecutionBinding
	}
	if overrides.Attachments.PersonaID != "" {
		def.Attachments.PersonaID = overrides.Attachments.PersonaID
	}
	if len(overrides.Attachments.SkillIDs) > 0 {
		def.Attachments.SkillIDs = overrides.Attachments.SkillIDs
	}
	if overrides.Attachments.StrategyID != "" {
		def.Attachments.StrategyID = overrides.Attachments.StrategyID
	}
	if overrides.Attachments.ScriptID != "" {
		def.Attachments.ScriptID = overrides.Attachments.ScriptID
	}
	if overrides.Attachments.AIProvider != "" {
		def.Attachments.AIProvider = overrides.Attachments.AIProvider
		def.Attachments.AIModel = overrides.Attachments.AIModel
	}
	if overrides.Constraints != (TaskConstraints{}) {
		def.Constraints = overrides.Constraints
	}
	if overrides.CreatedVia != "" {
		def.CreatedVia = overrides.CreatedVia
	}
	if overrides.Status != "" {
		def.Status = overrides.Status
	} else {
		def.Status = StatusDraft
	}
	return e.CreateTask(ctx, def)
}

// Activate transitions a task to active unless kill switch is asserted.
func (e *Engine) Activate(ctx context.Context, id string) (Task, error) {
	if e.killBlocked(ctx) {
		return Task{}, errors.New("kill switch active; cannot activate task")
	}
	t, err := e.store.SetTaskStatus(ctx, id, StatusActive, "")
	if err != nil {
		return Task{}, err
	}
	_, _ = e.store.AppendLog(ctx, TaskLog{TaskID: t.ID, Level: "info", Stage: "lifecycle", Message: "Task activated"})
	e.publish(TaskEvent{Event: "task.updated", TaskID: t.ID, Task: &t})
	return t, nil
}

// Pause pauses an active task cleanly.
func (e *Engine) Pause(ctx context.Context, id, reason string) (Task, error) {
	e.cancelTaskRuns(id)
	t, err := e.store.SetTaskStatus(ctx, id, StatusPaused, reason)
	if err != nil {
		return Task{}, err
	}
	msg := "Task paused"
	if strings.TrimSpace(reason) != "" {
		msg += ": " + strings.TrimSpace(reason)
	}
	_, _ = e.store.AppendLog(ctx, TaskLog{TaskID: t.ID, Level: "info", Stage: "lifecycle", Message: msg})
	e.publish(TaskEvent{Event: "task.updated", TaskID: t.ID, Task: &t})
	return t, nil
}

// StopTask marks a task stopped and cancels any in-flight run.
func (e *Engine) StopTask(ctx context.Context, id, reason string) (Task, error) {
	e.cancelTaskRuns(id)
	t, err := e.store.SetTaskStatus(ctx, id, StatusStopped, reason)
	if err != nil {
		return Task{}, err
	}
	_, _ = e.store.AppendLog(ctx, TaskLog{TaskID: t.ID, Level: "warn", Stage: "lifecycle", Message: "Task stopped"})
	e.publish(TaskEvent{Event: "task.updated", TaskID: t.ID, Task: &t})
	return t, nil
}

// KillAll halts every active/paused task immediately.
func (e *Engine) KillAll(ctx context.Context, reason, actor string) ([]string, error) {
	e.killed.Store(true)
	e.runMu.Lock()
	for id, fn := range e.inFlight {
		fn()
		delete(e.inFlight, id)
	}
	e.runMu.Unlock()
	if strings.TrimSpace(reason) == "" {
		reason = "emergency kill switch"
	}
	ids, err := e.store.KillAllTasks(ctx, fmt.Sprintf("%s (%s)", reason, actor))
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		_, _ = e.store.AppendLog(ctx, TaskLog{TaskID: id, Level: "error", Stage: "kill_switch", Message: "Killed: " + reason})
	}
	e.publish(TaskEvent{Event: "tasks.killed", Reason: reason})
	return ids, nil
}

func (e *Engine) cancelTaskRuns(taskID string) {
	e.runMu.Lock()
	for runID, fn := range e.inFlight {
		if strings.HasPrefix(runID, taskID+":") {
			fn()
			delete(e.inFlight, runID)
		}
	}
	e.runMu.Unlock()
}

// Tick advances scheduled due tasks and enforces expiry constraints.
func (e *Engine) Tick(ctx context.Context) {
	if e.killBlocked(ctx) {
		return
	}
	tasks, err := e.store.ListTasks(ctx, TaskFilter{Status: StatusActive, Limit: 200})
	if err != nil {
		return
	}
	nowUnix := e.now().Unix()
	for _, t := range tasks {
		if t.Constraints.ExpiresAt > 0 && nowUnix >= t.Constraints.ExpiresAt {
			_, _ = e.StopTask(ctx, t.ID, "task expired")
			continue
		}
		if t.NextRunAt > 0 && nowUnix >= t.NextRunAt {
			taskCopy := t
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				_, _ = e.RunTaskNow(ctx, taskCopy.ID, RunInput{TriggerSource: "scheduler"})
			}()
		}
	}
}

func (e *Engine) hasInFlightLocked(taskID string) bool {
	prefix := taskID + ":"
	for flightKey := range e.inFlight {
		if flightKey == taskID || strings.HasPrefix(flightKey, prefix) {
			return true
		}
	}
	return false
}

// EmitEvent evaluates active event-scheduled and event-filtered continuous tasks matching topic,
// enforces in-flight and cooldown deduplication, and returns the count of dispatched async runs.
func (e *Engine) EmitEvent(ctx context.Context, topic, payloadJSON string) int {
	if e == nil || e.killBlocked(ctx) {
		return 0
	}
	cleanTopic := strings.TrimSpace(topic)
	if cleanTopic == "" {
		return 0
	}
	tasks, err := e.store.ListTasks(ctx, TaskFilter{Status: StatusActive, Limit: 200})
	if err != nil {
		return 0
	}
	nowTime := e.now()
	nowUnix := nowTime.Unix()
	dispatched := 0
	for _, t := range tasks {
		if t.ScheduleKind != ScheduleEvent && !(t.ScheduleKind == ScheduleContinuous && strings.TrimSpace(t.ScheduleSpec.EventFilter) != "") {
			continue
		}
		if t.Constraints.MaxRuns > 0 && t.RunCount >= t.Constraints.MaxRuns {
			continue
		}
		if t.Constraints.ExpiresAt > 0 && nowUnix > t.Constraints.ExpiresAt {
			continue
		}
		if !matchesEventFilter(t.ScheduleSpec.EventFilter, cleanTopic) {
			continue
		}
		e.runMu.Lock()
		if e.hasInFlightLocked(t.ID) {
			e.runMu.Unlock()
			continue
		}
		if t.Constraints.CooldownSec > 0 {
			cd := time.Duration(t.Constraints.CooldownSec) * time.Second
			if prev, ok := e.lastEmit[t.ID]; ok && nowTime.Sub(prev) < cd {
				e.runMu.Unlock()
				continue
			}
			if t.LastRunAt > 0 && nowUnix-t.LastRunAt < t.Constraints.CooldownSec {
				e.runMu.Unlock()
				continue
			}
		}
		if e.lastEmit == nil {
			e.lastEmit = make(map[string]time.Time)
		}
		e.lastEmit[t.ID] = nowTime
		e.runMu.Unlock()

		taskID := t.ID
		dispatched++
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			_, _ = e.RunTaskNow(ctx, taskID, RunInput{
				TriggerSource:    "event:" + cleanTopic,
				EventTopic:       cleanTopic,
				EventPayloadJSON: payloadJSON,
			})
		}()
	}
	return dispatched
}

// RunTaskNow executes a task synchronously through its configured ExecMode (standalone / ai / hybrid),
// persists immutable run + structured telemetry logs, and triggers matching DAG child edges.
func (e *Engine) RunTaskNow(ctx context.Context, id string, input RunInput) (TaskRun, error) {
	if e.killBlocked(ctx) {
		return TaskRun{}, errors.New("kill switch active; execution refused")
	}
	t, err := e.store.GetTask(ctx, id)
	if err != nil {
		return TaskRun{}, err
	}
	if t.Status == StatusKilled {
		return TaskRun{}, errors.New("task is killed")
	}
	if t.Tainted && (t.HasLiveWalletBinding() || t.ApprovalPolicy == ApprovalAuto) {
		return TaskRun{}, errors.New("tainted task blocked from live or autonomous execution until reviewed")
	}
	if input.TriggerSource == "" {
		input.TriggerSource = "manual"
	}

	runID := randomHexID("run_")
	flightKey := t.ID + ":" + runID
	runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	e.runMu.Lock()
	e.inFlight[flightKey] = cancel
	e.runMu.Unlock()
	defer func() {
		cancel()
		e.runMu.Lock()
		delete(e.inFlight, flightKey)
		e.runMu.Unlock()
	}()

	started := e.now()
	_, _ = e.store.AppendLog(ctx, TaskLog{
		TaskID: t.ID, RunID: runID, Seq: 1,
		Level: "info", Stage: string(t.ExecMode),
		Message: fmt.Sprintf("Started %s (%s)", t.Name, input.TriggerSource),
	})

	var out RunOutput
	var runErr error
	var updatedHybridAt int64

	switch t.ExecMode {
	case ExecStandalone:
		// Strict guarantee: Standalone NEVER calls AIGovernor.
		if e.domainRunner != nil {
			out, runErr = e.domainRunner(runCtx, t, input)
		} else {
			out = RunOutput{Status: "completed", ActionTaken: "standalone_eval", DecisionSummary: "Deterministic task check completed"}
		}

	case ExecHybrid:
		if e.domainRunner != nil {
			out, runErr = e.domainRunner(runCtx, t, input)
		}
		cadence := t.Attachments.HybridCadenceSec
		if cadence <= 0 {
			cadence = 300
		}
		needGov := runErr == nil && (out.RequestGovernor || t.LastHybridAt == 0 || started.Unix()-t.LastHybridAt >= cadence)
		if needGov && e.aiGovernor != nil {
			personas, skills := e.resolveAttachments(ctx, t)
			govOut, gErr := e.aiGovernor(runCtx, t, personas, skills, out, input)
			if gErr != nil {
				runErr = gErr
			} else {
				updatedHybridAt = started.Unix()
				out.TokensIn += govOut.TokensIn
				out.TokensOut += govOut.TokensOut
				out.CostMicroUSD += govOut.CostMicroUSD
				if govOut.DecisionSummary != "" {
					out.DecisionSummary = govOut.DecisionSummary
				}
				if govOut.ActionTaken != "" {
					out.ActionTaken = govOut.ActionTaken
				}
				if govOut.SignalEmitted {
					out.SignalEmitted = true
				}
				if govOut.ThresholdHit {
					out.ThresholdHit = true
				}
				if govOut.ResultJSON != "" {
					out.ResultJSON = govOut.ResultJSON
				}
				out.RecommendPause = govOut.RecommendPause
				out.PauseReason = govOut.PauseReason
			}
		}

	case ExecAI:
		if e.aiGovernor != nil {
			personas, skills := e.resolveAttachments(ctx, t)
			out, runErr = e.aiGovernor(runCtx, t, personas, skills, RunOutput{}, input)
		} else {
			out = RunOutput{Status: "completed", ActionTaken: "ai_noop", DecisionSummary: "AI governor unavailable"}
		}
	}

	finished := e.now()
	runStatus := "completed"
	errStr := ""
	if runErr != nil {
		runStatus = "failed"
		errStr = runErr.Error()
	} else if out.Status != "" {
		runStatus = out.Status
	}

	run := TaskRun{
		ID:              runID,
		TaskID:          t.ID,
		ParentRunID:     input.ParentRunID,
		TriggerSource:   input.TriggerSource,
		ExecMode:        t.ExecMode,
		Status:          runStatus,
		Tainted:         t.Tainted,
		StartedAt:       started.Unix(),
		FinishedAt:      finished.Unix(),
		DurationMs:      finished.Sub(started).Milliseconds(),
		TokensIn:        out.TokensIn,
		TokensOut:       out.TokensOut,
		CostMicroUSD:    out.CostMicroUSD,
		PnLMicroUSD:     out.PnLMicroUSD,
		SatEarnedAtoms:  out.SatEarnedAtoms,
		SignalEmitted:   out.SignalEmitted,
		ThresholdHit:    out.ThresholdHit,
		ActionTaken:     out.ActionTaken,
		DecisionSummary: out.DecisionSummary,
		ResultJSON:      out.ResultJSON,
		Error:           errStr,
	}

	updatedTask, recErr := e.store.RecordRun(context.Background(), run, updatedHybridAt)
	if recErr == nil {
		if out.RecommendPause && t.Constraints.StopOnReversal {
			updatedTask, _ = e.Pause(context.Background(), t.ID, out.PauseReason)
		}
		e.publish(TaskEvent{Event: "task.run.completed", TaskID: t.ID, Task: &updatedTask, Run: &run})
	}
	logLevel := "info"
	logMsg := out.DecisionSummary
	if runErr != nil {
		logLevel = "error"
		logMsg = runErr.Error()
	} else if logMsg == "" {
		logMsg = "Run completed: " + runStatus
	}
	logEntry, _ := e.store.AppendLog(context.Background(), TaskLog{
		TaskID: t.ID, RunID: runID, Seq: 2,
		Level: logLevel, Stage: "result", Message: logMsg,
	})
	e.publish(TaskEvent{Event: "task.log", TaskID: t.ID, Log: &logEntry})

	if input.Depth < MaxDAGDepth {
		e.dispatchChildren(ctx, t, run, out, input.Depth+1)
	}
	return run, runErr
}

func (e *Engine) resolveAttachments(ctx context.Context, t Task) ([]TaskPersona, []TaskSkill) {
	libs, err := e.store.ListLibraries(ctx)
	if err != nil {
		return nil, nil
	}
	var personas []TaskPersona
	for _, p := range libs.Personas {
		if p.ID == t.Attachments.PersonaID {
			personas = append(personas, p)
		}
	}
	skillSet := map[string]bool{}
	for _, sid := range t.Attachments.SkillIDs {
		skillSet[sid] = true
	}
	var skills []TaskSkill
	for _, sk := range libs.Skills {
		if skillSet[sk.ID] {
			skills = append(skills, sk)
		}
	}
	return personas, skills
}

func (e *Engine) dispatchChildren(ctx context.Context, parent Task, run TaskRun, out RunOutput, nextDepth int) {
	if e.killBlocked(ctx) || nextDepth >= MaxDAGDepth {
		return
	}
	edges, err := e.store.ListEdges(ctx, parent.ID)
	if err != nil {
		return
	}
	for _, edge := range edges {
		if edge.ParentTaskID != parent.ID {
			continue
		}
		match := false
		switch edge.Condition {
		case EdgeAlways:
			match = true
		case EdgeOnSuccess:
			match = run.Status == "completed"
		case EdgeOnSignal:
			match = run.Status == "completed" && out.SignalEmitted
		case EdgeOnThreshold:
			match = run.Status == "completed" && out.ThresholdHit
		case EdgeOnFailure:
			match = run.Status == "failed"
		}
		if !match {
			continue
		}
		child, cErr := e.store.GetTask(ctx, edge.ChildTaskID)
		if cErr != nil || child.Status == StatusKilled || child.Status == StatusStopped {
			continue
		}
		childID := child.ID
		parentResult := out.ResultJSON
		if parentResult == "" && out.DecisionSummary != "" {
			b, _ := json.Marshal(map[string]any{"summary": out.DecisionSummary, "action": out.ActionTaken})
			parentResult = string(b)
		}
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			_, _ = e.RunTaskNow(ctx, childID, RunInput{
				TriggerSource:    "dag:" + parent.ID,
				ParentRunID:      run.ID,
				ParentOutputJSON: parentResult,
				Depth:            nextDepth,
			})
		}()
	}
}

// CreateSwarm builds a root coordinator task and linked worker child DAG cleanly.
func (e *Engine) CreateSwarm(ctx context.Context, req SwarmRequest) (SwarmResponse, error) {
	if len(req.Children) == 0 || len(req.Children) > MaxDAGFanOut {
		return SwarmResponse{}, fmt.Errorf("swarm children must be 1..%d", MaxDAGFanOut)
	}
	groupID := randomHexID("swm_")
	rootTask, err := e.CreateTask(ctx, Task{
		Name:             req.Name,
		GoalDomain:       req.GoalDomain,
		GoalObjective:    req.GoalObjective,
		ScheduleKind:     req.ScheduleKind,
		ScheduleSpec:     req.ScheduleSpec,
		ExecMode:         ExecAI,
		ApprovalPolicy:   req.ApprovalPolicy,
		ExecutionBinding: req.ExecutionBinding,
		Constraints:      req.Constraints,
		Status:           StatusDraft,
		SwarmGroupID:     groupID,
		CreatedVia:       req.CreatedVia,
	})
	if err != nil {
		return SwarmResponse{}, err
	}

	children := make([]Task, 0, len(req.Children))
	edges := make([]TaskEdge, 0, len(req.Children))

	for i, ch := range req.Children {
		gd := ch.GoalDomain
		if gd == "" {
			gd = rootTask.GoalDomain
		}
		em := ch.ExecMode
		if em == "" {
			em = ExecAI
		}
		childIn := Task{
			Name:             ch.Name,
			GoalDomain:       gd,
			GoalObjective:    ch.Objective,
			ScheduleKind:     ScheduleEvent,
			ScheduleSpec:     ScheduleSpec{EventFilter: "dag:" + rootTask.ID},
			ExecMode:         em,
			ApprovalPolicy:   rootTask.ApprovalPolicy,
			ExecutionBinding: rootTask.ExecutionBinding,
			Constraints:      rootTask.Constraints,
			Attachments:      TaskAttachments{PersonaID: ch.PersonaID, SkillIDs: ch.SkillIDs},
			Status:           StatusDraft,
			ParentTaskID:     rootTask.ID,
			SwarmGroupID:     groupID,
			TemplateID:       ch.TemplateID,
			CreatedVia:       "swarm",
		}
		if childIn.GoalObjective == "" {
			childIn.GoalObjective = rootTask.GoalObjective
		}
		createdChild, cErr := e.CreateTask(ctx, childIn)
		if cErr != nil {
			_ = e.store.DeleteTask(ctx, rootTask.ID)
			for _, prev := range children {
				_ = e.store.DeleteTask(ctx, prev.ID)
			}
			return SwarmResponse{}, fmt.Errorf("swarm child[%d] failed: %w", i, cErr)
		}
		children = append(children, createdChild)
	}

	for i, ch := range req.Children {
		cond := ch.Condition
		if cond == "" {
			cond = EdgeOnSuccess
		}
		parents := ch.DependsOn
		if len(parents) == 0 {
			ed, eErr := e.store.AddEdge(ctx, TaskEdge{
				ParentTaskID: rootTask.ID,
				ChildTaskID:  children[i].ID,
				Condition:    cond,
			})
			if eErr != nil {
				return SwarmResponse{}, eErr
			}
			edges = append(edges, ed)
			continue
		}
		for _, pidIdx := range parents {
			if pidIdx < 0 || pidIdx >= i || pidIdx >= len(children) {
				return SwarmResponse{}, fmt.Errorf("invalid dependsOn index %d on child %d", pidIdx, i)
			}
			ed, eErr := e.store.AddEdge(ctx, TaskEdge{
				ParentTaskID: children[pidIdx].ID,
				ChildTaskID:  children[i].ID,
				Condition:    cond,
			})
			if eErr != nil {
				return SwarmResponse{}, eErr
			}
			edges = append(edges, ed)
		}
	}

	return SwarmResponse{
		SwarmGroupID: groupID,
		RootTask:     rootTask,
		Children:     children,
		Edges:        edges,
	}, nil
}
