package core

// Compiled synthetic scenarios only. These are not BTC observations, fee
// measurements, settlement oracles, funded positions, or mining payout models.
import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

type WENSessionScenario struct {
	ID        string             `json:"id"`
	Digest    string             `json:"digest"`
	Hours     int                `json:"hours"`
	StartsAt  string             `json:"startsAt"`
	ExpiresAt string             `json:"expiresAt"`
	Slots     []wenstrategy.Slot `json:"slots"`
	Recipe    string             `json:"recipe"`
}

func wenSessionScenarios() []WENSessionScenario {
	result := []WENSessionScenario{}
	for _, def := range []struct {
		id    string
		hours int
	}{{"fixture-4h", 4}, {"fixture-12h", 12}, {"fixture-4h-unknown", 4}, {"fixture-4h-stale", 4}, {"fixture-4h-cutoff", 4}} {
		start := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
		s := WENSessionScenario{ID: def.id, Hours: def.hours, StartsAt: start.Format(time.RFC3339), ExpiresAt: start.Add(time.Duration(def.hours) * time.Hour).Format(time.RFC3339), Slots: []wenstrategy.Slot{}, Recipe: "wen-session-fixture/1; eval=open-70s; cutoff=open-60s; reconcile=open+30s; fee=1000lamports; worst-deduction=principal/10; confirmed-deduction=principal/20; unknown-release=unspent; quote=eval-1s; metric-cycle=-12,0,15; vol=20; initial=20000000000lamports"}
		for i := 0; i < def.hours*6; i++ {
			s.Slots = append(s.Slots, wenstrategy.Slot{RoundID: strconv.Itoa(i), OpensAt: start.Add(time.Duration(i) * 10 * time.Minute).Format(time.RFC3339)})
		}
		raw, _ := json.Marshal(s)
		s.Digest = wenHash(raw)
		result = append(result, s)
	}
	return result
}
func wenScenario(id string) (WENSessionScenario, error) {
	for _, s := range wenSessionScenarios() {
		if s.ID == id {
			return s, nil
		}
	}
	return WENSessionScenario{}, fmt.Errorf("select a compiled fixture scenario")
}
func wenFixtureMetrics(doc wenstrategy.Document, scenario WENSessionScenario, slot int, at time.Time) []wenstrategy.Metric {
	conditions := append([]wenstrategy.Condition{}, doc.Research.Conditions...)
	for _, r := range doc.Allocation.Rules {
		conditions = append(conditions, r.When...)
	}
	result := []wenstrategy.Metric{}
	seen := map[string]bool{}
	for _, c := range conditions {
		key := fmt.Sprintf("%s:%d", c.Metric, c.LookbackMinutes)
		if seen[key] {
			continue
		}
		seen[key] = true
		value := []string{"-12", "0", "15"}[slot%3]
		if c.Metric == "realizedVolBps" {
			value = "20"
		}
		quote := at.Add(-time.Second)
		if scenario.ID == "fixture-4h-stale" {
			quote = at.Add(-10 * time.Second)
		}
		result = append(result, wenstrategy.Metric{Metric: c.Metric, LookbackMinutes: c.LookbackMinutes, ValueBps: value, MethodVersion: "wen-research/1", MarketBinding: doc.Market, RecordID: fmt.Sprintf("%s:%d:%s", scenario.ID, slot, key), InputDigest: scenario.Digest, Source: "compiled synthetic fixture", AsOf: at.Format(time.RFC3339), QuoteAt: quote.Format(time.RFC3339), LastMinuteEnd: at.Truncate(time.Minute).Format(time.RFC3339), CompleteMinutes: c.LookbackMinutes, Quality: "valid", Connected: true})
	}
	return result
}
