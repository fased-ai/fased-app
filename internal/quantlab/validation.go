package quantlab

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
)

type permRNG uint64

func (r *permRNG) nextUint64() uint64 {
	*r += 0x9e3779b97f4a7c15
	z := uint64(*r)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *permRNG) intn(n int) int {
	if n <= 1 {
		return 0
	}
	return int(r.nextUint64() % uint64(n))
}

type barShape struct {
	openGap float64
	highLog float64
	lowLog  float64
	bodyLog float64
	volume  float64
}

// PermuteCandles shuffles bar log-returns and intrabar OHLC geometry uniformly
// using deterministic splitmix64 PRNG while preserving bar-0 scale & marginal distribution.
func PermuteCandles(candles []marketdata.Candle, seed uint64) []marketdata.Candle {
	n := len(candles)
	if n < 3 {
		out := make([]marketdata.Candle, n)
		copy(out, candles)
		return out
	}

	shapes := make([]barShape, n-1)
	for i := 1; i < n; i++ {
		prevC := math.Max(1e-9, candles[i-1].Close)
		openPx := math.Max(1e-9, candles[i].Open)
		highPx := math.Max(openPx, candles[i].High)
		lowPx := math.Max(1e-9, math.Min(openPx, candles[i].Low))
		closePx := math.Max(1e-9, candles[i].Close)

		shapes[i-1] = barShape{
			openGap: math.Log(openPx / prevC),
			highLog: math.Log(highPx / openPx),
			lowLog:  math.Log(lowPx / openPx),
			bodyLog: math.Log(closePx / openPx),
			volume:  candles[i].Volume,
		}
	}

	rng := permRNG(seed)
	if rng == 0 {
		rng = 1
	}
	for i := len(shapes) - 1; i > 0; i-- {
		j := rng.intn(i + 1)
		shapes[i], shapes[j] = shapes[j], shapes[i]
	}

	out := make([]marketdata.Candle, n)
	out[0] = candles[0]
	prevClose := math.Max(1e-9, out[0].Close)
	for i := 1; i < n; i++ {
		s := shapes[i-1]
		o := prevClose * math.Exp(s.openGap)
		h := o * math.Exp(s.highLog)
		l := o * math.Exp(s.lowLog)
		c := o * math.Exp(s.bodyLog)
		if h < math.Max(o, c) {
			h = math.Max(o, c)
		}
		if l > math.Min(o, c) {
			l = math.Min(o, c)
		}
		out[i] = marketdata.Candle{
			Time:   candles[i].Time,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: s.volume,
		}
		prevClose = c
	}
	return out
}

// PermuteCandlesWithPrefix keeps prefix untouched for causal indicator warm-up
// while permuting tail returns and intrabar OHLC geometry anchored to the last prefix close.
func PermuteCandlesWithPrefix(prefix, tail []marketdata.Candle, seed uint64) []marketdata.Candle {
	out := make([]marketdata.Candle, 0, len(prefix)+len(tail))
	out = append(out, prefix...)
	if len(prefix) == 0 || len(tail) < 2 {
		out = append(out, PermuteCandles(tail, seed)...)
		return out
	}

	shapes := make([]barShape, len(tail))
	prevC := math.Max(1e-9, prefix[len(prefix)-1].Close)
	for i := 0; i < len(tail); i++ {
		if i > 0 {
			prevC = math.Max(1e-9, tail[i-1].Close)
		}
		openPx := math.Max(1e-9, tail[i].Open)
		highPx := math.Max(openPx, tail[i].High)
		lowPx := math.Max(1e-9, math.Min(openPx, tail[i].Low))
		closePx := math.Max(1e-9, tail[i].Close)
		shapes[i] = barShape{
			openGap: math.Log(openPx / prevC),
			highLog: math.Log(highPx / openPx),
			lowLog:  math.Log(lowPx / openPx),
			bodyLog: math.Log(closePx / openPx),
			volume:  tail[i].Volume,
		}
	}

	rng := permRNG(seed)
	if rng == 0 {
		rng = 1
	}
	for i := len(shapes) - 1; i > 0; i-- {
		j := rng.intn(i + 1)
		shapes[i], shapes[j] = shapes[j], shapes[i]
	}

	prevClose := math.Max(1e-9, prefix[len(prefix)-1].Close)
	for i := 0; i < len(tail); i++ {
		s := shapes[i]
		o := prevClose * math.Exp(s.openGap)
		h := o * math.Exp(s.highLog)
		l := o * math.Exp(s.lowLog)
		c := o * math.Exp(s.bodyLog)
		if h < math.Max(o, c) {
			h = math.Max(o, c)
		}
		if l > math.Min(o, c) {
			l = math.Min(o, c)
		}
		out = append(out, marketdata.Candle{
			Time:   tail[i].Time,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: s.volume,
		})
		prevClose = c
	}
	return out
}

func sliceBoolTail(arr []bool, start int) []bool {
	if start <= 0 || len(arr) <= start {
		if start <= 0 {
			return arr
		}
		return nil
	}
	return arr[start:]
}

func sliceStrategy(spec fasedscript.StrategySpec, start int) fasedscript.StrategySpec {
	out := spec
	out.Long = sliceBoolTail(spec.Long, start)
	out.ExitLong = sliceBoolTail(spec.ExitLong, start)
	out.Short = sliceBoolTail(spec.Short, start)
	out.ExitShort = sliceBoolTail(spec.ExitShort, start)
	return out
}

func evalAndSimulateWindow(candles []marketdata.Candle, evalStart int, prog *fasedscript.Program, params map[string]float64, cfg BacktestConfig) (BacktestMetrics, error) {
	if evalStart < 0 {
		evalStart = 0
	}
	if evalStart >= len(candles) {
		return BacktestMetrics{FinalEquity: cfg.InitialCapital}, nil
	}
	ev, err := prog.Eval(candles, params)
	if err != nil {
		return BacktestMetrics{}, err
	}
	sliced := sliceStrategy(ev.Strategy, evalStart)
	m, _, _ := Simulate(candles[evalStart:], sliced, cfg)
	return m, nil
}

func buildParamGrid(inputs []fasedscript.InputSpec, baseParams map[string]float64) []map[string]float64 {
	cloneBase := func() map[string]float64 {
		m := make(map[string]float64, len(baseParams))
		for k, v := range baseParams {
			m[k] = v
		}
		return m
	}
	if len(inputs) == 0 {
		return []map[string]float64{cloneBase()}
	}

	grid := []map[string]float64{cloneBase()}
	limitInputs := inputs
	if len(limitInputs) > 3 {
		limitInputs = limitInputs[:3]
	}

	for _, inp := range limitInputs {
		var vals []float64
		if inp.Max > inp.Min {
			mid := inp.Default
			if bp, ok := baseParams[inp.Name]; ok && !math.IsNaN(bp) && !math.IsInf(bp, 0) {
				mid = math.Max(inp.Min, math.Min(inp.Max, bp))
			}
			step := inp.Step
			if step <= 0 {
				step = (inp.Max - inp.Min) / 4.0
			}
			cands := []float64{
				math.Max(inp.Min, mid-step),
				mid,
				math.Min(inp.Max, mid+step),
			}
			seen := make(map[float64]bool)
			for _, cv := range cands {
				r := math.Round(cv*10000) / 10000
				if !seen[r] {
					seen[r] = true
					vals = append(vals, r)
				}
			}
		} else {
			v := inp.Default
			if bp, ok := baseParams[inp.Name]; ok && !math.IsNaN(bp) && !math.IsInf(bp, 0) {
				v = bp
			}
			vals = []float64{v}
		}

		next := make([]map[string]float64, 0, len(grid)*len(vals))
		for _, g := range grid {
			for _, v := range vals {
				cp := make(map[string]float64, len(g)+1)
				for k, gv := range g {
					cp[k] = gv
				}
				cp[inp.Name] = v
				next = append(next, cp)
				if len(next) >= 25 {
					return next
				}
			}
		}
		grid = next
	}
	return grid
}

// RunFourGateValidation executes deterministic Gates 1-4 on compiled FasedScript and candles.
func RunFourGateValidation(ctx context.Context, candles []marketdata.Candle, prog *fasedscript.Program, cfg BacktestConfig) (FourGateReport, error) {
	var warnings []string

	// Reserve chronological locked holdout slice from the tail.
	n := len(candles)
	holdoutBars := int(math.Round(float64(n) * cfg.HoldoutPct))
	if holdoutBars < 20 {
		holdoutBars = 20
	}
	if n-holdoutBars < 40 {
		holdoutBars = n / 4
	}
	inSample := candles[:n-holdoutBars]
	holdoutSlice := candles[n-holdoutBars:]

	// Gate 1: Parameter Plateau Sweep across in-sample bars.
	grid := buildParamGrid(prog.Inputs, cfg.Params)
	cells := make([]PlateauCell, 0, len(grid))
	bestAnyIdx := 0
	bestEligibleIdx := -1
	passingCells := 0

	for idx, params := range grid {
		if ctx.Err() != nil {
			return FourGateReport{}, ctx.Err()
		}
		ev, err := prog.Eval(inSample, params)
		if err != nil {
			return FourGateReport{}, err
		}
		m, _, _ := Simulate(inSample, ev.Strategy, cfg)
		cellPassed := m.AnnualizedSharpe > 0 && m.TradeCount >= 3
		if cellPassed {
			passingCells++
		}
		cells = append(cells, PlateauCell{
			Params:    ev.Params,
			Sharpe:    m.AnnualizedSharpe,
			ReturnPct: m.TotalReturnPct,
			Trades:    m.TradeCount,
			Passed:    cellPassed,
		})
		if m.AnnualizedSharpe > cells[bestAnyIdx].Sharpe {
			bestAnyIdx = idx
		}
		if m.TradeCount >= 3 && (bestEligibleIdx < 0 || m.AnnualizedSharpe > cells[bestEligibleIdx].Sharpe) {
			bestEligibleIdx = idx
		}
	}

	bestCellIdx := bestAnyIdx
	if bestEligibleIdx >= 0 {
		bestCellIdx = bestEligibleIdx
	}
	plateauRatio := 0.0
	if len(cells) > 0 {
		plateauRatio = round4(float64(passingCells) / float64(len(cells)))
	}
	bestParams := cells[bestCellIdx].Params
	bestSharpe := cells[bestCellIdx].Sharpe
	gate1 := Gate1Plateau{
		Passed:       bestSharpe > 0 && plateauRatio >= 0.50 && cells[bestCellIdx].Trades >= 3,
		TestedCells:  len(cells),
		PassingCells: passingCells,
		PlateauRatio: plateauRatio,
		BestSharpe:   bestSharpe,
		BestParams:   bestParams,
		Cells:        cells,
	}

	if ctx.Err() != nil {
		return FourGateReport{}, ctx.Err()
	}
	// Evaluate real baseline across full non-holdout IS + full curve.
	fullEval, err := prog.Eval(candles, bestParams)
	if err != nil {
		return FourGateReport{}, err
	}
	baselineMetrics, trades, equityCurve := Simulate(candles, fullEval.Strategy, cfg)
	if baselineMetrics.TradeCount < 3 {
		warnings = append(warnings, "fewer than 3 round-trip trades executed")
	}

	// Gate 2: 1,000x Historical Bar Permutation Monte Carlo (bounded parallel deterministic workers).
	isEval, err := prog.Eval(inSample, bestParams)
	if err != nil {
		return FourGateReport{}, err
	}
	realISMetrics, _, _ := Simulate(inSample, isEval.Strategy, cfg)
	permSharpes, err := runPermutationSweep(ctx, inSample, prog, bestParams, cfg, cfg.Permutations, cfg.Seed)
	if err != nil {
		return FourGateReport{}, err
	}
	gate2 := summarizePermutations(realISMetrics.AnnualizedSharpe, permSharpes, 0.05)
	if len(grid) > 1 && gate2.Iterations > 0 {
		adjP := 1.0 - math.Pow(1.0-gate2.PValue, float64(len(grid)))
		if adjP > 1.0 {
			adjP = 1.0
		}
		gate2.PValue = round4(adjP)
		gate2.Passed = gate2.PValue <= 0.05 && realISMetrics.AnnualizedSharpe > 0
		warnings = append(warnings, fmt.Sprintf("Gate 2 p-value Šidák-adjusted across %d Gate 1 grid cells", len(grid)))
	}

	// Gate 3: 3-Fold Chronological Walk-Forward + Locked Holdout Evaluation (with causal warm-up prefix).
	holdoutStart := n - holdoutBars
	folds, frozenParams, err := runWalkForwardFolds(ctx, inSample, prog, grid, cfg)
	if err != nil {
		return FourGateReport{}, err
	}
	holdMetrics, err := evalAndSimulateWindow(candles, holdoutStart, prog, frozenParams, cfg)
	if err != nil {
		return FourGateReport{}, err
	}

	var sumOOSSharpe, sumOOSRet float64
	for _, f := range folds {
		sumOOSSharpe += f.OOSSharpe
		sumOOSRet += f.OOSReturnPct
	}
	avgOOSSharpe := 0.0
	avgOOSRet := 0.0
	if len(folds) > 0 {
		avgOOSSharpe = round4(sumOOSSharpe / float64(len(folds)))
		avgOOSRet = round4(sumOOSRet / float64(len(folds)))
	}
	gate3 := Gate3WalkForward{
		Passed:           avgOOSSharpe > 0 && holdMetrics.TradeCount >= 3 && holdMetrics.AnnualizedSharpe >= 0 && holdMetrics.TotalReturnPct > -5.0,
		Folds:            folds,
		OOSSharpe:        avgOOSSharpe,
		OOSReturnPct:     avgOOSRet,
		HoldoutBars:      len(holdoutSlice),
		HoldoutSharpe:    holdMetrics.AnnualizedSharpe,
		HoldoutReturnPct: holdMetrics.TotalReturnPct,
		HoldoutTrades:    holdMetrics.TradeCount,
		FrozenParams:     frozenParams,
	}

	// Gate 4: 200x Walk-Forward Out-Of-Sample / Holdout Permutation with frozen parameters & causal warm-up prefix.
	wfPermSharpes, err := runHoldoutPermutationSweep(ctx, inSample, holdoutSlice, prog, frozenParams, cfg, cfg.WFPermutations, cfg.Seed^0x5555555555555555)
	if err != nil {
		return FourGateReport{}, err
	}
	gate4Raw := summarizePermutations(holdMetrics.AnnualizedSharpe, wfPermSharpes, 0.10)
	gate4 := Gate4WFPermutation{
		Passed:     gate4Raw.Passed,
		Iterations: gate4Raw.Iterations,
		RealSharpe: gate4Raw.RealSharpe,
		MeanPerm:   gate4Raw.MeanPerm,
		P90Perm:    gate4Raw.P95Perm, // computed at percentile 90 below
		PValue:     gate4Raw.PValue,
		BeatCount:  gate4Raw.BeatCount,
		Histogram:  gate4Raw.Histogram,
	}
	gate4.P90Perm = percentile(wfPermSharpes, 0.90)

	passed := 0
	for _, ok := range []bool{gate1.Passed, gate2.Passed, gate3.Passed, gate4.Passed} {
		if ok {
			passed++
		}
	}
	grade := "REJECTED"
	if passed == 4 {
		grade = "PASS_4_GATE"
	} else if passed >= 2 {
		grade = "PASS_PARTIAL"
	}

	return FourGateReport{
		Grade:           grade,
		GatesPassed:     passed,
		PromotionReady:  passed == 4,
		ExecutionPolicy: "paper_shadow_only",
		Warnings:        warnings,
		Gate1:           gate1,
		Gate2:           gate2,
		Gate3:           gate3,
		Gate4:           gate4,
		BaselineMetrics: baselineMetrics,
		Equity:          equityCurve,
		Trades:          trades,
	}, nil
}

func runWalkForwardFolds(ctx context.Context, candles []marketdata.Candle, prog *fasedscript.Program, grid []map[string]float64, cfg BacktestConfig) ([]WalkForwardFold, map[string]float64, error) {
	const numFolds = 3
	n := len(candles)
	foldSpan := n / (numFolds + 1)
	if foldSpan < 15 {
		foldSpan = 15
	}
	folds := make([]WalkForwardFold, 0, numFolds)
	bestParams := grid[0]

	for fIdx := 0; fIdx < numFolds; fIdx++ {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		trainEnd := foldSpan * (fIdx + 1)
		testEnd := foldSpan * (fIdx + 2)
		if fIdx == numFolds-1 || testEnd > n {
			testEnd = n
		}
		if trainEnd >= n || testEnd <= trainEnd+5 {
			break
		}
		trainSlice := candles[:trainEnd]

		chosenEligible := grid[0]
		bestEligibleSharpe := math.Inf(-1)
		chosenActive := grid[0]
		bestActiveSharpe := math.Inf(-1)

		for _, params := range grid {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			ev, err := prog.Eval(trainSlice, params)
			if err != nil {
				continue
			}
			m, _, _ := Simulate(trainSlice, ev.Strategy, cfg)
			if m.TradeCount >= 3 && m.AnnualizedSharpe > bestEligibleSharpe {
				bestEligibleSharpe = m.AnnualizedSharpe
				chosenEligible = ev.Params
			}
			if m.TradeCount > 0 && m.AnnualizedSharpe > bestActiveSharpe {
				bestActiveSharpe = m.AnnualizedSharpe
				chosenActive = ev.Params
			}
		}

		chosen := grid[0]
		bestTrainSharpe := 0.0
		if !math.IsInf(bestEligibleSharpe, -1) {
			chosen = chosenEligible
			bestTrainSharpe = bestEligibleSharpe
		} else if !math.IsInf(bestActiveSharpe, -1) {
			chosen = chosenActive
			bestTrainSharpe = bestActiveSharpe
		}

		// Evaluate OOS test slice with full train prefix warm-up (zero look-ahead).
		oosMetrics, err := evalAndSimulateWindow(candles[:testEnd], trainEnd, prog, chosen, cfg)
		if err != nil {
			oosMetrics = BacktestMetrics{}
		}
		bestParams = chosen
		folds = append(folds, WalkForwardFold{
			Fold:         fIdx + 1,
			TrainBars:    len(trainSlice),
			TestBars:     testEnd - trainEnd,
			ChosenParams: chosen,
			TrainSharpe:  round4(bestTrainSharpe),
			OOSSharpe:    oosMetrics.AnnualizedSharpe,
			OOSReturnPct: oosMetrics.TotalReturnPct,
			OOSTrades:    oosMetrics.TradeCount,
		})
	}
	return folds, bestParams, nil
}

func runParallelPermutations(ctx context.Context, iters int, fn func(idx int) float64) ([]float64, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	results := make([]float64, iters)
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	if workers < 1 {
		workers = 1
	}

	var nextIdx int
	var mu sync.Mutex
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				idx := nextIdx
				nextIdx++
				mu.Unlock()
				if idx >= iters {
					return
				}
				results[idx] = fn(idx)
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return results, nil
}

func runPermutationSweep(ctx context.Context, candles []marketdata.Candle, prog *fasedscript.Program, params map[string]float64, cfg BacktestConfig, iters int, baseSeed uint64) ([]float64, error) {
	return runParallelPermutations(ctx, iters, func(idx int) float64 {
		seed := baseSeed ^ uint64(idx+1)*0x9e3779b97f4a7c15
		permed := PermuteCandles(candles, seed)
		ev, err := prog.Eval(permed, params)
		if err != nil {
			return 0
		}
		m, _, _ := Simulate(permed, ev.Strategy, cfg)
		return m.AnnualizedSharpe
	})
}

func runHoldoutPermutationSweep(ctx context.Context, prefix, holdout []marketdata.Candle, prog *fasedscript.Program, params map[string]float64, cfg BacktestConfig, iters int, baseSeed uint64) ([]float64, error) {
	evalStart := len(prefix)
	return runParallelPermutations(ctx, iters, func(idx int) float64 {
		seed := baseSeed ^ uint64(idx+1)*0x9e3779b97f4a7c15
		permedFull := PermuteCandlesWithPrefix(prefix, holdout, seed)
		m, err := evalAndSimulateWindow(permedFull, evalStart, prog, params, cfg)
		if err != nil {
			return 0
		}
		return m.AnnualizedSharpe
	})
}

func summarizePermutations(realSharpe float64, perms []float64, alpha float64) Gate2Permutation {
	n := len(perms)
	if n == 0 {
		return Gate2Permutation{}
	}
	beat := 0
	var sum float64
	for _, s := range perms {
		sum += s
		if s >= realSharpe {
			beat++
		}
	}
	mean := sum / float64(n)
	pVal := float64(beat+1) / float64(n+1)
	p95 := percentile(perms, 0.95)
	return Gate2Permutation{
		Passed:     pVal <= alpha && realSharpe > 0,
		Iterations: n,
		RealSharpe: round4(realSharpe),
		MeanPerm:   round4(mean),
		P95Perm:    round4(p95),
		PValue:     round4(pVal),
		BeatCount:  beat,
		Histogram:  buildHistogram(perms, 12),
	}
}

func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	idx := int(math.Floor(p * float64(len(cp)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return round4(cp[idx])
}

func buildHistogram(vals []float64, buckets int) []HistogramBucket {
	if len(vals) == 0 || buckets < 1 {
		return nil
	}
	minV, maxV := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV <= minV {
		return []HistogramBucket{{From: round4(minV), To: round4(maxV + 0.1), Count: len(vals)}}
	}
	width := (maxV - minV) / float64(buckets)
	out := make([]HistogramBucket, buckets)
	for b := 0; b < buckets; b++ {
		out[b] = HistogramBucket{
			From: round4(minV + float64(b)*width),
			To:   round4(minV + float64(b+1)*width),
		}
	}
	for _, v := range vals {
		b := int(math.Floor((v - minV) / width))
		if b < 0 {
			b = 0
		}
		if b >= buckets {
			b = buckets - 1
		}
		out[b].Count++
	}
	return out
}
