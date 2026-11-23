package agents

import "strings"

// UsageModelRow holds token and estimated micro-USD usage metrics grouped by provider and model.
type UsageModelRow struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Turns           int64  `json:"turns"`
	InputTokens     int64  `json:"inputTokens"`
	CachedTokens    int64  `json:"cachedTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	TotalTokens     int64  `json:"totalTokens"`
	EstimatedCostUS int64  `json:"estimatedCostMicroUsd"`
	CostKnown       bool   `json:"costKnown"`
}

// UsageSummary aggregates Agent V2 token usage and estimated costs across a time window.
type UsageSummary struct {
	ProfileID       string          `json:"profileId,omitempty"`
	Days            int             `json:"days"`
	Since           int64           `json:"since,omitempty"`
	Turns           int64           `json:"turns"`
	InputTokens     int64           `json:"inputTokens"`
	CachedTokens    int64           `json:"cachedTokens"`
	OutputTokens    int64           `json:"outputTokens"`
	TotalTokens     int64           `json:"totalTokens"`
	EstimatedCostUS int64           `json:"estimatedCostMicroUsd"`
	CostKnown       bool            `json:"costKnown"`
	UnpricedTurns   int64           `json:"unpricedTurns,omitempty"`
	Models          []UsageModelRow `json:"models"`
}

// ModelPricing supplies per-1M-token prices in micro-USD and currency identifier.
type ModelPricing struct {
	Input    int64
	Cached   int64
	Output   int64
	Currency string
}

// EstimateModelCostMicroUSD computes ceiling micro-USD cost for one model's token counts.
// Local providers, non-USD pricing, or missing/zero pricing return (0, false) without fabricated figures.
func EstimateModelCostMicroUSD(
	provider, modelID string,
	input, cached, output int64,
	lookup func(provider, modelID string) (ModelPricing, bool),
) (int64, bool) {
	if strings.EqualFold(strings.TrimSpace(provider), "local") {
		return 0, false
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || lookup == nil {
		return 0, false
	}
	m, ok := lookup(provider, modelID)
	if !ok {
		return 0, false
	}
	if m.Currency != "" && !strings.EqualFold(m.Currency, "USD") {
		return 0, false
	}
	if m.Input <= 0 || m.Output <= 0 {
		return 0, false
	}
	if input < 0 {
		input = 0
	}
	if cached < 0 {
		cached = 0
	}
	if cached > input {
		cached = input
	}
	if output < 0 {
		output = 0
	}
	nonCached := input - cached
	total := nonCached*m.Input + cached*m.Cached + output*m.Output
	if total == 0 {
		return 0, true
	}
	return (total + 999999) / 1000000, true
}
