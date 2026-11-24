import { describe, expect, it } from "vitest";
import {
  automationStateLabel,
  formatChatIdsInput,
  parseChatIdsInput,
  slugifyTriggerId,
  summarizeTriggerCondition,
} from "./automation";

describe("automation helpers", () => {
  it("parses and deduplicates Telegram chat IDs safely", () => {
    expect(parseChatIdsInput("777001, 0, -10012345 777001 bad")).toEqual([
      777001, -10012345,
    ]);
    expect(formatChatIdsInput([777001, -10012345])).toBe("777001, -10012345");
  });

  it("derives deterministic state labels and trigger condition summaries", () => {
    expect(
      automationStateLabel({
        paused: true,
        killSwitchActive: true,
        updatedAt: 1,
      }),
    ).toBe("Kill switch");
    expect(
      automationStateLabel({
        paused: true,
        killSwitchActive: false,
        updatedAt: 1,
      }),
    ).toBe("Paused");
    expect(
      automationStateLabel({
        paused: false,
        killSwitchActive: false,
        updatedAt: 1,
      }),
    ).toBe("Active");

    expect(
      summarizeTriggerCondition({
        id: "oil-1",
        name: "Oil",
        kind: "news",
        enabled: true,
        cooldownSeconds: 300,
        condition: { category: "energy", region: "mena", minUrgency: 0.7 },
        action: { type: "notify" },
        fireCount: 0,
        createdAt: 1,
        updatedAt: 1,
      }),
    ).toBe("energy · mena · urgency >= 70%");

    expect(slugifyTriggerId("BTC Breakout > 90k!")).toBe("btc-breakout-90k");

    expect(
      summarizeTriggerCondition({
        id: "script-1",
        name: "EMA Script",
        kind: "fasedscript",
        enabled: true,
        cooldownSeconds: 60,
        condition: {
          venue: "hyperliquid",
          symbol: "BTC",
          interval: "15m",
          scriptId: "ema-rsi-trend",
        },
        action: { type: "desk_cycle", deskMode: "shadow" },
        fireCount: 0,
        createdAt: 1,
        updatedAt: 1,
      }),
    ).toBe("hyperliquid BTC 15m · ema-rsi-trend");
  });
});
