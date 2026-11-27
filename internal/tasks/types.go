package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// GoalDomain constrains what objective and engine family a task may operate on.
type GoalDomain string

const (
	GoalMining        GoalDomain = "mining"
	GoalTrading       GoalDomain = "trading"
	GoalNews          GoalDomain = "news"
	GoalNotifications GoalDomain = "notifications"
	GoalPortfolio     GoalDomain = "portfolio"
	GoalResearch      GoalDomain = "research"
	GoalMonitoring    GoalDomain = "monitoring"
	GoalCustom        GoalDomain = "custom"
)

// ScheduleKind controls when a task executes.
type ScheduleKind string

const (
	ScheduleContinuous ScheduleKind = "continuous" // round-synchronized or real-time stream loop
	ScheduleInterval   ScheduleKind = "interval"   // fixed interval in seconds
	ScheduleCron       ScheduleKind = "cron"       // cron expression
	ScheduleOnce       ScheduleKind = "once"       // one-shot immediate or scheduled timestamp
	ScheduleEvent      ScheduleKind = "event"      // event bus filter (price, indicator, news, round-phase, parent)
)

// ExecMode determines whether AI models are invoked or avoided.
type ExecMode string

const (
	ExecStandalone ExecMode = "standalone" // deterministic Go/FasedScript/Mining loop — zero AI calls
	ExecAI         ExecMode = "ai"         // Agent V2 LLM turn with attached Goal + Persona + Skills
	ExecHybrid     ExecMode = "hybrid"     // deterministic fast loop + periodic AI strategic governor
)

// ApprovalPolicy controls autonomous execution gates.
type ApprovalPolicy string

const (
	ApprovalManual   ApprovalPolicy = "manual"
	ApprovalSemiAuto ApprovalPolicy = "semi_auto"
	ApprovalAuto     ApprovalPolicy = "auto"
)

// TaskStatus tracks lifecycle state of a task.
type TaskStatus string

const (
	StatusDraft     TaskStatus = "draft"
	StatusActive    TaskStatus = "active"
	StatusPaused    TaskStatus = "paused"
	StatusCompleted TaskStatus = "completed"
	StatusStopped   TaskStatus = "stopped"
	StatusKilled    TaskStatus = "killed"
	StatusError     TaskStatus = "error"
)

// EdgeCondition gates child task triggering along a DAG edge.
type EdgeCondition string

const (
	EdgeOnSuccess   EdgeCondition = "on_success"
	EdgeOnSignal    EdgeCondition = "on_signal"
	EdgeOnThreshold EdgeCondition = "on_threshold"
	EdgeOnFailure   EdgeCondition = "on_failure"
	EdgeAlways      EdgeCondition = "always"
)

// ScheduleSpec captures schedule parameters matching ScheduleKind.
type ScheduleSpec struct {
	IntervalSec int64  `json:"intervalSec,omitempty"`
	CronExpr    string `json:"cronExpr,omitempty"`
	RunAt       int64  `json:"runAt,omitempty"`
	EventFilter string `json:"eventFilter,omitempty"`
	RoundPhase  string `json:"roundPhase,omitempty"` // commit | reveal | settle
}

// ExecutionBinding links a task to a wallet, exchange/broker venue, or alert channel.
type ExecutionBinding struct {
	WalletHandle         string   `json:"walletHandle,omitempty"` // e.g. @wallet:wlt_...
	Venue                string   `json:"venue,omitempty"`
	Symbol               string   `json:"symbol,omitempty"`
	AccountID            string   `json:"accountId,omitempty"`
	Broker               string   `json:"broker,omitempty"`
	Cluster              string   `json:"cluster,omitempty"` // devnet | mainnet-beta
	NotificationChannels []string `json:"notificationChannels,omitempty"`
}

// TaskAttachments binds composable persona, skills, strategy, script, and AI model overrides.
type TaskAttachments struct {
	PersonaID        string   `json:"personaId,omitempty"`
	SkillIDs         []string `json:"skillIds,omitempty"`
	StrategyID       string   `json:"strategyId,omitempty"`
	ScriptID         string   `json:"scriptId,omitempty"`
	AIProvider       string   `json:"aiProvider,omitempty"`
	AIModel          string   `json:"aiModel,omitempty"`
	HybridCadenceSec int64    `json:"hybridCadenceSec,omitempty"`
	PromptTemplate   string   `json:"promptTemplate,omitempty"`
}

// TaskConstraints bounds budget, run counts, lifetimes, and reversal safeguards.
type TaskConstraints struct {
	MaxRuns          int64   `json:"maxRuns,omitempty"`
	ExpiresAt        int64   `json:"expiresAt,omitempty"`
	MaxSpendUSD      float64 `json:"maxSpendUsd,omitempty"`
	MaxSpendLamports uint64  `json:"maxSpendLamports,omitempty"`
	DailyLossCapUSD  float64 `json:"dailyLossCapUsd,omitempty"`
	StopOnReversal   bool    `json:"stopOnReversal,omitempty"`
	CooldownSec      int64   `json:"cooldownSec,omitempty"`
}

// Task is the authoritative universal task unit across Mining, Trading, News, and Ops.
type Task struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	GoalDomain       GoalDomain       `json:"goalDomain"`
	GoalObjective    string           `json:"goalObjective"`
	ScheduleKind     ScheduleKind     `json:"scheduleKind"`
	ScheduleSpec     ScheduleSpec     `json:"scheduleSpec"`
	ExecMode         ExecMode         `json:"execMode"`
	ApprovalPolicy   ApprovalPolicy   `json:"approvalPolicy"`
	ExecutionBinding ExecutionBinding `json:"executionBinding"`
	Attachments      TaskAttachments  `json:"attachments"`
	Constraints      TaskConstraints  `json:"constraints"`
	Status           TaskStatus       `json:"status"`
	Tainted          bool             `json:"tainted,omitempty"`
	ParentTaskID     string           `json:"parentTaskId,omitempty"`
	SwarmGroupID     string           `json:"swarmGroupId,omitempty"`
	TemplateID       string           `json:"templateId,omitempty"`
	CreatedVia       string           `json:"createdVia"`
	PolicyHash       string           `json:"policyHash"`
	LastRunAt        int64            `json:"lastRunAt,omitempty"`
	NextRunAt        int64            `json:"nextRunAt,omitempty"`
	LastHybridAt     int64            `json:"lastHybridAt,omitempty"`
	RunCount         int64            `json:"runCount"`
	ErrorCount       int64            `json:"errorCount"`
	LastError        string           `json:"lastError,omitempty"`
	CreatedAt        int64            `json:"createdAt"`
	UpdatedAt        int64            `json:"updatedAt"`
}

// TaskEdge represents a directed acyclic dependency between a parent and child task.
type TaskEdge struct {
	ID             string        `json:"id"`
	ParentTaskID   string        `json:"parentTaskId"`
	ChildTaskID    string        `json:"childTaskId"`
	Condition      EdgeCondition `json:"condition"`
	PayloadMapJSON string        `json:"payloadMapJson,omitempty"`
	CreatedAt      int64         `json:"createdAt"`
}

// TaskRun records one execution invocation of a task.
type TaskRun struct {
	ID              string   `json:"id"`
	TaskID          string   `json:"taskId"`
	ParentRunID     string   `json:"parentRunId,omitempty"`
	TriggerSource   string   `json:"triggerSource"`
	ExecMode        ExecMode `json:"execMode"`
	Status          string   `json:"status"` // completed | skipped | failed | killed | pending_approval
	Tainted         bool     `json:"tainted,omitempty"`
	StartedAt       int64    `json:"startedAt"`
	FinishedAt      int64    `json:"finishedAt"`
	DurationMs      int64    `json:"durationMs"`
	TokensIn        int64    `json:"tokensIn,omitempty"`
	TokensOut       int64    `json:"tokensOut,omitempty"`
	CostMicroUSD    int64    `json:"costMicroUsd,omitempty"`
	PnLMicroUSD     int64    `json:"pnlMicroUsd,omitempty"`
	SatEarnedAtoms  int64    `json:"satEarnedAtoms,omitempty"`
	SignalEmitted   bool     `json:"signalEmitted,omitempty"`
	ThresholdHit    bool     `json:"thresholdHit,omitempty"`
	ActionTaken     string   `json:"actionTaken,omitempty"`
	DecisionSummary string   `json:"decisionSummary,omitempty"`
	ResultJSON      string   `json:"resultJson,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// TaskLog is a bounded structured telemetry line emitted during task lifecycle or runs.
type TaskLog struct {
	ID        int64  `json:"id"`
	TaskID    string `json:"taskId"`
	RunID     string `json:"runId,omitempty"`
	Seq       int    `json:"seq"`
	Timestamp int64  `json:"timestamp"`
	Level     string `json:"level"` // info | warn | error
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	MetaJSON  string `json:"metaJson,omitempty"`
}

// TaskPersona defines an attachable AI operator persona.
type TaskPersona struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Role         string       `json:"role"`
	GoalDomains  []GoalDomain `json:"goalDomains"`
	SystemPrompt string       `json:"systemPrompt"`
	RiskProfile  string       `json:"riskProfile"`
	BuiltIn      bool         `json:"builtIn"`
	CreatedAt    int64        `json:"createdAt"`
}

// TaskSkill defines a goal-scoped AI capability module.
type TaskSkill struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	GoalDomain   GoalDomain `json:"goalDomain"`
	Instructions string     `json:"instructions"`
	AllowedTools []string   `json:"allowedTools"`
	Source       string     `json:"source"`
	SHA256       string     `json:"sha256"`
	BuiltIn      bool       `json:"builtIn"`
	CreatedAt    int64      `json:"createdAt"`
}

// TaskTemplate defines a ready-to-launch task blueprint.
type TaskTemplate struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	GoalDomain  GoalDomain `json:"goalDomain"`
	ExecMode    ExecMode   `json:"execMode"`
	DefaultTask Task       `json:"defaultTask"`
	BuiltIn     bool       `json:"builtIn"`
	CreatedAt   int64      `json:"createdAt"`
}

// TaskLibraries bundles personas, skills, and templates for UI/API inspection.
type TaskLibraries struct {
	Personas  []TaskPersona  `json:"personas"`
	Skills    []TaskSkill    `json:"skills"`
	Templates []TaskTemplate `json:"templates"`
}

// TaskEvent is published to live WebSocket listeners on changes or run progress.
type TaskEvent struct {
	Event     string   `json:"event"` // task.updated | task.deleted | task.run.completed | task.log | tasks.killed
	TaskID    string   `json:"taskId,omitempty"`
	Task      *Task    `json:"task,omitempty"`
	Run       *TaskRun `json:"run,omitempty"`
	Log       *TaskLog `json:"log,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Timestamp int64    `json:"timestamp"`
}

// TaskFilter narrows task listing queries.
type TaskFilter struct {
	GoalDomain GoalDomain `json:"goalDomain,omitempty"`
	Status     TaskStatus `json:"status,omitempty"`
	Query      string     `json:"query,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}

// SwarmChildSpec configures one child worker in a DAG swarm instantiation.
type SwarmChildSpec struct {
	Name       string        `json:"name"`
	TemplateID string        `json:"templateId,omitempty"`
	GoalDomain GoalDomain    `json:"goalDomain,omitempty"`
	ExecMode   ExecMode      `json:"execMode,omitempty"`
	PersonaID  string        `json:"personaId,omitempty"`
	SkillIDs   []string      `json:"skillIds,omitempty"`
	Objective  string        `json:"objective,omitempty"`
	Condition  EdgeCondition `json:"condition,omitempty"`
	DependsOn  []int         `json:"dependsOn,omitempty"` // zero-based indices into Children; default parent root
}

// SwarmRequest spawns a coordinated DAG task hierarchy from one root request.
type SwarmRequest struct {
	Name             string           `json:"name"`
	GoalDomain       GoalDomain       `json:"goalDomain"`
	GoalObjective    string           `json:"goalObjective"`
	ScheduleKind     ScheduleKind     `json:"scheduleKind,omitempty"`
	ScheduleSpec     ScheduleSpec     `json:"scheduleSpec,omitempty"`
	ApprovalPolicy   ApprovalPolicy   `json:"approvalPolicy,omitempty"`
	ExecutionBinding ExecutionBinding `json:"executionBinding,omitempty"`
	Constraints      TaskConstraints  `json:"constraints,omitempty"`
	CreatedVia       string           `json:"createdVia,omitempty"`
	Children         []SwarmChildSpec `json:"children"`
}

// SwarmResponse returns the created root task, child tasks, and DAG edges.
type SwarmResponse struct {
	SwarmGroupID string     `json:"swarmGroupId"`
	RootTask     Task       `json:"rootTask"`
	Children     []Task     `json:"children"`
	Edges        []TaskEdge `json:"edges"`
}

const (
	MaxDAGDepth       = 6
	MaxDAGFanOut      = 8
	maxLogsPerTask    = 500
	maxTaskNameBytes  = 96
	maxObjectiveBytes = 1024
)

var safeIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,79}$`)

// ValidGoalDomain reports whether d is a supported GoalDomain.
func ValidGoalDomain(d GoalDomain) bool {
	switch d {
	case GoalMining, GoalTrading, GoalNews, GoalNotifications, GoalPortfolio, GoalResearch, GoalMonitoring, GoalCustom:
		return true
	default:
		return false
	}
}

// ValidScheduleKind reports whether k is a supported ScheduleKind.
func ValidScheduleKind(k ScheduleKind) bool {
	switch k {
	case ScheduleContinuous, ScheduleInterval, ScheduleCron, ScheduleOnce, ScheduleEvent:
		return true
	default:
		return false
	}
}

// ValidExecMode reports whether m is a supported ExecMode.
func ValidExecMode(m ExecMode) bool {
	switch m {
	case ExecStandalone, ExecAI, ExecHybrid:
		return true
	default:
		return false
	}
}

// ValidApprovalPolicy reports whether p is a supported ApprovalPolicy.
func ValidApprovalPolicy(p ApprovalPolicy) bool {
	switch p {
	case ApprovalManual, ApprovalSemiAuto, ApprovalAuto:
		return true
	default:
		return false
	}
}

// ValidEdgeCondition reports whether c is a supported EdgeCondition.
func ValidEdgeCondition(c EdgeCondition) bool {
	switch c {
	case EdgeOnSuccess, EdgeOnSignal, EdgeOnThreshold, EdgeOnFailure, EdgeAlways:
		return true
	default:
		return false
	}
}

// HasLiveWalletBinding reports whether the task targets a live custody wallet reference.
func (t Task) HasLiveWalletBinding() bool {
	return strings.HasPrefix(strings.TrimSpace(t.ExecutionBinding.WalletHandle), "@wallet:") ||
		strings.TrimSpace(t.ExecutionBinding.AccountID) != ""
}

// NeedsOwnerStepUpToActivate reports whether activating this task requires owner step-up.
func (t Task) NeedsOwnerStepUpToActivate() bool {
	if t.Tainted {
		return true
	}
	if t.ApprovalPolicy == ApprovalAuto && (t.HasLiveWalletBinding() || t.GoalDomain == GoalMining || t.GoalDomain == GoalTrading) {
		return true
	}
	return false
}

// ComputeTaskPolicyHash returns a deterministic SHA-256 commitment over security-critical fields.
func ComputeTaskPolicyHash(t Task) string {
	canonical := struct {
		GoalDomain       GoalDomain       `json:"goalDomain"`
		GoalObjective    string           `json:"goalObjective"`
		ScheduleKind     ScheduleKind     `json:"scheduleKind"`
		ScheduleSpec     ScheduleSpec     `json:"scheduleSpec"`
		ExecMode         ExecMode         `json:"execMode"`
		ApprovalPolicy   ApprovalPolicy   `json:"approvalPolicy"`
		ExecutionBinding ExecutionBinding `json:"executionBinding"`
		Attachments      TaskAttachments  `json:"attachments"`
		Constraints      TaskConstraints  `json:"constraints"`
		ParentTaskID     string           `json:"parentTaskId,omitempty"`
		Tainted          bool             `json:"tainted,omitempty"`
	}{
		GoalDomain:       t.GoalDomain,
		GoalObjective:    strings.TrimSpace(t.GoalObjective),
		ScheduleKind:     t.ScheduleKind,
		ScheduleSpec:     t.ScheduleSpec,
		ExecMode:         t.ExecMode,
		ApprovalPolicy:   t.ApprovalPolicy,
		ExecutionBinding: t.ExecutionBinding,
		Attachments:      t.Attachments,
		Constraints:      t.Constraints,
		ParentTaskID:     strings.TrimSpace(t.ParentTaskID),
		Tainted:          t.Tainted,
	}
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NormalizeAndValidate validates task structure and fills defaults safely.
func NormalizeAndValidate(t *Task) error {
	if t == nil {
		return errors.New("task is nil")
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > maxTaskNameBytes {
		return fmt.Errorf("task name must be 1..%d bytes", maxTaskNameBytes)
	}
	t.GoalObjective = strings.TrimSpace(t.GoalObjective)
	if len(t.GoalObjective) > maxObjectiveBytes {
		return fmt.Errorf("goalObjective exceeds %d bytes", maxObjectiveBytes)
	}
	if t.GoalDomain == "" {
		t.GoalDomain = GoalCustom
	}
	if !ValidGoalDomain(t.GoalDomain) {
		return fmt.Errorf("invalid goalDomain %q", t.GoalDomain)
	}
	if t.ScheduleKind == "" {
		t.ScheduleKind = ScheduleInterval
	}
	if !ValidScheduleKind(t.ScheduleKind) {
		return fmt.Errorf("invalid scheduleKind %q", t.ScheduleKind)
	}
	if t.ScheduleKind == ScheduleInterval && t.ScheduleSpec.IntervalSec <= 0 {
		t.ScheduleSpec.IntervalSec = 60
	}
	if t.ScheduleKind == ScheduleContinuous && t.ScheduleSpec.IntervalSec <= 0 {
		t.ScheduleSpec.IntervalSec = 10
	}
	if t.ScheduleKind == ScheduleCron && strings.TrimSpace(t.ScheduleSpec.CronExpr) == "" {
		return errors.New("cron schedule requires cronExpr")
	}
	if t.ExecMode == "" {
		t.ExecMode = ExecStandalone
	}
	if !ValidExecMode(t.ExecMode) {
		return fmt.Errorf("invalid execMode %q", t.ExecMode)
	}
	if t.ApprovalPolicy == "" {
		t.ApprovalPolicy = ApprovalManual
	}
	if !ValidApprovalPolicy(t.ApprovalPolicy) {
		return fmt.Errorf("invalid approvalPolicy %q", t.ApprovalPolicy)
	}
	if t.Status == "" {
		t.Status = StatusDraft
	}
	if t.CreatedVia == "" {
		t.CreatedVia = "ui"
	}
	if t.Constraints.MaxSpendUSD < 0 || t.Constraints.DailyLossCapUSD < 0 || t.Constraints.MaxRuns < 0 || t.Constraints.CooldownSec < 0 {
		return errors.New("task constraints cannot be negative")
	}
	if t.ID != "" && !safeIDRe.MatchString(t.ID) {
		return errors.New("invalid task id format")
	}
	if t.ParentTaskID != "" && !safeIDRe.MatchString(t.ParentTaskID) {
		return errors.New("invalid parentTaskId format")
	}
	if len(t.Attachments.SkillIDs) > 16 {
		return errors.New("too many attached skills (max 16)")
	}
	t.PolicyHash = ComputeTaskPolicyHash(*t)
	return nil
}

func approvalRank(p ApprovalPolicy) int {
	switch p {
	case ApprovalManual:
		return 1
	case ApprovalSemiAuto:
		return 2
	case ApprovalAuto:
		return 3
	default:
		return 0
	}
}

// CheckChildPrivilege verifies that child never escalates goal, wallet, approval, or budget bounds above parent.
func CheckChildPrivilege(parent, child Task) error {
	if approvalRank(child.ApprovalPolicy) > approvalRank(parent.ApprovalPolicy) {
		return errors.New("child task cannot loosen approvalPolicy beyond parent")
	}
	if child.HasLiveWalletBinding() {
		if !parent.HasLiveWalletBinding() {
			return errors.New("child task cannot bind a live wallet when parent has no live wallet")
		}
		if strings.TrimSpace(child.ExecutionBinding.WalletHandle) != "" &&
			strings.TrimSpace(child.ExecutionBinding.WalletHandle) != strings.TrimSpace(parent.ExecutionBinding.WalletHandle) {
			return errors.New("child task cannot switch live wallet handle from parent")
		}
	}
	if parent.GoalDomain != GoalResearch && parent.GoalDomain != GoalCustom && parent.GoalDomain != GoalPortfolio {
		allowed := child.GoalDomain == parent.GoalDomain ||
			child.GoalDomain == GoalNotifications ||
			child.GoalDomain == GoalMonitoring
		if !allowed {
			return fmt.Errorf("child goalDomain %q exceeds parent scope %q", child.GoalDomain, parent.GoalDomain)
		}
	}
	if parent.Constraints.MaxSpendUSD > 0 && (child.Constraints.MaxSpendUSD <= 0 || child.Constraints.MaxSpendUSD > parent.Constraints.MaxSpendUSD) {
		return errors.New("child maxSpendUsd exceeds parent cap")
	}
	if parent.Constraints.MaxSpendLamports > 0 && (child.Constraints.MaxSpendLamports == 0 || child.Constraints.MaxSpendLamports > parent.Constraints.MaxSpendLamports) {
		return errors.New("child maxSpendLamports exceeds parent cap")
	}
	return nil
}
