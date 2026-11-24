import { useEffect, useMemo, useState } from "react";
import { useCore } from "../../core";
import { useUI } from "../../ui-preferences";
import { Trash2 } from "../../icons";
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
  type CrosshairSyncPoint,
  type DrawingToolMode,
  type Interval,
  type PaperOrderPreview,
} from "../../market-data";
import { FasedChartAdapter } from "./FasedChartAdapter";

export function ChartTile({
  tile,
  active,
  syncedCrosshair,
  onSelect,
  onChangeTile,
  onCrosshairMove,
}: {
  tile: ChartTileConfig;
  active: boolean;
  syncedCrosshair: CrosshairSyncPoint | null;
  onSelect: () => void;
  onChangeTile: (next: ChartTileConfig) => void;
  onCrosshairMove: (pt: CrosshairSyncPoint) => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const [symbolDraft, setSymbolDraft] = useState(tile.symbol);
  const [candles, setCandles] = useState<Candle[]>([]);
  const [sourceLabel, setSourceLabel] = useState("loading");
  const [error, setError] = useState("");
  const [tool, setTool] = useState<DrawingToolMode>("none");

  useEffect(() => {
    setSymbolDraft(tile.symbol);
  }, [tile.symbol]);

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;
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

    if (core.mode === "ready") {
      const q = new URLSearchParams({
        venue: tile.venue,
        symbol: tile.symbol,
        interval: tile.interval,
        style: tile.style,
      });
      void core
        .streamGet(`market/stream?${q.toString()}`, {
          signal: controller.signal,
          onEvent: (frame) => {
            if (!mounted) return;
            if (frame.event === "snapshot") {
              try {
                const parsed = JSON.parse(frame.data) as {
                  candles?: Candle[];
                  fixture?: boolean;
                  source?: string;
                };
                if (Array.isArray(parsed.candles)) {
                  setCandles(parsed.candles);
                  if (parsed.source) setSourceLabel(parsed.source);
                }
              } catch {}
            } else if (frame.event === "candle") {
              try {
                const ev = JSON.parse(frame.data) as {
                  payload?: Candle | { candles?: Candle[] };
                };
                const p = ev.payload;
                if (p && "candles" in p && Array.isArray(p.candles)) {
                  setCandles(p.candles);
                } else if (p && "close" in p) {
                  setCandles((prev) => {
                    if (prev.length === 0) return [p];
                    const last = prev[prev.length - 1];
                    if (last.time === p.time) {
                      return [...prev.slice(0, -1), p];
                    }
                    if (p.time > last.time) {
                      return [...prev.slice(-199), p];
                    }
                    return prev;
                  });
                }
              } catch {}
            }
          },
        })
        .catch(() => {
          /* ignore aborted or unavailable live stream when snapshot loaded */
        });
    }

    return () => {
      mounted = false;
      controller.abort();
    };
  }, [core, core.mode, tile.venue, tile.symbol, tile.interval, tile.style]);

  const latest = useMemo(() => candles[candles.length - 1] ?? null, [candles]);

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

        {(tile.drawings.length > 0 || tile.paperOrders.length > 0) && (
          <button
            type="button"
            className="trade-mini-btn"
            aria-label={t("Clear drawings")}
            onClick={() =>
              onChangeTile({ ...tile, drawings: [], paperOrders: [] })
            }
          >
            <Trash2 size={13} />
          </button>
        )}
      </header>

      <div className="trade-ohlc-strip">
        {latest ? (
          <>
            <span>
              <strong>{formatMarketPrice(latest.close)}</strong>
            </span>
            <span>
              {t("Vol")} {formatCompactVolume(latest.volume)}
            </span>
            <span className="trade-source-pill">{sourceLabel}</span>
            {tile.drawings.length + tile.paperOrders.length > 0 && (
              <span>
                {tile.drawings.length + tile.paperOrders.length} {t("overlays")}
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
          candles={candles}
          tool={tool}
          drawings={tile.drawings}
          paperOrders={tile.paperOrders}
          syncedCrosshair={syncedCrosshair}
          onCrosshairMove={onCrosshairMove}
          onAddDrawing={(d: ChartDrawing) =>
            onChangeTile({
              ...tile,
              drawings: [...tile.drawings, d].slice(-32),
            })
          }
          onAddPaperOrder={(o: PaperOrderPreview) =>
            onChangeTile({
              ...tile,
              paperOrders: [...tile.paperOrders, o].slice(-16),
            })
          }
        />
      </div>
    </article>
  );
}
