package desk

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	// ModeShadow evaluates every seat end-to-end and records simulated tickets
	// without touching paper capital or opening paper positions.
	ModeShadow = "shadow"
	// ModePaper executes approved paper tickets against isolated paper capital
	// in desk.sqlite. Live execution is strictly locked out in code.
	ModePaper = "paper"
)

var (
	// ErrLiveExecutionDisabled is returned whenever any caller attempts to set
	// or execute a live trading mode.
	ErrLiveExecutionDisabled = errors.New("live trading execution is disabled; desk is strictly locked to shadow/paper mode")
	// ErrKillSwitchActive is returned when either the desk kill switch or an
	// external kill switch (Telegram/Automation) is active.
	ErrKillSwitchActive = errors.New("desk kill switch is active; order tickets and fills are halted")
	// ErrDeskPaused is returned when the desk is paused by the owner.
	ErrDeskPaused = errors.New("desk is paused; resume before running new cycles or approving tickets")
	// ErrSeatPermissionDenied is returned when a seat attempts a tool outside its policy.
	ErrSeatPermissionDenied = errors.New("seat permission denied for requested tool")
	// ErrRiskRejected is returned when hard Go risk validation rejects a trade ticket.
	ErrRiskRejected = errors.New("risk limits rejected order ticket")
	// ErrSymbolBenched is returned when a symbol or script is inside a cooldown bench window.
	ErrSymbolBenched = errors.New("symbol or script is on cooldown bench after consecutive losses")
	// ErrNotFound indicates a missing ticket, position, or lesson record.
	ErrNotFound = errors.New("desk record not found")
)

var safeSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// SeatID identifies each specialist role on the Multi-Seat Trading Desk.
type SeatID string

const (
	SeatChief    SeatID = "chief"
	SeatScout    SeatID = "scout"
	SeatChartist SeatID = "chartist"
	SeatQuant    SeatID = "quant"
	SeatSkeptic  SeatID = "skeptic"
	SeatRisk     SeatID = "risk"
	SeatExecutor SeatID = "executor"
	SeatJournal  SeatID = "journal"
)

// SeatPolicy defines immutable Go-enforced capability bounds per desk seat.
type SeatPolicy struct {
	ID               SeatID   `json:"id"`
	Title            string   `json:"title"`
	Role             string   `json:"role"`
	AllowedTools     []string `json:"allowedTools"`
	CanExecutePaper  bool     `json:"canExecutePaper"`
	CanModifyLimits  bool     `json:"canModifyLimits"`
	CanProposeTrades bool     `json:"canProposeTrades"`
}

var seatPolicies = []SeatPolicy{
	{
		ID:               SeatChief,
		Title:            "Chief of Staff",
		Role:             "Coordinates route order, briefs PM, and escalates disagreements cleanly.",
		AllowedTools:     []string{"get_desk_state", "list_desk_journal", "list_desk_lessons"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: false,
	},
	{
		ID:               SeatScout,
		Title:            "Scout",
		Role:             "Scans WorldIntel catalysts and macro headlines; tags every item untrusted.",
		AllowedTools:     []string{"get_world_intel", "get_prediction_markets", "list_desk_lessons"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: true,
	},
	{
		ID:               SeatChartist,
		Title:            "Chartist",
		Role:             "Scores structure, trend, ATR volatility, RSI, and R:R cleanly.",
		AllowedTools:     []string{"get_market_candles", "get_orderbook", "get_footprint", "list_desk_lessons"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: true,
	},
	{
		ID:               SeatQuant,
		Title:            "Quant",
		Role:             "Checks FasedScript signals and 4-Gate validation readiness over MCP.",
		AllowedTools:     []string{"list_lab_scripts", "run_fasedscript_backtest", "run_four_gate_validation", "list_desk_lessons"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: true,
	},
	{
		ID:               SeatSkeptic,
		Title:            "Skeptic",
		Role:             "Adversarial reviewer; attacks weak/unsourced/unvalidated setups and never proposes trades.",
		AllowedTools:     []string{"get_world_intel", "get_market_candles", "list_desk_journal", "list_desk_lessons"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: false,
	},
	{
		ID:               SeatRisk,
		Title:            "Risk Officer",
		Role:             "Evaluates immutable Go risk caps and sizes tickets conservatively; cannot raise limits.",
		AllowedTools:     []string{"get_desk_state", "list_desk_journal", "list_desk_lessons"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: false,
	},
	{
		ID:               SeatExecutor,
		Title:            "Paper Executor",
		Role:             "Sole seat permitted to open or close strictly paper/shadow tickets after Risk + PM approval.",
		AllowedTools:     []string{"place_paper_order", "close_paper_position", "get_desk_state"},
		CanExecutePaper:  true,
		CanModifyLimits:  false,
		CanProposeTrades: false,
	},
	{
		ID:               SeatJournal,
		Title:            "Journal",
		Role:             "Records every trade, shadow signal, kill reason, decay ratio, and actionable lesson.",
		AllowedTools:     []string{"list_desk_journal", "list_desk_lessons", "record_journal_entry"},
		CanExecutePaper:  false,
		CanModifyLimits:  false,
		CanProposeTrades: false,
	},
}

// DefaultSeatPolicies returns a deep copy of the immutable Go-enforced desk policies.
func DefaultSeatPolicies() []SeatPolicy {
	out := make([]SeatPolicy, len(seatPolicies))
	for i, p := range seatPolicies {
		tools := make([]string, len(p.AllowedTools))
		copy(tools, p.AllowedTools)
		p.AllowedTools = tools
		out[i] = p
	}
	return out
}

// AssertSeatToolAllowed enforces tool-permission locks in Go rather than prompts.
// Only SeatExecutor can invoke place_paper_order / close_paper_position, and
// NO seat can invoke modify_risk_limits or override kill switches.
func AssertSeatToolAllowed(seat SeatID, tool string) error {
	cleanTool := strings.TrimSpace(tool)
	if cleanTool == "" || cleanTool == "modify_risk_limits" || cleanTool == "clear_kill_switch" || cleanTool == "execute_live_order" {
		return fmt.Errorf("%w: tool %q is permanently locked out for agents", ErrSeatPermissionDenied, cleanTool)
	}
	for _, p := range seatPolicies {
		if p.ID != seat {
			continue
		}
		if (cleanTool == "place_paper_order" || cleanTool == "close_paper_position") && !p.CanExecutePaper {
			return fmt.Errorf("%w: seat %q cannot execute orders", ErrSeatPermissionDenied, seat)
		}
		for _, allowed := range p.AllowedTools {
			if allowed == cleanTool {
				return nil
			}
		}
		return fmt.Errorf("%w: seat %q is not permitted to invoke %q", ErrSeatPermissionDenied, seat, cleanTool)
	}
	return fmt.Errorf("%w: unknown seat %q", ErrSeatPermissionDenied, seat)
}

// RiskLimits defines code-enforced trading bounds stored in desk.sqlite.
type RiskLimits struct {
	Mode                 string  `json:"mode"`
	InitialCapitalUSD    float64 `json:"initialCapitalUsd"`
	RiskPerTradePct      float64 `json:"riskPerTradePct"`
	MaxPositionPct       float64 `json:"maxPositionPct"`
	MaxOpenPositions     int     `json:"maxOpenPositions"`
	MaxDailyLossPct      float64 `json:"maxDailyLossPct"`
	MaxConsecutiveLosses int     `json:"maxConsecutiveLosses"`
	CooldownHours        int     `json:"cooldownHours"`
	MaxSlippageBps       float64 `json:"maxSlippageBps"`
	FeeBps               float64 `json:"feeBps"`
	MinChartScore        float64 `json:"minChartScore"`
	MinRiskReward        float64 `json:"minRiskReward"`
	StaleDataMaxAgeSec   int64   `json:"staleDataMaxAgeSec"`
	RequirePMTap         bool    `json:"requirePmTap"`
	RequireFourGatePass  bool    `json:"requireFourGatePass"`
}

// DefaultRiskLimits returns conservative Paper/Shadow defaults aligned with the desk rules.
func DefaultRiskLimits() RiskLimits {
	return RiskLimits{
		Mode:                 ModeShadow,
		InitialCapitalUSD:    100000,
		RiskPerTradePct:      1.0,
		MaxPositionPct:       10.0,
		MaxOpenPositions:     5,
		MaxDailyLossPct:      2.0,
		MaxConsecutiveLosses: 3,
		CooldownHours:        24,
		MaxSlippageBps:       25,
		FeeBps:               5,
		MinChartScore:        7.0,
		MinRiskReward:        1.5,
		StaleDataMaxAgeSec:   3600,
		RequirePMTap:         true,
		RequireFourGatePass:  false,
	}
}

// NormalizeMode validates that mode is strictly ModeShadow or ModePaper.
func NormalizeMode(raw string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(raw))
	if m == "" {
		return ModeShadow, nil
	}
	if m == ModeShadow || m == ModePaper {
		return m, nil
	}
	if m == "live" || m == "mainnet" || m == "real" {
		return "", ErrLiveExecutionDisabled
	}
	return "", fmt.Errorf("%w: unsupported mode %q", ErrLiveExecutionDisabled, raw)
}

// Validate verifies all risk bounds are finite, conservative, and non-live.
func (r *RiskLimits) Validate() error {
	if r == nil {
		return errors.New("risk limits required")
	}
	mode, err := NormalizeMode(r.Mode)
	if err != nil {
		return err
	}
	r.Mode = mode
	checks := []struct {
		name string
		val  float64
		min  float64
		max  float64
	}{
		{"initialCapitalUsd", r.InitialCapitalUSD, 100, 10_000_000},
		{"riskPerTradePct", r.RiskPerTradePct, 0.1, 5.0},
		{"maxPositionPct", r.MaxPositionPct, 1.0, 50.0},
		{"maxDailyLossPct", r.MaxDailyLossPct, 0.2, 20.0},
		{"maxSlippageBps", r.MaxSlippageBps, 0, 500},
		{"feeBps", r.FeeBps, 0, 200},
		{"minChartScore", r.MinChartScore, 1.0, 10.0},
		{"minRiskReward", r.MinRiskReward, 1.0, 10.0},
	}
	for _, c := range checks {
		if math.IsNaN(c.val) || math.IsInf(c.val, 0) || c.val < c.min || c.val > c.max {
			return fmt.Errorf("%s must be between %.2f and %.2f", c.name, c.min, c.max)
		}
	}
	if r.MaxOpenPositions < 1 || r.MaxOpenPositions > 25 {
		return errors.New("maxOpenPositions must be between 1 and 25")
	}
	if r.MaxConsecutiveLosses < 1 || r.MaxConsecutiveLosses > 15 {
		return errors.New("maxConsecutiveLosses must be between 1 and 15")
	}
	if r.CooldownHours < 1 || r.CooldownHours > 168 {
		return errors.New("cooldownHours must be between 1 and 168")
	}
	if r.StaleDataMaxAgeSec < 30 || r.StaleDataMaxAgeSec > 86400 {
		return errors.New("staleDataMaxAgeSec must be between 30 and 86400")
	}
	return nil
}

// ControlState summarizes persistent pause & unoverridable kill-switch state.
type ControlState struct {
	Paused             bool   `json:"paused"`
	KillSwitchActive   bool   `json:"killSwitchActive"`
	ExternalKillActive bool   `json:"externalKillActive"`
	EffectiveHalted    bool   `json:"effectiveHalted"`
	Reason             string `json:"reason,omitempty"`
	UpdatedBy          string `json:"updatedBy,omitempty"`
	UpdatedAt          int64  `json:"updatedAt"`
}

// PaperAccount summarizes isolated paper cash, equity, PnL, and streak state.
type PaperAccount struct {
	InitialCapitalUSD float64 `json:"initialCapitalUsd"`
	CashUSD           float64 `json:"cashUsd"`
	EquityUSD         float64 `json:"equityUsd"`
	RealizedPnLUSD    float64 `json:"realizedPnlUsd"`
	UnrealizedPnLUSD  float64 `json:"unrealizedPnlUsd"`
	DailyPnLUSD       float64 `json:"dailyPnlUsd"`
	DailyLossPct      float64 `json:"dailyLossPct"`
	TotalFeesUSD      float64 `json:"totalFeesUsd"`
	ConsecutiveLosses int     `json:"consecutiveLosses"`
	OpenPositions     int     `json:"openPositions"`
	PendingTickets    int     `json:"pendingTickets"`
	UpdatedAt         int64   `json:"updatedAt"`
}

// SeatArtifact records one seat's structured output inside a cycle.
type SeatArtifact struct {
	Seat      SeatID   `json:"seat"`
	Verdict   string   `json:"verdict"`
	Summary   string   `json:"summary"`
	Score     float64  `json:"score,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
	Timestamp int64    `json:"timestamp"`
}

// OrderTicket represents a proposed paper or shadow order produced by the desk.
type OrderTicket struct {
	ID           string  `json:"id"`
	CycleID      string  `json:"cycleId"`
	Venue        string  `json:"venue"`
	Symbol       string  `json:"symbol"`
	Interval     string  `json:"interval"`
	ScriptID     string  `json:"scriptId"`
	Side         string  `json:"side"`
	Mode         string  `json:"mode"`
	EntryPrice   float64 `json:"entryPrice"`
	StopPrice    float64 `json:"stopPrice"`
	TargetPrice  float64 `json:"targetPrice"`
	Quantity     float64 `json:"quantity"`
	NotionalUSD  float64 `json:"notionalUsd"`
	RiskUSD      float64 `json:"riskUsd"`
	RiskReward   float64 `json:"riskReward"`
	ExpectedAvgR float64 `json:"expectedAvgR"`
	ChartScore   float64 `json:"chartScore"`
	Status       string  `json:"status"`
	Reason       string  `json:"reason,omitempty"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

// PaperPosition tracks an open or closed paper trading position.
type PaperPosition struct {
	ID             string  `json:"id"`
	TicketID       string  `json:"ticketId"`
	Venue          string  `json:"venue"`
	Symbol         string  `json:"symbol"`
	ScriptID       string  `json:"scriptId"`
	Side           string  `json:"side"`
	Quantity       float64 `json:"quantity"`
	EntryPrice     float64 `json:"entryPrice"`
	MarkPrice      float64 `json:"markPrice"`
	StopPrice      float64 `json:"stopPrice"`
	TargetPrice    float64 `json:"targetPrice"`
	InitialRiskUSD float64 `json:"initialRiskUsd"`
	EntryFeeUSD    float64 `json:"entryFeeUsd"`
	ExitPrice      float64 `json:"exitPrice,omitempty"`
	ExitFeeUSD     float64 `json:"exitFeeUsd,omitempty"`
	UnrealizedPnL  float64 `json:"unrealizedPnlUsd"`
	RealizedPnL    float64 `json:"realizedPnlUsd"`
	PnLR           float64 `json:"pnlR"`
	Status         string  `json:"status"`
	ExitReason     string  `json:"exitReason,omitempty"`
	OpenedAt       int64   `json:"openedAt"`
	ClosedAt       int64   `json:"closedAt,omitempty"`
}

// BenchEntry records a cooldown bench applied after consecutive losses.
type BenchEntry struct {
	ID           string `json:"id"`
	ScopeKey     string `json:"scopeKey"`
	Reason       string `json:"reason"`
	Losses       int    `json:"losses"`
	BenchedUntil int64  `json:"benchedUntil"`
	CreatedAt    int64  `json:"createdAt"`
}

// JournalEntry captures every desk decision, filled/closed trade, killed name, or shadow signal.
type JournalEntry struct {
	ID             string  `json:"id"`
	CycleID        string  `json:"cycleId"`
	TicketID       string  `json:"ticketId,omitempty"`
	Seat           SeatID  `json:"seat"`
	Venue          string  `json:"venue"`
	Symbol         string  `json:"symbol"`
	ScriptID       string  `json:"scriptId"`
	Side           string  `json:"side"`
	Mode           string  `json:"mode"`
	Outcome        string  `json:"outcome"`
	Score          float64 `json:"score"`
	EntryPrice     float64 `json:"entryPrice"`
	StopPrice      float64 `json:"stopPrice"`
	TargetPrice    float64 `json:"targetPrice"`
	ExitPrice      float64 `json:"exitPrice,omitempty"`
	Quantity       float64 `json:"quantity"`
	RiskUSD        float64 `json:"riskUsd"`
	PnLUSD         float64 `json:"pnlUsd"`
	PnLR           float64 `json:"pnlR"`
	ExpectedAvgR   float64 `json:"expectedAvgR"`
	RiskCheck      string  `json:"riskCheck"`
	SkepticVerdict string  `json:"skepticVerdict"`
	Reason         string  `json:"reason,omitempty"`
	Lesson         string  `json:"lesson,omitempty"`
	CreatedAt      int64   `json:"createdAt"`
	UpdatedAt      int64   `json:"updatedAt"`
}

// LessonRecord persists a durable trading desk lesson read by every seat before acting.
type LessonRecord struct {
	ID            string `json:"id"`
	Category      string `json:"category"`
	Symbol        string `json:"symbol,omitempty"`
	Rule          string `json:"rule"`
	SourceTradeID string `json:"sourceTradeId,omitempty"`
	Active        bool   `json:"active"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// LessonInput creates or updates a LessonRecord cleanly.
type LessonInput struct {
	ID            string `json:"id,omitempty"`
	Category      string `json:"category,omitempty"`
	Symbol        string `json:"symbol,omitempty"`
	Rule          string `json:"rule"`
	SourceTradeID string `json:"sourceTradeId,omitempty"`
	Active        *bool  `json:"active,omitempty"`
}

// Validate normalizes and validates a LessonInput cleanly.
func (in *LessonInput) Validate() error {
	if in == nil {
		return errors.New("lesson input required")
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID != "" && !safeSlugPattern.MatchString(in.ID) {
		return fmt.Errorf("invalid lesson id %q", in.ID)
	}
	in.Category = strings.ToLower(strings.TrimSpace(in.Category))
	if in.Category == "" {
		in.Category = "risk"
	}
	switch in.Category {
	case "risk", "entry", "exit", "skeptic", "quant", "macro":
	default:
		return fmt.Errorf("invalid lesson category %q", in.Category)
	}
	in.Symbol = strings.ToUpper(strings.TrimSpace(in.Symbol))
	if len(in.Symbol) > 32 {
		return errors.New("lesson symbol exceeds 32 characters")
	}
	in.Rule = strings.TrimSpace(in.Rule)
	if in.Rule == "" || len(in.Rule) > 512 {
		return errors.New("lesson rule must be 1-512 characters")
	}
	in.SourceTradeID = strings.TrimSpace(in.SourceTradeID)
	if len(in.SourceTradeID) > 64 {
		return errors.New("sourceTradeId exceeds 64 characters")
	}
	return nil
}

// DecayReport monitors paper expectancy vs backtest expectancy across >= 20 trades.
type DecayReport struct {
	ClosedTrades   int     `json:"closedTrades"`
	PaperAvgR      float64 `json:"paperAvgR"`
	BacktestAvgR   float64 `json:"backtestAvgR"`
	WinRatePct     float64 `json:"winRatePct"`
	Decaying       bool    `json:"decaying"`
	Status         string  `json:"status"`
	SkepticKills   int     `json:"skepticKills"`
	RiskRejections int     `json:"riskRejections"`
	ShadowCount    int     `json:"shadowCount"`
	Summary        string  `json:"summary"`
}

// CycleRequest initiates a deterministic 8-seat paper/shadow trading desk cycle.
type CycleRequest struct {
	Venue    string `json:"venue,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Interval string `json:"interval,omitempty"`
	ScriptID string `json:"scriptId,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// CycleResult records all 8 seat artifacts, risk checks, and resulting ticket/position.
type CycleResult struct {
	ID          string         `json:"id"`
	Venue       string         `json:"venue"`
	Symbol      string         `json:"symbol"`
	Interval    string         `json:"interval"`
	ScriptID    string         `json:"scriptId"`
	Mode        string         `json:"mode"`
	FinalStatus string         `json:"finalStatus"`
	Artifacts   []SeatArtifact `json:"artifacts"`
	Ticket      *OrderTicket   `json:"ticket,omitempty"`
	Position    *PaperPosition `json:"position,omitempty"`
	RiskCheck   RiskEvaluation `json:"riskCheck"`
	CreatedAt   int64          `json:"createdAt"`
}

// DeskEvent carries live cycle, ticket, position, or kill-switch updates over SSE.
type DeskEvent struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	Summary   string `json:"summary"`
	Payload   any    `json:"payload,omitempty"`
}

// DeskSnapshot bundles current desk state for UI & MCP read inspection.
type DeskSnapshot struct {
	Control      ControlState    `json:"control"`
	Limits       RiskLimits      `json:"limits"`
	Seats        []SeatPolicy    `json:"seats"`
	Account      PaperAccount    `json:"account"`
	Positions    []PaperPosition `json:"positions"`
	Tickets      []OrderTicket   `json:"tickets"`
	Bench        []BenchEntry    `json:"bench"`
	Decay        DecayReport     `json:"decay"`
	LessonsCount int             `json:"lessonsCount"`
	UpdatedAt    int64           `json:"updatedAt"`
}

func finiteFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
