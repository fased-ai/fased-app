import { describe, it, expect } from "vitest";
import { atomic, units, USDC, WRAPPED_SOL } from "./onchain";
import { translate } from "./ui-language";
describe("swap review amounts", () => {
  it("preserves atomic precision and refuses rounding or numeric coercion", () => {
    expect(atomic("12.5", USDC)).toBe("12500000");
    expect(atomic("0.000000001", WRAPPED_SOL)).toBe("1");
    expect(units("999999999999999", WRAPPED_SOL)).toBe("999999.999999999");
    for (const value of ["1e6", "0", "-1", "0.0000001", "NaN", "0x10"]) {
      expect(atomic(value, USDC)).toBeUndefined();
    }
  });
  it("translates swap controls in every supported interface language", () => {
    for (const lang of ["ru", "es", "zh", "ko"] as const) {
      for (const text of [
        "Swap",
        "Quote",
        "Minimum",
        "Reconcile",
        "Download approval",
        "Network cost",
      ]) {
        expect(translate(lang, text)).not.toBe(text);
      }
    }
  });
});
