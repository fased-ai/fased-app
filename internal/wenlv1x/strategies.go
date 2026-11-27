package wenlv1x

import (
	"fmt"
	"math"
	"strings"
)

const (
	CurveBarCount = 80
	BarsPerBucket = 16 // 5 buckets * 16 bars = 80 bars total
)

var BucketCodes = [5]string{"A", "B", "C", "D", "E"}
var BucketLabels = [5]string{
	"Strong down (<-20bps)",
	"Down (-20..-5bps)",
	"Center (-5..+5bps)",
	"Up (+5..+20bps)",
	"Strong up (>+20bps)",
}

var BuiltInPresetWeights = map[string][5]uint16{
	"wide":       {2000, 2000, 2000, 2000, 2000},
	"center":     {500, 1500, 6000, 1500, 500},
	"trend-up":   {500, 1000, 1500, 4500, 2500},
	"trend-down": {2500, 4500, 1500, 1000, 500},
	"tails":      {4000, 1000, 0, 1000, 4000},
	"custom":     {2000, 2000, 2000, 2000, 2000},
}

// CurveFromBuckets expands 5 bucket bps weights smoothly across 80 allocation bars summing to 1.0.
func CurveFromBuckets(buckets [5]uint16) []float64 {
	out := make([]float64, CurveBarCount)
	var sum float64
	for b := range 5 {
		w := float64(buckets[b])
		for j := range BarsPerBucket {
			idx := b*BarsPerBucket + j
			// Smooth intra-bucket taper so bell/trend curves match WEN visual silhouette
			// while preserving exact bucket sums.
			pos := (float64(j) + 0.5) / float64(BarsPerBucket)
			shape := 0.88 + 0.24*math.Sin(pos*math.Pi)
			val := (w / float64(BarsPerBucket)) * shape
			out[idx] = val
			sum += val
		}
	}
	if sum <= 0 {
		for i := range out {
			out[i] = 1.0 / float64(CurveBarCount)
		}
		return out
	}
	for i := range out {
		out[i] = out[i] / sum
	}
	return out
}

// NormalizeCurve clamps finite non-negative 80-bar curve values to sum to 1.0.
func NormalizeCurve(raw []float64) []float64 {
	if len(raw) != CurveBarCount {
		return CurveFromBuckets(BuiltInPresetWeights["wide"])
	}
	out := make([]float64, CurveBarCount)
	var sum float64
	for i, v := range raw {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		out[i] = v
		sum += v
	}
	if sum <= 0 {
		return CurveFromBuckets(BuiltInPresetWeights["wide"])
	}
	for i := range out {
		out[i] = out[i] / sum
	}
	return out
}

// BucketsFromCurve collapses 80 normalized curve weights into 5 uint16 bps values summing to 10000.
func BucketsFromCurve(raw []float64) [5]uint16 {
	curve := NormalizeCurve(raw)
	var floats [5]float64
	for i, v := range curve {
		floats[i/BarsPerBucket] += v
	}
	var out [5]uint16
	var total int
	for i := range 5 {
		bps := int(math.Round(floats[i] * 10000))
		if bps < 0 {
			bps = 0
		}
		out[i] = uint16(bps)
		total += bps
	}
	if total != 10000 {
		maxIdx := 0
		for i := 1; i < 5; i++ {
			if out[i] > out[maxIdx] {
				maxIdx = i
			}
		}
		adj := int(out[maxIdx]) + (10000 - total)
		if adj < 0 {
			adj = 0
		}
		out[maxIdx] = uint16(adj)
	}
	return out
}

// CanonicalPresetID maps snake/space variants to canonical WEN preset IDs.
func CanonicalPresetID(id string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(id, "_", "-"))) {
	case "wide", "contrarian":
		return "wide"
	case "center", "chop", "neutral":
		return "center"
	case "trend-up", "bull", "up":
		return "trend-up"
	case "trend-down", "bear", "down":
		return "trend-down"
	case "tails", "edges", "breakout":
		return "tails"
	case "custom":
		return "custom"
	default:
		return "wide"
	}
}

// BuiltInPresets returns the 6 WEN strategy presets matching WEN RetailWorkspace.
func BuiltInPresets() []StrategyPreset {
	order := []struct {
		id   string
		name string
		desc string
		mode string
	}{
		{"wide", "Wide", "Balanced coverage across all five 10m BTC ranges", "neutral"},
		{"center", "Center", "Concentrates stake in tight -5..+5bps consolidation chop", "neutral"},
		{"trend-up", "Trend up", "Tilts 70% into +5..+20bps and breakout upside tails", "directional"},
		{"trend-down", "Trend down", "Tilts 70% into -5..-20bps and downside tail protection", "directional"},
		{"tails", "Tails", "Barbell volatility capture on strong directional expansion", "defensive"},
		{"custom", "Custom", "Interactive 80-bar user distribution curve", "neutral"},
	}
	out := make([]StrategyPreset, 0, len(order))
	for _, spec := range order {
		b := BuiltInPresetWeights[spec.id]
		out = append(out, StrategyPreset{
			ID:          spec.id,
			Name:        spec.name,
			Description: spec.desc,
			Mode:        spec.mode,
			Buckets:     b,
			Curve:       CurveFromBuckets(b),
		})
	}
	return out
}

// DominantBucket returns the bucket index (0..4) with highest bps weight.
func DominantBucket(weights [5]uint16) uint8 {
	bestIdx := 0
	for i := 1; i < 5; i++ {
		if weights[i] > weights[bestIdx] {
			bestIdx = i
		}
	}
	return uint8(bestIdx)
}

// ExpectedEVMultiple computes stake-weighted EV multiple against live bucket EV multiples.
func ExpectedEVMultiple(weights [5]uint16, buckets []RoundBucket) float64 {
	if len(buckets) < 5 {
		return 1.0
	}
	var ev float64
	for i := range 5 {
		w := float64(weights[i]) / 10000.0
		ev += w * buckets[i].EVMultiple
	}
	return math.Round(ev*1000) / 1000
}

// TrendDecision represents the Hybrid AI/Algo Mining governor decision for the active round.
type TrendDecision struct {
	Action          string  `json:"action"` // commit | pause | hold
	SelectedPreset  string  `json:"selectedPreset"`
	SelectedBucket  uint8   `json:"selectedBucket"`
	TrendRegime     string  `json:"trendRegime"` // uptrend | downtrend | rangebound | reversal_abort
	Reason          string  `json:"reason"`
	RecommendedSOL  float64 `json:"recommendedSol"`
	SignalTriggered bool    `json:"signalTriggered"`
}

// EvaluateTrendDecision evaluates BTC momentum, volatility, and crowd skew.
// If currentStrategy is directional (e.g. trend-up) and BTC momentum reverses sharply against it,
// or realized volatility whipsaws above threshold, it returns Action="pause" when stopOnReversal is set.
func EvaluateTrendDecision(currentStrategy string, stopOnReversal bool, moveBps int, volBps int, round RoundTelemetry, baseSOL float64) TrendDecision {
	canon := CanonicalPresetID(currentStrategy)
	regime := "rangebound"
	if moveBps >= 12 {
		regime = "uptrend"
	} else if moveBps <= -12 {
		regime = "downtrend"
	}

	// Detect adversarial reversal or excessive whipsaw.
	reversed := (canon == "trend-up" && moveBps <= -14) ||
		(canon == "trend-down" && moveBps >= 14) ||
		volBps >= 45
	if stopOnReversal && reversed {
		return TrendDecision{
			Action:          "pause",
			SelectedPreset:  canon,
			SelectedBucket:  2,
			TrendRegime:     "reversal_abort",
			Reason:          fmt.Sprintf("Trend reversal safeguard triggered (move=%dbps, vol=%dbps against %s)", moveBps, volBps, canon),
			RecommendedSOL:  0,
			SignalTriggered: true,
		}
	}

	// Contrarian crowd-skew optimization: pick bucket with highest model EV when strategy is wide/custom.
	bestIdx := uint8(2)
	bestEV := -1.0
	for _, b := range round.Buckets {
		if b.EVMultiple > bestEV {
			bestEV = b.EVMultiple
			bestIdx = b.Index
		}
	}
	chosenPreset := canon
	if canon == "wide" || canon == "" {
		switch {
		case moveBps >= 10:
			chosenPreset = "trend-up"
			bestIdx = 3
		case moveBps <= -10:
			chosenPreset = "trend-down"
			bestIdx = 1
		case volBps <= 8:
			chosenPreset = "center"
			bestIdx = 2
		default:
			chosenPreset = "wide"
		}
	} else if canon != "custom" {
		bestIdx = DominantBucket(BuiltInPresetWeights[canon])
	}
	return TrendDecision{
		Action:          "commit",
		SelectedPreset:  chosenPreset,
		SelectedBucket:  bestIdx,
		TrendRegime:     regime,
		Reason:          fmt.Sprintf("Regime=%s (move=%dbps, bestBucket=%s EV=%.2fx)", regime, moveBps, BucketCodes[bestIdx], bestEV),
		RecommendedSOL:  baseSOL,
		SignalTriggered: bestEV >= 1.15,
	}
}
