package fasedscript

import (
	"errors"
	"math"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func nanSlice(n int) []float64 {
	out := make([]float64, n)
	nan := math.NaN()
	for i := range out {
		out[i] = nan
	}
	return out
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// SMA computes strictly causal Simple Moving Average (first period-1 valid bars are NaN).
func SMA(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	var sum float64
	nanCount := 0
	for i := 0; i < n; i++ {
		if isFinite(src[i]) {
			sum += src[i]
		} else {
			nanCount++
		}
		if i >= period {
			old := src[i-period]
			if isFinite(old) {
				sum -= old
			} else {
				nanCount--
			}
		}
		if i >= period-1 && nanCount == 0 {
			out[i] = sum / float64(period)
		}
	}
	return out
}

// EMA computes strictly causal Exponential Moving Average seeded by first valid SMA.
func EMA(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	k := 2.0 / float64(period+1)
	var sum float64
	validRun := 0
	seeded := false
	ema := math.NaN()

	for i := 0; i < n; i++ {
		v := src[i]
		if !isFinite(v) {
			sum = 0
			validRun = 0
			seeded = false
			continue
		}
		if !seeded {
			sum += v
			validRun++
			if validRun == period {
				ema = sum / float64(period)
				out[i] = ema
				seeded = true
			}
			continue
		}
		ema = v*k + ema*(1-k)
		out[i] = ema
	}
	return out
}

// RMA computes Wilder's Running Moving Average (alpha = 1 / period).
func RMA(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	alpha := 1.0 / float64(period)
	var sum float64
	validRun := 0
	seeded := false
	rma := math.NaN()
	for i := 0; i < n; i++ {
		v := src[i]
		if !isFinite(v) {
			sum = 0
			validRun = 0
			seeded = false
			continue
		}
		if !seeded {
			sum += v
			validRun++
			if validRun == period {
				rma = sum / float64(period)
				out[i] = rma
				seeded = true
			}
			continue
		}
		rma = alpha*v + (1-alpha)*rma
		out[i] = rma
	}
	return out
}

// WMA computes Weighted Moving Average linearly weighted from 1..period.
func WMA(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	denom := float64(period*(period+1)) / 2.0
	for i := period - 1; i < n; i++ {
		var num float64
		valid := true
		for j := 0; j < period; j++ {
			v := src[i-period+1+j]
			if !isFinite(v) {
				valid = false
				break
			}
			num += v * float64(j+1)
		}
		if valid {
			out[i] = num / denom
		}
	}
	return out
}

// RSI computes strictly causal Wilder 0..100 Relative Strength Index.
func RSI(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n <= period {
		return out
	}
	gains := make([]float64, n)
	losses := make([]float64, n)
	gains[0] = math.NaN()
	losses[0] = math.NaN()
	for i := 1; i < n; i++ {
		if !isFinite(src[i]) || !isFinite(src[i-1]) {
			gains[i] = math.NaN()
			losses[i] = math.NaN()
			continue
		}
		d := src[i] - src[i-1]
		if d >= 0 {
			gains[i] = d
		} else {
			losses[i] = -d
		}
	}
	avgG := RMA(gains[1:], period)
	avgL := RMA(losses[1:], period)
	for i := 1; i < n; i++ {
		g := avgG[i-1]
		l := avgL[i-1]
		if !isFinite(g) || !isFinite(l) {
			continue
		}
		if l == 0 {
			if g == 0 {
				out[i] = 50
			} else {
				out[i] = 100
			}
			continue
		}
		rs := g / l
		out[i] = 100 - (100 / (1 + rs))
	}
	return out
}

// TrueRange computes per-bar causal true range across OHLC candles.
func TrueRange(candles []marketdata.Candle) []float64 {
	n := len(candles)
	out := nanSlice(n)
	if n == 0 {
		return out
	}
	out[0] = candles[0].High - candles[0].Low
	for i := 1; i < n; i++ {
		hl := candles[i].High - candles[i].Low
		hc := math.Abs(candles[i].High - candles[i-1].Close)
		lc := math.Abs(candles[i].Low - candles[i-1].Close)
		out[i] = math.Max(hl, math.Max(hc, lc))
	}
	return out
}

// ATR computes strictly causal Wilder Average True Range.
func ATR(candles []marketdata.Candle, period int) []float64 {
	return RMA(TrueRange(candles), period)
}

// VWAP computes session-anchored typical-price volume-weighted average price over candles,
// resetting cumulative sums on UTC day boundaries when timestamps are available.
func VWAP(candles []marketdata.Candle) []float64 {
	n := len(candles)
	out := nanSlice(n)
	var pvSum, volSum float64
	normDay := func(ts int64) int64 {
		if ts > 1_000_000_000_000 {
			ts /= 1000
		}
		return ts / 86400
	}
	var prevDay int64
	for i, c := range candles {
		if c.Time > 0 {
			day := normDay(c.Time)
			if i > 0 && candles[i-1].Time > 0 && day != prevDay {
				pvSum = 0
				volSum = 0
			}
			prevDay = day
		}
		typ := (c.High + c.Low + c.Close) / 3.0
		v := math.Max(0, c.Volume)
		pvSum += typ * v
		volSum += v
		if volSum > 0 {
			out[i] = pvSum / volSum
		} else {
			out[i] = typ
		}
	}
	return out
}

// StdDev computes strictly causal rolling population standard deviation.
func StdDev(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	basis := SMA(src, period)
	for i := period - 1; i < n; i++ {
		mean := basis[i]
		if !isFinite(mean) {
			continue
		}
		var sumSq float64
		valid := true
		for j := i - period + 1; j <= i; j++ {
			if !isFinite(src[j]) {
				valid = false
				break
			}
			diff := src[j] - mean
			sumSq += diff * diff
		}
		if valid {
			out[i] = math.Sqrt(sumSq / float64(period))
		}
	}
	return out
}

// BBUpper computes Bollinger upper band (SMA(period) + mult*StdDev(period)).
func BBUpper(src []float64, period int, mult float64) []float64 {
	basis := SMA(src, period)
	sd := StdDev(src, period)
	out := nanSlice(len(src))
	for i := range out {
		if isFinite(basis[i]) && isFinite(sd[i]) {
			out[i] = basis[i] + mult*sd[i]
		}
	}
	return out
}

// BBLower computes Bollinger lower band (SMA(period) - mult*StdDev(period)).
func BBLower(src []float64, period int, mult float64) []float64 {
	basis := SMA(src, period)
	sd := StdDev(src, period)
	out := nanSlice(len(src))
	for i := range out {
		if isFinite(basis[i]) && isFinite(sd[i]) {
			out[i] = basis[i] - mult*sd[i]
		}
	}
	return out
}

// MACD computes strictly causal MACD line, signal line, and histogram.
func MACD(src []float64, fastPeriod, slowPeriod, signalPeriod int) (line, sig, hist []float64) {
	n := len(src)
	fast := EMA(src, fastPeriod)
	slow := EMA(src, slowPeriod)
	line = nanSlice(n)
	for i := 0; i < n; i++ {
		if isFinite(fast[i]) && isFinite(slow[i]) {
			line[i] = fast[i] - slow[i]
		}
	}
	sig = EMA(line, signalPeriod)
	hist = nanSlice(n)
	for i := 0; i < n; i++ {
		if isFinite(line[i]) && isFinite(sig[i]) {
			hist[i] = line[i] - sig[i]
		}
	}
	return line, sig, hist
}

// Highest computes rolling causal maximum over the last period bars.
func Highest(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	for i := period - 1; i < n; i++ {
		maxVal := math.Inf(-1)
		valid := true
		for j := i - period + 1; j <= i; j++ {
			if !isFinite(src[j]) {
				valid = false
				break
			}
			if src[j] > maxVal {
				maxVal = src[j]
			}
		}
		if valid {
			out[i] = maxVal
		}
	}
	return out
}

// Lowest computes rolling causal minimum over the last period bars.
func Lowest(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n < period {
		return out
	}
	for i := period - 1; i < n; i++ {
		minVal := math.Inf(1)
		valid := true
		for j := i - period + 1; j <= i; j++ {
			if !isFinite(src[j]) {
				valid = false
				break
			}
			if src[j] < minVal {
				minVal = src[j]
			}
		}
		if valid {
			out[i] = minVal
		}
	}
	return out
}

// ROC computes causal percentage Rate of Change over period bars.
func ROC(src []float64, period int) []float64 {
	n := len(src)
	out := nanSlice(n)
	if period < 1 || n <= period {
		return out
	}
	for i := period; i < n; i++ {
		prev := src[i-period]
		if isFinite(src[i]) && isFinite(prev) && prev != 0 {
			out[i] = ((src[i] - prev) / prev) * 100.0
		}
	}
	return out
}

// Shift shifts src forward by lag >= 0 historical bars (e.g., lag=1 reads i-1).
// Negative lag is explicitly rejected to eliminate look-ahead bias.
func Shift(src []float64, lag int) ([]float64, error) {
	if lag < 0 {
		return nil, errors.New("negative shift lag rejected (look-ahead bias forbidden)")
	}
	n := len(src)
	out := nanSlice(n)
	if lag >= n {
		return out, nil
	}
	copy(out[lag:], src[:n-lag])
	return out, nil
}

// ZScore computes rolling causal z-score `(src - SMA) / StdDev`.
func ZScore(src []float64, period int) []float64 {
	mean := SMA(src, period)
	sd := StdDev(src, period)
	out := nanSlice(len(src))
	for i := range out {
		if isFinite(src[i]) && isFinite(mean[i]) && isFinite(sd[i]) && sd[i] > 0 {
			out[i] = (src[i] - mean[i]) / sd[i]
		}
	}
	return out
}

// Crossover returns true at bar i iff a[i-1] <= b[i-1] && a[i] > b[i].
func Crossover(a, b []float64) []bool {
	n := minInt(len(a), len(b))
	out := make([]bool, n)
	for i := 1; i < n; i++ {
		if isFinite(a[i-1]) && isFinite(b[i-1]) && isFinite(a[i]) && isFinite(b[i]) {
			out[i] = a[i-1] <= b[i-1] && a[i] > b[i]
		}
	}
	return out
}

// Crossunder returns true at bar i iff a[i-1] >= b[i-1] && a[i] < b[i].
func Crossunder(a, b []float64) []bool {
	n := minInt(len(a), len(b))
	out := make([]bool, n)
	for i := 1; i < n; i++ {
		if isFinite(a[i-1]) && isFinite(b[i-1]) && isFinite(a[i]) && isFinite(b[i]) {
			out[i] = a[i-1] >= b[i-1] && a[i] < b[i]
		}
	}
	return out
}

// Rising returns true when src strictly rises over lookback consecutive bars.
func Rising(src []float64, lookback int) []bool {
	n := len(src)
	out := make([]bool, n)
	if lookback < 1 {
		lookback = 1
	}
	for i := lookback; i < n; i++ {
		ok := true
		for j := i - lookback + 1; j <= i; j++ {
			if !isFinite(src[j]) || !isFinite(src[j-1]) || src[j] <= src[j-1] {
				ok = false
				break
			}
		}
		out[i] = ok
	}
	return out
}

// Falling returns true when src strictly falls over lookback consecutive bars.
func Falling(src []float64, lookback int) []bool {
	n := len(src)
	out := make([]bool, n)
	if lookback < 1 {
		lookback = 1
	}
	for i := lookback; i < n; i++ {
		ok := true
		for j := i - lookback + 1; j <= i; j++ {
			if !isFinite(src[j]) || !isFinite(src[j-1]) || src[j] >= src[j-1] {
				ok = false
				break
			}
		}
		out[i] = ok
	}
	return out
}

// NZ replaces non-finite values with fallback.
func NZ(src []float64, fallback float64) []float64 {
	out := make([]float64, len(src))
	for i, v := range src {
		if isFinite(v) {
			out[i] = v
		} else {
			out[i] = fallback
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
