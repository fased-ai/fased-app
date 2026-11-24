import { describe, expect, it } from "vitest";
import {
  buildEquityPolyline,
  deskStatusLabel,
  formatCurrencyUSD,
  formatRMultiple,
  formatSignedPct,
  normalizeHistogramHeights,
  slugifyScriptId,
} from "./quantlab-desk";

describe("quantlab-desk helpers", () => {
  it("normalizes script IDs and formats values safely", () => {
    expect(slugifyScriptId("BTC EMA Cross v2!")).toBe("btc-ema-cross-v2");
    expect(slugifyScriptId("   ")).toBe("custom-script");

    expect(formatCurrencyUSD(100000)).toBe("$100,000.00");
    expect(formatCurrencyUSD(-425.5)).toBe("-$425.50");
    expect(formatCurrencyUSD(Number.NaN)).toBe("$0.00");

    expect(formatSignedPct(12.345)).toBe("+12.35%");
    expect(formatSignedPct(-3.1)).toBe("-3.10%");
    expect(formatSignedPct(Number.POSITIVE_INFINITY)).toBe("0.00%");

    expect(formatRMultiple(1.25)).toBe("+1.25R");
    expect(formatRMultiple(-1)).toBe("-1.00R");
  });

  it("prioritizes kill switch and external kill switch over pause", () => {
    expect(deskStatusLabel(null)).toBe("Active");
    expect(
      deskStatusLabel({
        paused: true,
        killSwitchActive: false,
        externalKillActive: false,
      }),
    ).toBe("Paused");
    expect(
      deskStatusLabel({
        paused: true,
        killSwitchActive: true,
        externalKillActive: false,
      }),
    ).toBe("Kill switch");
    expect(
      deskStatusLabel({
        paused: false,
        killSwitchActive: false,
        externalKillActive: true,
      }),
    ).toBe("Kill switch");
  });

  it("computes histogram heights and SVG equity polyline coordinates safely", () => {
    expect(normalizeHistogramHeights([])).toEqual([]);
    expect(
      normalizeHistogramHeights([
        { from: 0, to: 1, count: 0 },
        { from: 1, to: 2, count: 0 },
      ]),
    ).toEqual([0, 0]);
    expect(
      normalizeHistogramHeights([
        { from: 0, to: 1, count: 5 },
        { from: 1, to: 2, count: 20 },
        { from: 2, to: 3, count: 10 },
      ]),
    ).toEqual([25, 100, 50]);

    expect(buildEquityPolyline([])).toBe("");
    expect(
      buildEquityPolyline(
        [{ time: 1, equity: 10000, drawdownPct: 0 }],
        200,
        80,
      ),
    ).toBe("0,40.0 200,40.0");
    expect(
      buildEquityPolyline(
        [
          { time: 1, equity: 10000, drawdownPct: 0 },
          { time: 2, equity: 11000, drawdownPct: 0 },
        ],
        200,
        80,
      ),
    ).toBe("0.0,74.0 200.0,6.0");
  });
});
