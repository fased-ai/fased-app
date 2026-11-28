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
  createSeriesMarkers,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from "lightweight-charts";
import type { CandleBar, RoundTelemetry, StreakEntry } from "../../mining-lv1x";
import { MineTechnicalsView } from "./MineTechnicalsView";

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
  userWeightsBps?: number[];
  plannedSlots?: number[];
  onApplyPresetFromTechnicals?: (weightsBps: number[]) => void;
}

const INTERVALS: readonly [ChartInterval, string][] = [
  ["10s", "10s"],
  ["1m", "1m"],
  ["5m", "5m"],
  ["10m", "10m"],
  ["1h", "1h"],
  ["1d", "1D"],
];

const INTERVAL_SECONDS: Record<ChartInterval, number> = {
  "10s": 10,
  "1m": 60,
  "5m": 300,
  "10m": 600,
  "1h": 3600,
  "1d": 86400,
};

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

const RANGE_TEXT_COLORS = [
  "var(--wt-danger)",
  "var(--wt-warn)",
  "var(--wt-muted)",
  "var(--wt-ok)",
  "var(--wt-cyan-text)",
] as const;

const RANGE_LETTERS = ["A", "B", "C", "D", "E"] as const;

function formatClockTime(ms: number): string {
  const hm = new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(ms);
  let tz = "";
  try {
    tz =
      new Intl.DateTimeFormat("en-US", { timeZoneName: "short" })
        .formatToParts(ms)
        .find((p) => p.type === "timeZoneName")?.value ?? "";
  } catch {
    tz = "";
  }
  return tz ? `${hm} ${tz}` : hm;
}

function formatHm(ms: number): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(ms);
}

function formatPriceUsd(v: number): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(v);
}

function formatVol(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "0";
  if (v >= 1000) return `${(v / 1000).toFixed(2)}k`;
  if (v >= 10) return v.toFixed(2);
  if (v >= 1) return v.toFixed(3);
  return v.toFixed(4);
}

function formatBarCountdown(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const rem = s % 60;
  if (h > 0)
    return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(rem).padStart(2, "0")}`;
  return `${String(m).padStart(2, "0")}:${String(rem).padStart(2, "0")}`;
}

function ChartIcon({
  kind,
}: {
  kind: "clock" | "fx" | "reset" | ChartStyleMode;
}) {
  if (kind === "clock") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <circle cx="8" cy="8" r="6" />
        <path d="M8 4.8V8l2.2 1.4" />
      </svg>
    );
  }
  if (kind === "fx") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M2.5 11.5L6 7.5l3 2.5 4.5-5.5" />
        <path d="M10.5 4.5h3v3" />
      </svg>
    );
  }
  if (kind === "reset") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M3 8a5 5 0 1 1 1.4 3.5" />
        <path d="M3 4.5V8h3.5" />
      </svg>
    );
  }
  if (kind === "line") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M2 11.5l3.5-4 3 2.5L14 4" />
      </svg>
    );
  }
  if (kind === "baseline") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <line
          x1="2"
          y1="8.5"
          x2="14"
          y2="8.5"
          stroke="currentColor"
          strokeDasharray="2 2"
          opacity="0.65"
        />
        <path d="M2 11l3-4 2.5 2L11 4.5 14 7" stroke="#2FB88C" />
      </svg>
    );
  }
  if (kind === "bars") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        aria-hidden="true"
      >
        <path d="M5 3v10M3.2 6H5M5 10h1.8M11 2.5v11M9.2 9.5H11M11 5.5h1.8" />
      </svg>
    );
  }
  if (kind === "renko") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        strokeWidth="1.2"
        aria-hidden="true"
      >
        <rect x="2" y="9" width="3.5" height="3.5" rx="0.5" fill="#E0566F" />
        <rect x="6" y="6" width="3.5" height="3.5" rx="0.5" fill="#2FB88C" />
        <rect x="10" y="3" width="3.5" height="3.5" rx="0.5" fill="#33A9B8" />
      </svg>
    );
  }
  if (kind === "pulse") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        aria-hidden="true"
      >
        <path d="M3 13V9M6.3 13V5.5M9.7 13V3.5M13 13V7.5" />
      </svg>
    );
  }
  if (kind === "technicals") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        aria-hidden="true"
      >
        <path d="M2.5 11.5a5.5 5.5 0 0 1 11 0" />
        <path d="M8 11.5L10.8 7" />
        <circle cx="8" cy="11.5" r="1.1" fill="currentColor" stroke="none" />
      </svg>
    );
  }
  if (kind === "hollow") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        aria-hidden="true"
      >
        <path d="M5 2.5v2M5 10.5v3M11 3.5v2M11 11.5v2" />
        <rect x="3.5" y="4.5" width="3" height="6" rx="0.5" />
        <rect
          x="9.5"
          y="5.5"
          width="3"
          height="6"
          rx="0.5"
          fill="currentColor"
        />
      </svg>
    );
  }
  if (kind === "rounds") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        aria-hidden="true"
      >
        <rect
          x="2.5"
          y="4"
          width="3"
          height="8"
          rx="0.7"
          fill="#E0566F"
          stroke="#E0566F"
        />
        <rect
          x="6.5"
          y="6"
          width="3"
          height="5"
          rx="0.7"
          fill="#6F7889"
          stroke="#6F7889"
        />
        <rect
          x="10.5"
          y="3"
          width="3"
          height="9"
          rx="0.7"
          fill="#2FB88C"
          stroke="#2FB88C"
        />
      </svg>
    );
  }
  return (
    <svg
      className="rmc-ico"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d="M5 2v2.5M5 10.5V14M11 3v2M11 11v3" />
      <rect x="3.5" y="4.5" width="3" height="6" rx="0.5" fill="currentColor" />
      <rect
        x="9.5"
        y="5.5"
        width="3"
        height="6"
        rx="0.5"
        fill={kind === "ha" ? "#2FB88C" : "none"}
      />
    </svg>
  );
}

function SkewIcon({ skew }: { skew: string }) {
  const k = skew.toLowerCase();
  if (k === "bullish") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M2.5 11.5 6.5 7.5 9 10l4.5-5" />
        <path d="M10 5h3.5v3.5" />
      </svg>
    );
  }
  if (k === "bearish") {
    return (
      <svg
        className="rmc-ico"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M2.5 4.5 6.5 8.5 9 6l4.5 5" />
        <path d="M10 11h3.5V7.5" />
      </svg>
    );
  }
  return (
    <svg
      className="rmc-ico"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d="M3 6h10M3 10h10" />
    </svg>
  );
}

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
  userWeightsBps,
  plannedSlots = [],
  onApplyPresetFromTechnicals,
}: MineMarketChartProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const topbarRef = useRef<HTMLDivElement | null>(null);
  const streakStripRef = useRef<HTMLDivElement | null>(null);
  const [styleMode, setStyleMode] = useState<ChartStyleMode>("candles");
  const [openMenu, setOpenMenu] = useState<
    "interval" | "style" | "indicators" | null
  >(null);
  const [showRanges, setShowRanges] = useState(true);
  const [showOutcomes, setShowOutcomes] = useState(true);
  const [showVolume, setShowVolume] = useState(true);
  const [showEmaCross, setShowEmaCross] = useState(false);
  const [showBollinger, setShowBollinger] = useState(false);
  const [showVwap, setShowVwap] = useState(false);
  const [showAtr, setShowAtr] = useState(false);
  const [showRsi, setShowRsi] = useState(false);
  const [themeTick, setThemeTick] = useState(0);
  const [hoverBar, setHoverBar] = useState<CandleBar | null>(null);
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [selectedStreakSlot, setSelectedStreakSlot] = useState<number | null>(
    null,
  );
  const [streakTip, setStreakTip] = useState<{
    entry: StreakEntry;
    x: number;
  } | null>(null);

  useEffect(() => {
    const id = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    if (!openMenu) return;
    const onDown = (e: MouseEvent) => {
      if (topbarRef.current && !topbarRef.current.contains(e.target as Node)) {
        setOpenMenu(null);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpenMenu(null);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [openMenu]);

  useEffect(() => {
    const obs = new MutationObserver(() => setThemeTick((t) => t + 1));
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => obs.disconnect();
  }, []);

  const prepared = useMemo(() => {
    const sorted = [...candles]
      .filter((c) => Number.isFinite(c.time) && c.close > 0)
      .sort((a, b) => a.time - b.time);
    const dedup: CandleBar[] = [];
    for (const c of sorted) {
      if (dedup.length === 0 || dedup[dedup.length - 1].time !== c.time) {
        dedup.push(c);
      } else {
        dedup[dedup.length - 1] = c;
      }
    }
    return dedup;
  }, [candles]);

  const rsi14 = useMemo(() => computeRSI14(prepared), [prepared]);
  const atrBps = useMemo(() => computeATR10mBps(prepared), [prepared]);

  useEffect(() => {
    if (styleMode === "pulse" || styleMode === "technicals") return;
    const host = hostRef.current;
    if (!host || prepared.length === 0) return;

    const isLight =
      document.documentElement.getAttribute("data-theme") === "light";
    const bg = isLight ? "#FFFFFF" : "#0F1218";
    const textCol = isLight ? "#505A6B" : "#868FA2";
    const gridCol = isLight
      ? "rgba(16, 24, 40, 0.05)"
      : "rgba(148, 163, 184, 0.05)";
    const borderCol = isLight
      ? "rgba(16, 24, 40, 0.08)"
      : "rgba(255, 255, 255, 0.06)";

    let chart: IChartApi | null = null;
    const lines: IPriceLine[] = [];
    let mainSeries: ISeriesApi<any> | null = null;

    try {
      chart = createChart(host, {
        width: Math.max(260, host.clientWidth || 640),
        height: Math.max(260, host.clientHeight || 360),
        layout: {
          attributionLogo: false,
          background: { type: ColorType.Solid, color: bg },
          textColor: textCol,
          fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif",
          fontSize: 11,
        },
        grid: {
          vertLines: { color: gridCol },
          horzLines: { color: gridCol },
        },
        crosshair: {
          mode: CrosshairMode.Normal,
          vertLine: {
            color: "rgba(134, 143, 162, 0.4)",
            style: LineStyle.Dashed,
          },
          horzLine: {
            color: "rgba(134, 143, 162, 0.4)",
            style: LineStyle.Dashed,
          },
        },
        rightPriceScale: { borderColor: borderCol },
        timeScale: {
          borderColor: borderCol,
          timeVisible: true,
          secondsVisible: interval === "10s",
        },
      });

      const barByTime = new Map<number, CandleBar>();
      for (const b of prepared) barByTime.set(b.time, b);

      const srcBars =
        styleMode === "ha"
          ? toHeikinAshi(prepared)
          : styleMode === "renko"
            ? toRenko(prepared)
            : prepared;

      if (styleMode === "line") {
        mainSeries = chart.addSeries(AreaSeries, {
          lineColor: "#2FB88C",
          topColor: "rgba(47, 184, 140, 0.24)",
          bottomColor: "rgba(47, 184, 140, 0.0)",
          lineWidth: 2,
        });
        mainSeries.setData(
          srcBars.map((b) => ({
            time: b.time as UTCTimestamp,
            value: b.close,
          })),
        );
      } else if (styleMode === "baseline") {
        const base = round?.openPriceUsd || srcBars[0].open;
        mainSeries = chart.addSeries(BaselineSeries, {
          baseValue: { type: "price", price: base },
          topLineColor: "#2FB88C",
          topFillColor1: "rgba(47, 184, 140, 0.25)",
          topFillColor2: "rgba(47, 184, 140, 0.0)",
          bottomLineColor: "#E0566F",
          bottomFillColor1: "rgba(224, 86, 111, 0.0)",
          bottomFillColor2: "rgba(224, 86, 111, 0.25)",
        });
        mainSeries.setData(
          srcBars.map((b) => ({
            time: b.time as UTCTimestamp,
            value: b.close,
          })),
        );
      } else if (styleMode === "bars") {
        mainSeries = chart.addSeries(BarSeries, {
          upColor: "#2FB88C",
          downColor: "#E0566F",
        });
        mainSeries.setData(
          srcBars.map((b) => ({
            time: b.time as UTCTimestamp,
            open: b.open,
            high: b.high,
            low: b.low,
            close: b.close,
          })),
        );
      } else {
        mainSeries = chart.addSeries(CandlestickSeries, {
          upColor: styleMode === "hollow" ? "transparent" : "#2FB88C",
          downColor: "#E0566F",
          borderUpColor: "#2FB88C",
          borderDownColor: "#E0566F",
          wickUpColor: "#2FB88C",
          wickDownColor: "#E0566F",
        });
        mainSeries.setData(
          srcBars.map((b) => {
            if (styleMode === "rounds" && typeof b.winningRange === "number") {
              const col = RANGE_COLORS[b.winningRange] ?? "#6F7889";
              return {
                time: b.time as UTCTimestamp,
                open: b.open,
                high: b.high,
                low: b.low,
                close: b.close,
                color: col,
                borderColor: col,
                wickColor: col,
              };
            }
            return {
              time: b.time as UTCTimestamp,
              open: b.open,
              high: b.high,
              low: b.low,
              close: b.close,
            };
          }),
        );
      }

      if (showOutcomes && mainSeries && srcBars.length > 4) {
        const markers: SeriesMarker<Time>[] = [];
        const step = Math.max(1, Math.floor(srcBars.length / 10));
        for (let i = step; i < srcBars.length; i += step) {
          const b = srcBars[i];
          const prev = srcBars[Math.max(0, i - step)];
          const bps =
            prev.open > 0 ? ((b.close - prev.open) / prev.open) * 10_000 : 0;
          const rIdx =
            typeof b.winningRange === "number"
              ? b.winningRange
              : bps < -(round?.outerBps ?? 20)
                ? 0
                : bps < -(round?.innerBps ?? 5)
                  ? 1
                  : bps < (round?.innerBps ?? 5)
                    ? 2
                    : bps < (round?.outerBps ?? 20)
                      ? 3
                      : 4;
          markers.push({
            time: b.time as UTCTimestamp,
            position: "aboveBar",
            color: RANGE_COLORS[rIdx] ?? "#6F7889",
            shape: "circle",
            text: RANGE_LETTERS[rIdx] ?? "C",
          });
        }
        if (markers.length > 0) {
          try {
            createSeriesMarkers(mainSeries, markers);
          } catch {}
        }
      }

      if (showRanges && mainSeries) {
        const openPx = round?.openPriceUsd || srcBars[srcBars.length - 1].open;
        const inner = (round?.innerBps ?? 5) / 10_000;
        const outer = (round?.outerBps ?? 20) / 10_000;
        const defs: Array<{
          price: number;
          color: string;
          title: string;
          showLabel: boolean;
        }> = [
          {
            price: openPx * (1 + outer),
            color: "rgba(51, 169, 184, 0.55)",
            title: "",
            showLabel: false,
          },
          {
            price: openPx * (1 + inner),
            color: "rgba(47, 184, 140, 0.55)",
            title: "",
            showLabel: false,
          },
          {
            price: openPx,
            color: "#868FA2",
            title: "Open",
            showLabel: true,
          },
          {
            price: openPx * (1 - inner),
            color: "rgba(217, 132, 102, 0.55)",
            title: "",
            showLabel: false,
          },
          {
            price: openPx * (1 - outer),
            color: "rgba(224, 86, 111, 0.55)",
            title: "",
            showLabel: false,
          },
        ];
        for (const d of defs) {
          lines.push(
            mainSeries.createPriceLine({
              price: d.price,
              color: d.color,
              lineWidth: 1,
              lineStyle: LineStyle.Dotted,
              axisLabelVisible: d.showLabel,
              title: d.title,
            }),
          );
        }
      }

      if (showVolume) {
        const volSeries = chart.addSeries(HistogramSeries, {
          priceFormat: { type: "volume" },
          priceScaleId: "vol",
          lastValueVisible: false,
          priceLineVisible: false,
        });
        chart.priceScale("vol").applyOptions({
          scaleMargins: { top: 0.82, bottom: 0 },
        });
        volSeries.setData(
          prepared.map((b) => ({
            time: b.time as UTCTimestamp,
            value: b.volume || 1,
            color:
              b.close >= b.open
                ? "rgba(47, 184, 140, 0.25)"
                : "rgba(224, 86, 111, 0.25)",
          })),
        );
      }

      if (showEmaCross) {
        const f = chart.addSeries(LineSeries, {
          color: "#2FB88C",
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
          color: "#D4A24C",
          lineWidth: 1,
        });
        vw.setData(computeVWAP(prepared));
      }

      chart.subscribeCrosshairMove((param) => {
        if (!param.time || typeof param.time !== "number") {
          setHoverBar(null);
          return;
        }
        const hit = barByTime.get(param.time);
        setHoverBar(hit ?? null);
      });

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
    interval,
    showRanges,
    showOutcomes,
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
  const legendBar = hoverBar ?? lastBar;
  const markUsd =
    hoverBar?.close ?? round?.markPriceUsd ?? lastBar?.close ?? 94265;
  const moveBps = round?.moveBps ?? 0;
  const activeStyleLabel =
    CHART_MODES.find((m) => m.id === styleMode)?.label ?? "Candles";
  const activeIntervalLabel =
    INTERVALS.find(([k]) => k === interval)?.[1] ?? interval;

  const indicatorList = [
    {
      key: "ranges",
      label: "Range Lines",
      color: "#2FB88C",
      active: showRanges,
      toggle: () => setShowRanges((v) => !v),
    },
    {
      key: "outcomes",
      label: "Round Outcomes",
      color: "#33A9B8",
      active: showOutcomes,
      toggle: () => setShowOutcomes((v) => !v),
    },
    {
      key: "volume",
      label: "Volume",
      color: "#6F7889",
      active: showVolume,
      toggle: () => setShowVolume((v) => !v),
    },
    {
      key: "ema",
      label: "EMA 9 / 21",
      color: "#2FB88C",
      active: showEmaCross,
      toggle: () => setShowEmaCross((v) => !v),
    },
    {
      key: "bb",
      label: "Bollinger Bands",
      color: "#868FA2",
      active: showBollinger,
      toggle: () => setShowBollinger((v) => !v),
    },
    {
      key: "vwap",
      label: "VWAP",
      color: "#D4A24C",
      active: showVwap,
      toggle: () => setShowVwap((v) => !v),
    },
    {
      key: "atr",
      label: "Avg 10m Move",
      color: "#D4A24C",
      active: showAtr,
      toggle: () => setShowAtr((v) => !v),
    },
    {
      key: "rsi",
      label: "RSI (14)",
      color: "#33A9B8",
      active: showRsi,
      toggle: () => setShowRsi((v) => !v),
    },
  ];
  const activeIndicatorCount = indicatorList.filter((i) => i.active).length;

  const pulseWeights =
    userWeightsBps && userWeightsBps.length === 5
      ? userWeightsBps
      : [2000, 2000, 2000, 2000, 2000];
  const winIdx =
    moveBps < -(round?.outerBps ?? 20)
      ? 0
      : moveBps < -(round?.innerBps ?? 5)
        ? 1
        : moveBps < (round?.innerBps ?? 5)
          ? 2
          : moveBps < (round?.outerBps ?? 20)
            ? 3
            : 4;
  const pulseNeedlePct = ((winIdx + 0.5) / 5) * 100;

  // Derive historical range hit distribution from streak (fallback to crowdWeights).
  const pulseBucketPcts = useMemo(() => {
    if (streak.length >= 5) {
      const counts = [0, 0, 0, 0, 0];
      for (const s of streak.slice(0, 24)) {
        if (s.winningRange >= 0 && s.winningRange < 5) {
          counts[s.winningRange]++;
        }
      }
      const total = counts.reduce((a, b) => a + b, 0);
      if (total > 0) return counts.map((c) => (c / total) * 100);
    }
    return crowdWeights.map((w) => w / 100);
  }, [streak, crowdWeights]);

  const pulseSkew = useMemo(() => {
    const bull = pulseBucketPcts[3] + pulseBucketPcts[4];
    const bear = pulseBucketPcts[0] + pulseBucketPcts[1];
    if (bull - bear > 6) return "Bullish";
    if (bear - bull > 6) return "Bearish";
    return "Balanced";
  }, [pulseBucketPcts]);

  const pulseToWeights = () => {
    const total = pulseBucketPcts.reduce((a, b) => a + b, 0);
    if (total <= 0) return [2000, 2000, 2000, 2000, 2000];
    const raw = pulseBucketPcts.map((p) => Math.round((p / total) * 100) * 100);
    const diff = 10000 - raw.reduce((a, b) => a + b, 0);
    raw[2] = Math.max(0, raw[2] + diff);
    return raw;
  };

  const ivSec = INTERVAL_SECONDS[interval] ?? 60;
  const barSecLeft = ivSec - (Math.floor(nowMs / 1000) % ivSec);
  const clockLabel = formatClockTime(nowMs);

  const showTip = (entry: StreakEntry, el: HTMLElement) => {
    const strip = streakStripRef.current;
    if (!strip) return;
    const sRect = strip.getBoundingClientRect();
    const cRect = el.getBoundingClientRect();
    const rawX = cRect.left - sRect.left + cRect.width / 2;
    const x = Math.max(76, Math.min(Math.max(76, sRect.width - 76), rawX));
    setStreakTip({ entry, x });
  };

  return (
    <section
      className="retail-market-chart mine-chart-card"
      aria-label="BTC USD chart"
    >
      <div className="rmc-tv-topbar" ref={topbarRef}>
        <div
          className="rmc-tv-tools"
          role="toolbar"
          aria-label="Chart controls"
        >
          <div className="rmc-dd-wrap">
            <button
              type="button"
              className={`rmc-dd-trigger${openMenu === "interval" ? " is-open" : ""}`}
              aria-label="Chart timeframe"
              aria-haspopup="menu"
              aria-expanded={openMenu === "interval"}
              title={`Timeframe: ${activeIntervalLabel}`}
              onClick={() =>
                setOpenMenu((v) => (v === "interval" ? null : "interval"))
              }
            >
              <ChartIcon kind="clock" />
              <span>{activeIntervalLabel}</span>
              <i className="rmc-dd-caret" aria-hidden="true">
                ▾
              </i>
            </button>
            {openMenu === "interval" && (
              <div
                className="rmc-dd-menu rmc-dd-time"
                role="group"
                aria-label="Chart timeframe options"
              >
                {INTERVALS.map(([iv, lbl]) => (
                  <button
                    key={iv}
                    type="button"
                    className={interval === iv ? "is-selected" : ""}
                    aria-pressed={interval === iv}
                    onClick={() => {
                      onIntervalChange(iv);
                      setOpenMenu(null);
                    }}
                  >
                    {lbl}
                  </button>
                ))}
              </div>
            )}
          </div>

          <div className="rmc-dd-wrap">
            <button
              type="button"
              className={`rmc-dd-trigger${openMenu === "style" ? " is-open" : ""}`}
              aria-label="Chart style"
              aria-haspopup="menu"
              aria-expanded={openMenu === "style"}
              title={`Chart style: ${activeStyleLabel}`}
              onClick={() =>
                setOpenMenu((v) => (v === "style" ? null : "style"))
              }
            >
              <ChartIcon kind={styleMode} />
              <i className="rmc-dd-caret" aria-hidden="true">
                ▾
              </i>
            </button>
            {openMenu === "style" && (
              <div
                className="rmc-dd-menu"
                role="group"
                aria-label="Chart style options"
              >
                {CHART_MODES.map((m) => (
                  <button
                    key={m.id}
                    type="button"
                    className={`rmc-dd-item${styleMode === m.id ? " is-selected" : ""}`}
                    aria-pressed={styleMode === m.id}
                    onClick={() => {
                      setStyleMode(m.id);
                      setOpenMenu(null);
                    }}
                  >
                    <ChartIcon kind={m.id} />
                    <span className="rmc-dd-label">{m.label}</span>
                  </button>
                ))}
              </div>
            )}
          </div>

          {styleMode !== "pulse" && styleMode !== "technicals" && (
            <>
              <div className="rmc-dd-wrap">
                <button
                  type="button"
                  className={`rmc-dd-trigger${activeIndicatorCount > 0 ? " has-active" : ""}${openMenu === "indicators" ? " is-open" : ""}`}
                  aria-label="Chart indicators"
                  aria-haspopup="menu"
                  aria-expanded={openMenu === "indicators"}
                  title={`Indicators (${activeIndicatorCount})`}
                  onClick={() =>
                    setOpenMenu((v) =>
                      v === "indicators" ? null : "indicators",
                    )
                  }
                >
                  <ChartIcon kind="fx" />
                  {activeIndicatorCount > 0 && (
                    <em className="rmc-dd-badge">{activeIndicatorCount}</em>
                  )}
                  <i className="rmc-dd-caret" aria-hidden="true">
                    ▾
                  </i>
                </button>
                {openMenu === "indicators" && (
                  <div
                    className="rmc-dd-menu"
                    role="group"
                    aria-label="Chart indicator toggles"
                  >
                    {indicatorList.map((ind) => (
                      <button
                        key={ind.key}
                        type="button"
                        className={`rmc-dd-item${ind.active ? " is-selected" : ""}`}
                        aria-pressed={ind.active}
                        onClick={ind.toggle}
                      >
                        <span
                          className="rmc-dd-check"
                          style={{
                            borderColor: ind.active ? ind.color : undefined,
                            color: ind.active ? ind.color : undefined,
                          }}
                        >
                          {ind.active ? "✓" : ""}
                        </span>
                        <span className="rmc-dd-label">{ind.label}</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>

              <button
                type="button"
                className="rmc-dd-trigger rmc-reset-btn"
                aria-label="Reset chart"
                title="Reset chart defaults"
                onClick={() => {
                  setStyleMode("candles");
                  setShowRanges(true);
                  setShowOutcomes(true);
                  setShowVolume(true);
                  setShowEmaCross(false);
                  setShowBollinger(false);
                  setShowVwap(false);
                  setShowAtr(false);
                  setShowRsi(false);
                  setOpenMenu(null);
                }}
              >
                <ChartIcon kind="reset" />
              </button>
            </>
          )}
        </div>

        <div className="rmc-tv-head-right">
          <div className="rmc-quote">
            <strong className="rmc-price">{formatPriceUsd(markUsd)}</strong>
          </div>
          <div className="rmc-meta">
            <span className="rmc-live">
              <i aria-hidden="true" />
              <span>{clockLabel}</span>
            </span>
          </div>
        </div>
      </div>

      {styleMode === "pulse" ? (
        <div className="rmc-pulse" aria-label="Range pulse histogram">
          <div className="rmc-pulse-head">
            <div className="rmc-pulse-badges">
              <span className={`rmc-regime is-${pulseSkew.toLowerCase()}`}>
                {pulseSkew}
              </span>
              <span className="rmc-edge-legend">
                <i className="is-hit" aria-hidden="true" />
                <span>Hit rate</span>
                <i className="is-curve" aria-hidden="true" />
                <span>Your curve</span>
              </span>
            </div>
            {onApplyPresetFromTechnicals && (
              <button
                type="button"
                className="rmc-pulse-apply"
                onClick={() => onApplyPresetFromTechnicals(pulseToWeights())}
              >
                Apply
              </button>
            )}
          </div>
          <div className="rmc-meter-stage">
            <div className="rmc-meter-grid" aria-hidden="true">
              <span />
              <span />
              <span />
            </div>
            <div
              className="rmc-meter-marker"
              style={{ left: `${pulseNeedlePct}%` }}
            >
              <span className="rmc-meter-tag">
                {moveBps >= 0 ? "+" : ""}
                {(moveBps / 100).toFixed(2)}% · {RANGE_LETTERS[winIdx]}
              </span>
              <span className="rmc-meter-line" />
            </div>
            <div className="rmc-meter-bars">
              {RANGE_LETTERS.map((code, rIdx) => {
                const hitPct = pulseBucketPcts[rIdx] ?? 20;
                const userPct = (pulseWeights[rIdx] ?? 2000) / 100;
                const fillH = Math.min(100, Math.max(6, hitPct * 2.2));
                const ghostH = Math.min(100, Math.max(4, userPct * 2.2));
                return (
                  <div
                    key={code}
                    className={`rmc-meter-group${rIdx === winIdx ? " is-current" : ""}`}
                  >
                    {Array.from({ length: 16 }, (_, sIdx) => {
                      const taper =
                        0.82 + 0.3 * Math.sin(((sIdx + 0.5) / 16) * Math.PI);
                      return (
                        <div key={sIdx} className="rmc-meter-slot">
                          <span
                            className="rmc-meter-ghost"
                            style={{
                              height: `${Math.min(100, ghostH * taper)}%`,
                            }}
                          />
                          <i
                            className="rmc-meter-fill has-fill"
                            style={{
                              height: `${Math.min(100, fillH * taper)}%`,
                              background: RANGE_COLORS[rIdx],
                            }}
                          />
                        </div>
                      );
                    })}
                  </div>
                );
              })}
            </div>
          </div>
          <div className="rmc-meter-axis">
            {RANGE_LETTERS.map((code, idx) => {
              const hitPct = pulseBucketPcts[idx] ?? 20;
              const userPct = (pulseWeights[idx] ?? 2000) / 100;
              const delta = Math.round(hitPct - userPct);
              return (
                <div
                  key={code}
                  className={`rmc-meter-col${idx === winIdx ? " is-current" : ""}`}
                >
                  <div className="rmc-meter-col-top">
                    <strong style={{ color: RANGE_TEXT_COLORS[idx] }}>
                      {code}
                    </strong>
                    <span className="rmc-pulse-pct">
                      {hitPct.toFixed(0)}%{" "}
                      <em
                        className={
                          delta > 3 ? "is-pos" : delta < -3 ? "is-neg" : ""
                        }
                      >
                        {delta > 0 ? `+${delta}` : `${delta}`}
                      </em>
                    </span>
                  </div>
                  <small>Your {userPct.toFixed(0)}%</small>
                </div>
              );
            })}
          </div>
        </div>
      ) : styleMode === "technicals" ? (
        <MineTechnicalsView
          bars={prepared}
          intervalLabel={activeIntervalLabel}
          onApplyCurve={onApplyPresetFromTechnicals}
        />
      ) : (
        <div className="rmc-tv-wrap">
          {legendBar && (
            <div className="rmc-tv-ohlc" aria-hidden="true">
              <span>
                O <em>{formatPriceUsd(legendBar.open)}</em>
              </span>
              <span>
                H <em>{formatPriceUsd(legendBar.high)}</em>
              </span>
              <span>
                L <em>{formatPriceUsd(legendBar.low)}</em>
              </span>
              <span>
                C <em>{formatPriceUsd(legendBar.close)}</em>
              </span>
              {showVolume && legendBar.volume > 0 && (
                <span className="rmc-vol-stat">
                  Vol <em>{formatVol(legendBar.volume)} BTC</em>
                </span>
              )}
              {showEmaCross && (
                <span className="rmc-ind-tag" style={{ color: "#2FB88C" }}>
                  EMA 9/21
                </span>
              )}
              {showBollinger && (
                <span className="rmc-ind-tag" style={{ color: "#868FA2" }}>
                  BB
                </span>
              )}
              {showVwap && (
                <span className="rmc-ind-tag" style={{ color: "#D4A24C" }}>
                  VWAP
                </span>
              )}
              {showAtr && atrBps !== null && (
                <span className="rmc-ind-tag" style={{ color: "#D4A24C" }}>
                  Avg 10m {(atrBps / 100).toFixed(2)}%
                </span>
              )}
              {showRsi && rsi14 !== null && (
                <span className="rmc-ind-tag" style={{ color: "#33A9B8" }}>
                  RSI {rsi14.toFixed(1)}
                </span>
              )}
            </div>
          )}
          <div
            ref={hostRef}
            className="rmc-tv-canvas"
            aria-label="BTC USD mining chart"
          />
          {plannedSlots.length > 0 && (
            <div className="rmc-tv-bottom-pills">
              <span className="rmc-planned-pill">
                <i>•</i> Planned: #{plannedSlots[0]}
                {plannedSlots.length > 1
                  ? ` (+${plannedSlots.length - 1})`
                  : ""}
              </span>
            </div>
          )}
          <div className="rmc-tv-countdown" aria-hidden="true">
            <small>{activeIntervalLabel.toUpperCase()}</small>
            <b>{formatBarCountdown(barSecLeft)}</b>
          </div>
        </div>
      )}

      {styleMode !== "pulse" && styleMode !== "technicals" && (
        <div
          className="rmc-sentiment-footer"
          aria-label="Recent round distribution and outcomes"
        >
          <div className="rmc-sentiment-row">
            <div className="rmc-sentiment-tags">
              <span
                className={`rmc-regime rmc-regime-icon is-${pulseSkew.toLowerCase()}`}
                role="img"
                aria-label={pulseSkew}
                title={pulseSkew}
              >
                <SkewIcon skew={pulseSkew} />
              </span>
            </div>
            <div
              className="rmc-inline-meter"
              role="img"
              aria-label={RANGE_LETTERS.map(
                (l, i) => `${l} ${Math.round(pulseBucketPcts[i] ?? 0)}%`,
              ).join(", ")}
            >
              <div className="rmc-inline-bars">
                {RANGE_LETTERS.map((letter, rIdx) => {
                  const pct = pulseBucketPcts[rIdx] ?? 0;
                  const maxP = Math.max(...pulseBucketPcts, 1);
                  const baseH = (pct / maxP) * 100;
                  return (
                    <div
                      key={letter}
                      className={`rmc-inline-group${winIdx === rIdx ? " is-current" : ""}`}
                      title={`${letter} ${pct.toFixed(1)}%`}
                    >
                      {Array.from({ length: 6 }, (_, bIdx) => {
                        const taper =
                          0.78 + 0.32 * Math.sin(((bIdx + 0.5) / 6) * Math.PI);
                        const h = Math.min(100, baseH * taper);
                        return (
                          <i
                            key={bIdx}
                            style={{
                              height: `${Math.max(h > 0 ? 10 : 0, h)}%`,
                              background: RANGE_COLORS[rIdx],
                            }}
                          />
                        );
                      })}
                    </div>
                  );
                })}
              </div>
              <i
                className="rmc-inline-needle"
                style={{
                  left: `${Math.max(2, Math.min(98, pulseNeedlePct))}%`,
                }}
                aria-hidden="true"
              />
            </div>
            <div className="rmc-inline-stats">
              {RANGE_LETTERS.map((letter, rIdx) => (
                <span
                  key={letter}
                  className={winIdx === rIdx ? "is-current" : undefined}
                >
                  <b style={{ color: RANGE_TEXT_COLORS[rIdx] }}>{letter}</b>{" "}
                  {Math.round(pulseBucketPcts[rIdx] ?? 0)}%
                </span>
              ))}
            </div>
            {onApplyPresetFromTechnicals && (
              <button
                type="button"
                className="rmc-sentiment-apply"
                title="Match curve to recent round distribution"
                onClick={() => onApplyPresetFromTechnicals(pulseToWeights())}
              >
                Apply
              </button>
            )}
          </div>

          {streak.length > 0 && (
            <div className="rmc-streak-strip" ref={streakStripRef}>
              {streakTip && (
                <div
                  className="rmc-streak-tip"
                  role="tooltip"
                  style={{ left: `${streakTip.x}px` }}
                >
                  <span className="rmc-muted">
                    #{streakTip.entry.roundSlot}
                    {streakTip.entry.settledAt > 0
                      ? ` · ${formatHm((streakTip.entry.settledAt - 600) * 1000)}–${formatHm(streakTip.entry.settledAt * 1000)}`
                      : ""}
                  </span>
                  <b
                    style={{
                      color:
                        RANGE_TEXT_COLORS[streakTip.entry.winningRange] ??
                        "var(--wt-muted)",
                    }}
                  >
                    {streakTip.entry.code} (
                    {streakTip.entry.moveBps >= 0 ? "+" : ""}
                    {(streakTip.entry.moveBps / 100).toFixed(2)}%)
                  </b>
                </div>
              )}
              <div
                className="rmc-streak-cells"
                role="group"
                aria-label="Recent round outcomes"
              >
                {streak.slice(0, 24).map((entry) => {
                  const isSelected = selectedStreakSlot === entry.roundSlot;
                  const accent =
                    RANGE_TEXT_COLORS[entry.winningRange] ?? "var(--wt-muted)";
                  return (
                    <button
                      key={entry.roundSlot}
                      type="button"
                      className={`rmc-streak-cell${isSelected ? " is-selected" : ""}${entry.userStaked ? " is-user" : ""}`}
                      aria-pressed={isSelected}
                      style={{ color: accent }}
                      aria-label={`Round #${entry.roundSlot} ${entry.code} (${entry.moveBps >= 0 ? "+" : ""}${(entry.moveBps / 100).toFixed(2)}%)`}
                      onMouseEnter={(e) => showTip(entry, e.currentTarget)}
                      onFocus={(e) => showTip(entry, e.currentTarget)}
                      onMouseLeave={() => setStreakTip(null)}
                      onBlur={() => setStreakTip(null)}
                      onClick={() =>
                        setSelectedStreakSlot((cur) =>
                          cur === entry.roundSlot ? null : entry.roundSlot,
                        )
                      }
                    >
                      {entry.code}
                    </button>
                  );
                })}
              </div>
              {selectedStreakSlot !== null && (
                <button
                  type="button"
                  className="rmc-streak-clear"
                  onClick={() => setSelectedStreakSlot(null)}
                >
                  Live
                </button>
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
