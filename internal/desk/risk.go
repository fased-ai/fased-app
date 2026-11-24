package desk

import (
	"fmt"
	"math"
	"strings"
)

// RiskCheckInput provides deterministic pre-trade state checked in Go code.
type RiskCheckInput struct {
	Limits             RiskLimits `json:"limits"`
	Mode               string     `json:"mode"`
	KillSwitchActive   bool       `json:"killSwitchActive"`
	ExternalKillActive bool       `json:"externalKillActive"`
	Paused             bool       `json:"paused"`
	SkepticVerdict     string     `json:"skepticVerdict"`
	Benched            bool       `json:"benched"`
	StaleData          bool       `json:"staleData"`
	FourGatePassed     bool       `json:"fourGatePassed"`
	EquityUSD          float64    `json:"equityUsd"`
	OpenPositions      int        `json:"openPositions"`
	DailyLossPct       float64    `json:"dailyLossPct"`
	ConsecutiveLosses  int        `json:"consecutiveLosses"`
	ChartScore         float64    `json:"chartScore"`
	Side               string     `json:"side"`
	EntryPrice         float64    `json:"entryPrice"`
	StopPrice          float64    `json:"stopPrice"`
	TargetPrice        float64    `json:"targetPrice"`
}

// RiskEvaluation captures deterministic sizing and hard rule verdicts.
type RiskEvaluation struct {
	Approved      bool     `json:"approved"`
	ViolatedRules []string `json:"violatedRules,omitempty"`
	ComputedQty   float64  `json:"computedQty"`
	NotionalUSD   float64  `json:"notionalUsd"`
	RiskUSD       float64  `json:"riskUsd"`
	RiskReward    float64  `json:"riskReward"`
	DailyLossPct  float64  `json:"dailyLossPct"`
	Summary       string   `json:"summary"`
}

// EvaluateRisk applies immutable code-level risk gates and position sizing.
// Chat prompts cannot negotiate around any rule violated here.
func EvaluateRisk(in RiskCheckInput) RiskEvaluation {
	out := RiskEvaluation{
		DailyLossPct: finiteFloat(in.DailyLossPct),
	}
	var violations []string
	addViolation := func(code string) {
		violations = append(violations, code)
	}

	mode, err := NormalizeMode(in.Mode)
	if err != nil {
		addViolation("LIVE_EXECUTION_DISABLED")
	} else if mode != ModeShadow && mode != ModePaper {
		addViolation("LIVE_EXECUTION_DISABLED")
	}

	if in.KillSwitchActive || in.ExternalKillActive {
		addViolation("KILL_SWITCH_ACTIVE")
	}
	if in.Paused {
		addViolation("DESK_PAUSED")
	}
	if strings.ToUpper(strings.TrimSpace(in.SkepticVerdict)) != "PASS" {
		addViolation("SKEPTIC_KILLED")
	}
	if in.Benched {
		addViolation("COOLDOWN_BENCH_ACTIVE")
	}
	if in.StaleData {
		addViolation("STALE_MARKET_DATA")
	}
	if in.Limits.RequireFourGatePass && !in.FourGatePassed {
		addViolation("FOUR_GATE_NOT_PASSED")
	}
	if finiteFloat(in.ChartScore) < in.Limits.MinChartScore {
		addViolation("CHART_SCORE_BELOW_MIN")
	}
	if in.OpenPositions >= in.Limits.MaxOpenPositions {
		addViolation("MAX_OPEN_POSITIONS")
	}
	if finiteFloat(in.DailyLossPct) >= in.Limits.MaxDailyLossPct {
		addViolation("MAX_DAILY_LOSS_BREACHED")
	}
	if in.ConsecutiveLosses >= in.Limits.MaxConsecutiveLosses {
		addViolation("MAX_CONSECUTIVE_LOSSES")
	}

	side := strings.ToLower(strings.TrimSpace(in.Side))
	entry := finiteFloat(in.EntryPrice)
	stop := finiteFloat(in.StopPrice)
	target := finiteFloat(in.TargetPrice)

	var riskDist, rewardDist float64
	levelsOK := entry > 0 && stop > 0 && target > 0 && finiteFloat(in.EquityUSD) > 0
	if levelsOK {
		switch side {
		case "long":
			if stop >= entry || target <= entry {
				levelsOK = false
			} else {
				riskDist = entry - stop
				rewardDist = target - entry
			}
		case "short":
			if stop <= entry || target >= entry {
				levelsOK = false
			} else {
				riskDist = stop - entry
				rewardDist = entry - target
			}
		default:
			levelsOK = false
		}
	}

	if !levelsOK || riskDist <= 0 || rewardDist <= 0 {
		addViolation("INVALID_PRICE_LEVELS")
	} else {
		rr := rewardDist / riskDist
		out.RiskReward = math.Round(finiteFloat(rr)*100) / 100
		if rr < in.Limits.MinRiskReward {
			addViolation("RISK_REWARD_TOO_LOW")
		}

		riskBudget := in.EquityUSD * (in.Limits.RiskPerTradePct / 100.0)
		qty := riskBudget / riskDist
		maxNotional := in.EquityUSD * (in.Limits.MaxPositionPct / 100.0)
		if entry > 0 && qty*entry > maxNotional {
			qty = maxNotional / entry
		}
		qty = math.Floor(finiteFloat(qty)*1e4) / 1e4
		if qty <= 0 {
			addViolation("SIZE_BELOW_MINIMUM")
		} else {
			out.ComputedQty = qty
			out.NotionalUSD = math.Round(qty*entry*100) / 100
			out.RiskUSD = math.Round(qty*riskDist*100) / 100
		}
	}

	out.ViolatedRules = violations
	out.Approved = len(violations) == 0
	if out.Approved {
		out.Summary = fmt.Sprintf("APPROVED %s qty=%.4f notional=$%.2f risk=$%.2f R:R=%.2f", side, out.ComputedQty, out.NotionalUSD, out.RiskUSD, out.RiskReward)
	} else {
		out.Summary = fmt.Sprintf("REJECTED (%s)", strings.Join(violations, ", "))
	}
	return out
}
