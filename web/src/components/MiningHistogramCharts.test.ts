import { describe, expect, it } from "vitest";
import {
  computePresetAllocations,
  MINING_PRESETS,
} from "./MiningHistogramCharts";

describe("WEN Mining preset histogram allocations", () => {
  it("conserves total lamports across all presets and odd inputs", () => {
    for (const preset of MINING_PRESETS) {
      const sumBps = preset.weightsBps.reduce((acc, w) => acc + w, 0);
      expect(sumBps).toBe(10000);

      for (const amount of ["200000000", "100000001", "999999999"]) {
        const allocs = computePresetAllocations(amount, preset.weightsBps);
        expect(allocs).toHaveLength(5);
        const total = allocs.reduce((acc, s) => acc + BigInt(s), 0n);
        expect(total.toString()).toBe(amount);
      }
    }
  });

  it("returns zeroes for empty or invalid lamport inputs", () => {
    expect(
      computePresetAllocations("", [2000, 2000, 2000, 2000, 2000]),
    ).toEqual(["0", "0", "0", "0", "0"]);
    expect(
      computePresetAllocations("abc", [2000, 2000, 2000, 2000, 2000]),
    ).toEqual(["0", "0", "0", "0", "0"]);
  });
});
