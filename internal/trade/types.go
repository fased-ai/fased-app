package trade

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

const (
	ModeManual   = "manual"
	ModeSemiAuto = "semi_auto"
	ModeBot      = "bot"
	ModeAIAgent  = "ai_agent"

	StrategyMarket      = "market"
	StrategyLimit       = "limit"
	StrategyBracket     = "bracket"
	StrategyTrailingATR = "trailing_atr"
	StrategyGrid        = "grid"
	StrategyDCA         = "dca"
	StrategyTWAP        = "twap"

	SideBuy  = "buy"
	SideSell = "sell"

	StatusActive    = "active"
	StatusFilled    = "filled"
	StatusTriggered = "triggered"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
	StatusKilled    = "killed"
)

var (
	ErrKillSwitchActive = errors.New("global kill switch is active; order placement is halted")
	ErrRiskRejected     = errors.New("trade risk policy rejected order")
	ErrPlanNotFound     = errors.New("trade order plan not found")

	validModes = map[string]bool{
		ModeManual:   true,
		ModeSemiAuto: true,
		ModeBot:      true,
		ModeAIAgent:  true,
	}

	validStrategies = map[string]bool{
		StrategyMarket:      true,
		StrategyLimit:       true,
		StrategyBracket:     true,
		StrategyTrailingATR: true,
		StrategyGrid:        true,
		StrategyDCA:         true,
		StrategyTWAP:        true,
	}
)

// OrderRequest defines a unified paper/simulated multi-strategy order request.
type OrderRequest struct {
	Venue           string  `json:"venue"`
	Symbol          string  `json:"symbol"`
	Interval        string  `json:"interval,omitempty"`
	Mode            string  `json:"mode,omitempty"`
	Strategy        string  `json:"strategy,omitempty"`
	Side            string  `json:"side"`
	Quantity        float64 `json:"quantity"`
	EntryPrice      float64 `json:"entryPrice,omitempty"`
	StopPrice       float64 `json:"stopPrice,omitempty"`
	TargetPrice     float64 `json:"targetPrice,omitempty"`
	ATRMultiplier   float64 `json:"atrMultiplier,omitempty"`
	GridLower       float64 `json:"gridLower,omitempty"`
	GridUpper       float64 `json:"gridUpper,omitempty"`
	GridLevels      int     `json:"gridLevels,omitempty"`
	Slices          int     `json:"slices,omitempty"`
	IntervalSeconds int     `json:"intervalSeconds,omitempty"`
	AgentNote       string  `json:"agentNote,omitempty"`
}

// OrderPlan persists a unified trade plan managed deterministically in SQLite.
type OrderPlan struct {
	ID                string  `json:"id"`
	Venue             string  `json:"venue"`
	Symbol            string  `json:"symbol"`
	Interval          string  `json:"interval"`
	Mode              string  `json:"mode"`
	Strategy          string  `json:"strategy"`
	Side              string  `json:"side"`
	Status            string  `json:"status"`
	Simulated         bool    `json:"simulated"`
	Quantity          float64 `json:"quantity"`
	FilledQty         float64 `json:"filledQty"`
	AvgFillPrice      float64 `json:"avgFillPrice,omitempty"`
	EntryPrice        float64 `json:"entryPrice,omitempty"`
	StopPrice         float64 `json:"stopPrice,omitempty"`
	TargetPrice       float64 `json:"targetPrice,omitempty"`
	TrailingStopPrice float64 `json:"trailingStopPrice,omitempty"`
	ATRMultiplier     float64 `json:"atrMultiplier,omitempty"`
	GridLower         float64 `json:"gridLower,omitempty"`
	GridUpper         float64 `json:"gridUpper,omitempty"`
	GridLevels        int     `json:"gridLevels,omitempty"`
	GridFills         int     `json:"gridFills,omitempty"`
	Slices            int     `json:"slices,omitempty"`
	SlicesFilled      int     `json:"slicesFilled,omitempty"`
	IntervalSeconds   int     `json:"intervalSeconds,omitempty"`
	MarkPrice         float64 `json:"markPrice,omitempty"`
	UnrealizedPnLUSD  float64 `json:"unrealizedPnlUsd"`
	RealizedPnLUSD    float64 `json:"realizedPnlUsd"`
	AgentNote         string  `json:"agentNote,omitempty"`
	LastError         string  `json:"lastError,omitempty"`
	CreatedAt         int64   `json:"createdAt"`
	UpdatedAt         int64   `json:"updatedAt"`
	LastEvaluatedAt   int64   `json:"lastEvaluatedAt,omitempty"`
}

// RiskPolicy enforces hard Go pre-trade bounds and daily loss ceilings.
type RiskPolicy struct {
	MaxOrderNotionalUSD float64 `json:"maxOrderNotionalUsd"`
	MaxDailyLossUSD     float64 `json:"maxDailyLossUsd"`
	MaxOpenPlans        int     `json:"maxOpenPlans"`
	DailyLossUSD        float64 `json:"dailyLossUsd"`
	DailyLossDay        string  `json:"dailyLossDay,omitempty"`
}

// KillState tracks the persistent trade engine halt state.
type KillState struct {
	Active    bool   `json:"active"`
	Reason    string `json:"reason,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Snapshot summarizes current trade engine controls, policy, and recent plans.
type Snapshot struct {
	ExecutionProfile string      `json:"executionProfile"`
	Simulated        bool        `json:"simulated"`
	KillSwitchActive bool        `json:"killSwitchActive"`
	KillReason       string      `json:"killReason,omitempty"`
	Policy           RiskPolicy  `json:"policy"`
	ActiveCount      int         `json:"activeCount"`
	Plans            []OrderPlan `json:"plans"`
}

// EvaluateSummary reports outcomes of an EvaluateActive sweep.
type EvaluateSummary struct {
	Evaluated int  `json:"evaluated"`
	Updated   int  `json:"updated"`
	Triggered int  `json:"triggered"`
	Completed int  `json:"completed"`
	Killed    int  `json:"killed"`
	Halted    bool `json:"halted,omitempty"`
}

// DefaultRiskPolicy returns conservative paper trade risk bounds.
func DefaultRiskPolicy() RiskPolicy {
	return RiskPolicy{
		MaxOrderNotionalUSD: 25000,
		MaxDailyLossUSD:     2000,
		MaxOpenPlans:        25,
	}
}

// ValidateRiskPolicy verifies risk bounds are finite and within safe limits.
func ValidateRiskPolicy(p RiskPolicy) error {
	if !isFinitePositive(p.MaxOrderNotionalUSD) || p.MaxOrderNotionalUSD > 10_000_000 {
		return fmt.Errorf("%w: maxOrderNotionalUsd must be between 0 and 10,000,000", ErrRiskRejected)
	}
	if !isFinitePositive(p.MaxDailyLossUSD) || p.MaxDailyLossUSD > 1_000_000 {
		return fmt.Errorf("%w: maxDailyLossUsd must be between 0 and 1,000,000", ErrRiskRejected)
	}
	if p.MaxOpenPlans < 1 || p.MaxOpenPlans > 100 {
		return fmt.Errorf("%w: maxOpenPlans must be between 1 and 100", ErrRiskRejected)
	}
	if !isFiniteNonNeg(p.DailyLossUSD) {
		return fmt.Errorf("%w: dailyLossUsd must be finite non-negative", ErrRiskRejected)
	}
	return nil
}

// NormalizeOrderRequest validates and canonicalizes an incoming OrderRequest.
func NormalizeOrderRequest(req OrderRequest) (OrderRequest, error) {
	venue, err := marketdata.NormalizeVenue(req.Venue)
	if err != nil {
		return OrderRequest{}, err
	}
	symbol, err := marketdata.NormalizeSymbol(req.Symbol)
	if err != nil {
		return OrderRequest{}, err
	}
	ivRaw := strings.TrimSpace(req.Interval)
	if ivRaw == "" {
		ivRaw = "1h"
	}
	iv, err := marketdata.NormalizeInterval(ivRaw)
	if err != nil {
		return OrderRequest{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = ModeManual
	}
	if !validModes[mode] {
		return OrderRequest{}, fmt.Errorf("unsupported trade mode %q", req.Mode)
	}
	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	if strategy == "" {
		strategy = StrategyMarket
	}
	if !validStrategies[strategy] {
		return OrderRequest{}, fmt.Errorf("unsupported trade strategy %q", req.Strategy)
	}
	side := strings.ToLower(strings.TrimSpace(req.Side))
	if side != SideBuy && side != SideSell {
		return OrderRequest{}, fmt.Errorf("unsupported side %q (want buy or sell)", req.Side)
	}
	if !isFinitePositive(req.Quantity) {
		return OrderRequest{}, errors.New("quantity must be positive and finite")
	}
	if req.EntryPrice != 0 && !isFinitePositive(req.EntryPrice) {
		return OrderRequest{}, errors.New("entryPrice must be positive and finite")
	}
	if req.StopPrice != 0 && !isFinitePositive(req.StopPrice) {
		return OrderRequest{}, errors.New("stopPrice must be positive and finite")
	}
	if req.TargetPrice != 0 && !isFinitePositive(req.TargetPrice) {
		return OrderRequest{}, errors.New("targetPrice must be positive and finite")
	}

	out := OrderRequest{
		Venue:       venue,
		Symbol:      symbol,
		Interval:    iv,
		Mode:        mode,
		Strategy:    strategy,
		Side:        side,
		Quantity:    req.Quantity,
		EntryPrice:  req.EntryPrice,
		StopPrice:   req.StopPrice,
		TargetPrice: req.TargetPrice,
		AgentNote:   strings.TrimSpace(req.AgentNote),
	}
	if len(out.AgentNote) > 240 {
		out.AgentNote = out.AgentNote[:240]
	}

	switch strategy {
	case StrategyLimit:
		if !isFinitePositive(out.EntryPrice) {
			return OrderRequest{}, errors.New("limit strategy requires positive entryPrice")
		}
	case StrategyBracket:
		if !isFinitePositive(out.StopPrice) || !isFinitePositive(out.TargetPrice) {
			return OrderRequest{}, errors.New("bracket strategy requires positive stopPrice and targetPrice")
		}
		if out.EntryPrice > 0 {
			if err := validateBracketPrices(out.Side, out.EntryPrice, out.StopPrice, out.TargetPrice); err != nil {
				return OrderRequest{}, err
			}
		}
	case StrategyTrailingATR:
		mult := req.ATRMultiplier
		if mult == 0 {
			mult = 2.0
		}
		if math.IsNaN(mult) || math.IsInf(mult, 0) || mult < 0.5 || mult > 10.0 {
			return OrderRequest{}, errors.New("atrMultiplier must be between 0.5 and 10.0")
		}
		out.ATRMultiplier = mult
	case StrategyGrid:
		if !isFinitePositive(req.GridLower) || !isFinitePositive(req.GridUpper) || req.GridUpper <= req.GridLower {
			return OrderRequest{}, errors.New("grid strategy requires finite positive gridLower < gridUpper")
		}
		levels := req.GridLevels
		if levels == 0 {
			levels = 5
		}
		if levels < 2 || levels > 20 {
			return OrderRequest{}, errors.New("gridLevels must be between 2 and 20")
		}
		out.GridLower = req.GridLower
		out.GridUpper = req.GridUpper
		out.GridLevels = levels
	case StrategyDCA, StrategyTWAP:
		slices := req.Slices
		if slices == 0 {
			slices = 4
		}
		if slices < 2 || slices > 24 {
			return OrderRequest{}, errors.New("slices must be between 2 and 24")
		}
		ivSec := req.IntervalSeconds
		if ivSec == 0 {
			ivSec = 300
		}
		if ivSec < 60 || ivSec > 86400 {
			return OrderRequest{}, errors.New("intervalSeconds must be between 60 and 86400")
		}
		out.Slices = slices
		out.IntervalSeconds = ivSec
	}
	return out, nil
}

func validateBracketPrices(side string, entry, stop, target float64) error {
	if side == SideBuy {
		if !(stop < entry && entry < target) {
			return errors.New("buy bracket requires stopPrice < entryPrice < targetPrice")
		}
		return nil
	}
	if !(target < entry && entry < stop) {
		return errors.New("sell bracket requires targetPrice < entryPrice < stopPrice")
	}
	return nil
}

// ComputeATR14 computes average true range over up to the last 14 candle intervals.
func ComputeATR14(candles []marketdata.Candle) (float64, bool) {
	if len(candles) < 2 {
		return 0, false
	}
	start := 1
	if len(candles) > 15 {
		start = len(candles) - 14
	}
	var sum float64
	count := 0
	for i := start; i < len(candles); i++ {
		cur := candles[i]
		prev := candles[i-1]
		tr1 := cur.High - cur.Low
		tr2 := math.Abs(cur.High - prev.Close)
		tr3 := math.Abs(cur.Low - prev.Close)
		tr := math.Max(tr1, math.Max(tr2, tr3))
		if math.IsNaN(tr) || math.IsInf(tr, 0) || tr < 0 {
			continue
		}
		sum += tr
		count++
	}
	if count == 0 {
		return 0, false
	}
	atr := sum / float64(count)
	if !isFinitePositive(atr) {
		return 0, false
	}
	return atr, true
}

func newPlanID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "tr-" + hex.EncodeToString(b[:]), nil
}

func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func isFiniteNonNeg(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}
