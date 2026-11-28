// TradingView-style Technicals calculator ported cleanly from WEN retail-technicals.mjs.
// Zero external API calls; maps signals onto the 5 WEN Rose-to-Emerald buckets (A..E).

export const TECHNICAL_BAND_COLORS = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

export type SignalAction = "Sell" | "Neutral" | "Buy";
export type SummaryLabel =
  "Strong sell" | "Sell" | "Neutral" | "Buy" | "Strong buy";

export interface OhlcBar {
  open: number;
  high: number;
  low: number;
  close: number;
}

export interface SignalRow {
  name: string;
  value: number | null;
  action: SignalAction;
}

export interface TechnicalGaugeSummary {
  sell: number;
  neutral: number;
  buy: number;
  total: number;
  score: number;
  label: SummaryLabel;
  bandIndex: number;
  color: string;
  suggestedWeights: number[];
}

export interface TechnicalsReport {
  ready: boolean;
  barCount: number;
  oscillators: TechnicalGaugeSummary;
  movingAverages: TechnicalGaugeSummary;
  summary: TechnicalGaugeSummary;
  oscillatorRows: SignalRow[];
  maRows: SignalRow[];
}

function sma(values: readonly number[], period: number): number | null {
  if (values.length < period || period <= 0) return null;
  let sum = 0;
  for (let i = values.length - period; i < values.length; i++) sum += values[i];
  return sum / period;
}

function emaSeries(values: readonly number[], period: number): number[] {
  if (values.length < period || period <= 0) return [];
  const k = 2 / (period + 1);
  let prev = 0;
  for (let i = 0; i < period; i++) prev += values[i];
  prev /= period;
  const out = [prev];
  for (let i = period; i < values.length; i++) {
    prev = values[i] * k + prev * (1 - k);
    out.push(prev);
  }
  return out;
}

function ema(values: readonly number[], period: number): number | null {
  const s = emaSeries(values, period);
  return s.length ? s[s.length - 1] : null;
}

function rsiSeries(closes: readonly number[], period = 14): number[] {
  if (closes.length < period + 1) return [];
  let gains = 0;
  let losses = 0;
  for (let i = 1; i <= period; i++) {
    const d = closes[i] - closes[i - 1];
    if (d >= 0) gains += d;
    else losses -= d;
  }
  let avgGain = gains / period;
  let avgLoss = losses / period;
  const out = [
    avgLoss === 0
      ? avgGain === 0
        ? 50
        : 100
      : 100 - 100 / (1 + avgGain / avgLoss),
  ];
  for (let i = period + 1; i < closes.length; i++) {
    const d = closes[i] - closes[i - 1];
    const g = d > 0 ? d : 0;
    const l = d < 0 ? -d : 0;
    avgGain = (avgGain * (period - 1) + g) / period;
    avgLoss = (avgLoss * (period - 1) + l) / period;
    out.push(
      avgLoss === 0
        ? avgGain === 0
          ? 50
          : 100
        : 100 - 100 / (1 + avgGain / avgLoss),
    );
  }
  return out;
}

function stochasticK(
  bars: readonly OhlcBar[],
  period = 14,
  smooth = 3,
): number | null {
  if (bars.length < period + smooth - 1) return null;
  const rawK: number[] = [];
  for (let i = period - 1; i < bars.length; i++) {
    let hi = -Infinity;
    let lo = Infinity;
    for (let j = i - period + 1; j <= i; j++) {
      if (bars[j].high > hi) hi = bars[j].high;
      if (bars[j].low < lo) lo = bars[j].low;
    }
    const span = hi - lo;
    rawK.push(span > 0 ? ((bars[i].close - lo) / span) * 100 : 50);
  }
  return sma(rawK, smooth);
}

function cci(bars: readonly OhlcBar[], period = 20): number | null {
  if (bars.length < period) return null;
  const tps: number[] = [];
  for (let i = bars.length - period; i < bars.length; i++) {
    tps.push((bars[i].high + bars[i].low + bars[i].close) / 3);
  }
  const mean = tps.reduce((a, b) => a + b, 0) / period;
  const mad = tps.reduce((a, b) => a + Math.abs(b - mean), 0) / period;
  if (mad === 0) return 0;
  return (tps[tps.length - 1] - mean) / (0.015 * mad);
}

function adx(
  bars: readonly OhlcBar[],
  period = 14,
): { value: number; plusDi: number; minusDi: number } | null {
  if (bars.length < period * 2 + 1) return null;
  const trs: number[] = [];
  const plusDMs: number[] = [];
  const minusDMs: number[] = [];
  for (let i = 1; i < bars.length; i++) {
    const cur = bars[i];
    const prev = bars[i - 1];
    const up = cur.high - prev.high;
    const dn = prev.low - cur.low;
    plusDMs.push(up > dn && up > 0 ? up : 0);
    minusDMs.push(dn > up && dn > 0 ? dn : 0);
    trs.push(
      Math.max(
        cur.high - cur.low,
        Math.abs(cur.high - prev.close),
        Math.abs(cur.low - prev.close),
      ),
    );
  }
  let smTr = 0;
  let smPlus = 0;
  let smMinus = 0;
  for (let i = 0; i < period; i++) {
    smTr += trs[i];
    smPlus += plusDMs[i];
    smMinus += minusDMs[i];
  }
  const dxs: number[] = [];
  let lastPlusDi = 0;
  let lastMinusDi = 0;
  for (let i = period; i < trs.length; i++) {
    smTr = smTr - smTr / period + trs[i];
    smPlus = smPlus - smPlus / period + plusDMs[i];
    smMinus = smMinus - smMinus / period + minusDMs[i];
    const pDi = smTr > 0 ? (100 * smPlus) / smTr : 0;
    const mDi = smTr > 0 ? (100 * smMinus) / smTr : 0;
    lastPlusDi = pDi;
    lastMinusDi = mDi;
    const denom = pDi + mDi;
    dxs.push(denom > 0 ? (100 * Math.abs(pDi - mDi)) / denom : 0);
  }
  const val = sma(dxs, period);
  return val === null
    ? null
    : { value: val, plusDi: lastPlusDi, minusDi: lastMinusDi };
}

function awesomeOscillator(
  bars: readonly OhlcBar[],
): { value: number; prev: number } | null {
  if (bars.length < 35) return null;
  const mids = bars.map((b) => (b.high + b.low) / 2);
  const aoNow = (sma(mids, 5) ?? 0) - (sma(mids, 34) ?? 0);
  const aoPrev =
    (sma(mids.slice(0, -1), 5) ?? 0) - (sma(mids.slice(0, -1), 34) ?? 0);
  return { value: aoNow, prev: aoPrev };
}

function macd(
  closes: readonly number[],
): { value: number; signal: number } | null {
  if (closes.length < 35) return null;
  const fast = emaSeries(closes, 12);
  const slow = emaSeries(closes, 26);
  const offset = fast.length - slow.length;
  const line = slow.map((s, i) => fast[i + offset] - s);
  const sig = ema(line, 9);
  if (sig === null || !line.length) return null;
  return { value: line[line.length - 1], signal: sig };
}

function stochRsiFast(closes: readonly number[]): number | null {
  const rsis = rsiSeries(closes, 14);
  if (rsis.length < 16) return null;
  const raw: number[] = [];
  for (let i = 13; i < rsis.length; i++) {
    let hi = -Infinity;
    let lo = Infinity;
    for (let j = i - 13; j <= i; j++) {
      if (rsis[j] > hi) hi = rsis[j];
      if (rsis[j] < lo) lo = rsis[j];
    }
    const span = hi - lo;
    raw.push(span > 0 ? ((rsis[i] - lo) / span) * 100 : 50);
  }
  return sma(raw, 3);
}

function williamsR(bars: readonly OhlcBar[], period = 14): number | null {
  if (bars.length < period) return null;
  let hi = -Infinity;
  let lo = Infinity;
  for (let i = bars.length - period; i < bars.length; i++) {
    if (bars[i].high > hi) hi = bars[i].high;
    if (bars[i].low < lo) lo = bars[i].low;
  }
  const span = hi - lo;
  return span > 0 ? ((hi - bars[bars.length - 1].close) / span) * -100 : -50;
}

function bullBearPower(bars: readonly OhlcBar[], period = 13): number | null {
  if (bars.length < period) return null;
  const closes = bars.map((b) => b.close);
  const e = ema(closes, period);
  if (e === null) return null;
  const last = bars[bars.length - 1];
  return last.high - e + (last.low - e);
}

function summarizeGauge(
  sell: number,
  neutral: number,
  buy: number,
): TechnicalGaugeSummary {
  const total = sell + neutral + buy;
  const score = total > 0 ? (buy - sell) / total : 0;
  let label: SummaryLabel = "Neutral";
  let bandIndex = 2;
  let suggestedWeights = [500, 2000, 5000, 2000, 500];
  if (score <= -0.5) {
    label = "Strong sell";
    bandIndex = 0;
    suggestedWeights = [4000, 3000, 1500, 1000, 500];
  } else if (score <= -0.15) {
    label = "Sell";
    bandIndex = 1;
    suggestedWeights = [2000, 3500, 2500, 1500, 500];
  } else if (score >= 0.5) {
    label = "Strong buy";
    bandIndex = 4;
    suggestedWeights = [500, 1000, 1500, 3000, 4000];
  } else if (score >= 0.15) {
    label = "Buy";
    bandIndex = 3;
    suggestedWeights = [500, 1500, 2500, 3500, 2000];
  }
  return {
    sell,
    neutral,
    buy,
    total,
    score,
    label,
    bandIndex,
    color: TECHNICAL_BAND_COLORS[bandIndex],
    suggestedWeights,
  };
}

export function computeRetailTechnicals(
  rawBars: readonly OhlcBar[],
): TechnicalsReport {
  const bars = rawBars.filter(
    (b) =>
      b &&
      Number.isFinite(b.open) &&
      Number.isFinite(b.high) &&
      Number.isFinite(b.low) &&
      Number.isFinite(b.close) &&
      b.open > 0 &&
      b.high > 0 &&
      b.low > 0 &&
      b.close > 0 &&
      b.high >= b.low &&
      b.high < 1e15,
  );

  if (bars.length < 15) {
    return {
      ready: false,
      barCount: bars.length,
      oscillators: summarizeGauge(0, 0, 0),
      movingAverages: summarizeGauge(0, 0, 0),
      summary: summarizeGauge(0, 0, 0),
      oscillatorRows: [],
      maRows: [],
    };
  }

  const closes = bars.map((b) => b.close);
  const lastClose = closes[closes.length - 1];

  const oscillatorRows: SignalRow[] = [];

  const rsis = rsiSeries(closes, 14);
  const rsiVal = rsis.length ? rsis[rsis.length - 1] : null;
  oscillatorRows.push({
    name: "Relative Strength Index (14)",
    value: rsiVal,
    action:
      rsiVal === null
        ? "Neutral"
        : rsiVal < 30
          ? "Buy"
          : rsiVal > 70
            ? "Sell"
            : "Neutral",
  });

  const stochVal = stochasticK(bars, 14, 3);
  oscillatorRows.push({
    name: "Stochastic %K (14, 3, 3)",
    value: stochVal,
    action:
      stochVal === null
        ? "Neutral"
        : stochVal < 20
          ? "Buy"
          : stochVal > 80
            ? "Sell"
            : "Neutral",
  });

  const cciVal = cci(bars, 20);
  oscillatorRows.push({
    name: "Commodity Channel Index (20)",
    value: cciVal,
    action:
      cciVal === null
        ? "Neutral"
        : cciVal < -100
          ? "Buy"
          : cciVal > 100
            ? "Sell"
            : "Neutral",
  });

  const adxRes = adx(bars, 14);
  oscillatorRows.push({
    name: "Average Directional Index (14)",
    value: adxRes?.value ?? null,
    action:
      !adxRes || adxRes.value < 20
        ? "Neutral"
        : adxRes.plusDi > adxRes.minusDi
          ? "Buy"
          : adxRes.minusDi > adxRes.plusDi
            ? "Sell"
            : "Neutral",
  });

  const aoRes = awesomeOscillator(bars);
  oscillatorRows.push({
    name: "Awesome Oscillator",
    value: aoRes?.value ?? null,
    action: !aoRes
      ? "Neutral"
      : aoRes.value > 0 && aoRes.value > aoRes.prev
        ? "Buy"
        : aoRes.value < 0 && aoRes.value < aoRes.prev
          ? "Sell"
          : "Neutral",
  });

  const momVal =
    closes.length >= 11 ? lastClose - closes[closes.length - 11] : null;
  oscillatorRows.push({
    name: "Momentum (10)",
    value: momVal,
    action:
      momVal === null
        ? "Neutral"
        : momVal > 0
          ? "Buy"
          : momVal < 0
            ? "Sell"
            : "Neutral",
  });

  const macdRes = macd(closes);
  oscillatorRows.push({
    name: "MACD Level (12, 26)",
    value: macdRes?.value ?? null,
    action: !macdRes
      ? "Neutral"
      : macdRes.value > macdRes.signal
        ? "Buy"
        : macdRes.value < macdRes.signal
          ? "Sell"
          : "Neutral",
  });

  const sRsiVal = stochRsiFast(closes);
  oscillatorRows.push({
    name: "Stochastic RSI Fast (3, 3, 14, 14)",
    value: sRsiVal,
    action:
      sRsiVal === null
        ? "Neutral"
        : sRsiVal < 20
          ? "Buy"
          : sRsiVal > 80
            ? "Sell"
            : "Neutral",
  });

  const wrVal = williamsR(bars, 14);
  oscillatorRows.push({
    name: "Williams Percent Range (14)",
    value: wrVal,
    action:
      wrVal === null
        ? "Neutral"
        : wrVal < -80
          ? "Buy"
          : wrVal > -20
            ? "Sell"
            : "Neutral",
  });

  const bbpVal = bullBearPower(bars, 13);
  oscillatorRows.push({
    name: "Bull Bear Power (13)",
    value: bbpVal,
    action:
      bbpVal === null
        ? "Neutral"
        : bbpVal > 0
          ? "Buy"
          : bbpVal < 0
            ? "Sell"
            : "Neutral",
  });

  const maRows: SignalRow[] = [];
  for (const period of [10, 20, 30, 50, 100]) {
    const eVal = ema(closes, period);
    maRows.push({
      name: `Exponential Moving Average (${period})`,
      value: eVal,
      action:
        eVal === null
          ? "Neutral"
          : lastClose > eVal
            ? "Buy"
            : lastClose < eVal
              ? "Sell"
              : "Neutral",
    });
    const sVal = sma(closes, period);
    maRows.push({
      name: `Simple Moving Average (${period})`,
      value: sVal,
      action:
        sVal === null
          ? "Neutral"
          : lastClose > sVal
            ? "Buy"
            : lastClose < sVal
              ? "Sell"
              : "Neutral",
    });
  }

  const tally = (rows: readonly SignalRow[]) => {
    let sell = 0;
    let neutral = 0;
    let buy = 0;
    for (const r of rows) {
      if (r.value === null || !Number.isFinite(r.value)) continue;
      if (r.action === "Sell") sell++;
      else if (r.action === "Buy") buy++;
      else neutral++;
    }
    return summarizeGauge(sell, neutral, buy);
  };

  const oscGauge = tally(oscillatorRows);
  const maGauge = tally(maRows);
  const sumGauge = summarizeGauge(
    oscGauge.sell + maGauge.sell,
    oscGauge.neutral + maGauge.neutral,
    oscGauge.buy + maGauge.buy,
  );

  return {
    ready: sumGauge.total > 0,
    barCount: bars.length,
    oscillators: oscGauge,
    movingAverages: maGauge,
    summary: sumGauge,
    oscillatorRows,
    maRows,
  };
}
