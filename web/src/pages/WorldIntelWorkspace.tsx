import { useCallback, useEffect, useRef, useState } from "react";
import { WorldMapView } from "../components/intel/WorldMapView";
import { WidgetGrid, type WidgetCatalogItem } from "../components/WidgetGrid";
import {
  CandlestickChart,
  ExternalLink,
  FlaskConical,
  Globe2,
  MessageSquare,
  Newspaper,
  Plus,
  RefreshCw,
  Settings,
  TrendingUp,
  Zap,
} from "../icons";
import {
  DATA_PLUGINS_CHANGED_EVENT,
  fetchDataPlugins,
} from "../data-plugins";
import { parseTradeWorkspaceConfig, TRADE_STORAGE_KEY } from "../market-data";
import { TRIGGER_DRAFT_KEY, type TriggerDraftSeed } from "../automation";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import {
  buildIntelChatPrompt,
  fetchIntelMap,
  fetchPredictions,
  formatCompactUSD,
  formatProbPct,
  INTEL_CATEGORIES,
  syncIntelSources,
  type IntelEvent,
  type IntelMapSummary,
  type PredictionList,
} from "../world-intel";
import type { GridLayout } from "../widget-layout";
import "./WorldIntelWorkspace.css";

type IntelWidgetID = "map" | "wire" | "predictions" | "macro";

const INTEL_LAYOUT_KEY = "fased.intel.layout.v1";

const INTEL_CATALOG: readonly WidgetCatalogItem<IntelWidgetID>[] = [
  { id: "map", title: "World Map", icon: Globe2, defaultSize: "wide" },
  { id: "wire", title: "News Wire", icon: Newspaper, defaultSize: "tall" },
  {
    id: "predictions",
    title: "Prediction Markets",
    icon: TrendingUp,
    defaultSize: "small",
  },
  {
    id: "macro",
    title: "Macro & Filings",
    icon: FlaskConical,
    defaultSize: "small",
  },
];

const DEFAULT_INTEL_LAYOUT: GridLayout<IntelWidgetID> = {
  version: 1,
  widgets: [
    { id: "map", size: "wide" },
    { id: "wire", size: "tall" },
    { id: "predictions", size: "small" },
    { id: "macro", size: "small" },
  ],
};

export function WorldIntelWorkspace() {
  const { t } = useUI();
  const {
    core,
    agents,
    streamClient,
    launchChatPrompt,
    navigate,
    openSettingsModal,
  } = useWorkspace();

  const [category, setCategory] = useState("");
  const [region, setRegion] = useState("");
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [addingWidgets, setAddingWidgets] = useState(false);
  const [activeFeedCount, setActiveFeedCount] = useState<number | null>(null);

  const [mapSummary, setMapSummary] = useState<IntelMapSummary | null>(null);
  const [predictions, setPredictions] = useState<PredictionList | null>(null);
  const [selectedEvent, setSelectedEvent] = useState<IntelEvent | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query), 300);
    return () => window.clearTimeout(timer);
  }, [query]);

  const loadAll = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const [mapRes, predRes, plugRes] = await Promise.all([
        fetchIntelMap(core, { category, hours: 72 }),
        fetchPredictions(core, { category, q: debouncedQuery, limit: 60 }),
        fetchDataPlugins(core),
      ]);
      setMapSummary(mapRes);
      setPredictions(predRes);
      const count =
        (plugRes.summary?.activeNews ?? 0) +
        (plugRes.summary?.activePredictions ?? 0);
      setActiveFeedCount(count);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load intel");
    }
  }, [core.mode, core.request, category, debouncedQuery]);

  const loadAllRef = useRef(loadAll);
  useEffect(() => {
    loadAllRef.current = loadAll;
  }, [loadAll]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  useEffect(() => {
    const onPluginsChanged = () => void loadAllRef.current();
    window.addEventListener(DATA_PLUGINS_CHANGED_EVENT, onPluginsChanged);
    return () =>
      window.removeEventListener(DATA_PLUGINS_CHANGED_EVENT, onPluginsChanged);
  }, []);

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    let reloadTimer: number | undefined;
    const scheduleReload = (immediate = false) => {
      if (reloadTimer !== undefined) {
        window.clearTimeout(reloadTimer);
        reloadTimer = undefined;
      }
      if (immediate) {
        void loadAllRef.current();
        return;
      }
      reloadTimer = window.setTimeout(() => {
        reloadTimer = undefined;
        void loadAllRef.current();
      }, 500);
    };
    const unsub = streamClient.subscribe("intel", (frame) => {
      if (frame.event === "sync_complete") {
        scheduleReload(true);
      } else if (frame.event === "event" || frame.event === "prediction") {
        scheduleReload(false);
      }
    });
    return () => {
      if (reloadTimer !== undefined) window.clearTimeout(reloadTimer);
      unsub();
    };
  }, [streamClient, core.mode]);

  const rawEvents = mapSummary?.events ?? [];
  function sentimentLabel(sentiment: string): string {
    if (sentiment === "bullish") return t("Bullish");
    if (sentiment === "bearish") return t("Bearish");
    return t("Neutral");
  }

  const filteredEvents = rawEvents.filter((ev) => {
    if (region && ev.region !== region) return false;
    if (query.trim()) {
      const qLower = query.trim().toLowerCase();
      const hay =
        `${ev.title} ${ev.summary ?? ""} ${ev.sourceName} ${(ev.tickers ?? []).join(" ")}`.toLowerCase();
      if (!hay.includes(qLower)) return false;
    }
    return true;
  });

  const macroAndFilings = filteredEvents.filter(
    (ev) => ev.category === "macro" || ev.category === "policy",
  );

  function openTickerChart(symbol: string) {
    try {
      const parsed = parseTradeWorkspaceConfig(
        localStorage.getItem(TRADE_STORAGE_KEY),
      );
      const updated = {
        ...parsed,
        tiles: parsed.tiles.map((tile) =>
          tile.id === parsed.activeTileId ? { ...tile, symbol } : tile,
        ),
      };
      localStorage.setItem(TRADE_STORAGE_KEY, JSON.stringify(updated));
    } catch {}
    navigate("trade");
  }

  function askAgent(ev: IntelEvent) {
    const profileId = agents.view?.profileId || "trading-analyst";
    launchChatPrompt({
      profileId,
      title: ev.title,
      prompt: buildIntelChatPrompt(ev),
    });
  }

  function createTriggerFromEvent(ev: IntelEvent) {
    const seed: TriggerDraftSeed = {
      kind: "news",
      name: ev.title.slice(0, 64),
      category: ev.category || "macro",
      region: ev.region || "",
      ticker: ev.tickers?.[0] || "",
      minUrgency: Number(Math.max(0.5, ev.urgency || 0.6).toFixed(2)),
    };
    try {
      sessionStorage.setItem(TRIGGER_DRAFT_KEY, JSON.stringify(seed));
    } catch {}
    navigate("agents/tasks");
  }

  async function handleSync() {
    if (busy || core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await syncIntelSources(core);
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sync failed");
    } finally {
      setBusy(false);
    }
  }

  function renderWidgetBody(id: IntelWidgetID) {
    if (id === "map") {
      return (
        <div className="intel-widget-stack">
          <WorldMapView
            events={filteredEvents}
            clusters={mapSummary?.clusters ?? []}
            regionCounts={mapSummary?.regionCounts ?? {}}
            selectedRegion={region}
            selectedEventId={selectedEvent?.id ?? ""}
            onSelectRegion={setRegion}
            onSelectEvent={setSelectedEvent}
          />
          {selectedEvent && (
            <article className="intel-inspector" data-testid="intel-inspector">
              <div className="intel-card-row">
                <strong>{selectedEvent.title}</strong>
                <div className="intel-meta-pills">
                  <span className="pill">
                    {`${Math.round(selectedEvent.urgency * 100)}%`}
                  </span>
                  <span className="pill">
                    {sentimentLabel(selectedEvent.sentiment)}
                  </span>
                </div>
              </div>
              <div className="intel-card-actions">
                <button
                  type="button"
                  className="primary"
                  onClick={() => askAgent(selectedEvent)}
                >
                  <MessageSquare size={13} aria-hidden="true" />
                  <span>{t("Ask in Chat")}</span>
                </button>
                {selectedEvent.tickers?.[0] && (
                  <button
                    type="button"
                    className="secondary"
                    onClick={() => openTickerChart(selectedEvent.tickers![0])}
                  >
                    <CandlestickChart size={13} aria-hidden="true" />
                    <span>{selectedEvent.tickers[0]}</span>
                  </button>
                )}
                <button
                  type="button"
                  className="secondary"
                  onClick={() => createTriggerFromEvent(selectedEvent)}
                >
                  <Zap size={13} aria-hidden="true" />
                  <span>{t("Create trigger")}</span>
                </button>
              </div>
            </article>
          )}
        </div>
      );
    }

    if (id === "wire") {
      return (
        <div className="intel-list" data-testid="intel-events-list">
          {filteredEvents.map((ev) => (
            <article className="intel-card" key={ev.id}>
              <div className="intel-card-row">
                <button
                  type="button"
                  className="intel-card-title"
                  onClick={() => setSelectedEvent(ev)}
                >
                  {ev.title}
                </button>
              </div>
              <div className="intel-meta-pills">
                <span
                  className={`pill ${ev.urgency >= 0.7 ? "intel-pill-high" : ""}`}
                >
                  {`${Math.round(ev.urgency * 100)}%`}
                </span>
                <span className="pill">{ev.sourceName}</span>
                {ev.tickers?.map((tk) => (
                  <span key={tk} className="pill mono">
                    {tk}
                  </span>
                ))}
              </div>
              <div className="intel-card-actions">
                <button
                  type="button"
                  className="icon-button"
                  title={t("Ask in Chat")}
                  onClick={() => askAgent(ev)}
                >
                  <MessageSquare size={13} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  className="icon-button"
                  title={t("Create trigger")}
                  onClick={() => createTriggerFromEvent(ev)}
                >
                  <Zap size={13} aria-hidden="true" />
                </button>
                {ev.tickers?.[0] && (
                  <button
                    type="button"
                    className="icon-button"
                    title={t("Open chart")}
                    onClick={() => openTickerChart(ev.tickers![0])}
                  >
                    <CandlestickChart size={13} aria-hidden="true" />
                  </button>
                )}
                {ev.url?.startsWith("https://") && (
                  <a
                    href={ev.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="icon-button"
                    title={t("Open")}
                  >
                    <ExternalLink size={13} aria-hidden="true" />
                  </a>
                )}
              </div>
            </article>
          ))}
          {filteredEvents.length === 0 && (
            <p className="subtle">{t("None")}</p>
          )}
        </div>
      );
    }

    if (id === "predictions") {
      return (
        <div className="intel-list" data-testid="intel-predictions-list">
          {(predictions?.arbitrage ?? []).map((arb) => (
            <article className="intel-arb-card" key={arb.id}>
              <div className="intel-card-row">
                <strong>{arb.topic}</strong>
                <span className="pill mono">{`Δ ${arb.spreadPct.toFixed(1)}%`}</span>
              </div>
              <div className="intel-meta-pills mono">
                <span className="pill">{`Polymarket ${formatProbPct(arb.polymarketProb)}`}</span>
                <span className="pill">{`Kalshi ${formatProbPct(arb.kalshiProb)}`}</span>
              </div>
            </article>
          ))}
          {(predictions?.markets ?? []).map((mkt) => (
            <article className="intel-card" key={mkt.id}>
              <div className="intel-card-row">
                <strong>{mkt.question}</strong>
                <span className="pill mono">{formatProbPct(mkt.yesProb)}</span>
              </div>
              <div className="intel-meta-pills">
                <span className="pill">{mkt.venue}</span>
                <span className="pill mono">{formatCompactUSD(mkt.volume24h)}</span>
              </div>
            </article>
          ))}
          {(predictions?.markets?.length ?? 0) === 0 && (
            <p className="subtle">{t("None")}</p>
          )}
        </div>
      );
    }

    return (
      <div className="intel-list">
        {macroAndFilings.map((ev) => (
          <article className="intel-card" key={ev.id}>
            <div className="intel-card-row">
              <strong>{ev.title}</strong>
            </div>
            <div className="intel-meta-pills">
              <span className="pill">{ev.sourceName}</span>
              {ev.tickers?.map((tk) => (
                <span key={tk} className="pill mono">
                  {tk}
                </span>
              ))}
            </div>
          </article>
        ))}
        {macroAndFilings.length === 0 && <p className="subtle">{t("None")}</p>}
      </div>
    );
  }

  const isZeroState =
    activeFeedCount === 0 &&
    filteredEvents.length === 0 &&
    (predictions?.markets?.length ?? 0) === 0;

  return (
    <section className="intel-workspace" aria-label={t("News & Intel")}>
      <header className="intel-toolbar">
        <div className="intel-title-group">
          <h1>{t("News & Intel")}</h1>
        </div>
        <div className="intel-controls">
          <select
            aria-label={t("Category")}
            value={category}
            onChange={(e) => setCategory(e.target.value)}
          >
            {INTEL_CATEGORIES.map((c) => (
              <option key={c.id || "all"} value={c.id}>
                {t(c.label)}
              </option>
            ))}
          </select>
          <input
            type="search"
            aria-label={t("Search")}
            placeholder={t("Search")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <button
            type="button"
            className="icon-button"
            title={t("Sync")}
            aria-label={t("Sync")}
            disabled={busy || core.mode !== "ready"}
            onClick={() => void handleSync()}
          >
            <RefreshCw size={15} className={busy ? "spin" : ""} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="icon-button"
            title={t("Data & Feeds")}
            aria-label={t("Data & Feeds")}
            onClick={() => openSettingsModal("data")}
          >
            <Settings size={15} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="icon-button"
            title={t("Widgets")}
            aria-label={t("Widgets")}
            onClick={() => setAddingWidgets(true)}
          >
            <Plus size={15} aria-hidden="true" />
          </button>
        </div>
      </header>

      {error && <p role="alert">{error}</p>}

      {isZeroState ? (
        <div className="intel-empty-cta">
          <button
            type="button"
            className="primary"
            onClick={() => openSettingsModal("data")}
          >
            <Settings size={15} aria-hidden="true" />
            <span>{t("Enable feeds in Settings")}</span>
          </button>
        </div>
      ) : (
        <WidgetGrid
          storageKey={INTEL_LAYOUT_KEY}
          catalog={INTEL_CATALOG}
          defaultLayout={DEFAULT_INTEL_LAYOUT}
          renderBody={renderWidgetBody}
          addDialogOpen={addingWidgets}
          onAddDialogOpenChange={setAddingWidgets}
        />
      )}
    </section>
  );
}
