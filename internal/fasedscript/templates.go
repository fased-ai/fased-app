package fasedscript

// Templates returns the curated built-in FasedScript starter scripts.
func Templates() []Template {
	return []Template{
		{
			ID:          "ema-rsi-trend",
			Name:        "EMA + RSI Momentum",
			Category:    "trend",
			Description: "Fast/slow EMA crossover filtered by RSI regime with ATR trailing risk.",
			Source: `const fast = input("fast", 9, 5, 17, 4);
const slow = input("slow", 21, 18, 34, 4);
const rsiMax = input("rsiMax", 72, 65, 80, 5);

const emaFast = ema(close, fast);
const emaSlow = ema(close, slow);
const r = rsi(close, 14);

plot("EMA Fast", emaFast, "accent", "overlay");
plot("EMA Slow", emaSlow, "muted", "overlay");
plot("RSI 14", r, "warn", "oscillator");

strategy({
  long: crossover(emaFast, emaSlow) && r < rsiMax,
  exitLong: crossunder(emaFast, emaSlow),
  short: crossunder(emaFast, emaSlow) && r > 32,
  exitShort: crossover(emaFast, emaSlow),
  stopLossPct: 2.5,
  takeProfitPct: 6.0,
  trailingAtrMult: 2.0,
  sizePct: 25
});`,
		},
		{
			ID:          "vwap-mean-reversion",
			Name:        "VWAP + Z-Score Mean Reversion",
			Category:    "mean_reversion",
			Description: "Fades short-term z-score deviations back toward session VWAP.",
			Source: `const period = input("period", 20, 14, 30, 4);
const zEntry = input("zEntry", 1.8, 1.2, 2.4, 0.4);

const sessionVwap = vwap();
const z = zscore(close, period);

plot("VWAP", sessionVwap, "accent", "overlay");
plot("Z-Score", z, "warn", "oscillator");

strategy({
  long: z < -zEntry && rising(close, 1),
  exitLong: close >= sessionVwap,
  short: z > zEntry && falling(close, 1),
  exitShort: close <= sessionVwap,
  stopLossPct: 2.0,
  takeProfitPct: 4.0,
  sizePct: 20
});`,
		},
		{
			ID:          "donchian-breakout",
			Name:        "Donchian Channel Breakout",
			Category:    "breakout",
			Description: "Enters on prior-bar high/low channel breakouts confirmed by volume.",
			Source: `const lookback = input("lookback", 14, 8, 26, 6);
const volMult = input("volMult", 1.1, 0.9, 1.5, 0.2);

const upperBand = shift(highest(high, lookback), 1);
const lowerBand = shift(lowest(low, lookback), 1);
const vRatio = volumeRatio(20);

plot("Upper Channel", upperBand, "ok", "overlay");
plot("Lower Channel", lowerBand, "danger", "overlay");

strategy({
  long: close > upperBand && vRatio >= volMult,
  exitLong: close < sma(close, lookback),
  short: close < lowerBand && vRatio >= volMult,
  exitShort: close > sma(close, lookback),
  trailingAtrMult: 2.5,
  sizePct: 25
});`,
		},
		{
			ID:          "bollinger-squeeze",
			Name:        "Bollinger + MACD Regime",
			Category:    "volatility",
			Description: "Combines Bollinger mean re-entry with positive MACD histogram.",
			Source: `const bbLen = input("bbLen", 20, 14, 28, 4);
const bbMult = input("bbMult", 2.0, 1.5, 2.5, 0.5);

const upper = bbUpper(close, bbLen, bbMult);
const basis = bbBasis(close, bbLen);
const lower = bbLower(close, bbLen, bbMult);
const hist = macdHist(close, 12, 26, 9);

plot("BB Upper", upper, "warn", "overlay");
plot("BB Basis", basis, "muted", "overlay");
plot("BB Lower", lower, "ok", "overlay");

strategy({
  long: crossover(close, lower) && hist > shift(hist, 1),
  exitLong: crossunder(close, upper) || crossunder(hist, 0),
  stopLossPct: 2.2,
  takeProfitPct: 5.0,
  sizePct: 20
});`,
		},
	}
}
