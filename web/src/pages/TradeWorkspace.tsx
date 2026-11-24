import { useEffect, useMemo, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Badge } from "../components/shared";
import { Crosshair, Grid2x2, Layers, List, Pin } from "../icons";
import { fetchIntelEvents, type IntelEvent } from "../world-intel";
import {
  defaultTradeWorkspaceConfig,
  ensureTilesForLayout,
  layoutTileCount,
  parseTradeWorkspaceConfig,
  TRADE_STORAGE_KEY,
  type Candle,
  type ChartTileConfig,
  type CrosshairSyncPoint,
  type OrderflowTab,
  type TradeLayoutMode,
  type TradeWorkspaceConfig,
  type WatchlistItem,
} from "../market-data";
import {
  ChartTile,
  type LiveMarketUpdate,
} from "../components/trade/ChartTile";
import { OrderflowPane } from "../components/trade/OrderflowPane";
import { WatchlistPanel } from "../components/trade/WatchlistPanel";
import "./TradeWorkspace.css";

const LAYOUTS: { id: TradeLayoutMode; label: string }[] = [
  { id: "1x1", label: "1×1" },
  { id: "1x2", label: "1×2" },
  { id: "2x2", label: "2×2" },
  { id: "3x3", label: "3×3" },
  { id: "4x4", label: "4×4" },
];

export function TradeWorkspace() {
  const { t } = useUI();
  const core = useCore();
  const [config, setConfig] = useState<TradeWorkspaceConfig>(() => {
    try {
      return parseTradeWorkspaceConfig(localStorage.getItem(TRADE_STORAGE_KEY));
    } catch {
      return defaultTradeWorkspaceConfig();
    }
  });
  const [crosshair, setCrosshair] = useState<CrosshairSyncPoint | null>(null);
  const [showEvents, setShowEvents] = useState(true);
  const [intelEvents, setIntelEvents] = useState<IntelEvent[]>([]);
  const [liveUpdate, setLiveUpdate] = useState<LiveMarketUpdate | null>(null);
  const liveSeqRef = useRef(0);

  useEffect(() => {
    if (core.mode !== "ready" || !showEvents) return;
    let cancelled = false;
    void fetchIntelEvents(core, { limit: 40 })
      .then((res) => {
        if (!cancelled) setIntelEvents(res.events ?? []);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core.mode, core.request, showEvents]);

  useEffect(() => {
    try {
      localStorage.setItem(TRADE_STORAGE_KEY, JSON.stringify(config));
    } catch {}
  }, [config]);

  const visibleCount = layoutTileCount(config.layout);
  const visibleTiles = useMemo(
    () => config.tiles.slice(0, visibleCount),
    [config.tiles, visibleCount],
  );
  const subsParam = useMemo(() => {
    const seen = new Set<string>();
    const entries: string[] = [];
    for (const tile of visibleTiles) {
      const tuple = `${tile.venue}:${tile.symbol}:${tile.interval}:${tile.style}`;
      if (!seen.has(tuple)) {
        seen.add(tuple);
        entries.push(tuple);
      }
    }
    return entries.join(",");
  }, [visibleTiles]);

  useEffect(() => {
    if (core.mode !== "ready" || !subsParam) return;
    const controller = new AbortController();
    const runStream = async () => {
      const delays = [1000, 2000, 4000, 8000, 15000];
      let attempt = 0;
      const qs = new URLSearchParams({ noSnapshot: "1", subs: subsParam });
      while (!controller.signal.aborted) {
        try {
          await core.streamGet(`market/stream?${qs.toString()}`, {
            signal: controller.signal,
            onEvent: (frame) => {
              attempt = 0;
              if (frame.event !== "candle") return;
              try {
                const ev = JSON.parse(frame.data) as {
                  venue?: string;
                  symbol?: string;
                  interval?: string;
                  style?: string;
                  payload?: Candle | { candles?: Candle[] };
                };
                if (!ev.venue || !ev.symbol || !ev.payload) return;
                liveSeqRef.current += 1;
                setLiveUpdate({
                  venue: ev.venue,
                  symbol: ev.symbol,
                  interval: ev.interval,
                  style: ev.style,
                  payload: ev.payload,
                  seq: liveSeqRef.current,
                });
              } catch {}
            },
          });
        } catch {
          if (controller.signal.aborted) return;
        }
        if (controller.signal.aborted) return;
        const waitMs = delays[Math.min(attempt, delays.length - 1)];
        attempt++;
        await new Promise<void>((resolve) => {
          const t = window.setTimeout(resolve, waitMs);
          controller.signal.addEventListener(
            "abort",
            () => {
              window.clearTimeout(t);
              resolve();
            },
            { once: true },
          );
        });
      }
    };
    void runStream();
    return () => {
      controller.abort();
    };
  }, [core.mode, core.streamGet, subsParam]);
  const activeTile = useMemo(
    () =>
      visibleTiles.find((item) => item.id === config.activeTileId) ??
      visibleTiles[0],
    [visibleTiles, config.activeTileId],
  );

  function setLayout(layout: TradeLayoutMode) {
    setConfig((prev) => {
      const nextTiles = ensureTilesForLayout(prev.tiles, layout);
      const count = layoutTileCount(layout);
      const activeStillVisible = nextTiles
        .slice(0, count)
        .some((x) => x.id === prev.activeTileId);
      return {
        ...prev,
        layout,
        tiles: nextTiles,
        activeTileId: activeStillVisible ? prev.activeTileId : nextTiles[0].id,
      };
    });
  }

  function updateTile(updated: ChartTileConfig) {
    setConfig((prev) => ({
      ...prev,
      tiles: prev.tiles.map((item) =>
        item.id === updated.id ? updated : item,
      ),
    }));
  }

  function loadWatchlistItem(item: WatchlistItem) {
    if (!activeTile) return;
    updateTile({
      ...activeTile,
      venue: item.venue,
      symbol: item.symbol,
      interval:
        (item.interval as ChartTileConfig["interval"]) || activeTile.interval,
      style: (item.style as ChartTileConfig["style"]) || activeTile.style,
    });
  }

  return (
    <section className="trade-workspace" aria-label={t("Trade")}>
      <header className="trade-toolbar">
        <div className="trade-title-group">
          <h1>{t("Trade")}</h1>
          <Badge>{t("Read-only")}</Badge>
        </div>

        <div className="trade-controls">
          <div
            className="trade-layout-group"
            role="group"
            aria-label={t("Chart layout")}
          >
            <Grid2x2 size={14} aria-hidden="true" />
            {LAYOUTS.map((l) => (
              <button
                key={l.id}
                type="button"
                aria-pressed={config.layout === l.id}
                className={config.layout === l.id ? "is-selected" : ""}
                onClick={() => setLayout(l.id)}
              >
                {l.label}
              </button>
            ))}
          </div>

          <button
            type="button"
            className={`trade-toggle-btn ${config.syncCrosshair ? "is-selected" : ""}`}
            aria-pressed={config.syncCrosshair}
            aria-label={t("Sync crosshairs")}
            onClick={() =>
              setConfig((prev) => ({
                ...prev,
                syncCrosshair: !prev.syncCrosshair,
              }))
            }
          >
            <Crosshair size={14} />
            {t("Sync")}
          </button>

          <button
            type="button"
            className={`trade-toggle-btn ${config.showWatchlist ? "is-selected" : ""}`}
            aria-pressed={config.showWatchlist}
            onClick={() =>
              setConfig((prev) => ({
                ...prev,
                showWatchlist: !prev.showWatchlist,
              }))
            }
          >
            <List size={14} />
            {t("Watchlist")}
          </button>

          <button
            type="button"
            className={`trade-toggle-btn ${config.showOrderflow ? "is-selected" : ""}`}
            aria-pressed={config.showOrderflow}
            onClick={() =>
              setConfig((prev) => ({
                ...prev,
                showOrderflow: !prev.showOrderflow,
              }))
            }
          >
            <Layers size={14} />
            {t("Orderflow")}
          </button>

          <button
            type="button"
            className={`trade-toggle-btn ${showEvents ? "is-selected" : ""}`}
            aria-pressed={showEvents}
            onClick={() => setShowEvents((prev) => !prev)}
          >
            <Pin size={14} />
            {t("Events")}
          </button>
        </div>
      </header>

      <div
        className={`trade-workbench ${config.showWatchlist ? "has-watchlist" : ""} ${config.showOrderflow ? "has-orderflow" : ""}`}
      >
        {config.showWatchlist && activeTile && (
          <WatchlistPanel
            activeVenue={activeTile.venue}
            activeSymbol={activeTile.symbol}
            onSelectItem={loadWatchlistItem}
          />
        )}

        <div
          className={`trade-grid layout-${config.layout}`}
          data-testid="trade-chart-grid"
        >
          {visibleTiles.map((tile) => (
            <ChartTile
              key={tile.id}
              tile={tile}
              active={tile.id === activeTile?.id}
              syncedCrosshair={config.syncCrosshair ? crosshair : null}
              liveUpdate={liveUpdate}
              intelEvents={intelEvents}
              showEvents={showEvents}
              onSelect={() =>
                setConfig((prev) =>
                  prev.activeTileId === tile.id
                    ? prev
                    : { ...prev, activeTileId: tile.id },
                )
              }
              onChangeTile={updateTile}
              onCrosshairMove={(pt) => {
                if (config.syncCrosshair) setCrosshair(pt);
              }}
            />
          ))}
        </div>

        {config.showOrderflow && activeTile && (
          <OrderflowPane
            venue={activeTile.venue}
            symbol={activeTile.symbol}
            interval={activeTile.interval}
            tab={config.orderflowTab}
            syncedTime={crosshair?.time ?? null}
            onChangeTab={(nextTab: OrderflowTab) =>
              setConfig((prev) => ({ ...prev, orderflowTab: nextTab }))
            }
          />
        )}
      </div>
    </section>
  );
}
