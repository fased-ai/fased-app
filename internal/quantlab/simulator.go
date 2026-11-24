package quantlab

import (
	"math"

	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
)

type simPosition struct {
	side           string // "long" | "short"
	qty            float64
	entryPrice     float64
	entryTime      int64
	entryFee       float64
	stopPrice      float64
	takePrice      float64
	trailingStop   float64
	trailingActive bool
}

// Simulate runs a strictly causal next-bar-open simulation over candles.
// Signals triggered at bar i enter or exit at bar i+1 Open with adverse slippage + fees.
// Intrabar stops evaluate StopLoss before TakeProfit conservatively.
func Simulate(candles []marketdata.Candle, strat fasedscript.StrategySpec, cfg BacktestConfig) (BacktestMetrics, []TradeFill, []EquityPoint) {
	n := len(candles)
	if n < 2 {
		return BacktestMetrics{FinalEquity: cfg.InitialCapital}, nil, nil
	}

	slipRate := cfg.SlippageBps / 10000.0
	feeRate := cfg.FeeBps / 10000.0
	sizeFrac := strat.SizePct / 100.0
	if sizeFrac <= 0 || sizeFrac > 1 {
		sizeFrac = 1.0
	}

	var atrSeries []float64
	if strat.TrailingAtrMult > 0 {
		atrSeries = fasedscript.ATR(candles, 14)
	}

	equity := cfg.InitialCapital
	peakEquity := equity
	maxDD := 0.0
	totalFees := 0.0

	var pos *simPosition
	var trades []TradeFill
	equityCurve := make([]EquityPoint, 0, n)
	barReturns := make([]float64, 0, n)
	prevEq := equity

	closePos := func(exitPx float64, exitTime int64, reason string) {
		if pos == nil || exitPx <= 0 {
			return
		}
		adjExit := exitPx
		if pos.side == "long" {
			adjExit = exitPx * (1.0 - slipRate)
		} else {
			adjExit = exitPx * (1.0 + slipRate)
		}
		exitNotional := pos.qty * adjExit
		exitFee := exitNotional * feeRate
		var grossPnL float64
		if pos.side == "long" {
			grossPnL = (adjExit - pos.entryPrice) * pos.qty
		} else {
			grossPnL = (pos.entryPrice - adjExit) * pos.qty
		}
		netPnL := grossPnL - pos.entryFee - exitFee
		equity += grossPnL - exitFee
		if equity < 0 {
			equity = 0
		}
		totalFees += exitFee

		entryNotional := pos.qty * pos.entryPrice
		pnlPct := 0.0
		if entryNotional > 0 {
			pnlPct = (netPnL / entryNotional) * 100.0
		}
		trades = append(trades, TradeFill{
			Side:       pos.side,
			EntryTime:  pos.entryTime,
			ExitTime:   exitTime,
			EntryPrice: roundPrice(pos.entryPrice),
			ExitPrice:  roundPrice(adjExit),
			PnLPct:     round4(pnlPct),
			PnLUSD:     round4(netPnL),
			FeeUSD:     round4(pos.entryFee + exitFee),
			ExitReason: reason,
		})
		pos = nil
	}

	openPos := func(side string, openPx float64, entryTime int64, causalATR float64) {
		if openPx <= 0 || equity <= 1.0 {
			return
		}
		adjEntry := openPx * (1.0 + slipRate)
		if side == "short" {
			adjEntry = openPx * (1.0 - slipRate)
		}
		if adjEntry <= 0 {
			return
		}
		notional := equity * sizeFrac
		entryFee := notional * feeRate
		if equity <= entryFee {
			return
		}
		equity -= entryFee
		totalFees += entryFee
		qty := notional / adjEntry
		p := &simPosition{
			side:       side,
			qty:        qty,
			entryPrice: adjEntry,
			entryTime:  entryTime,
			entryFee:   entryFee,
		}
		if strat.StopLossPct > 0 {
			if side == "long" {
				p.stopPrice = adjEntry * (1.0 - strat.StopLossPct/100.0)
			} else {
				p.stopPrice = adjEntry * (1.0 + strat.StopLossPct/100.0)
			}
		}
		if strat.TakeProfitPct > 0 {
			if side == "long" {
				p.takePrice = adjEntry * (1.0 + strat.TakeProfitPct/100.0)
			} else {
				p.takePrice = adjEntry * (1.0 - strat.TakeProfitPct/100.0)
			}
		}
		if strat.TrailingAtrMult > 0 && !math.IsNaN(causalATR) && causalATR > 0 {
			p.trailingActive = true
			if side == "long" {
				p.trailingStop = adjEntry - strat.TrailingAtrMult*causalATR
			} else {
				p.trailingStop = adjEntry + strat.TrailingAtrMult*causalATR
			}
		}
		pos = p
	}

	flagAt := func(arr []bool, idx int) bool {
		return idx >= 0 && idx < len(arr) && arr[idx]
	}

	for i := 0; i < n; i++ {
		c := candles[i]

		// 1. Apply signal scheduled at bar i-1 on bar i Open (strict next-bar open).
		if i > 0 {
			sigIdx := i - 1
			causalATR := math.NaN()
			if len(atrSeries) == n && !math.IsNaN(atrSeries[sigIdx]) {
				causalATR = atrSeries[sigIdx]
			}

			wantLong := flagAt(strat.Long, sigIdx)
			wantExitLong := flagAt(strat.ExitLong, sigIdx)
			wantShort := flagAt(strat.Short, sigIdx)
			wantExitShort := flagAt(strat.ExitShort, sigIdx)

			if pos != nil && pos.side == "long" && (wantExitLong || wantShort) {
				closePos(c.Open, c.Time, "signal")
			} else if pos != nil && pos.side == "short" && (wantExitShort || wantLong) {
				closePos(c.Open, c.Time, "signal")
			}

			if pos == nil && c.Open > 0 {
				if wantLong && !wantShort {
					openPos("long", c.Open, c.Time, causalATR)
				} else if wantShort && !wantLong {
					openPos("short", c.Open, c.Time, causalATR)
				}
			}
		}

		// 2. Conservative intrabar risk check: SL / Trailing Stop BEFORE Take Profit.
		if pos != nil {
			if pos.side == "long" {
				effectiveStop := pos.stopPrice
				if pos.trailingActive && pos.trailingStop > effectiveStop {
					effectiveStop = pos.trailingStop
				}
				if effectiveStop > 0 && c.Low <= effectiveStop {
					reason := "stop_loss"
					if pos.trailingActive && effectiveStop == pos.trailingStop && pos.trailingStop > pos.stopPrice {
						reason = "trailing_stop"
					}
					closePos(math.Min(c.Open, effectiveStop), c.Time, reason)
				} else if pos.takePrice > 0 && c.High >= pos.takePrice {
					closePos(math.Max(c.Open, pos.takePrice), c.Time, "take_profit")
				} else if pos.trailingActive && len(atrSeries) == n && !math.IsNaN(atrSeries[i]) && atrSeries[i] > 0 {
					candStop := c.Close - strat.TrailingAtrMult*atrSeries[i]
					if candStop > pos.trailingStop {
						pos.trailingStop = candStop
					}
				}
			} else {
				effectiveStop := pos.stopPrice
				if pos.trailingActive && (effectiveStop == 0 || pos.trailingStop < effectiveStop) {
					effectiveStop = pos.trailingStop
				}
				if effectiveStop > 0 && c.High >= effectiveStop {
					reason := "stop_loss"
					if pos.trailingActive && (pos.stopPrice == 0 || effectiveStop == pos.trailingStop && pos.trailingStop < pos.stopPrice) {
						reason = "trailing_stop"
					}
					closePos(math.Max(c.Open, effectiveStop), c.Time, reason)
				} else if pos.takePrice > 0 && c.Low <= pos.takePrice {
					closePos(math.Min(c.Open, pos.takePrice), c.Time, "take_profit")
				} else if pos.trailingActive && len(atrSeries) == n && !math.IsNaN(atrSeries[i]) && atrSeries[i] > 0 {
					candStop := c.Close + strat.TrailingAtrMult*atrSeries[i]
					if pos.trailingStop == 0 || candStop < pos.trailingStop {
						pos.trailingStop = candStop
					}
				}
			}
		}

		// Close any open position on final bar close so total equity is realized cleanly.
		if i == n-1 && pos != nil {
			closePos(c.Close, c.Time, "eod")
		}

		// Mark-to-market equity.
		markEq := equity
		if pos != nil {
			if pos.side == "long" {
				markEq += (c.Close - pos.entryPrice) * pos.qty
			} else {
				markEq += (pos.entryPrice - c.Close) * pos.qty
			}
			if markEq < 0 {
				markEq = 0
			}
		}
		if markEq > peakEquity {
			peakEquity = markEq
		}
		dd := 0.0
		if peakEquity > 0 {
			dd = ((peakEquity - markEq) / peakEquity) * 100.0
		}
		if dd > maxDD {
			maxDD = dd
		}
		if i > 0 && prevEq > 0 {
			barReturns = append(barReturns, (markEq-prevEq)/prevEq)
		}
		prevEq = markEq
		equityCurve = append(equityCurve, EquityPoint{
			Time:        c.Time,
			Equity:      round4(markEq),
			DrawdownPct: round4(dd),
		})
	}

	metrics := computeMetrics(cfg, equity, totalFees, maxDD, trades, barReturns)
	if len(trades) > maxSerializedPoints {
		trades = trades[len(trades)-maxSerializedPoints:]
	}
	equityCurve = downsampleEquity(equityCurve, maxSerializedPoints)
	return metrics, trades, equityCurve
}

func computeMetrics(cfg BacktestConfig, finalEq, totalFees, maxDD float64, trades []TradeFill, barReturns []float64) BacktestMetrics {
	totalRet := 0.0
	if cfg.InitialCapital > 0 {
		totalRet = ((finalEq - cfg.InitialCapital) / cfg.InitialCapital) * 100.0
	}

	var grossProfit, grossLoss, sumTradePct, sumNetUSD float64
	wins := 0
	for _, tr := range trades {
		sumTradePct += tr.PnLPct
		sumNetUSD += tr.PnLUSD
		if tr.PnLUSD > 0 {
			wins++
			grossProfit += tr.PnLUSD
		} else {
			grossLoss += -tr.PnLUSD
		}
	}

	winRate := 0.0
	avgTrade := 0.0
	expectancy := 0.0
	if len(trades) > 0 {
		winRate = (float64(wins) / float64(len(trades))) * 100.0
		avgTrade = sumTradePct / float64(len(trades))
		expectancy = sumNetUSD / float64(len(trades))
	}

	pf := 0.0
	if grossLoss > 0 {
		pf = grossProfit / grossLoss
	} else if grossProfit > 0 {
		pf = 99.0
	}

	sharpe := computeSharpe(barReturns, cfg.Interval)
	return BacktestMetrics{
		TotalReturnPct:   round4(totalRet),
		AnnualizedSharpe: round4(sharpe),
		ProfitFactor:     round4(pf),
		WinRatePct:       round4(winRate),
		MaxDrawdownPct:   round4(maxDD),
		TradeCount:       len(trades),
		AvgTradePct:      round4(avgTrade),
		TotalFeesUSD:     round4(totalFees),
		FinalEquity:      round4(finalEq),
		ExpectancyUSD:    round4(expectancy),
	}
}

func computeSharpe(rets []float64, interval string) float64 {
	if len(rets) < 4 {
		return 0
	}
	var sum float64
	for _, r := range rets {
		sum += r
	}
	mean := sum / float64(len(rets))
	var sq float64
	for _, r := range rets {
		d := r - mean
		sq += d * d
	}
	std := math.Sqrt(sq / float64(len(rets)-1))
	if std <= 1e-12 {
		return 0
	}
	barsPerYear := 8760.0
	switch interval {
	case "1m":
		barsPerYear = 525600.0
	case "5m":
		barsPerYear = 105120.0
	case "15m":
		barsPerYear = 35040.0
	case "1h":
		barsPerYear = 8760.0
	case "4h":
		barsPerYear = 2190.0
	case "1d":
		barsPerYear = 365.0
	}
	return (mean / std) * math.Sqrt(barsPerYear)
}

func downsampleEquity(curve []EquityPoint, maxPts int) []EquityPoint {
	if len(curve) <= maxPts || maxPts < 2 {
		return curve
	}
	out := make([]EquityPoint, 0, maxPts)
	step := float64(len(curve)-1) / float64(maxPts-1)
	for i := 0; i < maxPts; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx >= len(curve) {
			idx = len(curve) - 1
		}
		out = append(out, curve[idx])
	}
	return out
}

func round4(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10000) / 10000
}

func round6(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1000000) / 1000000
}

func roundPrice(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v == 0 {
		return 0
	}
	av := math.Abs(v)
	if av >= 1 {
		return round6(v)
	}
	exp := int(math.Floor(math.Log10(av)))
	scale := math.Pow10(5 - exp)
	return math.Round(v*scale) / scale
}
