package quantlab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
)

const (
	maxScripts            = 64
	maxRunsRetained       = 200
	defaultInitialCapital = 10000.0
	defaultFeeBps         = 5.0
	defaultSlippageBps    = 3.0
	defaultPermutations   = 1000
	defaultWFPermutations = 200
	defaultHoldoutPct     = 0.20
	maxSerializedPoints   = 250
)

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ScriptRecord stores a validated FasedScript indicator or strategy definition.
type ScriptRecord struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Category    string                  `json:"category"`
	Description string                  `json:"description,omitempty"`
	Source      string                  `json:"source"`
	BuiltIn     bool                    `json:"builtIn"`
	Inputs      []fasedscript.InputSpec `json:"inputs,omitempty"`
	CreatedAt   int64                   `json:"createdAt"`
	UpdatedAt   int64                   `json:"updatedAt"`
}

// ScriptInput creates or updates a custom FasedScript document cleanly.
type ScriptInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
}

// BacktestConfig parameterizes market data lookup, execution friction, and 4-Gate settings.
type BacktestConfig struct {
	Venue          string             `json:"venue,omitempty"`
	Symbol         string             `json:"symbol,omitempty"`
	Interval       string             `json:"interval,omitempty"`
	Limit          int                `json:"limit,omitempty"`
	InitialCapital float64            `json:"initialCapital,omitempty"`
	FeeBps         float64            `json:"feeBps,omitempty"`
	SlippageBps    float64            `json:"slippageBps,omitempty"`
	Params         map[string]float64 `json:"params,omitempty"`
	Seed           uint64             `json:"seed,omitempty"`
	Permutations   int                `json:"permutations,omitempty"`
	WFPermutations int                `json:"wfPermutations,omitempty"`
	HoldoutPct     float64            `json:"holdoutPct,omitempty"`
	feeBpsSet      bool
	slippageBpsSet bool
}

// UnmarshalJSON tracks whether feeBps and slippageBps were explicitly provided (including 0)
// while preserving strict unknown-field checking.
func (c *BacktestConfig) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*c = BacktestConfig{}
		return nil
	}
	type alias BacktestConfig
	var aux alias
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	var rawKeys map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &rawKeys); err != nil {
		return err
	}
	for k := range rawKeys {
		switch {
		case strings.EqualFold(k, "feeBps"):
			aux.feeBpsSet = true
		case strings.EqualFold(k, "slippageBps"):
			aux.slippageBpsSet = true
		}
	}
	*c = BacktestConfig(aux)
	return nil
}

// TradeFill records a single simulated causal entry/exit trade fill.
type TradeFill struct {
	Side       string  `json:"side"`
	EntryTime  int64   `json:"entryTime"`
	ExitTime   int64   `json:"exitTime"`
	EntryPrice float64 `json:"entryPrice"`
	ExitPrice  float64 `json:"exitPrice"`
	PnLPct     float64 `json:"pnlPct"`
	PnLUSD     float64 `json:"pnlUsd"`
	FeeUSD     float64 `json:"feeUsd"`
	ExitReason string  `json:"exitReason"`
}

// EquityPoint records downsampled account equity and peak drawdown percent.
type EquityPoint struct {
	Time        int64   `json:"time"`
	Equity      float64 `json:"equity"`
	DrawdownPct float64 `json:"drawdownPct"`
}

// BacktestMetrics summarizes deterministic simulation performance metrics.
type BacktestMetrics struct {
	TotalReturnPct   float64 `json:"totalReturnPct"`
	AnnualizedSharpe float64 `json:"annualizedSharpe"`
	ProfitFactor     float64 `json:"profitFactor"`
	WinRatePct       float64 `json:"winRatePct"`
	MaxDrawdownPct   float64 `json:"maxDrawdownPct"`
	TradeCount       int     `json:"tradeCount"`
	AvgTradePct      float64 `json:"avgTradePct"`
	TotalFeesUSD     float64 `json:"totalFeesUsd"`
	FinalEquity      float64 `json:"finalEquity"`
	ExpectancyUSD    float64 `json:"expectancyUsd"`
}

// EvalIndicatorRequest runs a script or inline FasedScript source to extract chart plots.
type EvalIndicatorRequest struct {
	ScriptID string             `json:"scriptId,omitempty"`
	Source   string             `json:"source,omitempty"`
	Venue    string             `json:"venue,omitempty"`
	Symbol   string             `json:"symbol,omitempty"`
	Interval string             `json:"interval,omitempty"`
	Limit    int                `json:"limit,omitempty"`
	Params   map[string]float64 `json:"params,omitempty"`
}

// EvalIndicatorResult returns aligned timestamps and computed indicator plot series.
type EvalIndicatorResult struct {
	ScriptID string                     `json:"scriptId,omitempty"`
	Venue    string                     `json:"venue"`
	Symbol   string                     `json:"symbol"`
	Interval string                     `json:"interval"`
	Times    []int64                    `json:"times"`
	Inputs   []fasedscript.InputSpec    `json:"inputs"`
	Params   map[string]float64         `json:"params"`
	Plots    []fasedscript.PlotSeries   `json:"plots"`
	Signals  []fasedscript.SignalMarker `json:"signals,omitempty"`
	BarCount int                        `json:"barCount"`
}

// BacktestRequest executes a next-bar-open backtest for a stored or inline script.
type BacktestRequest struct {
	ScriptID string         `json:"scriptId,omitempty"`
	Source   string         `json:"source,omitempty"`
	Config   BacktestConfig `json:"config"`
}

// BacktestResult bundles deterministic simulation metrics, trades, equity, and plots.
type BacktestResult struct {
	ScriptID string                   `json:"scriptId,omitempty"`
	Config   BacktestConfig           `json:"config"`
	Metrics  BacktestMetrics          `json:"metrics"`
	Trades   []TradeFill              `json:"trades"`
	Equity   []EquityPoint            `json:"equity"`
	Plots    []fasedscript.PlotSeries `json:"plots"`
	Params   map[string]float64       `json:"params"`
	BarCount int                      `json:"barCount"`
}

// ValidateRequest triggers full 4-Gate statistical validation for a stored or inline script.
type ValidateRequest struct {
	ScriptID string         `json:"scriptId,omitempty"`
	Source   string         `json:"source,omitempty"`
	Config   BacktestConfig `json:"config"`
}

// HistogramBucket captures frequency counts across permutation Sharpe bins.
type HistogramBucket struct {
	From  float64 `json:"from"`
	To    float64 `json:"to"`
	Count int     `json:"count"`
}

// PlateauCell captures one parameter grid cell inside Gate 1.
type PlateauCell struct {
	Params    map[string]float64 `json:"params"`
	Sharpe    float64            `json:"sharpe"`
	ReturnPct float64            `json:"returnPct"`
	Trades    int                `json:"trades"`
	Passed    bool               `json:"passed"`
}

// WalkForwardFold captures one chronological train->OOS fold inside Gate 3.
type WalkForwardFold struct {
	Fold         int                `json:"fold"`
	TrainBars    int                `json:"trainBars"`
	TestBars     int                `json:"testBars"`
	ChosenParams map[string]float64 `json:"chosenParams"`
	TrainSharpe  float64            `json:"trainSharpe"`
	OOSSharpe    float64            `json:"oosSharpe"`
	OOSReturnPct float64            `json:"oosReturnPct"`
	OOSTrades    int                `json:"oosTrades"`
}

// Gate1Plateau summarizes parameter sensitivity across nearby parameter values.
type Gate1Plateau struct {
	Passed       bool               `json:"passed"`
	TestedCells  int                `json:"testedCells"`
	PassingCells int                `json:"passingCells"`
	PlateauRatio float64            `json:"plateauRatio"`
	BestSharpe   float64            `json:"bestSharpe"`
	BestParams   map[string]float64 `json:"bestParams"`
	Cells        []PlateauCell      `json:"cells"`
}

// Gate2Permutation summarizes the 1,000x historical bar permutation Monte Carlo test.
type Gate2Permutation struct {
	Passed     bool              `json:"passed"`
	Iterations int               `json:"iterations"`
	RealSharpe float64           `json:"realSharpe"`
	MeanPerm   float64           `json:"meanPerm"`
	P95Perm    float64           `json:"p95Perm"`
	PValue     float64           `json:"pValue"`
	BeatCount  int               `json:"beatCount"`
	Histogram  []HistogramBucket `json:"histogram"`
}

// Gate3WalkForward summarizes chronological 3-fold walk-forward + locked holdout testing.
type Gate3WalkForward struct {
	Passed           bool               `json:"passed"`
	Folds            []WalkForwardFold  `json:"folds"`
	OOSSharpe        float64            `json:"oosSharpe"`
	OOSReturnPct     float64            `json:"oosReturnPct"`
	HoldoutBars      int                `json:"holdoutBars"`
	HoldoutSharpe    float64            `json:"holdoutSharpe"`
	HoldoutReturnPct float64            `json:"holdoutReturnPct"`
	HoldoutTrades    int                `json:"holdoutTrades"`
	FrozenParams     map[string]float64 `json:"frozenParams"`
}

// Gate4WFPermutation summarizes 200x OOS/holdout permutations with frozen parameters.
type Gate4WFPermutation struct {
	Passed     bool              `json:"passed"`
	Iterations int               `json:"iterations"`
	RealSharpe float64           `json:"realSharpe"`
	MeanPerm   float64           `json:"meanPerm"`
	P90Perm    float64           `json:"p90Perm"`
	PValue     float64           `json:"pValue"`
	BeatCount  int               `json:"beatCount"`
	Histogram  []HistogramBucket `json:"histogram"`
}

// FourGateReport captures all 4 statistical gates, promotion eligibility (paper/shadow only),
// and representative simulation curves.
type FourGateReport struct {
	Grade           string             `json:"grade"`
	GatesPassed     int                `json:"gatesPassed"`
	PromotionReady  bool               `json:"promotionReady"`
	ExecutionPolicy string             `json:"executionPolicy"`
	Warnings        []string           `json:"warnings,omitempty"`
	Gate1           Gate1Plateau       `json:"gate1"`
	Gate2           Gate2Permutation   `json:"gate2"`
	Gate3           Gate3WalkForward   `json:"gate3"`
	Gate4           Gate4WFPermutation `json:"gate4"`
	BaselineMetrics BacktestMetrics    `json:"baselineMetrics"`
	Equity          []EquityPoint      `json:"equity"`
	Trades          []TradeFill        `json:"trades"`
}

// ValidationRunRecord stores a persisted 4-Gate validation report in lab.sqlite.
type ValidationRunRecord struct {
	ID         string         `json:"id"`
	ScriptID   string         `json:"scriptId"`
	ScriptName string         `json:"scriptName"`
	Config     BacktestConfig `json:"config"`
	Report     FourGateReport `json:"report"`
	DurationMs int64          `json:"durationMs"`
	CreatedAt  int64          `json:"createdAt"`
}

// ValidationRunSummary provides a compact row view for listing recent lab validation runs.
type ValidationRunSummary struct {
	ID             string  `json:"id"`
	ScriptID       string  `json:"scriptId"`
	ScriptName     string  `json:"scriptName"`
	Venue          string  `json:"venue"`
	Symbol         string  `json:"symbol"`
	Interval       string  `json:"interval"`
	Grade          string  `json:"grade"`
	GatesPassed    int     `json:"gatesPassed"`
	PromotionReady bool    `json:"promotionReady"`
	TotalReturnPct float64 `json:"totalReturnPct"`
	Sharpe         float64 `json:"sharpe"`
	DurationMs     int64   `json:"durationMs"`
	CreatedAt      int64   `json:"createdAt"`
}

// ValidateSafeID validates a bounded script or run identifier slug.
func ValidateSafeID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if !safeIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid identifier %q", raw)
	}
	return id, nil
}

// NormalizeBacktestConfig validates and defaults BacktestConfig bounds cleanly.
func NormalizeBacktestConfig(raw BacktestConfig) (BacktestConfig, error) {
	venue := strings.TrimSpace(raw.Venue)
	if venue == "" {
		venue = marketdata.VenueHyperliquid
	}
	normVenue, err := marketdata.NormalizeVenue(venue)
	if err != nil {
		return BacktestConfig{}, err
	}

	sym := strings.TrimSpace(raw.Symbol)
	if sym == "" {
		sym = "BTC"
	}
	normSym, err := marketdata.NormalizeSymbol(sym)
	if err != nil {
		return BacktestConfig{}, err
	}

	iv := strings.TrimSpace(raw.Interval)
	if iv == "" {
		iv = "1h"
	}
	normIV, err := marketdata.NormalizeInterval(iv)
	if err != nil {
		return BacktestConfig{}, err
	}

	limit := raw.Limit
	if limit == 0 {
		limit = 400
	}
	if limit < 60 || limit > 2000 {
		return BacktestConfig{}, errors.New("limit must be between 60 and 2000 candles")
	}

	capUSD := raw.InitialCapital
	if capUSD == 0 {
		capUSD = defaultInitialCapital
	}
	if math.IsNaN(capUSD) || math.IsInf(capUSD, 0) || capUSD < 100 || capUSD > 1e9 {
		return BacktestConfig{}, errors.New("initialCapital must be between 100 and 1e9")
	}

	feeBps := raw.FeeBps
	if feeBps == 0 && !raw.feeBpsSet {
		feeBps = defaultFeeBps
	}
	if math.IsNaN(feeBps) || math.IsInf(feeBps, 0) || feeBps < 0 || feeBps > 200 {
		return BacktestConfig{}, errors.New("feeBps must be between 0 and 200")
	}

	slipBps := raw.SlippageBps
	if slipBps == 0 && !raw.slippageBpsSet {
		slipBps = defaultSlippageBps
	}
	if math.IsNaN(slipBps) || math.IsInf(slipBps, 0) || slipBps < 0 || slipBps > 200 {
		return BacktestConfig{}, errors.New("slippageBps must be between 0 and 200")
	}

	perms := raw.Permutations
	if perms == 0 {
		perms = defaultPermutations
	}
	if perms < 50 || perms > 1000 {
		return BacktestConfig{}, errors.New("permutations must be between 50 and 1000")
	}

	wfPerms := raw.WFPermutations
	if wfPerms == 0 {
		wfPerms = defaultWFPermutations
	}
	if wfPerms < 25 || wfPerms > 200 {
		return BacktestConfig{}, errors.New("wfPermutations must be between 25 and 200")
	}

	holdout := raw.HoldoutPct
	if holdout == 0 {
		holdout = defaultHoldoutPct
	}
	if math.IsNaN(holdout) || math.IsInf(holdout, 0) || holdout < 0.10 || holdout > 0.35 {
		return BacktestConfig{}, errors.New("holdoutPct must be between 0.10 and 0.35")
	}

	seed := raw.Seed
	if seed == 0 {
		seed = 42
	}

	paramsCopy := make(map[string]float64, len(raw.Params))
	for k, v := range raw.Params {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			paramsCopy[k] = v
		}
	}

	return BacktestConfig{
		Venue:          normVenue,
		Symbol:         normSym,
		Interval:       normIV,
		Limit:          limit,
		InitialCapital: capUSD,
		FeeBps:         feeBps,
		SlippageBps:    slipBps,
		Params:         paramsCopy,
		Seed:           seed,
		Permutations:   perms,
		WFPermutations: wfPerms,
		HoldoutPct:     holdout,
	}, nil
}
