import { describe, expect, it } from "vitest";
import {
  computeFibLevels,
  defaultTradeWorkspaceConfig,
  DOM_SUPPORTED_VENUES,
  ensureTilesForLayout,
  formatCompactVolume,
  formatMarketPrice,
  layoutTileCount,
  parseTradeWorkspaceConfig,
  symbolOverlayKey,
  TAPE_SUPPORTED_VENUES,
} from "./market-data";

describe("market-data workspace config and helpers", () => {
  it("computes exact tile counts across 1x1 through 4x4 layouts", () => {
    expect(layoutTileCount("1x1")).toBe(1);
    expect(layoutTileCount("1x2")).toBe(2);
    expect(layoutTileCount("2x2")).toBe(4);
    expect(layoutTileCount("3x3")).toBe(9);
    expect(layoutTileCount("4x4")).toBe(16);
  });

  it("pads tiles up to 16 when switching to 4x4 without discarding existing tiles", () => {
    const def = defaultTradeWorkspaceConfig();
    def.tiles[0].symbol = "SOL";
    const expanded = ensureTilesForLayout(def.tiles, "4x4");
    expect(expanded.length).toBe(16);
    expect(expanded[0].symbol).toBe("SOL");
    expect(new Set(expanded.map((t) => t.id)).size).toBe(16);
  });

  it("parses malformed workspace JSON safely and migrates legacy per-tile overlays into symbol-scoped storage", () => {
    const bad = parseTradeWorkspaceConfig("{not json");
    expect(bad.layout).toBe("2x2");
    expect(bad.tiles.length).toBeGreaterThanOrEqual(4);

    const weird = parseTradeWorkspaceConfig(
      JSON.stringify({
        version: 1,
        layout: "invalid-grid",
        orderflowTab: "bogus",
        tiles: [],
      }),
    );
    expect(weird.layout).toBe("2x2");
    expect(weird.orderflowTab).toBe("footprint");

    const migrated = parseTradeWorkspaceConfig(
      JSON.stringify({
        version: 1,
        layout: "1x1",
        tiles: [
          {
            id: "tile-1",
            venue: "hyperliquid",
            symbol: "btc",
            interval: "1h",
            style: "standard",
            drawings: [
              { id: "d1", kind: "hline", p1: { time: 1, price: 94000 } },
            ],
            paperOrders: [],
          },
        ],
      }),
    );
    const key = symbolOverlayKey("hyperliquid", "BTC");
    expect(migrated.drawingsBySymbol?.[key]).toHaveLength(1);
    expect(migrated.tiles[0].drawings).toEqual([]);
  });

  it("computes Fibonacci retracement levels, venue capabilities, and sub-penny formatters", () => {
    const levels = computeFibLevels(100, 200);
    expect(levels.map((l) => l.price)).toEqual([
      200, 176.4, 161.8, 150, 138.2, 121.4, 100,
    ]);
    const tinyFib = computeFibLevels(0.0001, 0.0002);
    expect(tinyFib[3].price).toBe(0.00015);
    expect(computeFibLevels(Number.NaN, 10)).toEqual([]);
    expect(formatMarketPrice(94500.5)).toBe("94500.50");
    expect(formatMarketPrice(12.3456)).toBe("12.346");
    expect(formatMarketPrice(0.00001234)).toBe("0.00001234");
    expect(formatCompactVolume(2_500_000)).toBe("2.50M");
    expect(formatCompactVolume(Number.NaN)).toBe("0");
    expect(DOM_SUPPORTED_VENUES.has("solana")).toBe(false);
    expect(DOM_SUPPORTED_VENUES.has("kraken")).toBe(false);
    expect(TAPE_SUPPORTED_VENUES.has("kraken")).toBe(true);
  });
});
