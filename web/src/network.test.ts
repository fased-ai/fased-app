import { describe, expect, it, vi } from "vitest";
import type { AgentV2CoreClient } from "./agents-v2";
import {
  broadcastSwarmSignal,
  getCEXStatus,
  getEquitiesStatus,
  getOptionsChain,
  getSwarmStatus,
  installHubPackage,
  listHubPackages,
  previewCEXStatus,
  previewEquitiesStatus,
  previewHubList,
  previewSwarmStatus,
  submitCEXOrder,
  submitEquitiesOrder,
  updateSwarmConfig,
} from "./network";

describe("Stage 5 & Stage 6 network helpers", () => {
  it("preserves compile-time mainnet/live/WEN locks in preview fixtures", () => {
    const hub = previewHubList();
    expect(hub.wenHeld).toBe(true);
    expect(hub.satStatus).toBe("deferred_wen_hold");

    const swarm = previewSwarmStatus();
    expect(swarm.config.enabled).toBe(false);
    expect(swarm.liveCopyLocked).toBe(true);

    const cex = previewCEXStatus();
    expect(cex.mainnetLocked).toBe(true);
    expect(cex.supportedVenues).toHaveLength(6);

    const eq = previewEquitiesStatus();
    expect(eq.liveLocked).toBe(true);
  });

  it("dispatches expected Stage 5 and Stage 6 Core routes", async () => {
    const req = vi.fn(async () => ({}));
    const client: AgentV2CoreClient = {
      mode: "ready",
      instanceId: "inst-1",
      request: req as AgentV2CoreClient["request"],
      stream: vi.fn() as AgentV2CoreClient["stream"],
    };

    await listHubPackages(client);
    await installHubPackage(client, "hub-momentum-rsi");
    await getSwarmStatus(client);
    await updateSwarmConfig(client, { enabled: true, copyMode: "paper" });
    await broadcastSwarmSignal(client, {
      symbol: "SOL",
      direction: "LONG",
      thesis: "Momentum expansion",
      confidence: 0.78,
    });
    await getCEXStatus(client);
    await submitCEXOrder(client, {
      venue: "binance",
      symbol: "BTCUSDT",
      side: "BUY",
      orderType: "LIMIT",
      quantity: 0.1,
      price: 60000,
    });
    await getEquitiesStatus(client);
    await getOptionsChain(client, "nvda");
    await submitEquitiesOrder(client, {
      venue: "alpaca",
      symbol: "SPY",
      side: "BUY",
      orderType: "MARKET",
      quantity: 1,
    });

    expect(req.mock.calls).toEqual([
      ["hub/packages"],
      ["hub/install", "POST", { id: "hub-momentum-rsi" }],
      ["swarm/status"],
      ["swarm/config", "PUT", { enabled: true, copyMode: "paper" }],
      [
        "swarm/signals",
        "POST",
        {
          symbol: "SOL",
          direction: "LONG",
          thesis: "Momentum expansion",
          confidence: 0.78,
        },
      ],
      ["cex/status"],
      [
        "cex/orders",
        "POST",
        {
          venue: "binance",
          symbol: "BTCUSDT",
          side: "BUY",
          orderType: "LIMIT",
          quantity: 0.1,
          price: 60000,
        },
      ],
      ["equities/status"],
      ["equities/options?symbol=NVDA"],
      [
        "equities/orders",
        "POST",
        {
          venue: "alpaca",
          symbol: "SPY",
          side: "BUY",
          orderType: "MARKET",
          quantity: 1,
        },
      ],
    ]);
  });
});
