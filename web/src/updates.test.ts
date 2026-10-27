import { describe, expect, it } from "vitest";
import { readUpdateStatus } from "./updates";

const sample = () => ({
  version: 1,
  observedAt: "2026-10-20T00:00:00Z",
  readOnly: true,
  state: "not-configured",
  core: { phase: "idle" },
  signer: {
    state: "not-configured",
    locked: null,
    updateHeld: null,
    recoveryRequired: null,
  },
  guidance: [{ code: "scope", message: "Read only" }],
  walletSigner: false,
  financialExecution: false,
  automaticUpdates: false,
});
describe("read-only update status contract", () => {
  it("keeps unknown observations unknown", () => {
    expect(readUpdateStatus(sample()).signer.locked).toBeNull();
  });
  it("refuses unsupported versions, enabled capabilities and incomplete observations", () => {
    for (const value of [
      null,
      {},
      { ...sample(), version: 2 },
      { ...sample(), automaticUpdates: true },
      { ...sample(), financialExecution: true },
      { ...sample(), walletSigner: true },
      { ...sample(), readOnly: false },
      { ...sample(), observedAt: "invalid" },
      { ...sample(), signer: { ...sample().signer, locked: false } },
      { ...sample(), signer: { state: "available" } },
      { ...sample(), state: "__proto__" },
      { ...sample(), guidance: [] },
    ])
      expect(() => readUpdateStatus(value)).toThrow();
  });
});
