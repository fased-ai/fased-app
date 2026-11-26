import { useEffect, useState } from "react";
import { useCore } from "../../core";
import { useUI } from "../../ui-preferences";
import { Plus, Trash2 } from "../../icons";
import {
  CHART_VENUES,
  fetchCustomFeeds,
  fetchWatchlists,
  ingestCustomCandles,
  saveWatchlist,
  type CustomFeedSummary,
  type Venue,
  type Watchlist,
  type WatchlistItem,
} from "../../market-data";

export function WatchlistPanel({
  activeVenue,
  activeSymbol,
  enabledVenues,
  onSelectItem,
}: {
  activeVenue: string;
  activeSymbol: string;
  enabledVenues?: string[];
  onSelectItem: (item: WatchlistItem) => void;
}) {
  const venueOptions =
    enabledVenues && enabledVenues.length > 0
      ? enabledVenues
      : [...CHART_VENUES];
  const { t } = useUI();
  const core = useCore();
  const [lists, setLists] = useState<Watchlist[]>([]);
  const [venue, setVenue] = useState<Venue>("hyperliquid");
  const [symbol, setSymbol] = useState("");
  const [filterQuery, setFilterQuery] = useState("");
  const [venueFilter, setVenueFilter] = useState<"all" | Venue>("all");
  const [saving, setSaving] = useState(false);
  const [customFeeds, setCustomFeeds] = useState<CustomFeedSummary[]>([]);
  const [csvSymbol, setCsvSymbol] = useState("CUSTOM1");
  const [csvInterval, setCsvInterval] = useState("1h");
  const [csvText, setCsvText] = useState("");
  const [csvStatus, setCsvStatus] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;
    fetchWatchlists(core, controller.signal)
      .then((res) => {
        if (mounted) setLists(res);
      })
      .catch(() => {});
    fetchCustomFeeds(core, controller.signal)
      .then((res) => {
        if (mounted) setCustomFeeds(res);
      })
      .catch(() => {});
    return () => {
      mounted = false;
      controller.abort();
    };
  }, [core]);

  const current: Watchlist = lists[0] ?? {
    id: "core-desk",
    name: "Core Desk",
    items: [],
  };
  const visibleItems = current.items.filter((item) => {
    if (venueFilter !== "all" && item.venue !== venueFilter) return false;
    const q = filterQuery.trim().toLowerCase();
    if (
      q &&
      !item.symbol.toLowerCase().includes(q) &&
      !item.venue.toLowerCase().includes(q)
    ) {
      return false;
    }
    return true;
  });

  async function addItem(e: React.FormEvent) {
    e.preventDefault();
    const sym = symbol.trim();
    if (!sym || saving) return;
    setSaving(true);
    try {
      const next: Watchlist = {
        ...current,
        items: [
          ...current.items.filter(
            (x) => !(x.venue === venue && x.symbol === sym),
          ),
          { venue, symbol: sym, interval: "1h", style: "standard" },
        ],
      };
      const saved = await saveWatchlist(core, next);
      setLists([saved, ...lists.slice(1)]);
      setSymbol("");
    } finally {
      setSaving(false);
    }
  }

  async function removeItem(item: WatchlistItem) {
    if (saving) return;
    setSaving(true);
    try {
      const next: Watchlist = {
        ...current,
        items: current.items.filter(
          (x) => !(x.venue === item.venue && x.symbol === item.symbol),
        ),
      };
      const saved = await saveWatchlist(core, next);
      setLists([saved, ...lists.slice(1)]);
    } finally {
      setSaving(false);
    }
  }

  async function importCsvFeed(e: React.FormEvent) {
    e.preventDefault();
    const sym = csvSymbol.trim();
    const raw = csvText.trim();
    if (!sym || !raw || saving) return;
    setSaving(true);
    setCsvStatus("");
    try {
      const summary = await ingestCustomCandles(core, {
        symbol: sym,
        interval: csvInterval,
        format: "csv",
        csv: raw,
      });
      const feeds = await fetchCustomFeeds(core);
      setCustomFeeds(feeds);
      setCsvText("");
      setCsvStatus(`${summary.symbol} (${summary.barCount})`);
      onSelectItem({
        venue: "custom",
        symbol: summary.symbol,
        interval: summary.interval,
        style: "standard",
      });
    } catch (err) {
      setCsvStatus(err instanceof Error ? err.message : t("Save failed"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <aside className="wt-wpanel trade-watchlist" aria-label={t("Watchlist")}>
      <div className="trade-pane-head">
        <h2>{t("Watchlist")}</h2>
        <span className="trade-source-pill mono">{current.items.length}</span>
      </div>

      <form className="trade-watchlist-form" onSubmit={(e) => void addItem(e)}>
        <div className="trade-venue-filter-row" role="group" aria-label={t("Watchlist venue")}>
          {venueOptions.map((v) => (
            <button
              key={v}
              type="button"
              className={`trade-venue-filter-btn ${venue === v ? "is-selected" : ""}`}
              onClick={() => setVenue(v as Venue)}
            >
              {v === "hyperliquid" ? "HL" : v}
            </button>
          ))}
        </div>
        <div className="trade-watchlist-addrow">
          <input
            aria-label={t("Add ticker")}
            placeholder={t("Ticker")}
            value={symbol}
            onChange={(e) => setSymbol(e.target.value)}
          />
          <button
            type="submit"
            className="trade-mini-btn"
            disabled={saving}
            aria-label={t("Add to watchlist")}
          >
            <Plus size={14} />
          </button>
        </div>
      </form>

      <div className="trade-watchlist-filter">
        <input
          type="search"
          aria-label={t("Filter markets")}
          placeholder={t("Filter markets")}
          value={filterQuery}
          onChange={(e) => setFilterQuery(e.target.value)}
        />
        <div
          className="trade-venue-filter-row"
          role="group"
          aria-label={t("Filter markets")}
        >
          {(
            [
              { id: "all", label: t("All") },
              { id: "hyperliquid", label: "HL" },
              { id: "binance", label: "Binance" },
              { id: "kraken", label: "Kraken" },
              { id: "solana", label: "Solana" },
            ] as const
          ).map((chip) => (
            <button
              key={chip.id}
              type="button"
              className={`trade-venue-filter-btn ${venueFilter === chip.id ? "is-selected" : ""}`}
              aria-pressed={venueFilter === chip.id}
              onClick={() => setVenueFilter(chip.id)}
            >
              {chip.label}
            </button>
          ))}
        </div>
      </div>

      <ul className="trade-watchlist-items">
        {visibleItems.map((item) => {
          const isActive =
            item.venue === activeVenue && item.symbol === activeSymbol;
          return (
            <li
              key={`${item.venue}:${item.symbol}`}
              className={isActive ? "is-active" : ""}
            >
              <button
                type="button"
                className="trade-watchlist-pick"
                onClick={() => onSelectItem(item)}
              >
                <strong>{item.symbol}</strong>
                <span>
                  {item.venue} · {item.interval || "1h"}
                </span>
              </button>
              <button
                type="button"
                className="trade-mini-btn"
                aria-label={`${t("Remove")} ${item.symbol}`}
                onClick={() => void removeItem(item)}
              >
                <Trash2 size={13} />
              </button>
            </li>
          );
        })}
      </ul>

      <details className="trade-csv-import" data-testid="custom-csv-import">
        <summary>{t("Import CSV")}</summary>
        <form
          className="trade-csv-form"
          onSubmit={(e) => void importCsvFeed(e)}
        >
          <div className="trade-csv-row">
            <input
              aria-label={t("Symbol")}
              placeholder="CUSTOM1"
              value={csvSymbol}
              onChange={(e) => setCsvSymbol(e.target.value)}
            />
            <div className="trade-venue-filter-row" role="group" aria-label={t("Interval")}>
              {["1m", "5m", "15m", "1h", "4h", "1d"].map((iv) => (
                <button
                  key={iv}
                  type="button"
                  className={`trade-venue-filter-btn ${csvInterval === iv ? "is-selected" : ""}`}
                  onClick={() => setCsvInterval(iv)}
                >
                  {iv}
                </button>
              ))}
            </div>
          </div>
          <textarea
            aria-label={t("CSV data")}
            placeholder="time,open,high,low,close,volume"
            rows={3}
            value={csvText}
            onChange={(e) => setCsvText(e.target.value)}
          />
          <button
            type="submit"
            className="primary"
            disabled={core.mode !== "ready" || saving || !csvText.trim()}
          >
            {t("Import")}
          </button>
          {csvStatus && <span className="trade-source-pill">{csvStatus}</span>}
        </form>
        {customFeeds.length > 0 && (
          <ul className="trade-watchlist-items">
            {customFeeds.map((f) => (
              <li key={`${f.symbol}:${f.interval}`}>
                <button
                  type="button"
                  className="trade-watchlist-pick"
                  onClick={() =>
                    onSelectItem({
                      venue: "custom",
                      symbol: f.symbol,
                      interval: f.interval,
                      style: "standard",
                    })
                  }
                >
                  <strong>{f.symbol}</strong>
                  <span>
                    custom · {f.interval} · {f.barCount}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </details>
    </aside>
  );
}
