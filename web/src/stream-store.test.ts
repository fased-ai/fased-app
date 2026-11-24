import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SSEFrame } from "./agents-v2";
import { CrosshairBus, FasedStreamClient } from "./stream-store";

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  readyState = 0;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  sent: string[] = [];
  closed = false;

  constructor(
    readonly url: string,
    readonly protocols: string[],
  ) {
    MockWebSocket.instances.push(this);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  close(): void {
    this.closed = true;
    this.readyState = 3;
  }

  triggerOpen(): void {
    this.readyState = 1;
    this.onopen?.({});
  }

  triggerMessage(data: unknown): void {
    this.onmessage?.({ data: JSON.stringify(data) });
  }

  triggerClose(): void {
    this.readyState = 3;
    this.onclose?.({});
  }
}

describe("FasedStreamClient and CrosshairBus", () => {
  beforeEach(() => {
    MockWebSocket.instances = [];
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("ref-counts subscriptions, coalesces 16ms candle ticks, and preserves trades in order", async () => {
    const request = vi.fn().mockResolvedValue({ ticket: "tk-123" });
    const streamGet = vi.fn().mockResolvedValue(undefined);

    const client = new FasedStreamClient({
      createWebSocket: (url, protos) => new MockWebSocket(url, protos),
      isWails: () => false,
      resolveWSURL: () => "ws://127.0.0.1:8080/api/v1/stream",
    });

    client.setCore({ mode: "ready", request, streamGet });

    const c1: unknown[] = [];
    const c2: unknown[] = [];
    const trades: unknown[] = [];
    const topicCandle = "candle:hyperliquid:BTC:1h:standard";
    const topicTrade = "trade:hyperliquid:BTC";

    const unsub1 = client.subscribe(topicCandle, (m) => c1.push(m.payload));
    const unsub2 = client.subscribe(topicCandle, (m) => c2.push(m.payload));
    const unsubTrade = client.subscribe(topicTrade, (m) =>
      trades.push(m.payload),
    );

    await vi.runAllTicks();
    await Promise.resolve();
    expect(request).toHaveBeenCalledWith("stream/ticket", "POST", {});
    expect(MockWebSocket.instances).toHaveLength(1);
    const sock = MockWebSocket.instances[0];
    expect(sock.protocols).toEqual(["fased.stream.v1", "fased.ticket.tk-123"]);

    sock.triggerOpen();
    expect(client.getStatus()).toBe("ws");
    expect(JSON.parse(sock.sent[0])).toEqual({
      op: "sub",
      topics: [topicCandle, topicTrade],
    });

    // Push 100 candle frames in one frame + 3 trades; candle must coalesce to 1 delivery (close=199).
    for (let i = 100; i < 200; i++) {
      sock.triggerMessage({
        type: "event",
        topic: topicCandle,
        event: "candle",
        payload: { time: 1760000000, close: i },
      });
    }
    for (const px of [101, 102, 103]) {
      sock.triggerMessage({
        type: "event",
        topic: topicTrade,
        event: "trade",
        payload: { id: `t-${px}`, price: px },
      });
    }

    expect(c1).toEqual([]);
    await vi.advanceTimersByTimeAsync(20);
    expect(c1).toEqual([{ time: 1760000000, close: 199 }]);
    expect(c2).toEqual([{ time: 1760000000, close: 199 }]);
    expect(trades).toEqual([
      { id: "t-101", price: 101 },
      { id: "t-102", price: 102 },
      { id: "t-103", price: 103 },
    ]);

    // Removing 1 of 2 candle listeners must NOT send unsub; removing the second DOES.
    unsub1();
    expect(sock.sent).toHaveLength(1);
    unsub2();
    expect(JSON.parse(sock.sent[1])).toEqual({
      op: "unsub",
      topics: [topicCandle],
    });

    // Removing last topic closes WS cleanly -> idle.
    unsubTrade();
    expect(sock.closed).toBe(true);
    expect(client.getStatus()).toBe("idle");
    client.dispose();
  });

  it("falls back to multiplexed SSE on Wails desktop or repeated WS failures", async () => {
    const request = vi.fn().mockRejectedValue(new Error("ws blocked"));
    let capturedRoute = "";
    const sink: { onEvent?: (f: SSEFrame) => void } = {};
    const streamGet = vi.fn().mockImplementation(
      (
        route: string,
        opts: { signal?: AbortSignal; onEvent: (f: SSEFrame) => void },
      ) =>
        new Promise<void>((resolve) => {
          capturedRoute = route;
          sink.onEvent = opts.onEvent;
          opts.signal?.addEventListener("abort", () => resolve(), {
            once: true,
          });
        }),
    );

    const wailsClient = new FasedStreamClient({
      createWebSocket: (url, protos) => new MockWebSocket(url, protos),
      isWails: () => true,
    });
    wailsClient.setCore({ mode: "ready", request, streamGet });

    const seen: unknown[] = [];
    const unsub = wailsClient.subscribe(
      "candle:hyperliquid:BTC:1h:standard",
      (m) => seen.push(m.payload),
    );
    await Promise.resolve();
    expect(request).not.toHaveBeenCalled();
    expect(wailsClient.getStatus()).toBe("sse");
    expect(capturedRoute).toContain("market/stream?noSnapshot=1");

    sink.onEvent?.({
      event: "candle",
      data: JSON.stringify({
        type: "candle",
        venue: "hyperliquid",
        symbol: "BTC",
        interval: "1h",
        style: "standard",
        payload: { time: 1760000000, close: 94555 },
      }),
    });
    await vi.advanceTimersByTimeAsync(20);
    expect(seen).toEqual([{ time: 1760000000, close: 94555 }]);

    // Locking core must abort active SSE stream and transition to idle.
    wailsClient.setCore({ mode: "locked", request, streamGet });
    expect(wailsClient.getStatus()).toBe("idle");
    unsub();
    wailsClient.dispose();

    // Non-Wails client falls back cleanly after 2 WS handshake failures.
    const fbClient = new FasedStreamClient({
      createWebSocket: (url, protos) => new MockWebSocket(url, protos),
      isWails: () => false,
    });
    fbClient.setCore({ mode: "ready", request, streamGet });
    const unsubFb = fbClient.subscribe(
      "candle:binance:BTCUSDT:1m:standard",
      () => {},
    );
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(600);
    await Promise.resolve();
    expect(fbClient.getStatus()).toBe("sse");
    unsubFb();
    fbClient.dispose();
  });

  it("delivers CrosshairBus updates synchronously to listeners outside React", () => {
    const bus = new CrosshairBus();
    const got: unknown[] = [];
    const unsub = bus.subscribe((pt) => got.push(pt));
    bus.publish({ sourceId: "tile-1", time: 1760000000, price: 94000 });
    unsub();
    bus.publish({ sourceId: "tile-1", time: 1760003600, price: 94100 });
    expect(got).toEqual([
      { sourceId: "tile-1", time: 1760000000, price: 94000 },
    ]);
  });
});
