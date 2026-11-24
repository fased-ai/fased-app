import { useEffect, useRef } from "react";
import {
  CandlestickSeries,
  createChart,
  createSeriesMarkers,
  HistogramSeries,
  LineSeries,
  LineStyle,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from "lightweight-charts";
import type { ChartEventPin } from "../../world-intel";
import type { CrosshairBus } from "../../stream-store";
import {
  computeFibLevels,
  type Candle,
  type ChartDrawing,
  type CrosshairSyncPoint,
  type DrawingToolMode,
  type PaperOrderPreview,
} from "../../market-data";

export type ChartAdapterHandle = {
  updateCandle: (c: Candle) => void;
};

function pricePrecisionFor(price: number): {
  precision: number;
  minMove: number;
} {
  const abs = Math.abs(price);
  if (!Number.isFinite(abs) || abs === 0 || abs >= 100) {
    return { precision: 2, minMove: 0.01 };
  }
  if (abs >= 0.01) {
    return { precision: 4, minMove: 0.0001 };
  }
  if (abs >= 0.0001) {
    return { precision: 6, minMove: 0.000001 };
  }
  return { precision: 8, minMove: 0.00000001 };
}

function findNearestBarForTime(
  list: Candle[],
  targetTime: number,
): Candle | null {
  if (list.length === 0 || !Number.isFinite(targetTime)) return null;
  let lo = 0;
  let hi = list.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (list[mid].time < targetTime) lo = mid + 1;
    else hi = mid;
  }
  let best = list[lo];
  if (lo > 0) {
    const prev = list[lo - 1];
    if (Math.abs(prev.time - targetTime) < Math.abs(best.time - targetTime)) {
      best = prev;
    }
  }
  const avgStep =
    list.length >= 2
      ? Math.max(
          60,
          Math.abs(list[list.length - 1].time - list[0].time) /
            (list.length - 1),
        )
      : 3600;
  const maxGap = Math.max(avgStep, 3600);
  if (Math.abs(best.time - targetTime) > maxGap) return null;
  return best;
}

type ChartPalette = {
  surface: string;
  bg: string;
  border: string;
  text: string;
  muted: string;
  cyan: string;
  coral: string;
  violet: string;
};

function resolveThemePalette(): ChartPalette {
  const s = getComputedStyle(document.documentElement);
  const read = (name: string, fallback: string) =>
    s.getPropertyValue(name).trim() || fallback;
  return {
    surface: read("--wt-surface", "#161621"),
    bg: read("--wt-bg", "#0d0d12"),
    border: read("--wt-border", "#252536"),
    text: read("--wt-text", "#f5f5fa"),
    muted: read("--wt-muted", "#8f8fa6"),
    cyan: read("--wt-cyan", "#27c5da"),
    coral: read("--wt-coral", "#ff4f7c"),
    violet: read("--wt-violet", "#8b6fff"),
  };
}

export function FasedChartAdapter({
  tileId,
  seriesKey = tileId,
  candles,
  tool,
  drawings,
  paperOrders,
  eventPins = [],
  syncedCrosshair = null,
  crosshairBus,
  syncCrosshair = true,
  onReadyHandle,
  onCrosshairMove,
  onAddDrawing,
  onAddPaperOrder,
}: {
  tileId: string;
  seriesKey?: string;
  candles: Candle[];
  tool: DrawingToolMode;
  drawings: ChartDrawing[];
  paperOrders: PaperOrderPreview[];
  eventPins?: ChartEventPin[];
  syncedCrosshair?: CrosshairSyncPoint | null;
  crosshairBus?: CrosshairBus;
  syncCrosshair?: boolean;
  onReadyHandle?: (handle: ChartAdapterHandle | null) => void;
  onCrosshairMove?: (pt: CrosshairSyncPoint) => void;
  onAddDrawing: (d: ChartDrawing) => void;
  onAddPaperOrder: (o: PaperOrderPreview) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleSeriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volumeSeriesRef = useRef<ISeriesApi<"Histogram"> | null>(null);
  const candlesRef = useRef<Candle[]>([]);
  const fittedSeriesKeyRef = useRef<string>("");
  const seriesKeyRef = useRef(seriesKey);
  seriesKeyRef.current = seriesKey;
  const syncEnabledRef = useRef(syncCrosshair);
  syncEnabledRef.current = syncCrosshair;
  const priceLinesRef = useRef<IPriceLine[]>([]);
  const trendSeriesRef = useRef<ISeriesApi<"Line">[]>([]);
  const markersPluginRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);
  const paletteRef = useRef<ChartPalette>(resolveThemePalette());
  const pendingAnchorRef = useRef<{ time: number; price: number } | null>(null);
  const suppressEmitRef = useRef(false);

  const toolRef = useRef(tool);
  toolRef.current = tool;
  const callbacksRef = useRef({
    onReadyHandle,
    onCrosshairMove,
    onAddDrawing,
    onAddPaperOrder,
  });
  callbacksRef.current = {
    onReadyHandle,
    onCrosshairMove,
    onAddDrawing,
    onAddPaperOrder,
  };

  useEffect(() => {
    pendingAnchorRef.current = null;
  }, [tool]);

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const p = resolveThemePalette();
    paletteRef.current = p;

    const chart = createChart(el, {
      width: Math.max(el.clientWidth, 160),
      height: Math.max(el.clientHeight, 140),
      layout: {
        background: { color: p.surface },
        textColor: p.muted,
        fontFamily: "inherit",
        fontSize: 11,
        attributionLogo: false,
      },
      grid: {
        vertLines: { color: p.border },
        horzLines: { color: p.border },
      },
      rightPriceScale: {
        borderColor: p.border,
      },
      timeScale: {
        borderColor: p.border,
        timeVisible: true,
        secondsVisible: false,
      },
      crosshair: {
        vertLine: { color: p.violet, style: LineStyle.Dashed },
        horzLine: { color: p.violet, style: LineStyle.Dashed },
      },
    });

    const cSeries = chart.addSeries(CandlestickSeries, {
      upColor: p.cyan,
      downColor: p.coral,
      borderUpColor: p.cyan,
      borderDownColor: p.coral,
      wickUpColor: p.cyan,
      wickDownColor: p.coral,
    });

    const vSeries = chart.addSeries(HistogramSeries, {
      priceScaleId: "vol",
      priceFormat: { type: "volume" },
    });
    vSeries.priceScale().applyOptions({
      scaleMargins: { top: 0.82, bottom: 0 },
    });

    chartRef.current = chart;
    candleSeriesRef.current = cSeries;
    volumeSeriesRef.current = vSeries;
    markersPluginRef.current = createSeriesMarkers(cSeries, []);

    const applyTheme = () => {
      const next = resolveThemePalette();
      paletteRef.current = next;
      chart.applyOptions({
        layout: {
          background: { color: next.surface },
          textColor: next.muted,
        },
        grid: {
          vertLines: { color: next.border },
          horzLines: { color: next.border },
        },
        rightPriceScale: { borderColor: next.border },
        timeScale: { borderColor: next.border },
      });
      cSeries.applyOptions({
        upColor: next.cyan,
        downColor: next.coral,
        borderUpColor: next.cyan,
        borderDownColor: next.coral,
        wickUpColor: next.cyan,
        wickDownColor: next.coral,
      });
    };

    const mo = new MutationObserver(applyTheme);
    mo.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });

    const ro = new ResizeObserver((entries) => {
      const rect = entries[0]?.contentRect;
      if (!rect || rect.width <= 0 || rect.height <= 0) return;
      chart.resize(Math.floor(rect.width), Math.floor(rect.height));
    });
    ro.observe(el);

    chart.subscribeCrosshairMove((param) => {
      if (suppressEmitRef.current) return;
      const t = typeof param.time === "number" ? param.time : null;
      const rawPoint = param.point
        ? cSeries.coordinateToPrice(param.point.y)
        : null;
      const pt: CrosshairSyncPoint = {
        sourceId: tileId,
        time: t,
        price: typeof rawPoint === "number" ? rawPoint : null,
      };
      if (syncEnabledRef.current) {
        crosshairBus?.publish(pt);
      }
      callbacksRef.current.onCrosshairMove?.(pt);
    });

    chart.subscribeClick((param) => {
      const activeTool = toolRef.current;
      if (activeTool === "none" || !param.point) return;
      const price = cSeries.coordinateToPrice(param.point.y);
      if (typeof price !== "number" || !Number.isFinite(price)) return;
      const { precision } = pricePrecisionFor(price);
      const snappedPrice = Number(price.toFixed(precision));
      const clickTime =
        typeof param.time === "number"
          ? param.time
          : Math.floor(Date.now() / 1000);

      if (activeTool === "hline") {
        callbacksRef.current.onAddDrawing({
          id: `draw-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
          kind: "hline",
          p1: { time: clickTime, price: snappedPrice },
        });
        return;
      }

      if (activeTool === "paper_buy" || activeTool === "paper_sell") {
        callbacksRef.current.onAddPaperOrder({
          id: `paper-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
          side: activeTool === "paper_buy" ? "buy" : "sell",
          price: snappedPrice,
          size: 1,
          label: activeTool === "paper_buy" ? "PAPER BUY" : "PAPER SELL",
        });
        return;
      }

      if (activeTool === "trendline" || activeTool === "fib") {
        if (!pendingAnchorRef.current) {
          pendingAnchorRef.current = { time: clickTime, price: snappedPrice };
          return;
        }
        const p1 = pendingAnchorRef.current;
        pendingAnchorRef.current = null;
        callbacksRef.current.onAddDrawing({
          id: `draw-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
          kind: activeTool,
          p1,
          p2: { time: clickTime, price: snappedPrice },
        });
      }
    });

    callbacksRef.current.onReadyHandle?.({
      updateCandle: (c: Candle) => {
        const list = candlesRef.current;
        const pal = paletteRef.current;
        if (list.length === 0) {
          candlesRef.current = [c];
          const fmt = pricePrecisionFor(c.close);
          cSeries.applyOptions({
            priceFormat: { type: "price", ...fmt },
          });
          cSeries.setData([
            {
              time: c.time as UTCTimestamp,
              open: c.open,
              high: c.high,
              low: c.low,
              close: c.close,
            },
          ]);
          vSeries.setData([
            {
              time: c.time as UTCTimestamp,
              value: c.volume,
              color: c.close >= c.open ? pal.cyan : pal.coral,
            },
          ]);
          if (fittedSeriesKeyRef.current !== seriesKeyRef.current) {
            chart.timeScale().fitContent();
            fittedSeriesKeyRef.current = seriesKeyRef.current;
          }
          return;
        }
        const last = list[list.length - 1];
        if (c.time < last.time) return;
        if (c.time === last.time) {
          list[list.length - 1] = c;
        } else {
          list.push(c);
          if (list.length > 200) list.shift();
        }
        cSeries.update({
          time: c.time as UTCTimestamp,
          open: c.open,
          high: c.high,
          low: c.low,
          close: c.close,
        });
        vSeries.update({
          time: c.time as UTCTimestamp,
          value: c.volume,
          color: c.close >= c.open ? pal.cyan : pal.coral,
        });
      },
    });

    return () => {
      callbacksRef.current.onReadyHandle?.(null);
      ro.disconnect();
      mo.disconnect();
      markersPluginRef.current?.detach();
      markersPluginRef.current = null;
      priceLinesRef.current = [];
      trendSeriesRef.current = [];
      candleSeriesRef.current = null;
      volumeSeriesRef.current = null;
      chartRef.current = null;
      candlesRef.current = [];
      fittedSeriesKeyRef.current = "";
      chart.remove();
    };
  }, [tileId, crosshairBus]);

  useEffect(() => {
    const cSeries = candleSeriesRef.current;
    const vSeries = volumeSeriesRef.current;
    const chart = chartRef.current;
    if (!cSeries || !vSeries || !chart) return;

    const p = paletteRef.current;
    const sorted = [...candles].sort((a, b) => a.time - b.time);
    const deduped: Candle[] = [];
    for (const c of sorted) {
      if (deduped.length > 0 && c.time === deduped[deduped.length - 1].time) {
        deduped[deduped.length - 1] = c;
      } else {
        deduped.push(c);
      }
    }
    candlesRef.current = deduped;

    if (deduped.length === 0) {
      fittedSeriesKeyRef.current = "";
      cSeries.setData([]);
      vSeries.setData([]);
      return;
    }

    const refPrice = deduped[deduped.length - 1].close;
    const fmt = pricePrecisionFor(refPrice);
    cSeries.applyOptions({
      priceFormat: { type: "price", ...fmt },
    });

    cSeries.setData(
      deduped.map((c) => ({
        time: c.time as UTCTimestamp,
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
      })),
    );
    vSeries.setData(
      deduped.map((c) => ({
        time: c.time as UTCTimestamp,
        value: c.volume,
        color: c.close >= c.open ? p.cyan : p.coral,
      })),
    );
    if (fittedSeriesKeyRef.current !== seriesKey) {
      chart.timeScale().fitContent();
      fittedSeriesKeyRef.current = seriesKey;
    }
  }, [candles, seriesKey]);

  useEffect(() => {
    const markersPlugin = markersPluginRef.current;
    if (!markersPlugin) return;
    if (!candles.length || !eventPins.length) {
      markersPlugin.setMarkers([]);
      return;
    }
    const p = paletteRef.current;
    const markers: SeriesMarker<Time>[] = eventPins.map((pin) => ({
      time: pin.time as UTCTimestamp,
      position: "aboveBar",
      shape:
        pin.sentiment === "bullish"
          ? "arrowUp"
          : pin.sentiment === "bearish"
            ? "arrowDown"
            : "circle",
      color:
        pin.urgency >= 0.7 || pin.sentiment === "bearish"
          ? p.coral
          : pin.sentiment === "bullish"
            ? p.cyan
            : p.violet,
      text: pin.label,
    }));
    markersPlugin.setMarkers(markers);
  }, [candles.length, eventPins]);

  useEffect(() => {
    const cSeries = candleSeriesRef.current;
    const chart = chartRef.current;
    if (!cSeries || !chart) return;

    for (const line of priceLinesRef.current) {
      try {
        cSeries.removePriceLine(line);
      } catch {}
    }
    priceLinesRef.current = [];

    for (const ts of trendSeriesRef.current) {
      try {
        chart.removeSeries(ts);
      } catch {}
    }
    trendSeriesRef.current = [];

    const p = paletteRef.current;

    for (const d of drawings) {
      if (d.kind === "hline") {
        priceLinesRef.current.push(
          cSeries.createPriceLine({
            price: d.p1.price,
            color: p.violet,
            lineWidth: 1,
            lineStyle: LineStyle.Solid,
            axisLabelVisible: true,
            title: d.label || "H-LINE",
          }),
        );
      } else if (d.kind === "fib" && d.p2) {
        for (const lv of computeFibLevels(d.p1.price, d.p2.price)) {
          priceLinesRef.current.push(
            cSeries.createPriceLine({
              price: lv.price,
              color: p.muted,
              lineWidth: 1,
              lineStyle: LineStyle.Dotted,
              axisLabelVisible: true,
              title: lv.label,
            }),
          );
        }
      } else if (d.kind === "trendline" && d.p2 && d.p1.time !== d.p2.time) {
        const pts = [d.p1, d.p2].sort((a, b) => a.time - b.time);
        const line = chart.addSeries(LineSeries, {
          color: p.violet,
          lineWidth: 2,
          priceLineVisible: false,
          lastValueVisible: false,
        });
        line.setData(
          pts.map((pt) => ({
            time: pt.time as UTCTimestamp,
            value: pt.price,
          })),
        );
        trendSeriesRef.current.push(line);
      }
    }

    for (const ord of paperOrders) {
      priceLinesRef.current.push(
        cSeries.createPriceLine({
          price: ord.price,
          color: ord.side === "buy" ? p.cyan : p.coral,
          lineWidth: 1,
          lineStyle: LineStyle.Dashed,
          axisLabelVisible: true,
          title: `${ord.label} (${ord.size})`,
        }),
      );
    }
  }, [drawings, paperOrders]);

  useEffect(() => {
    const applySync = (pt: CrosshairSyncPoint | null) => {
      const chart = chartRef.current;
      const cSeries = candleSeriesRef.current;
      if (!chart || !cSeries) return;
      if (!syncEnabledRef.current || !pt) {
        chart.clearCrosshairPosition();
        return;
      }
      if (pt.sourceId === tileId) return;
      suppressEmitRef.current = true;
      try {
        if (pt.time !== null && candlesRef.current.length > 0) {
          const match = findNearestBarForTime(candlesRef.current, pt.time);
          if (match) {
            chart.setCrosshairPosition(
              match.close,
              match.time as UTCTimestamp,
              cSeries,
            );
          } else {
            chart.clearCrosshairPosition();
          }
        } else {
          chart.clearCrosshairPosition();
        }
      } catch {
        /* ignore out-of-range timestamps across disparate intervals */
      } finally {
        suppressEmitRef.current = false;
      }
    };

    if (crosshairBus) {
      return crosshairBus.subscribe(applySync);
    }
    applySync(syncedCrosshair);
  }, [crosshairBus, syncedCrosshair, tileId, candles.length, syncCrosshair]);

  return <div ref={hostRef} className="trade-chart-host" />;
}
