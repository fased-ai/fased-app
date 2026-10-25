import { describe, expect, it } from "vitest";
import { agentReplyDefaults } from "./agent-defaults";

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
