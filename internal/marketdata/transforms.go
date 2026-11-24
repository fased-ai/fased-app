package marketdata

import (
	"fmt"
	"math"
	"sort"
)

func sanitizeCandles(in []Candle) ([]Candle, error) {
	out := make([]Candle, 0, len(in))
	for _, c := range in {
		if !isFinitePositive(c.Open) || !isFinitePositive(c.High) || !isFinitePositive(c.Low) || !isFinitePositive(c.Close) {
			return nil, errorsInvalidCandle(c)
		}
		if !isFiniteNonNeg(c.Volume) || !isFiniteNonNeg(c.BuyVolume) || !isFiniteNonNeg(c.SellVolume) {
			return nil, errorsInvalidCandle(c)
		}
		hi := math.Max(c.High, math.Max(c.Open, c.Close))
		lo := math.Min(c.Low, math.Min(c.Open, c.Close))
		c.High = hi
		c.Low = lo
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time < out[j].Time
	})
	return out, nil
}

func errorsInvalidCandle(c Candle) error {
	return fmt.Errorf("non-finite or non-positive OHLC values at time %d", c.Time)
}

func appendMonotonicBar(out []Candle, c Candle) []Candle {
	if len(out) > 0 && c.Time <= out[len(out)-1].Time {
		c.Time = out[len(out)-1].Time + 1
	}
	return append(out, c)
}

func autoPriceStep(candles []Candle, divisor float64) float64 {
	if len(candles) == 0 {
		return 1
	}
	closes := make([]float64, len(candles))
	var rangeSum float64
	for i, c := range candles {
		closes[i] = c.Close
		rangeSum += math.Max(c.High-c.Low, 0)
	}
	sort.Float64s(closes)
	median := closes[len(closes)/2]
	avgRange := rangeSum / float64(len(candles))
	step := avgRange
	if !isFinitePositive(step) {
		step = median * 0.005
	}
	if divisor > 0 {
		step = step / divisor
	}
	minFloor := median * 1e-4
	if minFloor <= 0 || !isFinitePositive(minFloor) {
		minFloor = 1e-6
	}
	if step < minFloor {
		step = minFloor
	}
	if !isFinitePositive(step) {
		return 1
	}
	return step
}

// TransformCandles converts standard OHLCV candles into the requested chart style.
func TransformCandles(base []Candle, opts TransformOptions) ([]Candle, error) {
	style, err := NormalizeStyle(opts.Style)
	if err != nil {
		return nil, err
	}
	clean, err := sanitizeCandles(base)
	if err != nil {
		return nil, err
	}
	if len(clean) == 0 {
		return []Candle{}, nil
	}

	switch style {
	case StyleStandard:
		return clean, nil
	case StyleHeikinAshi:
		return transformHeikinAshi(clean), nil
	case StyleRenko:
		return transformRenko(clean, opts.BrickSize), nil
	case StyleRange:
		return transformRange(clean, opts.RangeSize), nil
	case StyleKagi:
		return transformKagi(clean, opts.Reversal), nil
	case StylePointFigure:
		return transformPointFigure(clean, opts.BoxSize, int(opts.Reversal)), nil
	case StyleThreeLineBreak:
		n := opts.LineBreakCount
		if n <= 0 {
			n = 3
		}
		return transformLineBreak(clean, n), nil
	case StyleTick:
		count := opts.TickCount
		if count <= 0 {
			count = 50
		}
		return groupCandlesByThreshold(clean, float64(count), true), nil
	case StyleVolume:
		thresh := opts.VolumeThreshold
		if !isFinitePositive(thresh) {
			var total float64
			for _, c := range clean {
				total += c.Volume
			}
			thresh = math.Max(total/math.Max(1, float64(len(clean))/2), 1)
		}
		return groupCandlesByThreshold(clean, thresh, false), nil
	default:
		return clean, nil
	}
}

func transformHeikinAshi(in []Candle) []Candle {
	out := make([]Candle, len(in))
	for i, c := range in {
		haClose := (c.Open + c.High + c.Low + c.Close) / 4.0
		var haOpen float64
		if i == 0 {
			haOpen = (c.Open + c.Close) / 2.0
		} else {
			haOpen = (out[i-1].Open + out[i-1].Close) / 2.0
		}
		haHigh := math.Max(c.High, math.Max(haOpen, haClose))
		haLow := math.Min(c.Low, math.Min(haOpen, haClose))
		out[i] = Candle{
			Time:       c.Time,
			Open:       haOpen,
			High:       haHigh,
			Low:        haLow,
			Close:      haClose,
			Volume:     c.Volume,
			BuyVolume:  c.BuyVolume,
			SellVolume: c.SellVolume,
			Trades:     c.Trades,
		}
	}
	return out
}

func transformRenko(in []Candle, brickSize float64) []Candle {
	if !isFinitePositive(brickSize) {
		brickSize = autoPriceStep(in, 1.0)
	}
	if brickSize <= in[0].Close*1e-4 {
		brickSize = math.Max(in[0].Close*1e-4, 1e-6)
	}
	anchor := math.Floor(in[0].Close/brickSize) * brickSize
	if anchor <= 0 {
		anchor = in[0].Close
	}
	var out []Candle
	lastDir := 0
	for _, c := range in {
		price := c.Close
		for len(out) < maxTransformedBars {
			upThresh := anchor + brickSize
			downThresh := anchor - brickSize
			if lastDir == -1 {
				upThresh = anchor + 2*brickSize
			} else if lastDir == 1 {
				downThresh = anchor - 2*brickSize
			}
			if price >= upThresh {
				openPrice := anchor
				if lastDir == -1 {
					openPrice = anchor + brickSize
				}
				closePrice := openPrice + brickSize
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   openPrice,
					High:   closePrice,
					Low:    openPrice,
					Close:  closePrice,
					Volume: c.Volume,
					Trades: c.Trades,
				})
				anchor = closePrice
				lastDir = 1
				continue
			}
			if price <= downThresh && anchor-brickSize > 0 {
				openPrice := anchor
				if lastDir == 1 {
					openPrice = anchor - brickSize
				}
				closePrice := math.Max(openPrice-brickSize, 1e-8)
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   openPrice,
					High:   openPrice,
					Low:    closePrice,
					Close:  closePrice,
					Volume: c.Volume,
					Trades: c.Trades,
				})
				anchor = closePrice
				lastDir = -1
				continue
			}
			break
		}
		if len(out) >= maxTransformedBars {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, in[0])
	}
	return out
}

func transformRange(in []Candle, rangeSize float64) []Candle {
	if !isFinitePositive(rangeSize) {
		rangeSize = autoPriceStep(in, 1.0)
	}
	if rangeSize <= in[0].Close*1e-4 {
		rangeSize = math.Max(in[0].Close*1e-4, 1e-6)
	}
	var out []Candle
	cur := in[0]
	for i := 1; i < len(in); i++ {
		c := in[i]
		cur.High = math.Max(cur.High, c.High)
		cur.Low = math.Min(cur.Low, c.Low)
		cur.Close = c.Close
		cur.Volume += c.Volume
		cur.BuyVolume += c.BuyVolume
		cur.SellVolume += c.SellVolume
		cur.Trades += c.Trades
		for cur.High-cur.Low >= rangeSize && len(out) < maxTransformedBars {
			capped := cur
			if cur.Close >= cur.Open {
				capped.High = cur.Low + rangeSize
				capped.Close = capped.High
			} else {
				capped.Low = cur.High - rangeSize
				capped.Close = capped.Low
			}
			capped.Time = c.Time
			out = appendMonotonicBar(out, capped)
			nextStart := capped.Close
			cur = Candle{
				Time:  c.Time,
				Open:  nextStart,
				High:  math.Max(nextStart, c.High),
				Low:   math.Min(nextStart, c.Low),
				Close: c.Close,
			}
		}
	}
	if len(out) < maxTransformedBars {
		out = appendMonotonicBar(out, cur)
	}
	return out
}

func transformKagi(in []Candle, reversalPct float64) []Candle {
	if !isFinitePositive(reversalPct) {
		reversalPct = 0.01 // 1% default swing threshold.
	}
	if reversalPct > 0.5 {
		reversalPct = reversalPct / 100.0
	}
	var out []Candle
	start := in[0].Close
	extreme := start
	dir := 0 // 1 up, -1 down
	startTime := in[0].Time
	var vol float64
	for _, c := range in {
		vol += c.Volume
		p := c.Close
		switch dir {
		case 0:
			if p >= start*(1+reversalPct) {
				dir = 1
				extreme = p
			} else if p <= start*(1-reversalPct) {
				dir = -1
				extreme = p
			}
		case 1:
			if p > extreme {
				extreme = p
			} else if p <= extreme*(1-reversalPct) {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   start,
					High:   math.Max(start, extreme),
					Low:    math.Min(start, extreme),
					Close:  extreme,
					Volume: vol,
				})
				start = extreme
				extreme = p
				dir = -1
				startTime = c.Time
				vol = 0
			}
		case -1:
			if p < extreme {
				extreme = p
			} else if p >= extreme*(1+reversalPct) {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   start,
					High:   math.Max(start, extreme),
					Low:    math.Min(start, extreme),
					Close:  extreme,
					Volume: vol,
				})
				start = extreme
				extreme = p
				dir = 1
				startTime = c.Time
				vol = 0
			}
		}
		if len(out) >= maxTransformedBars {
			break
		}
	}
	if len(out) < maxTransformedBars {
		out = appendMonotonicBar(out, Candle{
			Time:   maxInt64(startTime, in[len(in)-1].Time),
			Open:   start,
			High:   math.Max(start, in[len(in)-1].Close),
			Low:    math.Min(start, in[len(in)-1].Close),
			Close:  in[len(in)-1].Close,
			Volume: vol,
		})
	}
	return out
}

func transformPointFigure(in []Candle, boxSize float64, reversalBoxes int) []Candle {
	if !isFinitePositive(boxSize) {
		boxSize = autoPriceStep(in, 1.0)
	}
	if reversalBoxes <= 0 {
		reversalBoxes = 3
	}
	revDist := boxSize * float64(reversalBoxes)
	var out []Candle
	colOpen := in[0].Close
	colExtreme := colOpen
	dir := 0
	var vol float64
	for _, c := range in {
		vol += c.Volume
		if dir == 0 {
			if c.High >= colOpen+boxSize {
				dir = 1
				colExtreme = math.Floor(c.High/boxSize) * boxSize
			} else if c.Low <= colOpen-boxSize {
				dir = -1
				colExtreme = math.Ceil(c.Low/boxSize) * boxSize
			}
			continue
		}
		if dir == 1 {
			if c.High >= colExtreme+boxSize {
				colExtreme = math.Floor(c.High/boxSize) * boxSize
			} else if c.Low <= colExtreme-revDist {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   colOpen,
					High:   math.Max(colOpen, colExtreme),
					Low:    math.Min(colOpen, colExtreme),
					Close:  colExtreme,
					Volume: vol,
				})
				colOpen = colExtreme - boxSize
				colExtreme = math.Ceil(c.Low/boxSize) * boxSize
				dir = -1
				vol = 0
			}
		} else {
			if c.Low <= colExtreme-boxSize {
				colExtreme = math.Ceil(c.Low/boxSize) * boxSize
			} else if c.High >= colExtreme+revDist {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   colOpen,
					High:   math.Max(colOpen, colExtreme),
					Low:    math.Min(colOpen, colExtreme),
					Close:  colExtreme,
					Volume: vol,
				})
				colOpen = colExtreme + boxSize
				colExtreme = math.Floor(c.High/boxSize) * boxSize
				dir = 1
				vol = 0
			}
		}
		if len(out) >= maxTransformedBars {
			break
		}
	}
	if len(out) < maxTransformedBars {
		last := in[len(in)-1]
		out = appendMonotonicBar(out, Candle{
			Time:   last.Time,
			Open:   colOpen,
			High:   math.Max(colOpen, last.Close),
			Low:    math.Min(colOpen, last.Close),
			Close:  last.Close,
			Volume: vol,
		})
	}
	return out
}

func transformLineBreak(in []Candle, lines int) []Candle {
	if lines <= 0 {
		lines = 3
	}
	out := make([]Candle, 0, len(in))
	first := in[0]
	out = append(out, Candle{
		Time:   first.Time,
		Open:   first.Open,
		High:   math.Max(first.Open, first.Close),
		Low:    math.Min(first.Open, first.Close),
		Close:  first.Close,
		Volume: first.Volume,
	})
	for i := 1; i < len(in) && len(out) < maxTransformedBars; i++ {
		c := in[i]
		prev := out[len(out)-1]
		startIdx := len(out) - lines
		if startIdx < 0 {
			startIdx = 0
		}
		maxHigh := out[startIdx].High
		minLow := out[startIdx].Low
		for j := startIdx + 1; j < len(out); j++ {
			if out[j].High > maxHigh {
				maxHigh = out[j].High
			}
			if out[j].Low < minLow {
				minLow = out[j].Low
			}
		}
		if prev.Close >= prev.Open {
			if c.Close > prev.High {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   prev.Close,
					High:   c.Close,
					Low:    prev.Close,
					Close:  c.Close,
					Volume: c.Volume,
				})
			} else if c.Close < minLow {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   prev.Open,
					High:   prev.Open,
					Low:    c.Close,
					Close:  c.Close,
					Volume: c.Volume,
				})
			}
		} else {
			if c.Close < prev.Low {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   prev.Close,
					High:   prev.Close,
					Low:    c.Close,
					Close:  c.Close,
					Volume: c.Volume,
				})
			} else if c.Close > maxHigh {
				out = appendMonotonicBar(out, Candle{
					Time:   c.Time,
					Open:   prev.Open,
					High:   c.Close,
					Low:    prev.Open,
					Close:  c.Close,
					Volume: c.Volume,
				})
			}
		}
	}
	return out
}

func groupCandlesByThreshold(in []Candle, threshold float64, useTrades bool) []Candle {
	var out []Candle
	cur := Candle{}
	has := false
	var acc float64
	for _, c := range in {
		if !has {
			cur = c
			has = true
		} else {
			cur.High = math.Max(cur.High, c.High)
			cur.Low = math.Min(cur.Low, c.Low)
			cur.Close = c.Close
			cur.Volume += c.Volume
			cur.BuyVolume += c.BuyVolume
			cur.SellVolume += c.SellVolume
			cur.Trades += c.Trades
			cur.Time = c.Time
		}
		if useTrades {
			t := c.Trades
			if t <= 0 {
				t = 1
			}
			acc += float64(t)
		} else {
			acc += c.Volume
		}
		if acc >= threshold && len(out) < maxTransformedBars {
			out = appendMonotonicBar(out, cur)
			has = false
			acc = 0
		}
	}
	if has && len(out) < maxTransformedBars {
		out = appendMonotonicBar(out, cur)
	}
	return out
}

// AggregateTicksToCandles builds exact tick-count or volume-bucket bars from trade ticks.
func AggregateTicksToCandles(ticks []TradeTick, opts TransformOptions) ([]Candle, error) {
	style, err := NormalizeStyle(opts.Style)
	if err != nil {
		return nil, err
	}
	if style != StyleTick && style != StyleVolume {
		return nil, fmt.Errorf("AggregateTicksToCandles requires %q or %q style, got %q", StyleTick, StyleVolume, style)
	}
	clean := make([]TradeTick, 0, len(ticks))
	for _, t := range ticks {
		if !isFinitePositive(t.Price) || !isFiniteNonNeg(t.Size) {
			return nil, fmt.Errorf("invalid trade tick at %d", t.Time)
		}
		clean = append(clean, t)
	}
	sort.SliceStable(clean, func(i, j int) bool {
		return clean[i].Time < clean[j].Time
	})
	if len(clean) == 0 {
		return []Candle{}, nil
	}

	tickTarget := opts.TickCount
	if tickTarget <= 0 {
		tickTarget = 50
	}
	volTarget := opts.VolumeThreshold
	if !isFinitePositive(volTarget) {
		volTarget = 10
	}

	var out []Candle
	var cur Candle
	var count int
	has := false
	for _, t := range clean {
		sec := t.Time / 1000
		if sec <= 0 {
			sec = 1
		}
		if !has {
			cur = Candle{
				Time:  sec,
				Open:  t.Price,
				High:  t.Price,
				Low:   t.Price,
				Close: t.Price,
			}
			has = true
			count = 0
		}
		cur.Time = sec
		cur.High = math.Max(cur.High, t.Price)
		cur.Low = math.Min(cur.Low, t.Price)
		cur.Close = t.Price
		cur.Volume += t.Size
		if t.Side == "buy" {
			cur.BuyVolume += t.Size
		} else {
			cur.SellVolume += t.Size
		}
		cur.Trades++
		count++

		done := (style == StyleTick && count >= tickTarget) || (style == StyleVolume && cur.Volume >= volTarget)
		if done && len(out) < maxTransformedBars {
			out = appendMonotonicBar(out, cur)
			has = false
		}
	}
	if has && len(out) < maxTransformedBars {
		out = appendMonotonicBar(out, cur)
	}
	return out, nil
}

// ComputeFootprintAndProfile bins trades (or candle volume estimates) into FootprintBar
// levels with Delta, running CVD, VPOC, and 70% Value Area bounds (VAL/VAH).
func ComputeFootprintAndProfile(candles []Candle, ticks []TradeTick, stepSize float64) ([]FootprintBar, error) {
	clean, err := sanitizeCandles(candles)
	if err != nil {
		return nil, err
	}
	if len(clean) == 0 {
		return []FootprintBar{}, nil
	}
	if !isFinitePositive(stepSize) {
		stepSize = autoPriceStep(clean, 4.0)
	}
	if stepSize <= clean[0].Close*1e-5 {
		stepSize = math.Max(clean[0].Close*1e-4, 1e-6)
	}

	sortedTicks := make([]TradeTick, 0, len(ticks))
	for _, t := range ticks {
		if isFinitePositive(t.Price) && isFiniteNonNeg(t.Size) {
			sortedTicks = append(sortedTicks, t)
		}
	}
	sort.SliceStable(sortedTicks, func(i, j int) bool {
		return sortedTicks[i].Time < sortedTicks[j].Time
	})

	var tickIdx int
	var cvd float64
	out := make([]FootprintBar, 0, len(clean))
	for i, c := range clean {
		startMs := c.Time * 1000
		endMs := int64(math.MaxInt64)
		if i+1 < len(clean) {
			endMs = clean[i+1].Time * 1000
		}
		for tickIdx < len(sortedTicks) && sortedTicks[tickIdx].Time < startMs {
			tickIdx++
		}

		buckets := make(map[int64]*FootprintLevel)
		var barMatchedTicks bool
		for tickIdx < len(sortedTicks) && sortedTicks[tickIdx].Time < endMs {
			t := sortedTicks[tickIdx]
			tickIdx++
			barMatchedTicks = true
			addFootprintBucket(buckets, stepSize, t.Price, t.Size, t.Side == "buy")
		}

		if !barMatchedTicks {
			buyVol := c.BuyVolume
			sellVol := c.SellVolume
			if buyVol == 0 && sellVol == 0 && c.Volume > 0 {
				span := math.Max(c.High-c.Low, stepSize)
				buyRatio := 0.5
				if c.High > c.Low {
					buyRatio = 0.25 + 0.5*((c.Close-c.Low)/span)
				}
				buyVol = c.Volume * buyRatio
				sellVol = c.Volume - buyVol
			}
			// Distribute candle volume deterministically across O/H/L/C buckets.
			prices := []float64{c.Low, (c.Low + c.Close) / 2, (c.High + c.Close) / 2, c.High}
			weights := []float64{0.2, 0.3, 0.3, 0.2}
			for j, p := range prices {
				if buyVol > 0 {
					addFootprintBucket(buckets, stepSize, p, buyVol*weights[j], true)
				}
				if sellVol > 0 {
					addFootprintBucket(buckets, stepSize, p, sellVol*weights[j], false)
				}
			}
		}

		levels := make([]FootprintLevel, 0, len(buckets))
		var barDelta, barVol float64
		for _, lv := range buckets {
			lv.Delta = lv.AskVol - lv.BidVol
			lv.TotalVol = lv.AskVol + lv.BidVol
			barDelta += lv.Delta
			barVol += lv.TotalVol
			levels = append(levels, *lv)
		}
		sort.Slice(levels, func(a, b int) bool {
			return levels[a].Price < levels[b].Price
		})

		cvd += barDelta
		vpoc, vah, val := computeValueArea(levels, c.Close)
		out = append(out, FootprintBar{
			Time:   c.Time,
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: barVol,
			Delta:  barDelta,
			CVD:    cvd,
			VPOC:   vpoc,
			VAH:    vah,
			VAL:    val,
			Levels: levels,
		})
	}
	return out, nil
}

func addFootprintBucket(buckets map[int64]*FootprintLevel, step, price, size float64, isBuy bool) {
	if size <= 0 || !isFinitePositive(price) {
		return
	}
	idx := int64(math.Round(price / step))
	lv, ok := buckets[idx]
	if !ok {
		lv = &FootprintLevel{Price: float64(idx) * step}
		buckets[idx] = lv
	}
	if isBuy {
		lv.AskVol += size
	} else {
		lv.BidVol += size
	}
}

func computeValueArea(levels []FootprintLevel, fallbackPrice float64) (vpoc, vah, val float64) {
	if len(levels) == 0 {
		return fallbackPrice, fallbackPrice, fallbackPrice
	}
	pocIdx := 0
	var totalVol float64
	for i, lv := range levels {
		totalVol += lv.TotalVol
		if lv.TotalVol > levels[pocIdx].TotalVol {
			pocIdx = i
		}
	}
	vpoc = levels[pocIdx].Price
	if totalVol <= 0 {
		return vpoc, vpoc, vpoc
	}
	target := totalVol * 0.70
	covered := levels[pocIdx].TotalVol
	lowIdx, highIdx := pocIdx, pocIdx
	for covered < target && (lowIdx > 0 || highIdx+1 < len(levels)) {
		var leftVol, rightVol float64
		if lowIdx > 0 {
			leftVol = levels[lowIdx-1].TotalVol
		}
		if highIdx+1 < len(levels) {
			rightVol = levels[highIdx+1].TotalVol
		}
		if rightVol >= leftVol && highIdx+1 < len(levels) {
			highIdx++
			covered += levels[highIdx].TotalVol
		} else if lowIdx > 0 {
			lowIdx--
			covered += levels[lowIdx].TotalVol
		} else {
			break
		}
	}
	return vpoc, levels[highIdx].Price, levels[lowIdx].Price
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
