package desk

import (
	"testing"
)

func FuzzDeskInputs(f *testing.F) {
	f.Add("paper", float64(100000), float64(1.0), float64(10.0), float64(100), float64(98), float64(104), "long", "PASS", string(SeatExecutor), "place_paper_order", "No overtrading on CPI day")
	f.Add("live", float64(50000), float64(2.0), float64(15.0), float64(100), float64(105), float64(95), "short", "KILL", string(SeatSkeptic), "place_paper_order", "")
	f.Add("shadow", float64(-1), float64(99), float64(0), float64(0), float64(0), float64(0), "invalid", "PASS", string(SeatRisk), "modify_risk_limits", "rule")

	f.Fuzz(func(_ *testing.T, mode string, eq, riskPct, maxPosPct, entry, stop, target float64, side, skeptic, seat, tool, rule string) {
		limits := DefaultRiskLimits()
		limits.Mode = mode
		limits.InitialCapitalUSD = eq
		limits.RiskPerTradePct = riskPct
		limits.MaxPositionPct = maxPosPct
		_ = limits.Validate()

		_ = AssertSeatToolAllowed(SeatID(seat), tool)

		_ = EvaluateRisk(RiskCheckInput{
			Limits:            DefaultRiskLimits(),
			Mode:              mode,
			SkepticVerdict:    skeptic,
			FourGatePassed:    true,
			EquityUSD:         eq,
			OpenPositions:     0,
			DailyLossPct:      0,
			ConsecutiveLosses: 0,
			ChartScore:        8.0,
			Side:              side,
			EntryPrice:        entry,
			StopPrice:         stop,
			TargetPrice:       target,
		})

		in := LessonInput{
			Category: "risk",
			Symbol:   "BTC",
			Rule:     rule,
		}
		_ = in.Validate()
	})
}
