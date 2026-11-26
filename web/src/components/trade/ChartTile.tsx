import { useEffect, useMemo, useRef, useState } from "react";
import { useCore } from "../../core";
import { useUI } from "../../ui-preferences";
import { Trash2 } from "../../icons";
import type { CrosshairBus, FasedStreamClient } from "../../stream-store";
import {
  evaluateLabIndicator,
  type LabScript,
  type PaperPosition,
  type ScriptPlotSeries,
  type ScriptSignalMarker,
} from "../../quantlab-desk";
import {
  CANDLE_STYLES,
  CHART_INTERVALS,
  CHART_VENUES,
  fetchCandles,
  formatCompactVolume,
  formatMarketPrice,
  type Candle,
  type CandleStyle,
  type ChartDrawing,
  type ChartTileConfig,
  type DrawingToolMode,
  type Interval,
  type PaperOrderPreview,
} from "../../market-data";
import {
  buildChartEventPins,
  matchEventsForSymbol,
  type IntelEvent,
} from "../../world-intel";
import type { TradeOrderPlan } from "../../trade-engine";
import {
  FasedChartAdapter,
  type ChartAdapterHandle,
} from "./FasedChartAdapter";

export function ChartTile({
  tile,
  active,
  drawings,
  paperOrders,
  streamClient,
  crosshairBus,
  syncCrosshair = true,
  labScripts = [],
  deskPositions = [],
  tradePlans = [],
  intelEvents = [],
  showEvents = true,
  onSelect,
  onChangeTile,
  onAddDrawing,
  onAddPaperOrder,
  onClearOverlays,
}: {
  tile: ChartTileConfig;
  active: boolean;
  drawings?: ChartDrawing[];
  paperOrders?: PaperOrderPreview[];
  streamClient?: FasedStreamClient;
  crosshairBus?: CrosshairBus;
  syncCrosshair?: boolean;
  labScripts?: LabScript[];
  deskPositions?: PaperPosition[];
  tradePlans?: TradeOrderPlan[];
  intelEvents?: IntelEvent[];
  showEvents?: boolean;
  onSelect: () => void;
  onChangeTile: (next: ChartTileConfig) => void;
  onAddDrawing?: (d: ChartDrawing) => void;
  onAddPaperOrder?: (o: PaperOrderPreview) => void;
  onClearOverlays?: () => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const [symbolDraft, setSymbolDraft] = useState(tile.symbol);
  const [candles, setCandles] = useState<Candle[]>([]);
  const [sourceLabel, setSourceLabel] = useState("loading");
  const [error, setError] = useState("");
  const [tool, setTool] = useState<DrawingToolMode>("none");
  const [scriptTimes, setScriptTimes] = useState<number[]>([]);
  const [scriptPlots, setScriptPlots] = useState<ScriptPlotSeries[]>([]);
  const [scriptSignals, setScriptSignals] = useState<ScriptSignalMarker[]>([]);

  const chartHandleRef = useRef<ChartAdapterHandle | null>(null);
  const priceTextRef = useRef<HTMLElement | null>(null);
  const volTextRef = useRef<HTMLSpanElement | null>(null);

  const activeDrawings = drawings ?? tile.drawings;
  const activeOrders = paperOrders ?? tile.paperOrders;
  const overlayCount = activeDrawings.length + activeOrders.length;
  const seriesKey = `${tile.venue}:${tile.symbol}:${tile.interval}:${tile.style}`;

  useEffect(() => {
    setSymbolDraft(tile.symbol);
  }, [tile.symbol]);

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;
    setCandles([]);
    setSourceLabel("loading");
    setError("");

    fetchCandles(
      core,
      tile.venue,
      tile.symbol,
      tile.interval,
      tile.style,
      160,
      controller.signal,
    )
      .then((res) => {
        if (!mounted) return;
        setCandles(res.candles);
        setSourceLabel(res.fixture ? `${res.source}` : `${tile.venue}`);
      })
      .catch((err) => {
        if (!mounted || controller.signal.aborted) return;
        setError(err instanceof Error ? err.message : "Failed to load candles");
      });

    return () => {
      mounted = false;
      controller.abort();
    };
  }, [
    core.mode,
    core.request,
    tile.venue,
    tile.symbol,
    tile.interval,
    tile.style,
  ]);

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    const topic = `candle:${tile.venue}:${tile.symbol}:${tile.interval}:${tile.style}`;
    return streamClient.subscribe(topic, (msg) => {
      const payload = msg.payload as
        Candle | { candles?: Candle[] } | undefined;
      if (!payload) return;
      if ("candles" in payload && Array.isArray(payload.candles)) {
        if (payload.candles.length > 0) {
          setCandles(payload.candles);
        }
        return;
      }
      if ("close" in payload && typeof payload.close === "number") {
        chartHandleRef.current?.updateCandle(payload);
        if (priceTextRef.current) {
          priceTextRef.current.textContent = formatMarketPrice(payload.close);
        }
        if (volTextRef.current) {
          volTextRef.current.textContent = `${t("Vol")} ${formatCompactVolume(payload.volume)}`;
        }
      }
    });
  }, [
    streamClient,
    core.mode,
    tile.venue,
    tile.symbol,
    tile.interval,
    tile.style,
    t,
  ]);

  useEffect(() => {
    if (!tile.scriptId || core.mode !== "ready") {
      setScriptTimes([]);
      setScriptPlots([]);
      setScriptSignals([]);
      return;
    }
    let cancelled = false;
    void evaluateLabIndicator(core, {
      scriptId: tile.scriptId,
      venue: tile.venue,
      symbol: tile.symbol,
      interval: tile.interval,
      limit: 160,
    })
      .then((res) => {
        if (cancelled) return;
        setScriptTimes(res.times ?? []);
        setScriptPlots(res.plots ?? []);
        setScriptSignals(res.signals ?? []);
      })
      .catch(() => {
        if (cancelled) return;
        setScriptTimes([]);
        setScriptPlots([]);
        setScriptSignals([]);
      });
    return () => {
      cancelled = true;
    };
  }, [
    core.mode,
    core.request,
    tile.scriptId,
    tile.venue,
    tile.symbol,
    tile.interval,
  ]);

  const latest = useMemo(() => candles[candles.length - 1] ?? null, [candles]);
  const matchedPins = useMemo(
    () =>
      showEvents && intelEvents.length > 0
        ? buildChartEventPins(
            candles,
            matchEventsForSymbol(intelEvents, tile.symbol),
          )
        : [],
    [candles, intelEvents, showEvents, tile.symbol],
  );

  function commitSymbol() {
    const clean = symbolDraft.trim();
    if (clean && clean !== tile.symbol) {
      onChangeTile({ ...tile, symbol: clean });
    } else {
      setSymbolDraft(tile.symbol);
    }
  }

  return (
    <article
      className={`trade-tile ${active ? "is-active" : ""}`}
      data-testid={`chart-tile-${tile.id}`}
      onMouseDown={onSelect}
    >
      <header className="trade-tile-bar">
        <select
          aria-label={t("Venue")}
          value={tile.venue}
          onChange={(e) => onChangeTile({ ...tile, venue: e.target.value })}
        >
          {CHART_VENUES.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>

        <input
          className="trade-symbol-input"
          aria-label={t("Symbol")}
          value={symbolDraft}
          onChange={(e) => setSymbolDraft(e.target.value)}
          onBlur={commitSymbol}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              commitSymbol();
            }
          }}
        />

        <select
          aria-label={t("Interval")}
          value={tile.interval}
          onChange={(e) =>
            onChangeTile({ ...tile, interval: e.target.value as Interval })
          }
        >
          {CHART_INTERVALS.map((iv) => (
            <option key={iv} value={iv}>
              {iv}
            </option>
          ))}
        </select>

        <select
          aria-label={t("Style")}
          value={tile.style}
          onChange={(e) =>
            onChangeTile({ ...tile, style: e.target.value as CandleStyle })
          }
        >
          {CANDLE_STYLES.map((st) => (
            <option key={st.id} value={st.id}>
              {t(st.label)}
            </option>
          ))}
        </select>

        <select
          aria-label={t("Drawing tool")}
          value={tool}
          onChange={(e) => setTool(e.target.value as DrawingToolMode)}
        >
          <option value="none">{t("Cursor")}</option>
          <option value="hline">{t("H-Line")}</option>
          <option value="trendline">{t("Trendline")}</option>
          <option value="fib">{t("Fibonacci")}</option>
          <option value="paper_buy">{t("Paper buy")}</option>
          <option value="paper_sell">{t("Paper sell")}</option>
        </select>

        {labScripts.length > 0 && (
          <select
            aria-label={t("Indicator script")}
            value={tile.scriptId ?? ""}
            onChange={(e) =>
              onChangeTile({
                ...tile,
                scriptId: e.target.value || undefined,
              })
            }
          >
            <option value="">{t("None")}</option>
            {labScripts.map((sc) => (
              <option key={sc.id} value={sc.id}>
                {sc.name}
              </option>
            ))}
          </select>
        )}

        {overlayCount > 0 && (
          <button
            type="button"
            className="trade-mini-btn"
            aria-label={t("Clear drawings")}
            onClick={() => {
              if (onClearOverlays) {
                onClearOverlays();
              } else {
                onChangeTile({ ...tile, drawings: [], paperOrders: [] });
              }
            }}
          >
            <Trash2 size={13} />
          </button>
        )}
      </header>

      <div className="trade-ohlc-strip">
        {latest ? (
          <>
            <span>
              <strong ref={priceTextRef}>
                {formatMarketPrice(latest.close)}
              </strong>
            </span>
            <span ref={volTextRef}>
              {t("Vol")} {formatCompactVolume(latest.volume)}
            </span>
            <span className="trade-source-pill">{sourceLabel}</span>
            {overlayCount > 0 && (
              <span>
                {overlayCount} {t("overlays")}
              </span>
            )}
            {matchedPins.length > 0 && (
              <span
                className="trade-source-pill"
                data-testid={`chart-pins-${tile.id}`}
              >
                {matchedPins.length} {t("pins")}
              </span>
            )}
          </>
        ) : (
          <span>{error || t("Loading")}</span>
        )}
      </div>

      <div className="trade-tile-canvas">
        <FasedChartAdapter
          tileId={tile.id}
          seriesKey={seriesKey}
          candles={candles}
          tool={tool}
          drawings={activeDrawings}
          paperOrders={activeOrders}
          scriptTimes={scriptTimes}
          scriptPlots={scriptPlots}
          scriptSignals={scriptSignals}
          deskPositions={deskPositions}
          tradePlans={tradePlans}
          eventPins={matchedPins}
          crosshairBus={crosshairBus}
          syncCrosshair={syncCrosshair}
          onReadyHandle={(handle) => {
            chartHandleRef.current = handle;
          }}
          onAddDrawing={(d: ChartDrawing) => {
            if (onAddDrawing) {
              onAddDrawing(d);
            } else {
              onChangeTile({
                ...tile,
                drawings: [...tile.drawings, d].slice(-32),
              });
            }
          }}
          onAddPaperOrder={(o: PaperOrderPreview) => {
            if (onAddPaperOrder) {
              onAddPaperOrder(o);
            } else {
              onChangeTile({
                ...tile,
                paperOrders: [...tile.paperOrders, o].slice(-16),
              });
            }
          }}
        />
      </div>
    </article>
  );
}
