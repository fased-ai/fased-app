import { describe, expect, it } from "vitest";
import { agentReplyDefaults, modelForProviderChange } from "./agent-defaults";

describe("Agent reply defaults", () => {
  it("inherits the agent provider/model and refuses missing or locked access", () => {
    const p = { provider: "openai", model: "gpt-4.1-mini-2025-04-14" };
    expect(
      agentReplyDefaults(p, { ref: "test-ref", locked: false, model: p.model }),
    ).toEqual({ connectionRef: "test-ref", model: p.model, ready: true });
    expect(agentReplyDefaults(p, null).ready).toBe(false);
    expect(
      agentReplyDefaults(p, { ref: "test-ref", locked: true, model: p.model })
        .ready,
    ).toBe(false);
    expect(
      agentReplyDefaults(p, {
        ref: "test-ref",
        locked: false,
        model: "different",
      }).ready,
    ).toBe(false);
  });
  it("keeps Demo explicit and does not substitute it for an unavailable provider", () => {
    expect(agentReplyDefaults({ provider: "none", model: "" }, null)).toEqual({
      connectionRef: "",
      model: "",
      ready: true,
    });
    expect(
      agentReplyDefaults({ provider: "xai", model: "grok" }, null).ready,
    ).toBe(false);
  });
});

describe("Provider changes", () => {
  it("selects the connected model, clears Demo, and never carries a foreign model", () => {
    const model = "gpt-6.1-terra";
    expect(modelForProviderChange("openai", model)).toBe(model);
    expect(modelForProviderChange("none", model)).toBe("");
    expect(modelForProviderChange("xai", model)).toBe("");
    expect(modelForProviderChange("openai", undefined)).toBe("");
    expect(
      agentReplyDefaults(
        { provider: "none", model: modelForProviderChange("none", model) },
        null,
      ).ready,
    ).toBe(true);
  });
});

it("binds native provider defaults without carrying another provider or model", () => {
  for (const [provider, model] of [
    ["anthropic", "claude-sonnet-5-5"],
    ["xai", "grok-4.7"],
  ]) {
    const access = {
      ref: provider + "-account",
      provider,
      model,
      locked: false,
    };
    expect(agentReplyDefaults({ provider, model }, access).ready).toBe(true);
    expect(
      agentReplyDefaults({ provider, model }, { ...access, provider: "openai" })
        .ready,
    ).toBe(false);
    expect(
      agentReplyDefaults({ provider, model }, { ...access, locked: true })
        .ready,
    ).toBe(false);
    expect(modelForProviderChange(provider, model)).toBe(model);
    expect(modelForProviderChange(provider, "gpt-6.1-terra")).toBe("");
  }
});
