import type { AgentV2CoreClient } from "./agents-v2";
import type { Candle } from "./market-data";

export type IntelCategory =
  | "macro"
  | "crypto"
  | "geopolitics"
  | "conflict"
  | "energy"
  | "seismic"
  | "policy"
  | "prediction"
  | "sidecar";

export type IntelRegion =
  "na" | "eu" | "apac" | "mena" | "latam" | "africa" | "global";

export type IntelSource = {
  id: string;
  name: string;
  kind:
    "rss" | "gdelt" | "usgs" | "polymarket" | "kalshi" | "worldmonitor_sidecar";
  endpoint: string;
  category: IntelCategory;
  region: IntelRegion;
  enabled: boolean;
  allowLocalhost: boolean;
  builtIn: boolean;
  lastSyncAt?: number;
  lastError?: string;
};

export type IntelSourceInput = {
  id: string;
  name: string;
  kind: IntelSource["kind"];
  endpoint: string;
  category: IntelCategory;
  region: IntelRegion;
  enabled?: boolean;
  allowLocalhost?: boolean;
};

export type IntelEvent = {
  id: string;
  sourceId: string;
  sourceName: string;
  category: string;
  title: string;
  summary?: string;
  url: string;
  region: IntelRegion | string;
  countryCode?: string;
  lat: number;
  lon: number;
  tickers?: string[];
  publishedAt: number;
  ingestedAt: number;
  urgency: number;
  sentiment: "bullish" | "bearish" | "neutral" | string;
  confidence: number;
  scoreProvider: string;
  verified: boolean;
  untrusted: boolean;
};

export type CorrelatedCluster = {
  id: string;
  topic: string;
  category: string;
  region: string;
  lat: number;
  lon: number;
  eventCount: number;
  sources: string[];
  avgUrgency: number;
  sentiment: string;
  tickers?: string[];
  eventIds: string[];
};

export type PredictionMarket = {
  id: string;
  venue: "polymarket" | "kalshi" | string;
  slug: string;
  question: string;
  category: string;
  yesProb: number;
  noProb: number;
  volume24h: number;
  liquidity: number;
  change24h: number;
  url: string;
  endDate?: string;
  tickers?: string[];
  region: string;
  lat: number;
  lon: number;
  updatedAt: number;
};

export type PredictionArbitrage = {
  id: string;
  topic: string;
  polymarketId: string;
  kalshiId: string;
  polymarketProb: number;
  kalshiProb: number;
  spreadPct: number;
  similarity: number;
  polymarketUrl: string;
  kalshiUrl: string;
  tickers?: string[];
};

export type IntelMapSummary = {
  events: IntelEvent[];
  clusters: CorrelatedCluster[];
  regionCounts: Record<string, number>;
  updatedAt: number;
};

export type PredictionList = {
  markets: PredictionMarket[];
  arbitrage: PredictionArbitrage[];
  count: number;
};

export type IntelSyncResult = {
  syncedSources: number;
  eventCount: number;
  predictionCount: number;
  errors?: Record<string, string>;
  syncedAt: number;
};

export type ChartEventPin = {
  eventId: string;
  time: number;
  label: string;
  title: string;
  urgency: number;
  sentiment: string;
};

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export const INTEL_CATEGORIES: { id: string; label: string }[] = [
  { id: "", label: "All" },
  { id: "macro", label: "Macro" },
  { id: "crypto", label: "Crypto" },
  { id: "geopolitics", label: "Geopolitics" },
  { id: "energy", label: "Energy" },
  { id: "seismic", label: "Seismic" },
  { id: "sidecar", label: "Sidecar" },
];

export const INTEL_REGIONS: { id: string; label: string }[] = [
  { id: "", label: "All" },
  { id: "na", label: "NA" },
  { id: "eu", label: "EU" },
  { id: "apac", label: "APAC" },
  { id: "mena", label: "MENA" },
  { id: "latam", label: "LATAM" },
  { id: "africa", label: "Africa" },
];

export function projectLatLon(
  lat: number,
  lon: number,
  width = 960,
  height = 460,
): { x: number; y: number } {
  const safeLon = Number.isFinite(lon) ? Math.max(-180, Math.min(180, lon)) : 0;
  const safeLat = Number.isFinite(lat) ? Math.max(-60, Math.min(82, lat)) : 20;
  const x = ((safeLon + 180) / 360) * width;
  const y = ((82 - safeLat) / 142) * height;
  return {
    x: Math.round(x * 10) / 10,
    y: Math.round(y * 10) / 10,
  };
}

export function baseTickerFromSymbol(symbol: string): string {
  const upper = symbol.trim().toUpperCase();
  return upper
    .replace(/-PERP$/, "")
    .replace(/PERP$/, "")
    .replace(/\/?(USDT|USDC|USD)$/, "");
}

export function matchEventsForSymbol(
  events: IntelEvent[],
  symbol: string,
  limit = 8,
): IntelEvent[] {
  const base = baseTickerFromSymbol(symbol);
  return events
    .filter((ev) => {
      if (base && ev.tickers?.some((tk) => tk.toUpperCase() === base)) {
        return true;
      }
      return ev.category === "macro" && ev.urgency >= 0.6;
    })
    .sort((a, b) => b.publishedAt - a.publishedAt)
    .slice(0, limit);
}

export function buildChartEventPins(
  candles: Candle[],
  events: IntelEvent[],
): ChartEventPin[] {
  if (!candles.length || !events.length) return [];
  const sortedCandles = [...candles].sort((a, b) => a.time - b.time);
  const byTime = new Map<number, ChartEventPin>();

  for (const ev of events) {
    let best = sortedCandles[0];
    let bestDiff = Math.abs(sortedCandles[0].time - ev.publishedAt);
    for (let i = 1; i < sortedCandles.length; i++) {
      const diff = Math.abs(sortedCandles[i].time - ev.publishedAt);
      if (diff <= bestDiff) {
        bestDiff = diff;
        best = sortedCandles[i];
      }
    }
    const existing = byTime.get(best.time);
    if (!existing || ev.urgency > existing.urgency) {
      const tag = ev.tickers?.[0] ?? ev.category.toUpperCase();
      byTime.set(best.time, {
        eventId: ev.id,
        time: best.time,
        label: tag.slice(0, 8),
        title: ev.title,
        urgency: ev.urgency,
        sentiment: ev.sentiment,
      });
    }
  }

  return [...byTime.values()].sort((a, b) => a.time - b.time);
}

export function formatProbPct(p: number): string {
  const bounded = Number.isFinite(p) ? Math.max(0, Math.min(1, p)) : 0;
  return `${Math.round(bounded * 100)}%`;
}

export function formatCompactUSD(v: number): string {
  const val = Number.isFinite(v) ? Math.max(0, v) : 0;
  if (val >= 1e6) return `$${(val / 1e6).toFixed(2)}M`;
  if (val >= 1e3) return `$${(val / 1e3).toFixed(1)}K`;
  return `$${val.toFixed(0)}`;
}

export function buildIntelChatPrompt(ev: IntelEvent): string {
  const tickers = ev.tickers?.length ? ev.tickers.join(", ") : "macro";
  return `Analyze this untrusted ${ev.category} signal (${ev.region.toUpperCase()}, tickers: ${tickers}, urgency: ${Math.round(ev.urgency * 100)}%, sentiment: ${ev.sentiment}): "${ev.title}". Summarize potential market impact, verification caveats, and key levels to watch.`;
}

export function fetchIntelEvents(
  core: CoreRequestOnly,
  params: {
    category?: string;
    region?: string;
    ticker?: string;
    q?: string;
    minUrgency?: number;
    limit?: number;
  } = {},
) {
  const q = new URLSearchParams();
  if (params.category) q.set("category", params.category);
  if (params.region) q.set("region", params.region);
  if (params.ticker) q.set("ticker", params.ticker);
  if (params.q) q.set("q", params.q);
  if (params.minUrgency && params.minUrgency > 0)
    q.set("minUrgency", String(params.minUrgency));
  q.set("limit", String(params.limit ?? 80));
  return core.request<{ events: IntelEvent[]; count: number }>(
    `intel/events?${q.toString()}`,
  );
}

export function fetchIntelMap(
  core: CoreRequestOnly,
  params: { category?: string; minUrgency?: number; hours?: number } = {},
) {
  const q = new URLSearchParams();
  if (params.category) q.set("category", params.category);
  if (params.minUrgency && params.minUrgency > 0)
    q.set("minUrgency", String(params.minUrgency));
  q.set("hours", String(params.hours ?? 72));
  return core.request<IntelMapSummary>(`intel/map?${q.toString()}`);
}

export function fetchPredictions(
  core: CoreRequestOnly,
  params: {
    venue?: string;
    category?: string;
    q?: string;
    limit?: number;
  } = {},
) {
  const q = new URLSearchParams();
  if (params.venue) q.set("venue", params.venue);
  if (params.category) q.set("category", params.category);
  if (params.q) q.set("q", params.q);
  q.set("limit", String(params.limit ?? 60));
  return core.request<PredictionList>(`intel/predictions?${q.toString()}`);
}

export function fetchIntelSources(core: CoreRequestOnly) {
  return core.request<{ sources: IntelSource[]; count: number }>(
    "intel/sources",
  );
}

export function saveIntelSource(
  core: CoreRequestOnly,
  input: IntelSourceInput,
) {
  return core.request<IntelSource>("intel/sources", "PUT", input);
}

export function deleteIntelSource(core: CoreRequestOnly, id: string) {
  return core.request<{ deleted: boolean; id: string }>(
    `intel/sources/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export function syncIntelSources(core: CoreRequestOnly, sourceId?: string) {
  return core.request<IntelSyncResult>(
    "intel/sync",
    "POST",
    sourceId ? { sourceId } : undefined,
  );
}
