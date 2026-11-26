import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import { Badge } from "../components/shared";
import { WidgetGrid, type WidgetCatalogItem } from "../components/WidgetGrid";
import {
  Activity,
  Bot,
  CandlestickChart,
  Crosshair,
  Grid2x2,
  Layers,
  List,
  Pin,
  Plus,
  Settings,
  Shield,
  TrendingUp,
} from "../icons";
import { CrosshairBus } from "../stream-store";
import { fetchIntelEvents, type IntelEvent } from "../world-intel";
import {
  DATA_PLUGINS_CHANGED_EVENT,
  fetchDataPlugins,
} from "../data-plugins";
import {
  approveDeskTicket,
  closeDeskPosition,
  getDeskState,
  listLabScripts,
  rejectDeskTicket,
  type DeskSnapshot,
  type LabScript,
} from "../quantlab-desk";
import {
  defaultTradeWorkspaceConfig,
  deleteDrawing,
  ensureTilesForLayout,
  fetchDrawings,
  layoutTileCount,
  parseTradeWorkspaceConfig,
  saveDrawing,
  symbolOverlayKey,
  TRADE_STORAGE_KEY,
  type ChartDrawing,
  type ChartTileConfig,
  type OrderflowTab,
  type PaperOrderPreview,
  type TradeLayoutMode,
  type TradeWorkspaceConfig,
  type WatchlistItem,
} from "../market-data";
import {
  cancelTradeOrder,
  createTradeBot,
  deleteTradeBot,
  evaluateTradeOrders,
  getTradeAccounts,
  getTradeState,
  listTradeBots,
  setGlobalKillSwitch,
  setTradeBotStatus,
  submitTradeOrder,
  type TradeAccountOption,
  type TradeBot,
  type TradeOrderRequest,
  type TradeSnapshot,
} from "../trade-engine";
import { ChartTile } from "../components/trade/ChartTile";
import { OrderflowPane } from "../components/trade/OrderflowPane";
import { WatchlistPanel } from "../components/trade/WatchlistPanel";
import { OrderExecutionWidget } from "../components/trade/OrderExecutionWidget";
import { PositionsWidget } from "../components/trade/PositionsWidget";
import { BotAgentWidget } from "../components/trade/BotAgentWidget";
import type { GridLayout } from "../widget-layout";
import "./TradeWorkspace.css";

type TradeWidgetID =
  | "chart"
  | "execution"
  | "watchlist"
  | "microstructure"
  | "positions"
  | "bots";

const TRADE_WIDGET_LAYOUT_KEY = "fased.trade.layout.v1";

const TRADE_CATALOG: readonly WidgetCatalogItem<TradeWidgetID>[] = [
  { id: "chart", title: "Chart", icon: CandlestickChart, defaultSize: "wide" },
  {
    id: "execution",
    title: "Order Execution",
    icon: TrendingUp,
    defaultSize: "small",
  },
  { id: "watchlist", title: "Watchlist", icon: List, defaultSize: "small" },
  {
    id: "microstructure",
    title: "DOM & Tape",
    icon: Layers,
    defaultSize: "small",
  },
  {
    id: "positions",
    title: "Positions & Orders",
    icon: Activity,
    defaultSize: "wide",
  },
  { id: "bots", title: "Bots & AI Trader", icon: Bot, defaultSize: "small" },
];

const DEFAULT_TRADE_WIDGET_LAYOUT: GridLayout<TradeWidgetID> = {
  version: 1,
  widgets: [
    { id: "chart", size: "wide" },
    { id: "execution", size: "small" },
    { id: "positions", size: "wide" },
    { id: "watchlist", size: "small" },
    { id: "microstructure", size: "small" },
    { id: "bots", size: "small" },
  ],
};

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
  const { streamClient, agents, navigate, openSettingsModal } = useWorkspace();
  const [config, setConfig] = useState<TradeWorkspaceConfig>(() => {
    try {
      return parseTradeWorkspaceConfig(localStorage.getItem(TRADE_STORAGE_KEY));
    } catch {
      return defaultTradeWorkspaceConfig();
    }
  });
  const [showEvents, setShowEvents] = useState(true);
  const [addingWidgets, setAddingWidgets] = useState(false);
  const [enabledExchanges, setEnabledExchanges] = useState<string[] | null>(
    null,
  );
  const [intelEvents, setIntelEvents] = useState<IntelEvent[]>([]);
  const [labScripts, setLabScripts] = useState<LabScript[]>([]);
  const [desk, setDesk] = useState<DeskSnapshot | null>(null);
  const [deskBusy, setDeskBusy] = useState(false);
  const [trade, setTrade] = useState<TradeSnapshot | null>(null);
  const [accounts, setAccounts] = useState<TradeAccountOption[]>([]);
  const [bots, setBots] = useState<TradeBot[]>([]);
  const [tradeBusy, setTradeBusy] = useState(false);
  const [tradeError, setTradeError] = useState("");

  const crosshairBusRef = useRef<CrosshairBus | null>(null);
  if (!crosshairBusRef.current) {
    crosshairBusRef.current = new CrosshairBus();
  }

  const refreshPlugins = useCallback(async () => {
    if (core.mode !== "ready") {
      setEnabledExchanges(null);
      return;
    }
    try {
      const res = await fetchDataPlugins(core);
      const active = (res.plugins ?? [])
        .filter((p) => p.category === "exchanges" && p.enabled && p.dataFeedEnabled)
        .map((p) => p.id);
      setEnabledExchanges(active);
      if (active.length > 0) {
        setConfig((prev) => ({
          ...prev,
          tiles: prev.tiles.map((tile) =>
            active.includes(tile.venue)
              ? tile
              : {
                  ...tile,
                  venue: active[0],
                  symbol: active[0] === "binance" ? "BTCUSDT" : tile.symbol,
                },
          ),
        }));
      }
    } catch {
      setEnabledExchanges([]);
    }
  }, [core.mode, core.request]);

  const refreshDesk = useCallback(async () => {
    if (core.mode !== "ready") return;
    try {
      setDesk(await getDeskState(core));
    } catch {}
  }, [core.mode, core.request]);

  const refreshTrade = useCallback(async () => {
    if (core.mode !== "ready") return;
    try {
      const [snap, accs, bts] = await Promise.all([
        getTradeState(core),
        getTradeAccounts(core),
        listTradeBots(core),
      ]);
      setTrade(snap);
      setAccounts(accs);
      setBots(bts);
    } catch {}
  }, [core.mode, core.request]);

  useEffect(() => {
    void refreshPlugins();
    const onPluginsChanged = () => {
      void refreshPlugins();
      void refreshTrade();
    };
    window.addEventListener(DATA_PLUGINS_CHANGED_EVENT, onPluginsChanged);
    return () =>
      window.removeEventListener(DATA_PLUGINS_CHANGED_EVENT, onPluginsChanged);
  }, [refreshPlugins, refreshTrade]);

  useEffect(() => {
    if (core.mode !== "ready") {
      setLabScripts([]);
      setDesk(null);
      setTrade(null);
      return;
    }
    let cancelled = false;
    void listLabScripts(core)
      .then((res) => {
        if (!cancelled) setLabScripts(res.scripts ?? []);
      })
      .catch(() => {});
    void refreshDesk();
    void refreshTrade();
    void fetchDrawings(core)
      .then((serverDrawings) => {
        if (cancelled) return;
        setConfig((prev) => {
          const grouped: Record<string, ChartDrawing[]> = {};
          const serverIds = new Set<string>();
          for (const d of serverDrawings) {
            serverIds.add(d.id);
            const ovKey = symbolOverlayKey(
              d.venue || "hyperliquid",
              d.symbol || "BTC",
            );
            grouped[ovKey] = [...(grouped[ovKey] ?? []), d].slice(-32);
          }
          const merged: Record<string, ChartDrawing[]> = { ...grouped };
          for (const [k, list] of Object.entries(prev.drawingsBySymbol ?? {})) {
            const localOnly = list.filter(
              (d) => !serverIds.has(d.id) && d.venue === undefined,
            );
            if (localOnly.length > 0) {
              merged[k] = [...localOnly, ...(merged[k] ?? [])].slice(-32);
            }
          }
          return { ...prev, drawingsBySymbol: merged };
        });
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core.mode, core.request, refreshDesk, refreshTrade]);

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    let timer: number | undefined;
    const unsub = streamClient.subscribe("desk", () => {
      if (timer !== undefined) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = undefined;
        void refreshDesk();
        void refreshTrade();
      }, 150);
    });
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      unsub();
    };
  }, [streamClient, core.mode, refreshDesk, refreshTrade]);

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

  function addDrawing(tile: ChartTileConfig, d: ChartDrawing) {
    const ovKey = symbolOverlayKey(tile.venue, tile.symbol);
    const enriched: ChartDrawing = {
      ...d,
      venue: tile.venue,
      symbol: tile.symbol,
      interval: tile.interval,
    };
    setConfig((prev) => {
      const curMap = prev.drawingsBySymbol ?? {};
      const nextList = [...(curMap[ovKey] ?? []), enriched].slice(-32);
      return { ...prev, drawingsBySymbol: { ...curMap, [ovKey]: nextList } };
    });
    if (core.mode === "ready") {
      void saveDrawing(core, enriched).catch(() => {});
    }
  }

  function addPaperOrder(tile: ChartTileConfig, ord: PaperOrderPreview) {
    const ovKey = symbolOverlayKey(tile.venue, tile.symbol);
    setConfig((prev) => {
      const curMap = prev.paperOrdersBySymbol ?? {};
      const nextList = [...(curMap[ovKey] ?? []), ord].slice(-16);
      return {
        ...prev,
        paperOrdersBySymbol: { ...curMap, [ovKey]: nextList },
      };
    });
  }

  function clearOverlays(tile: ChartTileConfig) {
    const ovKey = symbolOverlayKey(tile.venue, tile.symbol);
    const existing = config.drawingsBySymbol?.[ovKey] ?? tile.drawings;
    setConfig((prev) => {
      const nextDrawings = { ...(prev.drawingsBySymbol ?? {}) };
      delete nextDrawings[ovKey];
      const nextOrders = { ...(prev.paperOrdersBySymbol ?? {}) };
      delete nextOrders[ovKey];
      return {
        ...prev,
        drawingsBySymbol: nextDrawings,
        paperOrdersBySymbol: nextOrders,
        tiles: prev.tiles.map((item) =>
          item.id === tile.id ? { ...item, drawings: [], paperOrders: [] } : item,
        ),
      };
    });
    if (core.mode === "ready" && existing.length > 0) {
      void Promise.allSettled(existing.map((d) => deleteDrawing(core, d.id)));
    }
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

  const openDeskPositions = useMemo(
    () => (desk?.positions ?? []).filter((p) => p.status === "OPEN"),
    [desk],
  );

  async function handleSubmitTrade(req: TradeOrderRequest) {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    setTradeError("");
    try {
      await submitTradeOrder(core, req);
      await refreshTrade();
    } catch (err) {
      setTradeError(
        err instanceof Error ? err.message : "Trade submission failed",
      );
    } finally {
      setTradeBusy(false);
    }
  }

  async function handleCancelTrade(id: string) {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    setTradeError("");
    try {
      await cancelTradeOrder(core, id);
      await refreshTrade();
    } catch (err) {
      setTradeError(err instanceof Error ? err.message : "Cancel failed");
    } finally {
      setTradeBusy(false);
    }
  }

  async function handleEvaluateTrades() {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    setTradeError("");
    try {
      await evaluateTradeOrders(core);
      await refreshTrade();
    } catch (err) {
      setTradeError(err instanceof Error ? err.message : "Evaluation failed");
    } finally {
      setTradeBusy(false);
    }
  }

  async function handleToggleKill(active: boolean) {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    setTradeError("");
    try {
      await setGlobalKillSwitch(
        core,
        active,
        active ? "Owner halted trading in #trade" : "Owner cleared kill switch",
      );
      await Promise.all([refreshTrade(), refreshDesk()]);
    } catch (err) {
      setTradeError(err instanceof Error ? err.message : "Kill switch failed");
    } finally {
      setTradeBusy(false);
    }
  }

  async function handleApproveTicket(id: string) {
    if (core.mode !== "ready" || deskBusy) return;
    setDeskBusy(true);
    try {
      await approveDeskTicket(core, id);
      await Promise.all([refreshDesk(), refreshTrade()]);
    } finally {
      setDeskBusy(false);
    }
  }

  async function handleRejectTicket(id: string) {
    if (core.mode !== "ready" || deskBusy) return;
    setDeskBusy(true);
    try {
      await rejectDeskTicket(core, id, "PM rejected in Trade");
      await refreshDesk();
    } finally {
      setDeskBusy(false);
    }
  }

  async function handleClosePosition(id: string) {
    if (core.mode !== "ready" || deskBusy) return;
    setDeskBusy(true);
    try {
      await closeDeskPosition(core, id, { reason: "PM_MANUAL_CLOSE" });
      await refreshDesk();
    } finally {
      setDeskBusy(false);
    }
  }

  async function handleCreateBot(req: {
    name: string;
    kind: "fasedscript" | "ai_agent";
    accountId: string;
    venue: string;
    symbol: string;
    interval: string;
    scriptId?: string;
    agentId?: string;
    maxNotionalUsd: number;
  }) {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    setTradeError("");
    try {
      await createTradeBot(core, req);
      await refreshTrade();
    } catch (err) {
      setTradeError(err instanceof Error ? err.message : "Create bot failed");
    } finally {
      setTradeBusy(false);
    }
  }

  async function handleBotAction(id: string, action: "start" | "pause" | "stop") {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    try {
      await setTradeBotStatus(core, id, action);
      await refreshTrade();
    } finally {
      setTradeBusy(false);
    }
  }

  async function handleDeleteBot(id: string) {
    if (core.mode !== "ready" || tradeBusy) return;
    setTradeBusy(true);
    try {
      await deleteTradeBot(core, id);
      await refreshTrade();
    } finally {
      setTradeBusy(false);
    }
  }

  function renderWidgetHeaderExtra(id: TradeWidgetID) {
    if (id !== "chart") return null;
    return (
      <div className="trade-layout-group" role="group" aria-label={t("Chart layout")}>
        <Grid2x2 size={13} aria-hidden="true" />
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
    );
  }

  function renderWidgetBody(id: TradeWidgetID) {
    if (id === "chart") {
      return (
        <div
          className={`trade-grid layout-${config.layout}`}
          data-testid="trade-chart-grid"
        >
          {visibleTiles.map((tile) => {
            const ovKey = symbolOverlayKey(tile.venue, tile.symbol);
            const drawings = config.drawingsBySymbol?.[ovKey] ?? tile.drawings;
            const paperOrders =
              config.paperOrdersBySymbol?.[ovKey] ?? tile.paperOrders;
            const matchingPositions = openDeskPositions.filter(
              (p) =>
                (!p.venue || p.venue === tile.venue) &&
                p.symbol.toUpperCase() === tile.symbol.toUpperCase(),
            );
            const matchingPlans = (trade?.plans ?? []).filter(
              (pl) =>
                (pl.status === "active" || pl.status === "filled") &&
                pl.venue === tile.venue &&
                pl.symbol.toUpperCase() === tile.symbol.toUpperCase(),
            );
            return (
              <ChartTile
                key={tile.id}
                tile={tile}
                active={tile.id === activeTile?.id}
                enabledVenues={enabledExchanges ?? undefined}
                drawings={drawings}
                paperOrders={paperOrders}
                streamClient={streamClient}
                crosshairBus={crosshairBusRef.current ?? undefined}
                syncCrosshair={config.syncCrosshair}
                labScripts={labScripts}
                deskPositions={matchingPositions}
                tradePlans={matchingPlans}
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
                onAddDrawing={(d) => addDrawing(tile, d)}
                onAddPaperOrder={(o) => addPaperOrder(tile, o)}
                onClearOverlays={() => clearOverlays(tile)}
              />
            );
          })}
        </div>
      );
    }

    if (id === "execution" && activeTile) {
      return (
        <OrderExecutionWidget
          venue={activeTile.venue}
          symbol={activeTile.symbol}
          interval={activeTile.interval}
          trade={trade}
          accounts={accounts}
          busy={tradeBusy}
          error={tradeError}
          onSubmitTrade={(req) => void handleSubmitTrade(req)}
        />
      );
    }

    if (id === "watchlist" && activeTile) {
      return (
        <WatchlistPanel
          activeVenue={activeTile.venue}
          activeSymbol={activeTile.symbol}
          enabledVenues={enabledExchanges ?? undefined}
          onSelectItem={loadWatchlistItem}
        />
      );
    }

    if (id === "microstructure" && activeTile) {
      return (
        <OrderflowPane
          venue={activeTile.venue}
          symbol={activeTile.symbol}
          interval={activeTile.interval}
          tab={config.orderflowTab}
          streamClient={streamClient}
          crosshairBus={crosshairBusRef.current ?? undefined}
          syncCrosshair={config.syncCrosshair}
          onChangeTab={(nextTab: OrderflowTab) =>
            setConfig((prev) => ({ ...prev, orderflowTab: nextTab }))
          }
        />
      );
    }

    if (id === "positions") {
      return (
        <PositionsWidget
          trade={trade}
          desk={desk}
          tradeBusy={tradeBusy}
          deskBusy={deskBusy}
          onEvaluateTrades={() => void handleEvaluateTrades()}
          onCancelTrade={(plId) => void handleCancelTrade(plId)}
          onApproveTicket={(tkId) => void handleApproveTicket(tkId)}
          onRejectTicket={(tkId) => void handleRejectTicket(tkId)}
          onClosePosition={(posId) => void handleClosePosition(posId)}
          onOpenLab={() => navigate("lab/desk")}
        />
      );
    }

    if (id === "bots" && activeTile) {
      return (
        <BotAgentWidget
          venue={activeTile.venue}
          symbol={activeTile.symbol}
          interval={activeTile.interval}
          scripts={labScripts}
          bots={bots}
          accounts={accounts}
          agentProfiles={(agents.view?.profiles ?? []).map((p) => ({
            id: p.id,
            name: p.name,
          }))}
          busy={tradeBusy}
          onCreateBot={(req) => void handleCreateBot(req)}
          onBotAction={(bId, act) => void handleBotAction(bId, act)}
          onDeleteBot={(bId) => void handleDeleteBot(bId)}
        />
      );
    }

    return null;
  }

  const isZeroState = core.mode === "ready" && enabledExchanges?.length === 0;

  return (
    <section className="trade-workspace" aria-label={t("Trade")}>
      <header className="trade-toolbar">
        <div className="trade-title-group">
          <h1>{t("Trade")}</h1>
          <Badge tone={trade?.killSwitchActive ? "danger" : undefined}>
            {t(trade?.killSwitchActive ? "Kill switch" : "Paper")}
          </Badge>
        </div>

        <div className="trade-controls">
          <button
            type="button"
            className={`icon-button ${config.syncCrosshair ? "is-selected" : ""}`}
            aria-pressed={config.syncCrosshair}
            aria-label={t("Sync crosshairs")}
            title={t("Sync crosshairs")}
            onClick={() =>
              setConfig((prev) => ({
                ...prev,
                syncCrosshair: !prev.syncCrosshair,
              }))
            }
          >
            <Crosshair size={15} aria-hidden="true" />
          </button>

          <button
            type="button"
            className={`icon-button ${showEvents ? "is-selected" : ""}`}
            aria-pressed={showEvents}
            aria-label={t("Events")}
            title={t("Events")}
            onClick={() => setShowEvents((prev) => !prev)}
          >
            <Pin size={15} aria-hidden="true" />
          </button>

          <button
            type="button"
            className={`icon-button ${trade?.killSwitchActive ? "trade-danger-btn" : ""}`}
            aria-label={t(
              trade?.killSwitchActive ? "Clear kill switch" : "Kill switch",
            )}
            title={t(
              trade?.killSwitchActive ? "Clear kill switch" : "Kill switch",
            )}
            disabled={tradeBusy || core.mode !== "ready"}
            onClick={() => void handleToggleKill(!trade?.killSwitchActive)}
          >
            <Shield size={15} aria-hidden="true" />
          </button>

          <button
            type="button"
            className="icon-button"
            aria-label={t("Data & Feeds")}
            title={t("Data & Feeds")}
            onClick={() => openSettingsModal("data")}
          >
            <Settings size={15} aria-hidden="true" />
          </button>

          <button
            type="button"
            className="icon-button"
            aria-label={t("Widgets")}
            title={t("Widgets")}
            onClick={() => setAddingWidgets(true)}
          >
            <Plus size={15} aria-hidden="true" />
          </button>
        </div>
      </header>

      {isZeroState ? (
        <div className="trade-empty-cta">
          <button
            type="button"
            className="primary"
            onClick={() => openSettingsModal("data")}
          >
            <Settings size={15} aria-hidden="true" />
            <span>{t("Enable exchanges in Settings")}</span>
          </button>
        </div>
      ) : (
        <WidgetGrid
          storageKey={TRADE_WIDGET_LAYOUT_KEY}
          catalog={TRADE_CATALOG}
          defaultLayout={DEFAULT_TRADE_WIDGET_LAYOUT}
          renderWidgetHeaderExtra={renderWidgetHeaderExtra}
          renderBody={renderWidgetBody}
          addDialogOpen={addingWidgets}
          onAddDialogOpenChange={setAddingWidgets}
        />
      )}
    </section>
  );
}
