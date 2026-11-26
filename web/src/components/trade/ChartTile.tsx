import { useEffect, useMemo, useRef, useState } from "react";
import { useCore } from "../../core";
import { useUI } from "../../ui-preferences";
import {
  CandlestickChart,
  ChevronDown,
  Pencil,
  SlidersHorizontal,
  Trash2,
} from "../../icons";
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
  fetchFootprint,
  formatCompactVolume,
  formatMarketPrice,
  type Candle,
  type CandleStyle,
  type ChartDrawing,
  type ChartTileConfig,
  type DrawingToolMode,
  type FootprintBar,
  type FootprintSeries,
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
import { FootprintCanvas } from "./OrderflowPane";

type TileMenu = "symbol" | "style" | "tools" | "indicators" | null;

export function ChartTile({
  tile,
  active,
  enabledVenues,
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
  enabledVenues?: string[];
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
  const [venueDraft, setVenueDraft] = useState(tile.venue);
  const [candles, setCandles] = useState<Candle[]>([]);
  const [footprint, setFootprint] = useState<FootprintSeries | null>(null);
  const [sourceLabel, setSourceLabel] = useState("loading");
  const [error, setError] = useState("");
  const [tool, setTool] = useState<DrawingToolMode>("none");
  const [openMenu, setOpenMenu] = useState<TileMenu>(null);
  const [scriptTimes, setScriptTimes] = useState<number[]>([]);
  const [scriptPlots, setScriptPlots] = useState<ScriptPlotSeries[]>([]);
  const [scriptSignals, setScriptSignals] = useState<ScriptSignalMarker[]>([]);

  const headerRef = useRef<HTMLElement | null>(null);
  const chartHandleRef = useRef<ChartAdapterHandle | null>(null);
  const priceTextRef = useRef<HTMLElement | null>(null);
  const volTextRef = useRef<HTMLSpanElement | null>(null);

  const venueList =
    enabledVenues && enabledVenues.length > 0 ? enabledVenues : [...CHART_VENUES];
  const activeDrawings = drawings ?? tile.drawings;
  const activeOrders = paperOrders ?? tile.paperOrders;
  const overlayCount = activeDrawings.length + activeOrders.length;
  const seriesKey = `${tile.venue}:${tile.symbol}:${tile.interval}:${tile.style}`;
  const isFootprint = tile.style === "footprint";

  useEffect(() => {
    setSymbolDraft(tile.symbol);
    setVenueDraft(tile.venue);
  }, [tile.symbol, tile.venue]);

  useEffect(() => {
    if (!openMenu) return;
    const onDocDown = (e: Event) => {
      if (!headerRef.current?.contains(e.target as Node)) setOpenMenu(null);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpenMenu(null);
    };
    document.addEventListener("pointerdown", onDocDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDocDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [openMenu]);

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;
    setCandles([]);
    setFootprint(null);
    setSourceLabel("loading");
    setError("");

    if (isFootprint) {
      fetchFootprint(
        core,
        tile.venue,
        tile.symbol,
        tile.interval,
        24,
        controller.signal,
      )
        .then((res) => {
          if (!mounted) return;
          setFootprint(res);
          setSourceLabel(res.fixture ? res.source : tile.venue);
        })
        .catch((err) => {
          if (!mounted || controller.signal.aborted) return;
          setError(
            err instanceof Error ? err.message : "Failed to load footprint",
          );
        });
    } else {
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
          setError(
            err instanceof Error ? err.message : "Failed to load candles",
          );
        });
    }

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
    isFootprint,
  ]);

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    if (isFootprint) {
      return streamClient.subscribe(
        `footprint:${tile.venue}:${tile.symbol}:${tile.interval}`,
        (msg) => {
          const bar = msg.payload as FootprintBar | undefined;
          if (!bar || typeof bar.time !== "number") return;
          setFootprint((prev) => {
            if (!prev) return prev;
            const bars = [...prev.bars];
            const idx = bars.findIndex((b) => b.time === bar.time);
            if (idx >= 0) bars[idx] = bar;
            else bars.push(bar);
            return { ...prev, bars: bars.slice(-48) };
          });
        },
      );
    }
    const topic = `candle:${tile.venue}:${tile.symbol}:${tile.interval}:${tile.style}`;
    return streamClient.subscribe(topic, (msg) => {
      const payload = msg.payload as
        | Candle
        | { candles?: Candle[] }
        | undefined;
      if (!payload) return;
      if ("candles" in payload && Array.isArray(payload.candles)) {
        if (payload.candles.length > 0) setCandles(payload.candles);
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
    isFootprint,
    t,
  ]);

  useEffect(() => {
    if (!tile.scriptId || core.mode !== "ready" || isFootprint) {
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
    isFootprint,
  ]);

  const latest = useMemo(() => {
    if (isFootprint) {
      const bar = footprint?.bars?.[footprint.bars.length - 1];
      return bar
        ? { close: bar.close, volume: bar.volume }
        : null;
    }
    return candles[candles.length - 1] ?? null;
  }, [candles, footprint, isFootprint]);

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

  function applySymbolForm(e: React.FormEvent) {
    e.preventDefault();
    const clean = symbolDraft.trim();
    if (clean) {
      onChangeTile({ ...tile, venue: venueDraft, symbol: clean });
    }
    setOpenMenu(null);
  }

  return (
    <article
      className={`trade-tile ${active ? "is-active" : ""}`}
      data-testid={`chart-tile-${tile.id}`}
      onMouseDown={onSelect}
    >
      <header className="trade-tile-bar" ref={headerRef}>
        <div className="trade-tile-menu-anchor">
          <button
            type="button"
            className="trade-symbol-pill"
            aria-expanded={openMenu === "symbol"}
            onClick={() =>
              setOpenMenu((m) => (m === "symbol" ? null : "symbol"))
            }
          >
            <strong>{tile.symbol}</strong>
            <span>{tile.venue}</span>
            <ChevronDown size={12} aria-hidden="true" />
          </button>
          {openMenu === "symbol" && (
            <form className="trade-tile-popover" onSubmit={applySymbolForm}>
              <div className="wt-venue-chips" role="group" aria-label={t("Venue")}>
                {venueList.map((v) => (
                  <button
                    key={v}
                    type="button"
                    className={`trade-mini-btn ${venueDraft === v ? "is-selected" : ""}`}
                    onClick={() => setVenueDraft(v)}
                  >
                    {v}
                  </button>
                ))}
              </div>
              <input
                aria-label={t("Symbol")}
                value={symbolDraft}
                onChange={(e) => setSymbolDraft(e.target.value)}
                autoFocus
              />
              <button type="submit" className="wt-btn-rose-sm">
                {t("Open")}
              </button>
            </form>
          )}
        </div>

        <div className="trade-interval-pills" role="group" aria-label={t("Interval")}>
          {CHART_INTERVALS.map((iv) => (
            <button
              key={iv}
              type="button"
              className={`trade-mini-btn ${tile.interval === iv ? "is-selected" : ""}`}
              aria-pressed={tile.interval === iv}
              onClick={() => onChangeTile({ ...tile, interval: iv })}
            >
              {iv}
            </button>
          ))}
        </div>

        <div className="trade-tile-icon-group">
          <div className="trade-tile-menu-anchor">
            <button
              type="button"
              className={`icon-button ${openMenu === "style" || isFootprint ? "is-selected" : ""}`}
              aria-label={t("Style")}
              title={t("Style")}
              onClick={() =>
                setOpenMenu((m) => (m === "style" ? null : "style"))
              }
            >
              <CandlestickChart size={14} aria-hidden="true" />
            </button>
            {openMenu === "style" && (
              <div className="trade-tile-popover is-list">
                {CANDLE_STYLES.map((st) => (
                  <button
                    key={st.id}
                    type="button"
                    className={tile.style === st.id ? "is-selected" : ""}
                    onClick={() => {
                      onChangeTile({ ...tile, style: st.id as CandleStyle });
                      setOpenMenu(null);
                    }}
                  >
                    {t(st.label)}
                  </button>
                ))}
              </div>
            )}
          </div>

          <div className="trade-tile-menu-anchor">
            <button
              type="button"
              className={`icon-button ${tool !== "none" ? "is-selected" : ""}`}
              aria-label={t("Drawing tool")}
              title={t("Drawing tool")}
              onClick={() =>
                setOpenMenu((m) => (m === "tools" ? null : "tools"))
              }
            >
              <Pencil size={14} aria-hidden="true" />
            </button>
            {openMenu === "tools" && (
              <div className="trade-tile-popover is-list">
                {(
                  [
                    { id: "none", label: "Cursor" },
                    { id: "hline", label: "H-Line" },
                    { id: "trendline", label: "Trendline" },
                    { id: "fib", label: "Fibonacci" },
                    { id: "paper_buy", label: "Paper buy" },
                    { id: "paper_sell", label: "Paper sell" },
                  ] as const
                ).map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    className={tool === item.id ? "is-selected" : ""}
                    onClick={() => {
                      setTool(item.id);
                      setOpenMenu(null);
                    }}
                  >
                    {t(item.label)}
                  </button>
                ))}
              </div>
            )}
          </div>

          {labScripts.length > 0 && (
            <div className="trade-tile-menu-anchor">
              <button
                type="button"
                className={`icon-button ${tile.scriptId ? "is-selected" : ""}`}
                aria-label={t("Indicator script")}
                title={t("Indicator script")}
                onClick={() =>
                  setOpenMenu((m) => (m === "indicators" ? null : "indicators"))
                }
              >
                <SlidersHorizontal size={14} aria-hidden="true" />
              </button>
              {openMenu === "indicators" && (
                <div className="trade-tile-popover is-list">
                  <button
                    type="button"
                    className={!tile.scriptId ? "is-selected" : ""}
                    onClick={() => {
                      onChangeTile({ ...tile, scriptId: undefined });
                      setOpenMenu(null);
                    }}
                  >
                    {t("None")}
                  </button>
                  {labScripts.map((sc) => (
                    <button
                      key={sc.id}
                      type="button"
                      className={tile.scriptId === sc.id ? "is-selected" : ""}
                      onClick={() => {
                        onChangeTile({ ...tile, scriptId: sc.id });
                        setOpenMenu(null);
                      }}
                    >
                      {sc.name}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          {overlayCount > 0 && (
            <button
              type="button"
              className="icon-button"
              aria-label={t("Clear drawings")}
              title={t("Clear drawings")}
              onClick={() => {
                if (onClearOverlays) onClearOverlays();
                else onChangeTile({ ...tile, drawings: [], paperOrders: [] });
              }}
            >
              <Trash2 size={14} aria-hidden="true" />
            </button>
          )}
        </div>
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
            {isFootprint && (
              <span className="trade-source-pill">{t("Footprint")}</span>
            )}
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
        {isFootprint ? (
          <FootprintCanvas
            bars={footprint?.bars ?? []}
            stepSize={footprint?.stepSize}
            focusedTime={null}
          />
        ) : (
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
              if (onAddDrawing) onAddDrawing(d);
              else {
                onChangeTile({
                  ...tile,
                  drawings: [...tile.drawings, d].slice(-32),
                });
              }
            }}
            onAddPaperOrder={(o: PaperOrderPreview) => {
              if (onAddPaperOrder) onAddPaperOrder(o);
              else {
                onChangeTile({
                  ...tile,
                  paperOrders: [...tile.paperOrders, o].slice(-16),
                });
              }
            }}
          />
        )}
      </div>
    </article>
  );
}
