package core

import "github.com/fased-ai/fased-app/internal/wenstrategy"

// These are conditional allocations, never claimed probabilities or rewards.
// The existing F1 validator/compiler remains the only amount authority.
type WENStrategyBranchAmounts struct {
	Index           int                     `json:"index"`
	When            []wenstrategy.Condition `json:"when"`
	AmountsLamports [5]string               `json:"amountsLamports"`
}

func compileWENStrategyBranches(doc wenstrategy.Document) ([]WENStrategyBranchAmounts, error) {
	result := []WENStrategyBranchAmounts{}
	for i, rule := range doc.Allocation.Rules {
		amounts, err := wenstrategy.CompileAllocation(doc.Limits.PerRoundLamports, rule.WeightsBps)
		if err != nil {
			return nil, err
		}
		result = append(result, WENStrategyBranchAmounts{i, rule.When, amounts})
	}
	return result, nil
}
