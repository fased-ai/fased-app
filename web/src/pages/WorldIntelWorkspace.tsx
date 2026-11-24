import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "../components/shared";
import { WorldMapView } from "../components/intel/WorldMapView";
import {
  CandlestickChart,
  ExternalLink,
  MessageSquare,
  RefreshCw,
  Zap,
} from "../icons";
import { parseTradeWorkspaceConfig, TRADE_STORAGE_KEY } from "../market-data";
import { TRIGGER_DRAFT_KEY, type TriggerDraftSeed } from "../automation";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import {
  buildIntelChatPrompt,
  deleteIntelSource,
  fetchIntelMap,
  fetchIntelSources,
  fetchPredictions,
  formatCompactUSD,
  formatProbPct,
  INTEL_CATEGORIES,
  saveIntelSource,
  syncIntelSources,
  type IntelCategory,
  type IntelEvent,
  type IntelMapSummary,
  type IntelSource,
  type PredictionList,
} from "../world-intel";
import "./WorldIntelWorkspace.css";

type IntelTab = "events" | "predictions" | "sources";

export function WorldIntelWorkspace() {
  const { t } = useUI();
  const { core, agents, streamClient, launchChatPrompt, navigate } =
    useWorkspace();

  const [tab, setTab] = useState<IntelTab>("events");
  const [category, setCategory] = useState("");
  const [region, setRegion] = useState("");
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const [mapSummary, setMapSummary] = useState<IntelMapSummary | null>(null);
  const [predictions, setPredictions] = useState<PredictionList | null>(null);
  const [sources, setSources] = useState<IntelSource[]>([]);
  const [selectedEvent, setSelectedEvent] = useState<IntelEvent | null>(null);

  const [srcId, setSrcId] = useState("");
  const [srcName, setSrcName] = useState("");
  const [srcKind, setSrcKind] = useState<"rss" | "worldmonitor_sidecar">("rss");
  const [srcEndpoint, setSrcEndpoint] = useState("");
  const [srcCategory, setSrcCategory] = useState<IntelCategory>("macro");
  const [srcAllowLocal, setSrcAllowLocal] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query), 300);
    return () => window.clearTimeout(timer);
  }, [query]);

  const loadAll = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const [mapRes, predRes, srcRes] = await Promise.all([
        fetchIntelMap(core, { category, hours: 72 }),
        fetchPredictions(core, { category, q: debouncedQuery, limit: 60 }),
        fetchIntelSources(core),
      ]);
      setMapSummary(mapRes);
      setPredictions(predRes);
      setSources(srcRes.sources ?? []);
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

  async function handleSync(sourceId?: string) {
    if (busy || core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await syncIntelSources(core, sourceId);
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sync failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleSaveSource() {
    if (busy || core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await saveIntelSource(core, {
        id: srcId.trim(),
        name: srcName.trim(),
        kind: srcKind,
        endpoint: srcEndpoint.trim(),
        category: srcCategory,
        region: "global",
        enabled: true,
        allowLocalhost: srcKind === "worldmonitor_sidecar" && srcAllowLocal,
      });
      setSrcId("");
      setSrcName("");
      setSrcEndpoint("");
      setSrcAllowLocal(false);
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save source failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleToggleSource(src: IntelSource) {
    if (busy || core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await saveIntelSource(core, {
        id: src.id,
        name: src.name,
        kind: src.kind,
        endpoint: src.endpoint,
        category: src.category,
        region: src.region,
        enabled: !src.enabled,
        allowLocalhost: src.allowLocalhost,
      });
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleDeleteSource(id: string) {
    if (busy || core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await deleteIntelSource(core, id);
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="intel-workspace" aria-label={t("World Intel")}>
      <header className="intel-toolbar">
        <div className="intel-title-group">
          <h1>{t("World Intel")}</h1>
          <Badge>{t("Read-only")}</Badge>
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
            className="secondary"
            disabled={busy || core.mode !== "ready"}
            onClick={() => void handleSync()}
          >
            <RefreshCw size={14} aria-hidden="true" />
            <span>{t("Sync")}</span>
          </button>
        </div>
      </header>

      {error && <p role="alert">{error}</p>}

      <div className="intel-layout">
        <aside className="intel-sources-rail" aria-label={t("Categories")}>
          <div className="intel-rail-group">
            {INTEL_CATEGORIES.map((c) => (
              <button
                key={c.id || "all"}
                type="button"
                className={`intel-filter-pill ${category === c.id ? "is-selected" : ""}`}
                aria-pressed={category === c.id}
                onClick={() => setCategory(c.id)}
              >
                {t(c.label)}
              </button>
            ))}
          </div>
          <div className="intel-rail-sources">
            {sources.slice(0, 8).map((src) => (
              <button
                key={src.id}
                type="button"
                className="intel-rail-source"
                onClick={() => setTab("sources")}
              >
                <span className="intel-rail-source-name">{src.name}</span>
                <span
                  className={`intel-source-dot ${src.enabled ? "is-on" : ""}`}
                  aria-hidden="true"
                />
              </button>
            ))}
          </div>
          <a className="intel-rail-link" href="#agents/news">
            {t("Agent RSS inputs")}
          </a>
        </aside>

        <div className="intel-left-col">
          <WorldMapView
            events={filteredEvents}
            clusters={mapSummary?.clusters ?? []}
            regionCounts={mapSummary?.regionCounts ?? {}}
            selectedRegion={region}
            selectedEventId={selectedEvent?.id ?? ""}
            onSelectRegion={setRegion}
            onSelectEvent={setSelectedEvent}
          />

          {(mapSummary?.clusters?.length ?? 0) > 0 && (
            <div
              className="intel-panel"
              aria-label={t("Clusters")}
              data-testid="intel-clusters"
            >
              <div className="intel-card-row">
                <strong>{t("Clusters")}</strong>
                <span className="pill">{mapSummary?.clusters.length ?? 0}</span>
              </div>
              <div className="intel-clusters-strip">
                {mapSummary?.clusters.map((cl) => (
                  <button
                    key={cl.id}
                    type="button"
                    className="intel-cluster-chip"
                    onClick={() => setRegion(cl.region)}
                  >
                    <span>{cl.topic}</span>
                    <span className="mono">{cl.eventCount}</span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {selectedEvent && (
            <article className="intel-inspector" data-testid="intel-inspector">
              <div className="intel-card-row">
                <strong>{selectedEvent.title}</strong>
                <div className="intel-meta-pills">
                  <span className="pill">
                    {`${t("Urgency")} ${Math.round(selectedEvent.urgency * 100)}%`}
                  </span>
                  <span className="pill">
                    {sentimentLabel(selectedEvent.sentiment)}
                  </span>
                </div>
              </div>
              {selectedEvent.summary && (
                <p className="intel-card-summary">{selectedEvent.summary}</p>
              )}
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
                    <span>{`${t("Open chart")} (${selectedEvent.tickers[0]})`}</span>
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

        <div className="intel-right-col">
          <div className="intel-panel">
            <div className="intel-tabs" role="tablist">
              {(
                [
                  {
                    id: "events",
                    label: "Events",
                    count: filteredEvents.length,
                  },
                  {
                    id: "predictions",
                    label: "Predictions",
                    count: predictions?.count ?? 0,
                  },
                  { id: "sources", label: "Sources", count: sources.length },
                ] as const
              ).map((tItem) => (
                <button
                  key={tItem.id}
                  type="button"
                  role="tab"
                  aria-selected={tab === tItem.id}
                  className={`intel-tab-btn ${tab === tItem.id ? "is-selected" : ""}`}
                  onClick={() => setTab(tItem.id)}
                >
                  <span>{t(tItem.label)}</span>
                  <span className="intel-pill-count">{tItem.count}</span>
                </button>
              ))}
            </div>

            {tab === "events" && (
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
                    {ev.summary && (
                      <p className="intel-card-summary">{ev.summary}</p>
                    )}
                    <div className="intel-meta-pills">
                      <span
                        className={`pill ${ev.urgency >= 0.7 ? "intel-pill-high" : ""}`}
                      >
                        {`${Math.round(ev.urgency * 100)}%`}
                      </span>
                      <span className="pill">
                        {sentimentLabel(ev.sentiment)}
                      </span>
                      <span className="pill">{ev.sourceName}</span>
                      {ev.verified ? (
                        <span className="pill">{t("Verified")}</span>
                      ) : (
                        ev.untrusted && (
                          <span className="pill">{t("Untrusted")}</span>
                        )
                      )}
                      {ev.tickers?.map((tk) => (
                        <span key={tk} className="pill mono">
                          {tk}
                        </span>
                      ))}
                    </div>
                    <div className="intel-card-actions">
                      <button
                        type="button"
                        className="secondary"
                        onClick={() => askAgent(ev)}
                      >
                        <MessageSquare size={13} aria-hidden="true" />
                        <span>{t("Ask in Chat")}</span>
                      </button>
                      <button
                        type="button"
                        className="secondary"
                        onClick={() => createTriggerFromEvent(ev)}
                      >
                        <Zap size={13} aria-hidden="true" />
                        <span>{t("Create trigger")}</span>
                      </button>
                      {ev.tickers?.[0] && (
                        <button
                          type="button"
                          className="secondary"
                          onClick={() => openTickerChart(ev.tickers![0])}
                        >
                          <CandlestickChart size={13} aria-hidden="true" />
                          <span>{t("Open chart")}</span>
                        </button>
                      )}
                      {ev.url?.startsWith("https://") && (
                        <a
                          href={ev.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="secondary"
                        >
                          <ExternalLink size={13} aria-hidden="true" />
                          <span>{t("Open")}</span>
                        </a>
                      )}
                    </div>
                  </article>
                ))}
              </div>
            )}

            {tab === "predictions" && (
              <div className="intel-list" data-testid="intel-predictions-list">
                {(predictions?.arbitrage ?? []).map((arb) => (
                  <article className="intel-arb-card" key={arb.id}>
                    <div className="intel-card-row">
                      <strong>{`${t("Arbitrage")}: ${arb.topic}`}</strong>
                      <span className="pill mono">{`Δ ${arb.spreadPct.toFixed(1)}%`}</span>
                    </div>
                    <div className="intel-meta-pills mono">
                      <span className="pill">{`Polymarket ${formatProbPct(arb.polymarketProb)}`}</span>
                      <span className="pill">{`Kalshi ${formatProbPct(arb.kalshiProb)}`}</span>
                    </div>
                    <div className="intel-prob-bars" aria-hidden="true">
                      <div className="intel-prob-track">
                        <div
                          className="intel-prob-fill is-poly"
                          style={{
                            width: `${Math.max(4, Math.min(100, Math.round(arb.polymarketProb * 100)))}%`,
                          }}
                        />
                      </div>
                      <div className="intel-prob-track">
                        <div
                          className="intel-prob-fill is-kalshi"
                          style={{
                            width: `${Math.max(4, Math.min(100, Math.round(arb.kalshiProb * 100)))}%`,
                          }}
                        />
                      </div>
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
                    <div className="intel-meta-pills">
                      <span className="pill">{mkt.venue}</span>
                      <span className="pill mono">
                        {`${t("24h Vol")} ${formatCompactUSD(mkt.volume24h)}`}
                      </span>
                      {mkt.tickers?.map((tk) => (
                        <span key={tk} className="pill mono">
                          {tk}
                        </span>
                      ))}
                    </div>
                  </article>
                ))}
              </div>
            )}

            {tab === "sources" && (
              <div className="intel-list" data-testid="intel-sources-list">
                <div className="intel-card-actions">
                  <a className="secondary" href="#agents/news">
                    {t("Agent RSS inputs")}
                  </a>
                </div>
                <form
                  className="intel-source-form"
                  aria-label={t("Custom source")}
                  onSubmit={(e) => {
                    e.preventDefault();
                    void handleSaveSource();
                  }}
                >
                  <div className="intel-source-inputs">
                    <input
                      type="text"
                      aria-label={t("Source ID")}
                      placeholder={t("Source ID")}
                      value={srcId}
                      disabled={busy}
                      onChange={(e) => setSrcId(e.target.value)}
                    />
                    <input
                      type="text"
                      aria-label={t("Name")}
                      placeholder={t("Name")}
                      value={srcName}
                      disabled={busy}
                      onChange={(e) => setSrcName(e.target.value)}
                    />
                    <select
                      aria-label={t("Kind")}
                      value={srcKind}
                      disabled={busy}
                      onChange={(e) =>
                        setSrcKind(
                          e.target.value as "rss" | "worldmonitor_sidecar",
                        )
                      }
                    >
                      <option value="rss">RSS / Atom</option>
                      <option value="worldmonitor_sidecar">
                        WorldMonitor Sidecar
                      </option>
                    </select>
                    <input
                      type="url"
                      aria-label={t("Endpoint")}
                      placeholder="https://..."
                      value={srcEndpoint}
                      disabled={busy}
                      onChange={(e) => setSrcEndpoint(e.target.value)}
                    />
                    <select
                      aria-label={t("Category")}
                      value={srcCategory}
                      disabled={busy}
                      onChange={(e) =>
                        setSrcCategory(e.target.value as IntelCategory)
                      }
                    >
                      {INTEL_CATEGORIES.filter((c) => c.id !== "").map((c) => (
                        <option key={c.id} value={c.id}>
                          {t(c.label)}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="intel-card-actions">
                    {srcKind === "worldmonitor_sidecar" && (
                      <label className="settings-checkbox-label">
                        <input
                          type="checkbox"
                          checked={srcAllowLocal}
                          disabled={busy}
                          onChange={(e) => setSrcAllowLocal(e.target.checked)}
                        />
                        <span>{t("Allow localhost")}</span>
                      </label>
                    )}
                    <button
                      type="submit"
                      className="primary"
                      disabled={
                        busy ||
                        !srcId.trim() ||
                        !srcName.trim() ||
                        !srcEndpoint.trim()
                      }
                    >
                      {t("Save")}
                    </button>
                  </div>
                </form>

                {sources.map((src) => (
                  <article
                    className="intel-card"
                    key={src.id}
                    data-testid={`intel-source-${src.id}`}
                  >
                    <div className="intel-card-row">
                      <strong>{src.name}</strong>
                      <div className="intel-meta-pills">
                        <span className="pill mono">{src.kind}</span>
                        <span className="pill">
                          {src.enabled ? t("Enabled") : t("Disabled")}
                        </span>
                      </div>
                    </div>
                    <div className="intel-card-summary mono">
                      {src.endpoint}
                    </div>
                    <div className="intel-card-actions">
                      <button
                        type="button"
                        className="secondary"
                        disabled={busy}
                        onClick={() => void handleToggleSource(src)}
                      >
                        {src.enabled ? t("Disabled") : t("Enabled")}
                      </button>
                      <button
                        type="button"
                        className="secondary"
                        disabled={busy || !src.enabled}
                        onClick={() => void handleSync(src.id)}
                      >
                        {t("Sync")}
                      </button>
                      {!src.builtIn && (
                        <button
                          type="button"
                          className="secondary"
                          disabled={busy}
                          onClick={() => void handleDeleteSource(src.id)}
                        >
                          {t("Remove")}
                        </button>
                      )}
                    </div>
                    {src.lastError && <p role="alert">{src.lastError}</p>}
                  </article>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
