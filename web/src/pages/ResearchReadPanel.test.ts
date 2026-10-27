import { describe, expect, it } from "vitest";
import { storedBundleJSON } from "./ResearchReadPanel";
describe("stored bundle identity", () => {
  it("matches Go escaping without changing native string quantities", () => {
    expect(
      storedBundleJSON({
        source: "<&>\u2028\u2029",
        cursor: 3,
        amount: "9007199254740993",
      }),
    ).toBe(
      '{"source":"\\u003c\\u0026\\u003e\\u2028\\u2029","cursor":3,"amount":"9007199254740993"}',
    );
  });
  it("refuses a browser-rounded integer rather than hashing altered identity", () => {
    expect(() =>
      storedBundleJSON({ rows: [{ cursor: 9007199254740992 }] }),
    ).toThrow("browser integer precision");
  });
});
