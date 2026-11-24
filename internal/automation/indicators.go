package automation

import (
	"math"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

// CompareMetric evaluates comparator against current/previous metric values and threshold.
func CompareMetric(comparator string, current, previous, threshold float64) bool {
	if math.IsNaN(current) || math.IsInf(current, 0) || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return false
	}
	switch comparator {
	case CompAbove:
		return current > threshold
	case CompBelow:
		return current < threshold
	case CompCrossAbove:
		if math.IsNaN(previous) || math.IsInf(previous, 0) {
			return false
		}
		return previous <= threshold && current > threshold
	case CompCrossBelow:
		if math.IsNaN(previous) || math.IsInf(previous, 0) {
			return false
		}
		return previous >= threshold && current < threshold
	case CompPctChangeGTE:
		if math.IsNaN(previous) || math.IsInf(previous, 0) || previous == 0 {
			return false
		}
		pct := math.Abs((current-previous)/previous) * 100.0
		return pct >= threshold
	default:
		return false
	}
}

func finiteCandle(c marketdata.Candle) bool {
	return !math.IsNaN(c.Close) && !math.IsInf(c.Close, 0) &&
		!math.IsNaN(c.High) && !math.IsInf(c.High, 0) &&
		!math.IsNaN(c.Low) && !math.IsInf(c.Low, 0) &&
		!math.IsNaN(c.Open) && !math.IsInf(c.Open, 0) &&
		!math.IsNaN(c.Volume) && !math.IsInf(c.Volume, 0)
}

// ComputeRSI14 computes Wilder's 14-period RSI for the latest and prior candle.
func ComputeRSI14(candles []marketdata.Candle) (current, previous float64, ok bool) {
	const period = 14
	if len(candles) < period+2 {
		return 0, 0, false
	}
	for _, c := range candles {
		if !finiteCandle(c) {
			return 0, 0, false
		}
	}

	var sumGain, sumLoss float64
	for i := 1; i <= period; i++ {
		delta := candles[i].Close - candles[i-1].Close
		if delta >= 0 {
			sumGain += delta
		} else {
			sumLoss -= delta
		}
	}
	avgGain := sumGain / float64(period)
	avgLoss := sumLoss / float64(period)

	rsiVal := func(g, l float64) float64 {
		if l == 0 {
			if g == 0 {
				return 50
			}
			return 100
		}
		rs := g / l
		return 100 - (100 / (1 + rs))
	}

	prevRSI := rsiVal(avgGain, avgLoss)
	currRSI := prevRSI
	for i := period + 1; i < len(candles); i++ {
		prevRSI = currRSI
		delta := candles[i].Close - candles[i-1].Close
		var gain, loss float64
		if delta >= 0 {
			gain = delta
		} else {
			loss = -delta
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
		currRSI = rsiVal(avgGain, avgLoss)
	}
	return currRSI, prevRSI, true
}

// ComputeEMACross computes fast minus slow EMA spread for the latest and prior candle.
func ComputeEMACross(candles []marketdata.Candle, fastPeriod, slowPeriod int) (current, previous float64, ok bool) {
	if fastPeriod < 1 || slowPeriod <= fastPeriod || len(candles) < slowPeriod+1 {
		return 0, 0, false
	}
	for _, c := range candles {
		if !finiteCandle(c) {
			return 0, 0, false
		}
	}
	kFast := 2.0 / float64(fastPeriod+1)
	kSlow := 2.0 / float64(slowPeriod+1)

	emaFast := candles[0].Close
	emaSlow := candles[0].Close
	prevSpread := 0.0
	currSpread := 0.0
	for i := 1; i < len(candles); i++ {
		prevSpread = currSpread
		emaFast = candles[i].Close*kFast + emaFast*(1-kFast)
		emaSlow = candles[i].Close*kSlow + emaSlow*(1-kSlow)
		currSpread = emaFast - emaSlow
	}
	return currSpread, prevSpread, true
}

// ComputeVWAPDelta computes close minus cumulative VWAP across the candle window
// for the latest and prior bars so cross_above/below against 0 indicates VWAP crosses.
func ComputeVWAPDelta(candles []marketdata.Candle) (current, previous float64, ok bool) {
	if len(candles) < 2 {
		return 0, 0, false
	}
	var pvSum, volSum float64
	var prevDelta, currDelta float64
	for i, c := range candles {
		if !finiteCandle(c) || c.Volume < 0 {
			return 0, 0, false
		}
		typ := (c.High + c.Low + c.Close) / 3.0
		pvSum += typ * c.Volume
		volSum += c.Volume
		vwap := typ
		if volSum > 0 {
			vwap = pvSum / volSum
		}
		delta := c.Close - vwap
		if i == len(candles)-2 {
			prevDelta = delta
		} else if i == len(candles)-1 {
			currDelta = delta
		}
	}
	return currDelta, prevDelta, true
}

// ComputeVolumeSpike computes the ratio of the latest bar volume to the prior lookback mean.
func ComputeVolumeSpike(candles []marketdata.Candle, lookback int) (current, previous float64, ok bool) {
	if lookback < 2 || len(candles) < lookback+2 {
		return 0, 0, false
	}
	for _, c := range candles {
		if !finiteCandle(c) || c.Volume < 0 {
			return 0, 0, false
		}
	}
	meanRange := func(endIdx int) float64 {
		start := endIdx - lookback
		if start < 0 {
			start = 0
		}
		var sum float64
		count := 0
		for i := start; i < endIdx; i++ {
			sum += candles[i].Volume
			count++
		}
		if count == 0 {
			return 0
		}
		return sum / float64(count)
	}
	n := len(candles)
	prevMean := meanRange(n - 2)
	currMean := meanRange(n - 1)
	if prevMean <= 0 || currMean <= 0 {
		return 0, 0, false
	}
	return candles[n-1].Volume / currMean, candles[n-2].Volume / prevMean, true
}

// ComputeCVDDelta computes cumulative close-open signed volume over the trailing lookback window.
func ComputeCVDDelta(candles []marketdata.Candle, lookback int) (current, previous float64, ok bool) {
	if lookback < 2 || len(candles) < lookback+1 {
		return 0, 0, false
	}
	for _, c := range candles {
		if !finiteCandle(c) || c.Volume < 0 {
			return 0, 0, false
		}
	}
	barDelta := func(c marketdata.Candle) float64 {
		if c.Close > c.Open {
			return c.Volume
		}
		if c.Close < c.Open {
			return -c.Volume
		}
		return 0
	}
	n := len(candles)
	sumDelta := func(end int) float64 {
		start := end - lookback
		if start < 0 {
			start = 0
		}
		var total float64
		for i := start; i < end; i++ {
			total += barDelta(candles[i])
		}
		return total
	}
	return sumDelta(n), sumDelta(n - 1), true
}
