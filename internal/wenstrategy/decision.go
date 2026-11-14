package wenstrategy

import (
	"fmt"
	"math/big"
	"time"
)

// Metric is an already calculated, pinned integer research record. This package
// deliberately contains no floating-point market metric calculator.
type Metric struct {
	Metric          string `json:"metric"`
	LookbackMinutes int    `json:"lookbackMinutes"`
	ValueBps        string `json:"valueBps"`
	MethodVersion   string `json:"methodVersion"`
	MarketBinding   Market `json:"marketBinding"`
	RecordID        string `json:"recordId"`
	InputDigest     string `json:"inputDigest"`
	Source          string `json:"source"`
	AsOf            string `json:"asOf"`
	QuoteAt         string `json:"quoteAt"`
	LastMinuteEnd   string `json:"lastMinuteEnd"`
	CompleteMinutes int    `json:"completeMinutes"`
	Quality         string `json:"quality"`
	Connected       bool   `json:"connected"`
}
type Decision struct {
	Pass       bool   `json:"pass"`
	Branch     int    `json:"branch"`
	WeightsBps []int  `json:"weightsBps"`
	Reason     string `json:"reason"`
}

func checkConditions(cs []Condition, records []Metric, market Market, now time.Time) (bool, bool) {
	matched := true
	for _, c := range cs {
		var found *Metric
		for i := range records {
			r := &records[i]
			if r.Metric == c.Metric && r.LookbackMinutes == c.LookbackMinutes {
				if found != nil {
					return false, false
				}
				found = r
			}
		}
		if found == nil {
			return false, false
		}
		r := found
		if r.Quality != "valid" || !r.Connected || r.MarketBinding != market || r.MethodVersion != "wen-research/1" || r.RecordID == "" || r.Source == "" || !seed.MatchString(r.InputDigest) || r.CompleteMinutes != r.LookbackMinutes {
			return false, false
		}
		asOf, e := timestamp(r.AsOf)
		if e != nil || asOf.After(now) {
			return false, false
		}
		quote, e := timestamp(r.QuoteAt)
		if e != nil || quote.After(asOf) || now.Sub(quote) > 5*time.Second {
			return false, false
		}
		end, e := timestamp(r.LastMinuteEnd)
		if e != nil || end.Second() != 0 || end.After(asOf) || now.Sub(end) > 90*time.Second {
			return false, false
		}
		v, e := integer(r.ValueBps)
		if e != nil || r.Metric == "realizedVolBps" && v.Sign() < 0 {
			return false, false
		}
		threshold, e := integer(c.ValueBps)
		if e != nil {
			return false, false
		}
		cmp := v.Cmp(threshold)
		if c.Operator == "gte" && cmp < 0 || c.Operator == "lte" && cmp > 0 {
			matched = false
		}
	}
	return matched, true
}

// Evaluate treats missing/ambiguous/stale branch input as SKIP, never as a
// reason to fall through to a favorable later branch or root allocation.
func Evaluate(doc Document, records []Metric, evaluationTime string) (Decision, error) {
	if e := doc.Validate(); e != nil {
		return Decision{}, e
	}
	now, e := timestamp(evaluationTime)
	if e != nil {
		return Decision{}, e
	}
	pass, valid := checkConditions(doc.Research.Conditions, records, doc.Market, now)
	if !valid {
		return Decision{Branch: -1, Reason: "unavailable-metric"}, nil
	}
	if !pass {
		return Decision{Branch: -1, Reason: "research-condition"}, nil
	}
	for i, r := range doc.Allocation.Rules {
		pass, valid = checkConditions(r.When, records, doc.Market, now)
		if !valid {
			return Decision{Branch: -1, Reason: "unavailable-metric"}, nil
		}
		if pass {
			return Decision{true, i, append([]int(nil), r.WeightsBps...), "branch"}, nil
		}
	}
	return Decision{true, -1, append([]int(nil), doc.Allocation.WeightsBps...), "base"}, nil
}

// BudgetState describes already consumed/reserved amounts. It is an input
// snapshot, not a ledger; callers must atomically reserve in their admitted path.
type BudgetState struct {
	TotalTurnoverLamports    string `json:"totalTurnoverLamports"`
	DailyTurnoverLamports    string `json:"dailyTurnoverLamports"`
	UnsettledLamports        string `json:"unsettledLamports"`
	UserFeesPaidLamports     string `json:"userFeesPaidLamports"`
	UserFeeHoldsLamports     string `json:"userFeeHoldsLamports"`
	ActualDeductionsLamports string `json:"actualDeductionsLamports"`
	DeductionHoldsLamports   string `json:"deductionHoldsLamports"`
	AvailableLamports        string `json:"availableLamports"`
}
type Reservation struct {
	PrincipalLamports          string `json:"principalLamports"`
	UserFeeLamports            string `json:"userFeeLamports"`
	WorstCaseDeductionLamports string `json:"worstCaseDeductionLamports"`
}

func sumWithin(limit string, terms ...string) error {
	l, e := amount(limit)
	if e != nil {
		return e
	}
	sum := new(big.Int)
	for _, s := range terms {
		n, e := amount(s)
		if e != nil {
			return e
		}
		sum.Add(sum, new(big.Int).SetUint64(n))
	}
	if sum.Cmp(new(big.Int).SetUint64(l)) > 0 {
		return fmt.Errorf("budget exceeded")
	}
	return nil
}

// CheckLimits conservatively counts gross deductions and fee holds. No reward
// or returned-principal field can erase spending, loss or unsettled holds.
func CheckLimits(l Limits, s BudgetState, r Reservation) error {
	if e := l.Validate(); e != nil {
		return e
	}
	p, e := amount(r.PrincipalLamports)
	if e != nil || p == 0 {
		return fmt.Errorf("positive reservation required")
	}
	d, e := amount(r.WorstCaseDeductionLamports)
	if e != nil || d > p {
		return fmt.Errorf("invalid deduction hold")
	}
	checks := []struct {
		limit string
		terms []string
	}{
		{l.PerRoundLamports, []string{r.PrincipalLamports}},
		{l.TotalTurnoverLamports, []string{s.TotalTurnoverLamports, r.PrincipalLamports}},
		{l.DailyTurnoverLamports, []string{s.DailyTurnoverLamports, r.PrincipalLamports}},
		{l.MaxUnsettledLamports, []string{s.UnsettledLamports, r.PrincipalLamports}},
		{l.MaxUserFeesPerRoundLamports, []string{r.UserFeeLamports}},
		{l.MaxUserFeesLamports, []string{s.UserFeesPaidLamports, s.UserFeeHoldsLamports, r.UserFeeLamports}},
		{l.MaxLossLamports, []string{s.ActualDeductionsLamports, s.UserFeesPaidLamports, s.DeductionHoldsLamports, s.UserFeeHoldsLamports, r.WorstCaseDeductionLamports, r.UserFeeLamports}},
		{s.AvailableLamports, []string{r.PrincipalLamports, r.UserFeeLamports}},
	}
	for i, c := range checks {
		if e := sumWithin(c.limit, c.terms...); e != nil {
			return fmt.Errorf("budget check %d: %w", i, e)
		}
	}
	return nil
}
