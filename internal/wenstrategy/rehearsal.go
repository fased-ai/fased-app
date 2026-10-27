package wenstrategy

// Rehearse composes the existing F1 pure functions. It neither reserves capital
// nor calculates rewards; a caller must separately admit any shadow operation.
type RehearsalEvaluation struct {
	Action          string    `json:"action"`
	Reason          string    `json:"reason"`
	Branch          int       `json:"branch"`
	WeightsBps      [5]int    `json:"weightsBps"`
	AmountsLamports [5]string `json:"amountsLamports"`
}

func Rehearse(doc Document, slot Slot, state ScheduleState, pinned []string, metrics []Metric, at string, budget BudgetState, reservation Reservation) (RehearsalEvaluation, error) {
	r := RehearsalEvaluation{Action: "skip", Branch: -1, AmountsLamports: [5]string{"0", "0", "0", "0", "0"}}
	ok, err := Eligible(doc.Schedule, slot, state, pinned)
	if err != nil {
		return r, err
	}
	if !ok {
		r.Reason = "schedule-ineligible"
		return r, nil
	}
	d, err := Evaluate(doc, metrics, at)
	if err != nil {
		return r, err
	}
	r.Reason = d.Reason
	r.Branch = d.Branch
	if !d.Pass {
		return r, nil
	}
	copy(r.WeightsBps[:], d.WeightsBps)
	r.AmountsLamports, err = CompileAllocation(doc.Limits.PerRoundLamports, d.WeightsBps)
	if err != nil {
		return r, err
	}
	if err = CheckLimits(doc.Limits, budget, reservation); err != nil {
		r.Reason = "budget-unavailable"
		return r, nil
	}
	r.Action = "offer"
	return r, nil
}
