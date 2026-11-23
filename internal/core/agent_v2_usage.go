package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fased-ai/fased-app/internal/agents"
)

type AgentV2UsageModelRow = agents.UsageModelRow
type AgentV2UsageSummary = agents.UsageSummary

func estimateV2ModelCostMicroUSD(provider, modelID string, input, cached, output int64) (int64, bool) {
	return estimateV2ModelCostMicroUSDForDir("", provider, modelID, input, cached, output)
}

func estimateV2ModelCostMicroUSDForDir(dir, provider, modelID string, input, cached, output int64) (int64, bool) {
	return agents.EstimateModelCostMicroUSD(provider, modelID, input, cached, output, func(p, id string) (agents.ModelPricing, bool) {
		var m currentModel
		var ok bool
		if dir != "" && modelManifestLookup != nil {
			m, ok = modelManifestLookup(dir, p, id)
		}
		if !ok {
			m, ok = currentProviderModel(id)
		}
		if !ok {
			return agents.ModelPricing{}, false
		}
		return agents.ModelPricing{Input: m.Input, Cached: m.Cached, Output: m.Output, Currency: m.Currency}, true
	})
}

// GetAgentV2Usage aggregates assistant turn counts, token usage, and estimated USD
// costs from agents.sqlite for a profile (or all profiles when profileID is empty).
func (s *Store) GetAgentV2Usage(ctx context.Context, profileID string, days int) (AgentV2UsageSummary, error) {
	if days < 0 || days > 365 {
		return AgentV2UsageSummary{}, errors.New("days must be between 0 and 365")
	}
	profileID = strings.TrimSpace(profileID)
	var since int64
	if days > 0 {
		since = s.now().Unix() - int64(days)*86400
	}
	out := AgentV2UsageSummary{
		ProfileID: profileID,
		Days:      days,
		Since:     since,
		Models:    []AgentV2UsageModelRow{},
	}
	if s.agentV2 == nil {
		if _, err := os.Stat(filepath.Join(s.dir, "agents.sqlite")); errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentV2UsageSummary{}, err
	}

	var where strings.Builder
	where.WriteString(`m.role = 'assistant' AND (m.status != 'error' OR m.input_tokens > 0 OR m.output_tokens > 0)`)
	var args []any
	if profileID != "" {
		where.WriteString(` AND t.profile_id = ?`)
		args = append(args, profileID)
	}
	if since > 0 {
		where.WriteString(` AND m.created_at >= ?`)
		args = append(args, since)
	}

	query := `
SELECT COALESCE(NULLIF(TRIM(m.provider), ''), ''),
       COALESCE(NULLIF(TRIM(m.model), ''), ''),
       COUNT(*),
       COALESCE(SUM(m.input_tokens), 0),
       COALESCE(SUM(m.cached_tokens), 0),
       COALESCE(SUM(m.output_tokens), 0)
FROM messages m
JOIN threads t ON t.id = m.thread_id
WHERE ` + where.String() + `
GROUP BY 1, 2
`
	rows, err := v2.db.QueryContext(ctx, query, args...)
	if err != nil {
		return AgentV2UsageSummary{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var row AgentV2UsageModelRow
		if err := rows.Scan(&row.Provider, &row.Model, &row.Turns, &row.InputTokens, &row.CachedTokens, &row.OutputTokens); err != nil {
			return AgentV2UsageSummary{}, err
		}
		row.TotalTokens = row.InputTokens + row.OutputTokens
		cost, known := estimateV2ModelCostMicroUSDForDir(s.dir, row.Provider, row.Model, row.InputTokens, row.CachedTokens, row.OutputTokens)
		row.EstimatedCostUS = cost
		row.CostKnown = known

		out.Turns += row.Turns
		out.InputTokens += row.InputTokens
		out.CachedTokens += row.CachedTokens
		out.OutputTokens += row.OutputTokens
		out.TotalTokens += row.TotalTokens
		if known {
			out.EstimatedCostUS += cost
			out.CostKnown = true
		} else {
			out.UnpricedTurns += row.Turns
		}
		out.Models = append(out.Models, row)
	}
	if err := rows.Err(); err != nil {
		return AgentV2UsageSummary{}, err
	}

	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].TotalTokens != out.Models[j].TotalTokens {
			return out.Models[i].TotalTokens > out.Models[j].TotalTokens
		}
		if out.Models[i].Turns != out.Models[j].Turns {
			return out.Models[i].Turns > out.Models[j].Turns
		}
		if out.Models[i].Provider != out.Models[j].Provider {
			return out.Models[i].Provider < out.Models[j].Provider
		}
		return out.Models[i].Model < out.Models[j].Model
	})
	return out, nil
}
