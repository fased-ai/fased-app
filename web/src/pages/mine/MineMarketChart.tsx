import { useEffect, useMemo, useRef, useState } from "react";
import {
  AreaSeries,
  BarSeries,
  BaselineSeries,
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  HistogramSeries,
  LineSeries,
  LineStyle,
  createChart,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type UTCTimestamp,
} from "lightweight-charts";
import type { CandleBar, RoundTelemetry, StreakEntry } from "../../mining-lv1x";

export type ChartInterval = "10s" | "1m" | "5m" | "10m" | "1h" | "1d";
export type ChartStyleMode =
  | "candles"
  | "ha"
  | "hollow"
  | "rounds"
  | "baseline"
  | "bars"
  | "renko"
  | "line"
  | "pulse"
  | "technicals";

interface MineMarketChartProps {
  candles: CandleBar[];
  interval: ChartInterval;
  onIntervalChange: (next: ChartInterval) => void;
  round?: RoundTelemetry;
  streak: StreakEntry[];
  crowdWeights: number[];
  onApplyPresetFromTechnicals?: (weightsBps: number[]) => void;
}

const INTERVALS: readonly ChartInterval[] = [
  "10s",
  "1m",
  "5m",
  "10m",
  "1h",
  "1d",
];
const CHART_MODES: readonly { id: ChartStyleMode; label: string }[] = [
  { id: "candles", label: "Candles" },
  { id: "ha", label: "Heikin Ashi" },
  { id: "hollow", label: "Hollow" },
  { id: "rounds", label: "Rounds" },
  { id: "baseline", label: "Baseline" },
  { id: "bars", label: "Bars" },
  { id: "renko", label: "Renko" },
  { id: "line", label: "Line" },
  { id: "pulse", label: "Pulse" },
  { id: "technicals", label: "Technicals" },
];

const RANGE_COLORS = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

function toHeikinAshi(bars: readonly CandleBar[]): CandleBar[] {
  const out: CandleBar[] = [];
  let prevOpen = bars[0]?.open ?? 0;
  let prevClose = bars[0]?.close ?? 0;
  for (let i = 0; i < bars.length; i++) {
    const b = bars[i];
    const close = (b.open + b.high + b.low + b.close) / 4;
    const open = i === 0 ? (b.open + b.close) / 2 : (prevOpen + prevClose) / 2;
    const high = Math.max(b.high, open, close);
    const low = Math.min(b.low, open, close);
    out.push({ ...b, open, high, low, close });
    prevOpen = open;
    prevClose = close;
  }
  return out;
}

function toRenko(bars: readonly CandleBar[], brickPct = 0.0005): CandleBar[] {
  if (bars.length < 2) return [...bars];
  const out: CandleBar[] = [];
  let ref = bars[0].close;
  let lastT = 0;
  for (const b of bars) {
    const step = Math.max(1e-6, ref * brickPct);
    let n = 0;
    while (b.close >= ref + step && n < 10) {
      const t = b.time > lastT ? b.time : lastT + 1;
      lastT = t;
      out.push({
        time: t,
        open: ref,
        high: ref + step,
        low: ref,
        close: ref + step,
        volume: n === 0 ? b.volume : 0,
      });
      ref += step;
      n++;
    }
    while (b.close <= ref - step && n < 10) {
      const t = b.time > lastT ? b.time : lastT + 1;
      lastT = t;
      out.push({
        time: t,
        open: ref,
        high: ref,
        low: ref - step,
        close: ref - step,
        volume: n === 0 ? b.volume : 0,
      });
      ref -= step;
      n++;
    }
  }
  return out.length >= 2 ? out : [...bars];
}

function computeEMA(bars: readonly CandleBar[], period: number) {
  if (bars.length < 2) return [];
  const k = 2 / (period + 1);
  let ema = bars[0].close;
  return bars.map((b, idx) => {
    ema = idx === 0 ? b.close : b.close * k + ema * (1 - k);
    return { time: b.time as UTCTimestamp, value: ema };
  });
}

function computeBollinger(bars: readonly CandleBar[], period = 20) {
  const upper: Array<{ time: UTCTimestamp; value: number }> = [];
  const lower: Array<{ time: UTCTimestamp; value: number }> = [];
  for (let i = period - 1; i < bars.length; i++) {
    let sum = 0;
    for (let j = i - period + 1; j <= i; j++) sum += bars[j].close;
    const mean = sum / period;
    let sq = 0;
    for (let j = i - period + 1; j <= i; j++) {
      const d = bars[j].close - mean;
      sq += d * d;
    }
    const std = Math.sqrt(sq / period);
    upper.push({ time: bars[i].time as UTCTimestamp, value: mean + 2 * std });
    lower.push({ time: bars[i].time as UTCTimestamp, value: mean - 2 * std });
  }
  return { upper, lower };
}

function computeVWAP(bars: readonly CandleBar[]) {
  let pv = 0;
  let vol = 0;
  return bars.map((b) => {
    const v = Math.max(0.01, b.volume || 1);
    pv += ((b.high + b.low + b.close) / 3) * v;
    vol += v;
    return { time: b.time as UTCTimestamp, value: pv / vol };
  });
}

function computeRSI14(bars: readonly CandleBar[]): number | null {
  if (bars.length < 15) return null;
  let gain = 0;
  let loss = 0;
  for (let i = bars.length - 14; i < bars.length; i++) {
    const diff = bars[i].close - bars[i - 1].close;
    if (diff >= 0) gain += diff;
    else loss -= diff;
  }
  if (loss === 0) return gain === 0 ? 50 : 100;
  return 100 - 100 / (1 + gain / loss);
}

function computeATR10mBps(bars: readonly CandleBar[]): number | null {
  if (bars.length < 2) return null;
  const slice = bars.slice(-14);
  let sum = 0;
  for (let i = 1; i < slice.length; i++) {
    const prevC = slice[i - 1].close;
    const tr = Math.max(
      slice[i].high - slice[i].low,
      Math.abs(slice[i].high - prevC),
      Math.abs(slice[i].low - prevC),
    );
    sum += slice[i].close > 0 ? (tr / slice[i].close) * 10_000 : 0;
  }
  return sum / Math.max(1, slice.length - 1);
}

export function MineMarketChart({
  candles,
  interval,
  onIntervalChange,
  round,
  streak,
  crowdWeights,
  onApplyPresetFromTechnicals,
}: MineMarketChartProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [styleMode, setStyleMode] = useState<ChartStyleMode>("candles");
  const [showRanges, setShowRanges] = useState(true);
  const [showVolume, setShowVolume] = useState(true);
  const [showEmaCross, setShowEmaCross] = useState(false);
  const [showBollinger, setShowBollinger] = useState(false);
  const [showVwap, setShowVwap] = useState(false);
  const [showAtr, setShowAtr] = useState(false);
  const [showRsi, setShowRsi] = useState(false);
  const [themeTick, setThemeTick] = useState(0);

  useEffect(() => {
    const obs = new MutationObserver(() => setThemeTick((v) => v + 1));
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme", "class"],
    });
    return () => obs.disconnect();
  }, []);

  const prepared = useMemo(() => {
    const dedup = new Map<number, CandleBar>();
    for (const c of candles) {
      if (Number.isFinite(c.time) && c.close > 0) {
        dedup.set(Math.floor(c.time), c);
      }
    }
    const base = [...dedup.values()].sort((a, b) => a.time - b.time);
    if (styleMode === "ha") return toHeikinAshi(base);
    if (styleMode === "renko") return toRenko(base);
    return base;
  }, [candles, styleMode]);

  const rsi14 = useMemo(() => computeRSI14(prepared), [prepared]);
  const atrBps = useMemo(() => computeATR10mBps(prepared), [prepared]);

  useEffect(() => {
    const host = hostRef.current;
    if (
      !host ||
      styleMode === "pulse" ||
      styleMode === "technicals" ||
      prepared.length === 0
    ) {
      return;
    }

    const css = getComputedStyle(document.documentElement);
    const val = (k: string, fb: string) => css.getPropertyValue(k).trim() || fb;
    const bg = val("--wt-surface", "#0F1218");
    const text = val("--wt-muted", "#868FA2");
    const grid = "rgba(148, 163, 184, 0.06)";

    let chart: IChartApi | null = null;
    try {
      chart = createChart(host, {
        width: Math.max(300, host.clientWidth || 640),
        height: 290,
        layout: {
          background: { type: ColorType.Solid, color: bg },
          textColor: text,
          attributionLogo: false,
        },
        grid: { vertLines: { color: grid }, horzLines: { color: grid } },
        crosshair: { mode: CrosshairMode.Normal },
        rightPriceScale: { borderColor: grid },
        timeScale: { borderColor: grid, timeVisible: true },
      });

      const lines: IPriceLine[] = [];
      let mainSeries:
        | ISeriesApi<"Candlestick">
        | ISeriesApi<"Bar">
        | ISeriesApi<"Area">
        | ISeriesApi<"Baseline">
        | null = null;

      if (styleMode === "bars") {
        const s = chart.addSeries(BarSeries, {
          upColor: "#2FB88C",
          downColor: "#E0566F",
        });
        s.setData(
          prepared.map((b) => ({
            time: b.time as UTCTimestamp,
            open: b.open,
            high: b.high,
            low: b.low,
            close: b.close,
          })),
        );
        mainSeries = s;
      } else if (styleMode === "line") {
        const s = chart.addSeries(AreaSeries, {
          lineColor: "#33A9B8",
          topColor: "rgba(51, 169, 184, 0.24)",
          bottomColor: "rgba(51, 169, 184, 0.0)",
        });
        s.setData(
          prepared.map((b) => ({
            time: b.time as UTCTimestamp,
            value: b.close,
          })),
        );
        mainSeries = s;
      } else if (styleMode === "baseline") {
        const openRef = round?.openPriceUsd ?? prepared[0].open;
        const s = chart.addSeries(BaselineSeries, {
          baseValue: { type: "price", price: openRef },
          topLineColor: "#2FB88C",
          bottomLineColor: "#E0566F",
        });
        s.setData(
          prepared.map((b) => ({
            time: b.time as UTCTimestamp,
            value: b.close,
          })),
        );
        mainSeries = s;
      } else {
        const hollow = styleMode === "hollow";
        const s = chart.addSeries(CandlestickSeries, {
          upColor: hollow ? "transparent" : "#2FB88C",
          downColor: "#E0566F",
          borderUpColor: "#2FB88C",
          borderDownColor: "#E0566F",
          wickUpColor: "#2FB88C",
          wickDownColor: "#E0566F",
        });
        s.setData(
          prepared.map((b) => ({
            time: b.time as UTCTimestamp,
            open: b.open,
            high: b.high,
            low: b.low,
            close: b.close,
          })),
        );
        mainSeries = s;
      }

      if (showRanges && mainSeries && (round?.openPriceUsd ?? 0) > 0) {
        const open = round!.openPriceUsd;
        const inner = (round?.innerBps ?? 5) / 10_000;
        const outer = (round?.outerBps ?? 20) / 10_000;
        const defs: Array<[number, string, string]> = [
          [open * (1 + outer), `#33A9B8`, `+${round?.outerBps ?? 20}bps`],
          [open * (1 + inner), `#2FB88C`, `+${round?.innerBps ?? 5}bps`],
          [open, text, "Open"],
          [open * (1 - inner), `#D98466`, `-${round?.innerBps ?? 5}bps`],
          [open * (1 - outer), `#E0566F`, `-${round?.outerBps ?? 20}bps`],
        ];
        for (const [priceVal, col, title] of defs) {
          lines.push(
            mainSeries.createPriceLine({
              price: priceVal,
              color: col,
              lineWidth: 1,
              lineStyle: LineStyle.Dashed,
              axisLabelVisible: true,
              title,
            }),
          );
        }
      }

      if (showVolume) {
        const vs = chart.addSeries(HistogramSeries, {
          priceFormat: { type: "volume" },
          priceScaleId: "",
        });
        vs.priceScale().applyOptions({
          scaleMargins: { top: 0.82, bottom: 0 },
        });
        vs.setData(
          prepared.map((b) => ({
            time: b.time as UTCTimestamp,
            value: b.volume,
            color:
              b.close >= b.open
                ? "rgba(47, 184, 140, 0.28)"
                : "rgba(224, 86, 111, 0.28)",
          })),
        );
      }

      if (showEmaCross) {
        const f = chart.addSeries(LineSeries, {
          color: "#33A9B8",
          lineWidth: 1,
        });
        f.setData(computeEMA(prepared, 9));
        const sl = chart.addSeries(LineSeries, {
          color: "#E0566F",
          lineWidth: 1,
        });
        sl.setData(computeEMA(prepared, 21));
      }

      if (showBollinger) {
        const bb = computeBollinger(prepared, 20);
        const up = chart.addSeries(LineSeries, {
          color: "rgba(134, 143, 162, 0.55)",
          lineWidth: 1,
          lineStyle: LineStyle.Dotted,
        });
        up.setData(bb.upper);
        const lo = chart.addSeries(LineSeries, {
          color: "rgba(134, 143, 162, 0.55)",
          lineWidth: 1,
          lineStyle: LineStyle.Dotted,
        });
        lo.setData(bb.lower);
      }

      if (showVwap) {
        const vw = chart.addSeries(LineSeries, {
          color: "#D98466",
          lineWidth: 1,
        });
        vw.setData(computeVWAP(prepared));
      }

      chart.timeScale().fitContent();

      const ro = new ResizeObserver((entries) => {
        for (const ent of entries) {
          if (ent.contentRect.width > 0 && chart) {
            chart.applyOptions({ width: Math.floor(ent.contentRect.width) });
          }
        }
      });
      ro.observe(host);

      return () => {
        ro.disconnect();
        for (const l of lines) {
          try {
            mainSeries?.removePriceLine(l);
          } catch {}
        }
        try {
          chart?.remove();
        } catch {}
      };
    } catch {
      return undefined;
    }
  }, [
    prepared,
    styleMode,
    showRanges,
    showVolume,
    showEmaCross,
    showBollinger,
    showVwap,
    round?.openPriceUsd,
    round?.innerBps,
    round?.outerBps,
    themeTick,
  ]);

  const lastBar = prepared[prepared.length - 1];
  const openUsd = round?.openPriceUsd ?? lastBar?.open ?? 94250;
  const markUsd = round?.markPriceUsd ?? lastBar?.close ?? 94265;
  const moveBps = round?.moveBps ?? 0;

  return (
    <div className="wt-wpanel mine-chart-card">
      <div className="mine-chart-toolbar">
        <div className="mine-chart-title">
          <strong>BTC / USD</strong>
          <span>
            ${markUsd.toLocaleString("en-US", { maximumFractionDigits: 2 })}
          </span>
          <span className={moveBps >= 0 ? "is-pos" : "is-neg"}>
            {moveBps >= 0 ? "+" : ""}
            {moveBps} bps
          </span>
          <span className="mine-muted-note">
            Open $
            {openUsd.toLocaleString("en-US", { maximumFractionDigits: 2 })}
          </span>
          {showAtr && atrBps !== null && (
            <span className="mine-ind-readout">
              ATR {atrBps.toFixed(1)} bps
            </span>
          )}
          {showRsi && rsi14 !== null && (
            <span className="mine-ind-readout">RSI(14) {rsi14.toFixed(1)}</span>
          )}
        </div>

        <div className="mine-chart-controls">
          <div
            className="mine-chip-group"
            role="group"
            aria-label="Chart timeframe"
          >
            {INTERVALS.map((iv) => (
              <button
                key={iv}
                type="button"
                className="mine-chip"
                aria-pressed={interval === iv}
                onClick={() => onIntervalChange(iv)}
              >
                {iv}
              </button>
            ))}
          </div>
          <div
            className="mine-chip-group"
            role="group"
            aria-label="Chart style"
          >
            {CHART_MODES.map((m) => (
              <button
                key={m.id}
                type="button"
                className="mine-chip"
                aria-pressed={styleMode === m.id}
                onClick={() => setStyleMode(m.id)}
              >
                {m.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div
        className="mine-indicator-row"
        role="group"
        aria-label="Chart indicators"
      >
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showRanges}
          onClick={() => setShowRanges((v) => !v)}
        >
          Ranges
        </button>
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showVolume}
          onClick={() => setShowVolume((v) => !v)}
        >
          Volume
        </button>
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showEmaCross}
          onClick={() => setShowEmaCross((v) => !v)}
        >
          EMA 9/21
        </button>
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showBollinger}
          onClick={() => setShowBollinger((v) => !v)}
        >
          Bollinger
        </button>
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showVwap}
          onClick={() => setShowVwap((v) => !v)}
        >
          VWAP
        </button>
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showAtr}
          onClick={() => setShowAtr((v) => !v)}
        >
          ATR 10m
        </button>
        <button
          type="button"
          className="mine-chip"
          aria-pressed={showRsi}
          onClick={() => setShowRsi((v) => !v)}
        >
          RSI 14
        </button>
      </div>

      {styleMode === "pulse" ? (
        <div className="mine-pulse-view" aria-label="Range pulse histogram">
          {["A", "B", "C", "D", "E"].map((code, i) => {
            const pct = (crowdWeights[i] ?? 2000) / 100;
            const b = round?.buckets?.[i];
            return (
              <div key={code} className="mine-pulse-col">
                <div className="mine-pulse-track">
                  <div
                    className="mine-pulse-fill"
                    style={{
                      height: `${Math.max(4, pct)}%`,
                      background: RANGE_COLORS[i],
                    }}
                  />
                </div>
                <strong>{code}</strong>
                <small>
                  {pct.toFixed(0)}% · {b ? `${b.evMultiple.toFixed(2)}×` : "—"}
                </small>
              </div>
            );
          })}
        </div>
      ) : styleMode === "technicals" ? (
        <div
          className="mine-technicals-view"
          aria-label="Technical indicators summary"
        >
          <div className="mine-tech-grid">
            <div className="mine-tech-card">
              <span>RSI (14)</span>
              <strong>{rsi14 !== null ? rsi14.toFixed(1) : "52.0"}</strong>
              <small>
                {(rsi14 ?? 50) > 60
                  ? "Bullish bias"
                  : (rsi14 ?? 50) < 40
                    ? "Bearish bias"
                    : "Neutral range"}
              </small>
            </div>
            <div className="mine-tech-card">
              <span>ATR 10m (bps)</span>
              <strong>{atrBps !== null ? atrBps.toFixed(1) : "11.4"}</strong>
              <small>
                {(atrBps ?? 11) > 18 ? "Expansion tails" : "Mean reversion"}
              </small>
            </div>
            <div className="mine-tech-card">
              <span>Round Drift</span>
              <strong>{moveBps >= 0 ? `+${moveBps}` : moveBps} bps</strong>
              <small>
                ±{round?.innerBps ?? 5} / ±{round?.outerBps ?? 20} bps
              </small>
            </div>
          </div>
          {onApplyPresetFromTechnicals && (
            <button
              type="button"
              className="wt-btn-soft"
              onClick={() => {
                const suggested =
                  (atrBps ?? 11) > 18
                    ? [4000, 1000, 0, 1000, 4000]
                    : (rsi14 ?? 50) > 58
                      ? [500, 1000, 1500, 4500, 2500]
                      : (rsi14 ?? 50) < 42
                        ? [2500, 4500, 1500, 1000, 500]
                        : [500, 1500, 6000, 1500, 500];
                onApplyPresetFromTechnicals(suggested);
              }}
            >
              Apply Technical Curve Bias
            </button>
          )}
        </div>
      ) : (
        <div
          ref={hostRef}
          className="mine-tv-host"
          aria-label="BTC USD mining chart"
        />
      )}

      <div className="mine-streak-foot">
        <div className="mine-section-head">
          <h2>24-Round Streak</h2>
          <span className="mine-muted-note">
            Pool {round ? `${round.totalPoolSol.toFixed(2)} SOL` : "0.00 SOL"} ·{" "}
            {round?.minerCount ?? 0} miners
          </span>
        </div>
        <div
          className="mine-streak-grid"
          role="list"
          aria-label="24-round streak"
        >
          {streak.slice(0, 24).map((entry) => (
            <div
              key={entry.roundSlot}
              role="listitem"
              className={`mine-streak-chip range-${entry.winningRange}`}
              title={`#${entry.roundSlot} · Range ${entry.code} · ${entry.moveBps >= 0 ? "+" : ""}${entry.moveBps} bps`}
            >
              <span>{entry.code}</span>
              <small>
                {entry.moveBps >= 0 ? "+" : ""}
                {entry.moveBps}
              </small>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
