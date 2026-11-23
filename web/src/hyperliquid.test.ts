import { describe, expect, it } from "vitest";
import {
  formatFundingRate,
  formatMicroUSD,
  hyperliquidOrderState,
  shortenHex,
} from "./hyperliquid";

describe("Hyperliquid formatters", () => {
  it("formats micro-USD amounts accurately", () => {
    expect(formatMicroUSD(1_600_250_000)).toBe("$1,600.25");
    expect(formatMicroUSD(0)).toBe("$0.00");
    expect(formatMicroUSD("5000000000")).toBe("$5,000.00");
  });

  it("formats hourly funding percentages and state labels", () => {
    expect(formatFundingRate("0.0000125")).toBe("+0.0013%");
    expect(formatFundingRate("-0.0000090")).toBe("-0.0009%");
    expect(hyperliquidOrderState("resting")).toBe("Resting");
    expect(hyperliquidOrderState("canceled")).toBe("Cancelled");
    expect(hyperliquidOrderState("mystery")).toBe("Status unknown");
  });

  it("shortens EIP-55 addresses and digests cleanly", () => {
    expect(shortenHex("0x2c7536E3605D9C16a7a3D7b1898e529396a65c23")).toBe(
      "0x2c7536…5c23",
    );
    expect(shortenHex("0x1234")).toBe("0x1234");
  });
});
