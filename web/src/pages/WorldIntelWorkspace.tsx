import { useCallback, useEffect, useRef, useState } from "react";
import { WorldMapView } from "../components/intel/WorldMapView";
import {
  CandlestickChart,
  ExternalLink,
  MessageSquare,
  RefreshCw,
  Settings,
  Zap,
} from "../icons";
import { DATA_PLUGINS_CHANGED_EVENT, fetchDataPlugins } from "../data-plugins";
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
import "./WorldIntelWorkspace.css";

type WireTab = "wire" | "macro";

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
  const [wireTab, setWireTab] = useState<WireTab>("wire");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
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

  const isZeroState =
    activeFeedCount === 0 &&
    filteredEvents.length === 0 &&
    (predictions?.markets?.length ?? 0) === 0;

  if (isZeroState) {
    return (
      <section
        className="intel-workspace wt-intel-shell"
        aria-label={t("News & Intel")}
      >
        <div className="intel-empty-cta">
          <button
            type="button"
            className="wt-btn-rose-sm"
            onClick={() => openSettingsModal("data")}
          >
            <Settings size={15} aria-hidden="true" />
            <span>{t("Enable feeds in Settings")}</span>
          </button>
        </div>
      </section>
    );
  }

  const shownRows = wireTab === "wire" ? filteredEvents : macroAndFilings;
  const regionEntries = Object.entries(mapSummary?.regionCounts ?? {});

  return (
    <section
      className="intel-workspace wt-intel-shell"
      aria-label={t("News & Intel")}
    >
      {error && <p role="alert">{error}</p>}

      <div className="wt-intel-grid">
        <aside className="wt-intel-left">
          <div className="wt-wpanel">
            <div className="wt-intel-head">
              <h2>{t("News & Intel")}</h2>
              <div className="wt-intel-head-actions">
                <button
                  type="button"
                  className="icon-button"
                  title={t("Sync")}
                  aria-label={t("Sync")}
                  disabled={busy || core.mode !== "ready"}
                  onClick={() => void handleSync()}
                >
                  <RefreshCw
                    size={14}
                    className={busy ? "spin" : ""}
                    aria-hidden="true"
                  />
                </button>
                <button
                  type="button"
                  className="icon-button"
                  title={t("Data & Feeds")}
                  aria-label={t("Data & Feeds")}
                  onClick={() => openSettingsModal("data")}
                >
                  <Settings size={14} aria-hidden="true" />
                </button>
              </div>
            </div>

            <div className="wt-kpi-box">
              <div>
                <span>{t("Events")}</span>
                <strong className="mono">{filteredEvents.length}</strong>
              </div>
              <div>
                <span>{t("Predictions")}</span>
                <strong className="mono">
                  {predictions?.markets?.length ?? 0}
                </strong>
              </div>
              <div>
                <span>{t("Sources")}</span>
                <strong className="mono">{activeFeedCount ?? 0}</strong>
              </div>
              <div>
                <span>{t("Regions")}</span>
                <strong className="mono">{regionEntries.length}</strong>
              </div>
            </div>

            <input
              type="search"
              className="wt-intel-search"
              aria-label={t("Search")}
              placeholder={t("Search")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />

            <div
              className="wt-chip-group"
              role="group"
              aria-label={t("Category")}
            >
              {INTEL_CATEGORIES.map((c) => (
                <button
                  key={c.id || "all"}
                  type="button"
                  className={`intel-filter-pill ${category === c.id ? "is-selected" : ""}`}
                  onClick={() => setCategory(c.id)}
                >
                  {t(c.label)}
                </button>
              ))}
            </div>
          </div>
        </aside>

        <div className="wt-intel-center">
          <div className="wt-wpanel">
            <WorldMapView
              events={filteredEvents}
              clusters={mapSummary?.clusters ?? []}
              regionCounts={mapSummary?.regionCounts ?? {}}
              selectedRegion={region}
              selectedEventId={selectedEvent?.id ?? ""}
              onSelectRegion={setRegion}
              onSelectEvent={setSelectedEvent}
            />
          </div>

          <div className="wt-wpanel">
            <div className="wt-tabs-underline" role="tablist">
              <button
                type="button"
                role="tab"
                aria-selected={wireTab === "wire"}
                className={wireTab === "wire" ? "is-selected" : ""}
                onClick={() => setWireTab("wire")}
              >
                <span>{t("News Wire")}</span>
                {filteredEvents.length > 0 && (
                  <span className="wt-tab-count mono">
                    {filteredEvents.length}
                  </span>
                )}
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={wireTab === "macro"}
                className={wireTab === "macro" ? "is-selected" : ""}
                onClick={() => setWireTab("macro")}
              >
                <span>{t("Macro & Filings")}</span>
                {macroAndFilings.length > 0 && (
                  <span className="wt-tab-count mono">
                    {macroAndFilings.length}
                  </span>
                )}
              </button>
            </div>

            <div className="wt-hairline-table" data-testid="intel-events-list">
              {shownRows.slice(0, 40).map((ev) => (
                <div
                  className={`wt-hairline-row wt-intel-row ${selectedEvent?.id === ev.id ? "is-selected" : ""}`}
                  key={ev.id}
                >
                  <div className="wt-intel-row-main">
                    <button
                      type="button"
                      className="intel-card-title"
                      onClick={() => setSelectedEvent(ev)}
                    >
                      {ev.title}
                    </button>
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
                </div>
              ))}
              {shownRows.length === 0 && <p className="subtle">{t("None")}</p>}
            </div>
          </div>
        </div>

        <aside className="wt-intel-right">
          {selectedEvent && (
            <article
              className="wt-wpanel intel-inspector"
              data-testid="intel-inspector"
            >
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
              <div className="wt-inspector-actions">
                <button
                  type="button"
                  className="wt-btn-rose"
                  onClick={() => askAgent(selectedEvent)}
                >
                  <MessageSquare size={14} aria-hidden="true" />
                  <span>{t("Ask in Chat")}</span>
                </button>
                <div className="wt-chip-group">
                  {selectedEvent.tickers?.[0] && (
                    <button
                      type="button"
                      className="trade-mini-btn"
                      onClick={() => openTickerChart(selectedEvent.tickers![0])}
                    >
                      <CandlestickChart size={13} aria-hidden="true" />
                      <span>{selectedEvent.tickers[0]}</span>
                    </button>
                  )}
                  <button
                    type="button"
                    className="trade-mini-btn"
                    onClick={() => createTriggerFromEvent(selectedEvent)}
                  >
                    <Zap size={13} aria-hidden="true" />
                    <span>{t("Create trigger")}</span>
                  </button>
                </div>
              </div>
            </article>
          )}

          <div className="wt-wpanel">
            <h2 className="wt-rail-title">{t("Prediction Markets")}</h2>
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
                    <span className="pill mono">
                      {formatProbPct(mkt.yesProb)}
                    </span>
                  </div>
                  <div className="intel-prob-bar">
                    <div
                      className={`intel-prob-fill ${mkt.venue === "kalshi" ? "is-kalshi" : "is-poly"}`}
                      style={{
                        width: `${Math.max(4, Math.min(100, mkt.yesProb * 100))}%`,
                      }}
                    />
                  </div>
                  <div className="intel-meta-pills">
                    <span className="pill">{mkt.venue}</span>
                    <span className="pill mono">
                      {formatCompactUSD(mkt.volume24h)}
                    </span>
                  </div>
                </article>
              ))}
              {(predictions?.markets?.length ?? 0) === 0 && (
                <p className="subtle">{t("None")}</p>
              )}
            </div>
          </div>
        </aside>
      </div>
    </section>
  );
}
