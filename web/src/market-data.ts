import type { SSEFrame } from "./agents-v2";

export type Venue =
  | "hyperliquid"
  | "binance"
  | "bybit"
  | "kraken"
  | "solana"
  | "lighter"
  | "fred"
  | "sec"
  | "custom";

export type Interval = "1m" | "5m" | "15m" | "1h" | "4h" | "1d";

export type CandleStyle =
  | "standard"
  | "heikin_ashi"
  | "renko"
  | "footprint"
  | "range"
  | "kagi"
  | "point_figure"
  | "three_line_break"
  | "tick"
  | "volume";

export type Instrument = {
  venue: Venue;
  symbol: string;
  base: string;
  quote: string;
  assetClass: string;
  tickSize?: number;
  lotSize?: number;
  maxLeverage?: number;
  updatedAt: number;
};

export type Candle = {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  buyVolume?: number;
  sellVolume?: number;
  trades?: number;
};

export type CandleSeries = {
  venue: Venue | string;
  symbol: string;
  interval: Interval | string;
  style: CandleStyle | string;
  candles: Candle[];
  source: string;
  fixture?: boolean;
  updatedAt: number;
};

export type TradeTick = {
  id: string;
  time: number;
  price: number;
  size: number;
  side: "buy" | "sell";
};

export type OrderBookLevel = {
  price: number;
  size: number;
  total: number;
};

export type OrderBookSnapshot = {
  venue: string;
  symbol: string;
  time: number;
  bids: OrderBookLevel[];
  asks: OrderBookLevel[];
  spread: number;
  midPrice: number;
  source: string;
  fixture?: boolean;
};

export type FootprintLevel = {
  price: number;
  bidVol: number;
  askVol: number;
  delta: number;
  totalVol: number;
};

export type FootprintBar = {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  candleVolume?: number;
  delta: number;
  cvd: number;
  vpoc: number;
  vah: number;
  val: number;
  sourceKind?: "ticks" | "partial_ticks" | "ohlcv_estimated";
  tickCoverage?: number;
  levels: FootprintLevel[];
};

export type FootprintSeries = {
  venue: string;
  symbol: string;
  interval: string;
  stepSize: number;
  bars: FootprintBar[];
  source: string;
  fixture?: boolean;
  updatedAt: number;
};

export type MacroPoint = {
  seriesId: string;
  date: string;
  value: number;
  source: string;
  updatedAt: number;
};

export type SECFiling = {
  cik: string;
  company: string;
  form: string;
  filingDate: string;
  accessionNumber: string;
  primaryDocument: string;
  url: string;
  description?: string;
};

export type CohortEntry = {
  address: string;
  displayName?: string;
  accountValue: number;
  dayRoi: number;
  weekRoi: number;
  monthRoi: number;
  allTimeRoi: number;
  rank: number;
};

export type CohortSnapshot = {
  venue: string;
  window: string;
  updatedAt: number;
  entries: CohortEntry[];
  source: string;
  fixture?: boolean;
};

export type WatchlistItem = {
  venue: Venue | string;
  symbol: string;
  label?: string;
  interval?: Interval | string;
  style?: CandleStyle | string;
};

export type Watchlist = {
  id: string;
  name: string;
  items: WatchlistItem[];
  createdAt?: number;
  updatedAt?: number;
};

export type TradeLayoutMode = "1x1" | "1x2" | "2x2" | "3x3" | "4x4";
export type OrderflowTab =
  "ticket" | "footprint" | "dom" | "tape" | "intel" | "desk";
export type DrawingToolMode =
  "none" | "hline" | "trendline" | "fib" | "paper_buy" | "paper_sell";

export type ChartDrawing = {
  id: string;
  kind: "hline" | "trendline" | "fib";
  p1: { time: number; price: number };
  p2?: { time: number; price: number };
  label?: string;
  venue?: string;
  symbol?: string;
  interval?: string;
  createdAt?: number;
  updatedAt?: number;
};

export type PaperOrderPreview = {
  id: string;
  side: "buy" | "sell";
  price: number;
  size: number;
  label: string;
};

export type ChartTileConfig = {
  id: string;
  venue: Venue | string;
  symbol: string;
  interval: Interval;
  style: CandleStyle;
  scriptId?: string;
  drawings: ChartDrawing[];
  paperOrders: PaperOrderPreview[];
};

export type TradeWorkspaceConfig = {
  version: 1;
  layout: TradeLayoutMode;
  syncCrosshair: boolean;
  activeTileId: string;
  showWatchlist: boolean;
  showOrderflow: boolean;
  orderflowTab: OrderflowTab;
  tiles: ChartTileConfig[];
  drawingsBySymbol?: Record<string, ChartDrawing[]>;
  paperOrdersBySymbol?: Record<string, PaperOrderPreview[]>;
};

export type CrosshairSyncPoint = {
  sourceId: string;
  time: number | null;
  price: number | null;
};

export const TRADE_STORAGE_KEY = "fased.trade.workspace.v1";

export const CHART_VENUES: readonly Venue[] = [
  "hyperliquid",
  "binance",
  "bybit",
  "kraken",
  "solana",
  "lighter",
  "custom",
];

export const DOM_SUPPORTED_VENUES = new Set([
  "hyperliquid",
  "binance",
  "bybit",
]);
export const TAPE_SUPPORTED_VENUES = new Set([
  "hyperliquid",
  "binance",
  "bybit",
  "kraken",
]);

export function symbolOverlayKey(venue: string, symbol: string): string {
  return `${venue.trim().toLowerCase()}:${symbol.trim().toUpperCase()}`;
}

export const CHART_INTERVALS: readonly Interval[] = [
  "1m",
  "5m",
  "15m",
  "1h",
  "4h",
  "1d",
];

export const CANDLE_STYLES: readonly { id: CandleStyle; label: string }[] = [
  { id: "standard", label: "Standard" },
  { id: "heikin_ashi", label: "Heikin Ashi" },
  { id: "renko", label: "Renko" },
  { id: "footprint", label: "Footprint (Orderflow)" },
  { id: "range", label: "Range" },
  { id: "kagi", label: "Kagi" },
  { id: "point_figure", label: "Point & Figure" },
  { id: "three_line_break", label: "3-Line Break" },
  { id: "tick", label: "Tick" },
  { id: "volume", label: "Volume" },
];

export const FIB_RATIOS = [0, 0.236, 0.382, 0.5, 0.618, 0.786, 1] as const;

export function layoutTileCount(layout: TradeLayoutMode): number {
  switch (layout) {
    case "1x1":
      return 1;
    case "1x2":
      return 2;
    case "2x2":
      return 4;
    case "3x3":
      return 9;
    case "4x4":
      return 16;
  }
}

const DEFAULT_SEEDS: {
  venue: Venue;
  symbol: string;
  interval: Interval;
  style: CandleStyle;
}[] = [
  { venue: "hyperliquid", symbol: "BTC", interval: "1h", style: "standard" },
  { venue: "hyperliquid", symbol: "ETH", interval: "1h", style: "standard" },
  {
    venue: "hyperliquid",
    symbol: "SOL",
    interval: "15m",
    style: "heikin_ashi",
  },
  { venue: "binance", symbol: "BTCUSDT", interval: "15m", style: "standard" },
  { venue: "kraken", symbol: "ETHUSD", interval: "1h", style: "standard" },
  { venue: "kraken", symbol: "XBTUSD", interval: "1h", style: "standard" },
  { venue: "hyperliquid", symbol: "HYPE", interval: "15m", style: "renko" },
  { venue: "binance", symbol: "ETHUSDT", interval: "5m", style: "standard" },
];

export function createTile(index: number): ChartTileConfig {
  const seed = DEFAULT_SEEDS[index % DEFAULT_SEEDS.length];
  return {
    id: `tile-${index + 1}`,
    venue: seed.venue,
    symbol: seed.symbol,
    interval: seed.interval,
    style: seed.style,
    drawings: [],
    paperOrders: [],
  };
}

export function ensureTilesForLayout(
  tiles: ChartTileConfig[],
  layout: TradeLayoutMode,
): ChartTileConfig[] {
  const target = layoutTileCount(layout);
  const next = tiles.slice(0, 16);
  while (next.length < Math.max(target, 4)) {
    next.push(createTile(next.length));
  }
  return next;
}

export function defaultTradeWorkspaceConfig(): TradeWorkspaceConfig {
  return {
    version: 1,
    layout: "2x2",
    syncCrosshair: true,
    activeTileId: "tile-1",
    showWatchlist: true,
    showOrderflow: true,
    orderflowTab: "footprint",
    tiles: [0, 1, 2, 3].map(createTile),
    drawingsBySymbol: {},
    paperOrdersBySymbol: {},
  };
}

export function parseTradeWorkspaceConfig(
  raw: string | null,
): TradeWorkspaceConfig {
  const def = defaultTradeWorkspaceConfig();
  if (!raw) return def;
  try {
    const v = JSON.parse(raw) as Partial<TradeWorkspaceConfig>;
    if (!v || v.version !== 1) return def;
    const layout: TradeLayoutMode = (
      ["1x1", "1x2", "2x2", "3x3", "4x4"] as const
    ).includes(v.layout as TradeLayoutMode)
      ? (v.layout as TradeLayoutMode)
      : def.layout;
    const orderflowTab: OrderflowTab = (
      ["ticket", "footprint", "dom", "tape", "intel", "desk"] as const
    ).includes(v.orderflowTab as OrderflowTab)
      ? (v.orderflowTab as OrderflowTab)
      : def.orderflowTab;
    const drawingsBySymbol: Record<string, ChartDrawing[]> = {};
    if (v.drawingsBySymbol && typeof v.drawingsBySymbol === "object") {
      for (const [k, list] of Object.entries(v.drawingsBySymbol).slice(0, 64)) {
        if (Array.isArray(list) && list.length > 0) {
          drawingsBySymbol[k] = list.slice(-32);
        }
      }
    }
    const paperOrdersBySymbol: Record<string, PaperOrderPreview[]> = {};
    if (v.paperOrdersBySymbol && typeof v.paperOrdersBySymbol === "object") {
      for (const [k, list] of Object.entries(v.paperOrdersBySymbol).slice(
        0,
        64,
      )) {
        if (Array.isArray(list) && list.length > 0) {
          paperOrdersBySymbol[k] = list.slice(-16);
        }
      }
    }
    const rawTiles = Array.isArray(v.tiles) ? v.tiles : [];
    const tiles: ChartTileConfig[] = rawTiles.slice(0, 16).map((item, idx) => {
      const venue =
        typeof item?.venue === "string" && item.venue.trim()
          ? item.venue
          : createTile(idx).venue;
      const symbol =
        typeof item?.symbol === "string" && item.symbol.trim()
          ? item.symbol
          : createTile(idx).symbol;
      const ovKey = symbolOverlayKey(venue, symbol);
      const legacyDrawings = Array.isArray(item?.drawings)
        ? item.drawings.slice(-32)
        : [];
      if (legacyDrawings.length > 0 && !drawingsBySymbol[ovKey]?.length) {
        drawingsBySymbol[ovKey] = legacyDrawings;
      }
      const legacyOrders = Array.isArray(item?.paperOrders)
        ? item.paperOrders.slice(-16)
        : [];
      if (legacyOrders.length > 0 && !paperOrdersBySymbol[ovKey]?.length) {
        paperOrdersBySymbol[ovKey] = legacyOrders;
      }
      return {
        id:
          typeof item?.id === "string" && item.id.trim()
            ? item.id
            : `tile-${idx + 1}`,
        venue,
        symbol,
        interval: CHART_INTERVALS.includes(item?.interval as Interval)
          ? (item.interval as Interval)
          : "1h",
        style: CANDLE_STYLES.some((s) => s.id === item?.style)
          ? (item.style as CandleStyle)
          : "standard",
        ...(typeof item?.scriptId === "string" && item.scriptId.trim()
          ? { scriptId: item.scriptId.trim() }
          : {}),
        drawings: [],
        paperOrders: [],
      };
    });
    const padded = ensureTilesForLayout(tiles, layout);
    const activeTileId =
      typeof v.activeTileId === "string" &&
      padded.some((t) => t.id === v.activeTileId)
        ? v.activeTileId
        : padded[0].id;
    return {
      version: 1,
      layout,
      syncCrosshair: v.syncCrosshair !== false,
      activeTileId,
      showWatchlist: v.showWatchlist !== false,
      showOrderflow: v.showOrderflow !== false,
      orderflowTab,
      tiles: padded,
      drawingsBySymbol,
      paperOrdersBySymbol,
    };
  } catch {
    return def;
  }
}

export function computeFibLevels(
  p1Price: number,
  p2Price: number,
): { ratio: number; price: number; label: string }[] {
  if (!Number.isFinite(p1Price) || !Number.isFinite(p2Price)) return [];
  const high = Math.max(p1Price, p2Price);
  const low = Math.min(p1Price, p2Price);
  const span = high - low;
  const digits = Math.max(Math.abs(p1Price), Math.abs(p2Price)) < 0.01 ? 8 : 4;
  return FIB_RATIOS.map((ratio) => ({
    ratio,
    price: Number((high - span * ratio).toFixed(digits)),
    label: `${(ratio * 100).toFixed(1)}%`,
  }));
}

export function formatMarketPrice(price: number): string {
  if (!Number.isFinite(price)) return "—";
  const abs = Math.abs(price);
  if (abs === 0) return "0.00";
  if (abs >= 1000) return price.toFixed(2);
  if (abs >= 1) return price.toFixed(3);
  if (abs >= 0.01) return price.toFixed(5);
  const decimals = Math.min(10, Math.max(6, -Math.floor(Math.log10(abs)) + 4));
  return price.toFixed(decimals).replace(/0+$/, "").replace(/\.$/, "");
}

export function formatCompactVolume(vol: number): string {
  if (!Number.isFinite(vol)) return "0";
  const abs = Math.abs(vol);
  if (abs >= 1e9) return `${(vol / 1e9).toFixed(2)}B`;
  if (abs >= 1e6) return `${(vol / 1e6).toFixed(2)}M`;
  if (abs >= 1e3) return `${(vol / 1e3).toFixed(1)}K`;
  return vol.toFixed(2);
}

export type MarketRequestClient = {
  mode: string;
  request: <T>(
    route: string,
    method?: string,
    body?: unknown,
    signal?: AbortSignal,
  ) => Promise<T>;
  streamGet: (
    route: string,
    options: {
      signal?: AbortSignal;
      onEvent: (frame: SSEFrame) => void;
    },
  ) => Promise<void>;
};

function syntheticSeedPrice(symbol: string): number {
  const up = symbol.toUpperCase();
  if (up.startsWith("BTC") || up.startsWith("XBT")) return 94500;
  if (up.startsWith("ETH")) return 3350;
  if (up.startsWith("SOL")) return 218;
  return 105;
}

export function generatePreviewCandles(
  venue: string,
  symbol: string,
  interval: Interval,
  style: CandleStyle,
  count = 60,
): CandleSeries {
  const step =
    interval === "1m"
      ? 60
      : interval === "5m"
        ? 300
        : interval === "15m"
          ? 900
          : interval === "4h"
            ? 14400
            : interval === "1d"
              ? 86400
              : 3600;
  const nowSec = Math.floor(Date.now() / 1000 / step) * step;
  const base = syntheticSeedPrice(symbol);
  const candles: Candle[] = [];
  let px = base;
  for (let i = 0; i < count; i++) {
    const ts = nowSec - (count - i) * step;
    const delta = Math.sin(i * 0.25) * base * 0.005;
    const o = px;
    const c = Math.max(o + delta, base * 0.4);
    const h = Math.max(o, c) + base * 0.002;
    const l = Math.max(Math.min(o, c) - base * 0.002, 0.01);
    const vol = 140 + ((i * 19) % 240);
    candles.push({
      time: ts,
      open: Number(o.toFixed(2)),
      high: Number(h.toFixed(2)),
      low: Number(l.toFixed(2)),
      close: Number(c.toFixed(2)),
      volume: vol,
      buyVolume: Number((vol * 0.56).toFixed(2)),
      sellVolume: Number((vol * 0.44).toFixed(2)),
      trades: 40 + (i % 30),
    });
    px = c;
  }
  return {
    venue,
    symbol,
    interval,
    style,
    candles,
    source: "preview",
    fixture: true,
    updatedAt: nowSec,
  };
}

export async function fetchCandles(
  client: MarketRequestClient,
  venue: string,
  symbol: string,
  interval: Interval,
  style: CandleStyle,
  limit = 160,
  signal?: AbortSignal,
): Promise<CandleSeries> {
  if (client.mode !== "ready") {
    return generatePreviewCandles(
      venue,
      symbol,
      interval,
      style,
      Math.min(limit, 80),
    );
  }
  const q = new URLSearchParams({
    venue,
    symbol,
    interval,
    style,
    limit: String(limit),
  });
  return client.request<CandleSeries>(
    `market/candles?${q.toString()}`,
    "GET",
    undefined,
    signal,
  );
}

export async function fetchOrderBook(
  client: MarketRequestClient,
  venue: string,
  symbol: string,
  limit = 14,
  signal?: AbortSignal,
): Promise<OrderBookSnapshot> {
  if (client.mode !== "ready") {
    const mid = syntheticSeedPrice(symbol);
    const bids: OrderBookLevel[] = [];
    const asks: OrderBookLevel[] = [];
    let bt = 0;
    let at = 0;
    for (let i = 0; i < limit; i++) {
      const sz = 1.2 + (i % 5) * 0.4;
      bt += sz;
      at += sz * 1.1;
      bids.push({
        price: Number((mid - (i + 1) * mid * 0.0004).toFixed(2)),
        size: Number(sz.toFixed(2)),
        total: Number(bt.toFixed(2)),
      });
      asks.push({
        price: Number((mid + (i + 1) * mid * 0.0004).toFixed(2)),
        size: Number((sz * 1.1).toFixed(2)),
        total: Number(at.toFixed(2)),
      });
    }
    return {
      venue,
      symbol,
      time: Date.now(),
      bids,
      asks,
      spread: Number((asks[0].price - bids[0].price).toFixed(2)),
      midPrice: mid,
      source: "preview",
      fixture: true,
    };
  }
  const q = new URLSearchParams({ venue, symbol, limit: String(limit) });
  return client.request<OrderBookSnapshot>(
    `market/orderbook?${q.toString()}`,
    "GET",
    undefined,
    signal,
  );
}

export async function fetchTrades(
  client: MarketRequestClient,
  venue: string,
  symbol: string,
  limit = 40,
  signal?: AbortSignal,
): Promise<TradeTick[]> {
  if (client.mode !== "ready") {
    const mid = syntheticSeedPrice(symbol);
    const now = Date.now();
    return Array.from({ length: 20 }, (_, i) => ({
      id: `prev-${i}`,
      time: now - (20 - i) * 1500,
      price: Number((mid + Math.sin(i) * mid * 0.001).toFixed(2)),
      size: Number((0.25 + (i % 7) * 0.15).toFixed(2)),
      side: i % 3 === 0 ? "sell" : "buy",
    }));
  }
  const q = new URLSearchParams({ venue, symbol, limit: String(limit) });
  const res = await client.request<{ trades: TradeTick[] }>(
    `market/trades?${q.toString()}`,
    "GET",
    undefined,
    signal,
  );
  return res.trades ?? [];
}

export async function fetchFootprint(
  client: MarketRequestClient,
  venue: string,
  symbol: string,
  interval: Interval,
  limit = 24,
  signal?: AbortSignal,
): Promise<FootprintSeries> {
  if (client.mode !== "ready") {
    const cs = generatePreviewCandles(
      venue,
      symbol,
      interval,
      "standard",
      limit,
    );
    let cvd = 0;
    const bars: FootprintBar[] = cs.candles.map((c) => {
      const delta =
        (c.buyVolume ?? c.volume * 0.52) - (c.sellVolume ?? c.volume * 0.48);
      cvd += delta;
      return {
        time: c.time,
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
        volume: c.volume,
        delta: Number(delta.toFixed(2)),
        cvd: Number(cvd.toFixed(2)),
        vpoc: c.close,
        vah: c.high,
        val: c.low,
        levels: [
          {
            price: c.low,
            bidVol: Number((c.volume * 0.25).toFixed(2)),
            askVol: Number((c.volume * 0.2).toFixed(2)),
            delta: Number((-c.volume * 0.05).toFixed(2)),
            totalVol: Number((c.volume * 0.45).toFixed(2)),
          },
          {
            price: c.close,
            bidVol: Number((c.volume * 0.2).toFixed(2)),
            askVol: Number((c.volume * 0.35).toFixed(2)),
            delta: Number((c.volume * 0.15).toFixed(2)),
            totalVol: Number((c.volume * 0.55).toFixed(2)),
          },
        ],
      };
    });
    return {
      venue,
      symbol,
      interval,
      stepSize: 10,
      bars,
      source: "preview",
      fixture: true,
      updatedAt: cs.updatedAt,
    };
  }
  const q = new URLSearchParams({
    venue,
    symbol,
    interval,
    limit: String(limit),
  });
  return client.request<FootprintSeries>(
    `market/footprint?${q.toString()}`,
    "GET",
    undefined,
    signal,
  );
}

export async function fetchWatchlists(
  client: MarketRequestClient,
  signal?: AbortSignal,
): Promise<Watchlist[]> {
  if (client.mode !== "ready") {
    return [
      {
        id: "core-desk",
        name: "Core Desk",
        items: [
          {
            venue: "hyperliquid",
            symbol: "BTC",
            interval: "1h",
            style: "standard",
          },
          {
            venue: "hyperliquid",
            symbol: "ETH",
            interval: "1h",
            style: "standard",
          },
          {
            venue: "hyperliquid",
            symbol: "SOL",
            interval: "15m",
            style: "heikin_ashi",
          },
        ],
      },
    ];
  }
  const res = await client.request<{ watchlists: Watchlist[] }>(
    "market/watchlists",
    "GET",
    undefined,
    signal,
  );
  return res.watchlists ?? [];
}

export async function saveWatchlist(
  client: MarketRequestClient,
  watchlist: Watchlist,
): Promise<Watchlist> {
  if (client.mode !== "ready") return watchlist;
  return client.request<Watchlist>("market/watchlists", "PUT", watchlist);
}

export async function deleteWatchlist(
  client: MarketRequestClient,
  id: string,
): Promise<void> {
  if (client.mode !== "ready") return;
  await client.request(`market/watchlists/${encodeURIComponent(id)}`, "DELETE");
}

export async function fetchDrawings(
  client: MarketRequestClient,
  opts?: { venue?: string; symbol?: string; interval?: string },
  signal?: AbortSignal,
): Promise<ChartDrawing[]> {
  if (client.mode !== "ready") {
    return [];
  }
  const params = new URLSearchParams();
  if (opts?.venue) params.set("venue", opts.venue);
  if (opts?.symbol) params.set("symbol", opts.symbol);
  if (opts?.interval) params.set("interval", opts.interval);
  const qs = params.toString();
  const res = await client.request<{ drawings?: ChartDrawing[] }>(
    qs ? `market/drawings?${qs}` : "market/drawings",
    "GET",
    undefined,
    signal,
  );
  return res.drawings ?? [];
}

export async function saveDrawing(
  client: MarketRequestClient,
  drawing: ChartDrawing,
): Promise<ChartDrawing> {
  if (client.mode !== "ready") return drawing;
  return client.request<ChartDrawing>("market/drawings", "PUT", drawing);
}

export async function deleteDrawing(
  client: MarketRequestClient,
  id: string,
): Promise<void> {
  if (client.mode !== "ready") return;
  await client.request(`market/drawings/${encodeURIComponent(id)}`, "DELETE");
}

export type CustomFeedSummary = {
  symbol: string;
  interval: string;
  barCount: number;
  lastTime: number;
  updatedAt: number;
};

export async function fetchCustomFeeds(
  client: MarketRequestClient,
  signal?: AbortSignal,
): Promise<CustomFeedSummary[]> {
  if (client.mode !== "ready") return [];
  const res = await client.request<{ feeds?: CustomFeedSummary[] }>(
    "market/custom-feeds",
    "GET",
    undefined,
    signal,
  );
  return res.feeds ?? [];
}

export async function ingestCustomCandles(
  client: MarketRequestClient,
  req: {
    symbol: string;
    interval?: string;
    format?: "csv" | "json";
    csv?: string;
    candles?: Candle[];
  },
): Promise<CustomFeedSummary> {
  if (client.mode !== "ready") {
    throw new Error("Connect Core before importing custom feeds.");
  }
  return client.request<CustomFeedSummary>("market/ingest", "POST", req);
}

export async function fetchCohorts(
  client: MarketRequestClient,
  window = "week",
  signal?: AbortSignal,
): Promise<CohortSnapshot> {
  if (client.mode !== "ready") {
    return {
      venue: "hyperliquid",
      window,
      updatedAt: Math.floor(Date.now() / 1000),
      source: "preview",
      fixture: true,
      entries: [
        {
          address: "0x1111...1111",
          displayName: "desk-alpha",
          accountValue: 2450000,
          dayRoi: 0.042,
          weekRoi: 0.148,
          monthRoi: 0.312,
          allTimeRoi: 1.84,
          rank: 1,
        },
      ],
    };
  }
  return client.request<CohortSnapshot>(
    `market/cohorts?window=${encodeURIComponent(window)}`,
    "GET",
    undefined,
    signal,
  );
}

export async function fetchMacroSeries(
  client: MarketRequestClient,
  seriesId = "CPIAUCSL",
  limit = 16,
  signal?: AbortSignal,
): Promise<MacroPoint[]> {
  if (client.mode !== "ready") {
    return [
      {
        seriesId,
        date: "2026-10-01",
        value: 314.2,
        source: "preview",
        updatedAt: 0,
      },
      {
        seriesId,
        date: "2026-11-01",
        value: 315.1,
        source: "preview",
        updatedAt: 0,
      },
    ];
  }
  const res = await client.request<{ points: MacroPoint[] }>(
    `market/macro?seriesId=${encodeURIComponent(seriesId)}&limit=${limit}`,
    "GET",
    undefined,
    signal,
  );
  return res.points ?? [];
}

export async function fetchSECFilings(
  client: MarketRequestClient,
  cik = "0000320193",
  limit = 8,
  signal?: AbortSignal,
): Promise<SECFiling[]> {
  if (client.mode !== "ready") {
    return [
      {
        cik: "0000320193",
        company: "Preview Issuer",
        form: "10-K",
        filingDate: "2026-10-30",
        accessionNumber: "0000320193-26-000100",
        primaryDocument: "filing.htm",
        url: "https://www.sec.gov/",
      },
    ];
  }
  const res = await client.request<{ filings: SECFiling[] }>(
    `market/sec?cik=${encodeURIComponent(cik)}&limit=${limit}`,
    "GET",
    undefined,
    signal,
  );
  return res.filings ?? [];
}
