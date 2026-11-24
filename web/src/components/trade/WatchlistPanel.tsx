import { useEffect, useState } from "react";
import { useCore } from "../../core";
import { useUI } from "../../ui-preferences";
import { Plus, Trash2 } from "../../icons";
import {
  CHART_VENUES,
  fetchWatchlists,
  saveWatchlist,
  type Venue,
  type Watchlist,
  type WatchlistItem,
} from "../../market-data";

export function WatchlistPanel({
  activeVenue,
  activeSymbol,
  onSelectItem,
}: {
  activeVenue: string;
  activeSymbol: string;
  onSelectItem: (item: WatchlistItem) => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const [lists, setLists] = useState<Watchlist[]>([]);
  const [venue, setVenue] = useState<Venue>("hyperliquid");
  const [symbol, setSymbol] = useState("");
  const [filterQuery, setFilterQuery] = useState("");
  const [venueFilter, setVenueFilter] = useState<"all" | Venue>("all");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;
    fetchWatchlists(core, controller.signal)
      .then((res) => {
        if (mounted) setLists(res);
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

  return (
    <aside className="panel trade-watchlist" aria-label={t("Watchlist")}>
      <div className="trade-pane-head">
        <h2>{t("Watchlist")}</h2>
        <span className="trade-source-pill">{current.items.length}</span>
      </div>

      <form className="trade-watchlist-form" onSubmit={(e) => void addItem(e)}>
        <select
          aria-label={t("Watchlist venue")}
          value={venue}
          onChange={(e) => setVenue(e.target.value as Venue)}
        >
          {CHART_VENUES.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
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
    </aside>
  );
}
