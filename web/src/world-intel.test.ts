import { describe, expect, it, vi } from "vitest";
import {
  baseTickerFromSymbol,
  buildChartEventPins,
  buildIntelChatPrompt,
  deleteIntelSource,
  fetchIntelEvents,
  fetchIntelMap,
  fetchIntelSources,
  fetchPredictions,
  formatCompactUSD,
  formatProbPct,
  matchEventsForSymbol,
  projectLatLon,
  saveIntelSource,
  syncIntelSources,
  type IntelEvent,
} from "./world-intel";

const sampleEvents: IntelEvent[] = [
  {
    id: "ev-btc",
    sourceId: "gdelt-global",
    sourceName: "GDELT",
    category: "crypto",
    title: "Bitcoin spot inflows rise in New York",
    url: "https://example.org/btc",
    region: "na",
    lat: 40.7,
    lon: -74.0,
    tickers: ["BTC", "SOL"],
    publishedAt: 1760003600,
    ingestedAt: 1760003600,
    urgency: 0.72,
    sentiment: "bullish",
    confidence: 0.8,
    scoreProvider: "deterministic-fallback",
    verified: true,
    untrusted: true,
  },
  {
    id: "ev-fed",
    sourceId: "fed-press",
    sourceName: "Fed",
    category: "macro",
    title: "Fed liquidity adjustment in Washington",
    url: "https://federalreserve.gov/1",
    region: "na",
    lat: 38.9,
    lon: -77.0,
    tickers: ["USD"],
    publishedAt: 1760000000,
    ingestedAt: 1760000000,
    urgency: 0.85,
    sentiment: "neutral",
    confidence: 0.75,
    scoreProvider: "deterministic-fallback",
    verified: true,
    untrusted: true,
  },
  {
    id: "ev-quake",
    sourceId: "usgs",
    sourceName: "USGS",
    category: "seismic",
    title: "Tokyo offshore tremor",
    url: "https://earthquake.usgs.gov/1",
    region: "apac",
    lat: 35.6,
    lon: 139.7,
    publishedAt: 1760001800,
    ingestedAt: 1760001800,
    urgency: 0.4,
    sentiment: "neutral",
    confidence: 0.6,
    scoreProvider: "deterministic-fallback",
    verified: true,
    untrusted: true,
  },
];

describe("world-intel helpers and API wrappers", () => {
  it("projects coordinates deterministically and extracts symbol bases", () => {
    const center = projectLatLon(11, 0, 960, 460);
    expect(center.x).toBe(480);
    expect(center.y).toBe(230);

    expect(baseTickerFromSymbol("BTCUSDT")).toBe("BTC");
    expect(baseTickerFromSymbol("SOL-PERP")).toBe("SOL");
    expect(baseTickerFromSymbol("ETH/USD")).toBe("ETH");
  });

  it("matches symbol or high-urgency macro catalysts and builds ascending bar pins", () => {
    const matched = matchEventsForSymbol(sampleEvents, "BTCUSDT");
    expect(matched.map((e) => e.id)).toEqual(["ev-btc", "ev-fed"]);

    const candles = [
      {
        time: 1760000000,
        open: 100,
        high: 101,
        low: 99,
        close: 100.5,
        volume: 10,
      },
      {
        time: 1760003600,
        open: 100.5,
        high: 103,
        low: 100,
        close: 102,
        volume: 20,
      },
    ];
    const pins = buildChartEventPins(candles, matched);
    expect(pins.map((p) => ({ time: p.time, label: p.label }))).toEqual([
      { time: 1760000000, label: "USD" },
      { time: 1760003600, label: "BTC" },
    ]);
    expect(buildIntelChatPrompt(sampleEvents[0])).toContain("untrusted");
    expect(formatProbPct(0.624)).toBe("62%");
    expect(formatCompactUSD(1840000)).toBe("$1.84M");
  });

  it("calls expected Core intel HTTP routes", async () => {
    const request = vi.fn().mockResolvedValue({});
    const core = { request };

    await fetchIntelEvents(core, {
      category: "macro",
      region: "na",
      limit: 20,
    });
    await fetchIntelMap(core, { hours: 48 });
    await fetchPredictions(core, { venue: "polymarket", limit: 15 });
    await fetchIntelSources(core);
    await saveIntelSource(core, {
      id: "custom-1",
      name: "Custom",
      kind: "rss",
      endpoint: "https://example.org/rss.xml",
      category: "macro",
      region: "global",
    });
    await syncIntelSources(core, "custom-1");
    await deleteIntelSource(core, "custom-1");

    expect(request.mock.calls).toEqual([
      ["intel/events?category=macro&region=na&limit=20"],
      ["intel/map?hours=48"],
      ["intel/predictions?venue=polymarket&limit=15"],
      ["intel/sources"],
      [
        "intel/sources",
        "PUT",
        {
          id: "custom-1",
          name: "Custom",
          kind: "rss",
          endpoint: "https://example.org/rss.xml",
          category: "macro",
          region: "global",
        },
      ],
      ["intel/sync", "POST", { sourceId: "custom-1" }],
      ["intel/sources/custom-1", "DELETE"],
    ]);
  });
});
