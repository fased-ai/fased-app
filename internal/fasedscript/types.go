package fasedscript

import (
	"encoding/json"
	"math"
	"strconv"
)

const (
	MaxSourceBytes    = 32 * 1024
	MaxASTNodes       = 2048
	MaxEvalSteps      = 200_000
	MaxRecursionDepth = 32
	MaxInputs         = 12
	MaxPlots          = 8
	MaxCandles        = 5000
	MaxSeriesLength   = MaxCandles
)

// NullableFloat serializes NaN/Inf indicator warm-up bars as JSON null
// so Go encoding/json never errors on non-finite values.
type NullableFloat float64

// MarshalJSON encodes NaN/Inf as JSON null and finite numbers as JSON floats.
func (f NullableFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	rounded := math.Round(v*1e6) / 1e6
	return []byte(strconv.FormatFloat(rounded, 'f', -1, 64)), nil
}

// UnmarshalJSON decodes null as NaN and numeric tokens as finite float64 values.
func (f *NullableFloat) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*f = NullableFloat(math.NaN())
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*f = NullableFloat(v)
	return nil
}

// InputSpec describes a tunable parameter declared via input(name, default, [min, max, step]).
type InputSpec struct {
	Name    string  `json:"name"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Step    float64 `json:"step"`
}

// PlotSeries captures a visual indicator curve emitted via plot(name, series, [tone, pane]).
type PlotSeries struct {
	Name   string          `json:"name"`
	Tone   string          `json:"tone"`
	Pane   string          `json:"pane"`
	Values []NullableFloat `json:"values"`
}

// SignalMarker records per-bar boolean triggers emitted via signal(kind, cond).
type SignalMarker struct {
	Kind string `json:"kind"`
	Bars []bool `json:"bars"`
}

// StrategySpec captures bar-by-bar entry/exit conditions and risk parameters
// emitted by strategy({...}).
type StrategySpec struct {
	Long            []bool  `json:"long,omitempty"`
	ExitLong        []bool  `json:"exitLong,omitempty"`
	Short           []bool  `json:"short,omitempty"`
	ExitShort       []bool  `json:"exitShort,omitempty"`
	StopLossPct     float64 `json:"stopLossPct,omitempty"`
	TakeProfitPct   float64 `json:"takeProfitPct,omitempty"`
	TrailingAtrMult float64 `json:"trailingAtrMult,omitempty"`
	SizePct         float64 `json:"sizePct,omitempty"`
}

// EvalResult holds all plotted series, parameter resolution, and strategy signals
// produced by running a compiled Program over causal OHLCV bars.
type EvalResult struct {
	Inputs    []InputSpec        `json:"inputs"`
	Params    map[string]float64 `json:"params"`
	Plots     []PlotSeries       `json:"plots"`
	Signals   []SignalMarker     `json:"signals,omitempty"`
	Strategy  StrategySpec       `json:"strategy"`
	StepsUsed int                `json:"stepsUsed"`
	Warnings  []string           `json:"warnings,omitempty"`
	BarCount  int                `json:"barCount"`
}

// Template defines a built-in curated FasedScript starter strategy or indicator.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Source      string `json:"source"`
}
