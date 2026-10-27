import { describe, expect, it, vi } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  OpenAIPilotSettings,
  pilotCommand,
  type PilotAllowance,
} from "./OpenAIPilot";
import type { Connection } from "./AIConnection";
import { translate } from "../ui-language";

vi.mock("../workspace", () => ({
  useWorkspace: () => ({
    core: { instanceId: "fixture-instance" },
    agents: { view: { profileId: "fixture-profile", revision: 1 } },
  }),
}));
vi.mock("../ui-preferences", () => ({
  useUI: () => ({ t: (key: string) => key }),
}));

const allowance: PilotAllowance = {
  id: "review-digest",
  instanceId: "fixture-instance",
  bootId: "fixture-boot",
  connectionRef: "fixture-ref",
  connectionEpoch: 1,
  accessEpoch: 0,
  profileId: "fixture-profile",
  model: "current-model",
  policyId: "reviewed-policy",
  revision: 1,
  createdAt: 1000,
  expiresAt: 1900,
  maxRequests: 4,
  budgetMicrousd: 500000,
  laneBudgetMicrousd: 250000,
  limits: {
    maxInputTokens: 2000,
    maxOutputTokens: 256,
    deadlineSeconds: 5,
    budgetMicrousd: 100000,
  },
  usedRequests: 0,
  allocatedMicrousd: 0,
};
describe("Scoped pilot controls", () => {
  it("keeps ordinary builds free of pilot controls", () => {
    expect(
      renderToStaticMarkup(
        createElement(OpenAIPilotSettings, {
          connection: { ref: "fixture-ref" } as Connection,
        }),
      ),
    ).toBe("");
    const html = renderToStaticMarkup(
      createElement(OpenAIPilotSettings, {
        connection: {
          ref: "fixture-ref",
          pilotAvailable: true,
          pilot: allowance,
          pilotState: "active",
        } as Connection,
      }),
    );
    expect(html).toContain("Pilot");
    expect(html).toContain("$0.50");
    expect(html).toContain("Revoke");
    expect(html).not.toContain("API key");
  });
  it("sends exact reviewed scope and clears secrets from review/revoke", () => {
    expect(
      pilotCommand(
        "pilot-review",
        "fixture-ref",
        "fixture-profile",
        1,
        allowance,
        "secret",
      ),
    ).toEqual({
      action: "pilot-review",
      ref: "fixture-ref",
      profileId: "fixture-profile",
      revision: 1,
    });
    expect(
      pilotCommand(
        "pilot-revoke",
        "fixture-ref",
        "fixture-profile",
        1,
        allowance,
        "secret",
      ),
    ).toEqual({ action: "pilot-revoke", ref: "fixture-ref" });
    expect(
      pilotCommand(
        "pilot-allow",
        "fixture-ref",
        "fixture-profile",
        1,
        allowance,
        "disposable-passphrase",
      ),
    ).toEqual({
      action: "pilot-allow",
      ref: "fixture-ref",
      pilot: allowance,
      consentDigest: allowance.id,
      passphrase: "disposable-passphrase",
    });
    expect(() =>
      pilotCommand(
        "pilot-allow",
        "other-ref",
        "fixture-profile",
        1,
        allowance,
        "secret",
      ),
    ).toThrow();
    expect(() =>
      pilotCommand(
        "pilot-allow",
        "fixture-ref",
        "other-profile",
        1,
        allowance,
        "secret",
      ),
    ).toThrow();
  });
  it("translates pilot controls without translating account or model records", () => {
    for (const language of ["ru", "es", "zh", "ko"] as const) {
      expect(translate(language, "Pilot")).not.toBe("Pilot");
      expect(translate(language, "Allow pilot")).not.toBe("Allow pilot");
      expect(
        translate(language, "{count} attempts · {minutes} minutes", {
          count: 4,
          minutes: 15,
        }),
      ).not.toContain("{count}");
    }
  });
});
