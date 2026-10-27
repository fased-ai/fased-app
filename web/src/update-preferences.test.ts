import { expect, it } from "vitest";
import { readUpdatePreferences } from "./update-preferences";
const sample = () => ({
  version: 1,
  revision: 0,
  policy: { notify: true },
  proposals: [],
  notifications: [],
  evaluatedAt: "2026-10-20T00:00:00Z",
  window: "unrestricted",
  installAllowed: false,
  resumeAllowed: false,
  automaticUpdates: false,
  financialExecution: false,
});
it("accepts a review-only metadata view without promoting it to authority", () => {
  expect(readUpdatePreferences(sample()).installAllowed).toBe(false);
});
it("rejects permission changes, unsupported versions and orphan notices", () => {
  for (const value of [
    null,
    {},
    { ...sample(), version: 2 },
    { ...sample(), automaticUpdates: true },
    { ...sample(), financialExecution: true },
    { ...sample(), installAllowed: true },
    { ...sample(), resumeAllowed: true },
    { ...sample(), revision: -1 },
    { ...sample(), notifications: ["a".repeat(64)] },
    {
      ...sample(),
      policy: {
        notify: true,
        pin: {
          sequence: "18446744073709551616",
          manifestSha256: "a".repeat(64),
        },
      },
    },
  ])
    expect(() => readUpdatePreferences(value)).toThrow();
});
