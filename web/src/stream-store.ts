import { useSyncExternalStore } from "react";
import type { SSEFrame } from "./agents-v2";
import type { CrosshairSyncPoint } from "./market-data";

export type StreamStatus =
  "idle" | "connecting" | "ws" | "sse" | "reconnecting";

export type StreamEnvelope<T = unknown> = {
  type: string;
  topic?: string;
  event?: string;
  ts?: number;
  payload?: T;
  error?: string;
};

export type StreamCoreBinding = {
  mode: string;
  request: <T>(
    route: string,
    method?: string,
    body?: unknown,
    signal?: AbortSignal,
  ) => Promise<T>;
  streamGet: (
    route: string,
    options: {
      signal?: AbortSignal;
      onEvent: (frame: SSEFrame) => void;
    },
  ) => Promise<void>;
};

export type StreamListener = (msg: StreamEnvelope) => void;

type WebSocketLike = {
  readyState: number;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: string }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  send: (data: string) => void;
  close: (code?: number, reason?: string) => void;
};

type StreamClientOptions = {
  createWebSocket?: (url: string, protocols: string[]) => WebSocketLike;
  isWails?: () => boolean;
  resolveWSURL?: () => string;
};

const WS_OPEN = 1;
const RETRY_DELAYS_MS = [500, 1000, 2000, 4000, 8000, 15000];
const MAX_TRADE_BATCH_PER_FRAME = 100;

function defaultIsWails(): boolean {
  if (typeof window === "undefined") return false;
  return (
    window.location.protocol === "wails:" ||
    window.location.hostname === "wails.localhost"
  );
}

function defaultResolveWSURL(): string {
  if (typeof window === "undefined") return "ws://127.0.0.1/api/v1/stream";
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/stream`;
}

function defaultCreateWS(url: string, protocols: string[]): WebSocketLike {
  return new WebSocket(url, protocols) as unknown as WebSocketLike;
}

/**
 * CrosshairBus propagates chart cursor timestamps across ChartTiles and
 * OrderflowPane outside React state so 4x4 grids stay render-silent at 60fps.
 */
export class CrosshairBus {
  private listeners = new Set<(pt: CrosshairSyncPoint) => void>();

  subscribe = (listener: (pt: CrosshairSyncPoint) => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  publish = (pt: CrosshairSyncPoint): void => {
    for (const listener of this.listeners) {
      listener(pt);
    }
  };
}

/**
 * FasedStreamClient maintains a single multiplexed Core <-> UI WebSocket
 * (with automatic Wails/error fallback to multiplexed SSE) outside React and
 * batches high-frequency ticks onto a 16ms requestAnimationFrame cadence.
 */
export class FasedStreamClient {
  private binding: StreamCoreBinding = {
    mode: "loading",
    request: () => Promise.reject(new Error("Core unavailable")),
    streamGet: () => Promise.reject(new Error("Core unavailable")),
  };
  private topics = new Map<string, Set<StreamListener>>();
  private status: StreamStatus = "idle";
  private statusListeners = new Set<() => void>();

  private ws: WebSocketLike | null = null;
  private sseAbort: AbortController | null = null;
  private sseDebounceTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private runGeneration = 0;
  private wsFailCount = 0;
  private retryAttempt = 0;
  private disposed = false;

  private pendingLatest = new Map<string, StreamEnvelope>();
  private pendingTrades = new Map<string, StreamEnvelope[]>();
  private flushTimer: number | null = null;
  private flushUsesRAF = false;

  private readonly createWebSocket: (
    url: string,
    protocols: string[],
  ) => WebSocketLike;
  private readonly isWails: () => boolean;
  private readonly resolveWSURL: () => string;

  constructor(options: StreamClientOptions = {}) {
    this.createWebSocket = options.createWebSocket ?? defaultCreateWS;
    this.isWails = options.isWails ?? defaultIsWails;
    this.resolveWSURL = options.resolveWSURL ?? defaultResolveWSURL;
  }

  getStatus = (): StreamStatus => this.status;

  subscribeStatus = (listener: () => void): (() => void) => {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  };

  private setStatus(next: StreamStatus): void {
    if (this.status === next) return;
    this.status = next;
    for (const listener of this.statusListeners) {
      listener();
    }
  }

  setCore(next: StreamCoreBinding): void {
    if (this.disposed) return;
    const prevMode = this.binding.mode;
    const handlesChanged =
      this.binding.request !== next.request ||
      this.binding.streamGet !== next.streamGet;
    this.binding = next;

    if (next.mode !== "ready") {
      if (prevMode === "ready" || this.ws || this.sseAbort) {
        this.teardownTransport();
        this.wsFailCount = 0;
        this.retryAttempt = 0;
      }
      return;
    }

    if (handlesChanged && prevMode === "ready") {
      this.teardownTransport();
    }
    if (this.topics.size > 0 && !this.ws && !this.sseAbort) {
      this.ensureConnected();
    }
  }

  subscribe(topic: string, listener: StreamListener): () => void {
    const cleanTopic = topic.trim();
    if (!cleanTopic) return () => {};

    let set = this.topics.get(cleanTopic);
    const isNewTopic = !set;
    if (!set) {
      set = new Set<StreamListener>();
      this.topics.set(cleanTopic, set);
    }
    set.add(listener);

    if (isNewTopic && this.binding.mode === "ready" && !this.disposed) {
      if (this.ws && this.ws.readyState === WS_OPEN) {
        this.safeSend({ op: "sub", topics: [cleanTopic] });
      } else if (this.sseAbort) {
        this.scheduleSSERestart();
      } else {
        this.ensureConnected();
      }
    }

    let removed = false;
    return () => {
      if (removed) return;
      removed = true;
      const cur = this.topics.get(cleanTopic);
      if (!cur) return;
      cur.delete(listener);
      if (cur.size > 0) return;

      this.topics.delete(cleanTopic);
      this.pendingLatest.delete(cleanTopic);
      this.pendingTrades.delete(cleanTopic);

      if (this.topics.size === 0) {
        this.teardownTransport();
        return;
      }
      if (this.ws && this.ws.readyState === WS_OPEN) {
        this.safeSend({ op: "unsub", topics: [cleanTopic] });
      } else if (this.sseAbort) {
        this.scheduleSSERestart();
      }
    };
  }

  dispose(): void {
    this.disposed = true;
    this.topics.clear();
    this.teardownTransport();
    this.clearFlush();
    this.pendingLatest.clear();
    this.pendingTrades.clear();
  }

  private safeSend(frame: unknown): void {
    if (!this.ws || this.ws.readyState !== WS_OPEN) return;
    try {
      this.ws.send(JSON.stringify(frame));
    } catch {
      /* close handler will transition reconnect */
    }
  }

  private teardownTransport(): void {
    this.runGeneration++;
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.sseDebounceTimer !== null) {
      clearTimeout(this.sseDebounceTimer);
      this.sseDebounceTimer = null;
    }
    if (this.sseAbort) {
      this.sseAbort.abort();
      this.sseAbort = null;
    }
    if (this.ws) {
      const sock = this.ws;
      this.ws = null;
      sock.onopen = null;
      sock.onmessage = null;
      sock.onerror = null;
      sock.onclose = null;
      try {
        sock.close(1000, "client unsubscribed");
      } catch {
        /* ignore close errors */
      }
    }
    this.setStatus("idle");
  }

  private shouldUseSSE(): boolean {
    if (this.isWails()) return true;
    if (
      typeof WebSocket === "undefined" &&
      this.createWebSocket === defaultCreateWS
    ) {
      return true;
    }
    return this.wsFailCount >= 2;
  }

  private ensureConnected(): void {
    if (
      this.disposed ||
      this.binding.mode !== "ready" ||
      this.topics.size === 0
    ) {
      return;
    }
    if (this.ws || this.sseAbort) return;
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    if (this.shouldUseSSE()) {
      void this.startSSELoop();
      return;
    }
    void this.startWebSocket();
  }

  private scheduleReconnect(): void {
    if (
      this.disposed ||
      this.binding.mode !== "ready" ||
      this.topics.size === 0 ||
      this.reconnectTimer !== null
    ) {
      return;
    }
    this.setStatus("reconnecting");
    const delay =
      RETRY_DELAYS_MS[Math.min(this.retryAttempt, RETRY_DELAYS_MS.length - 1)];
    this.retryAttempt++;
    const gen = this.runGeneration;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (gen !== this.runGeneration) return;
      this.ensureConnected();
    }, delay);
  }

  private async startWebSocket(): Promise<void> {
    const gen = ++this.runGeneration;
    this.setStatus("connecting");
    try {
      const tok = await this.binding.request<{ ticket: string }>(
        "stream/ticket",
        "POST",
        {},
      );
      if (
        gen !== this.runGeneration ||
        this.disposed ||
        this.binding.mode !== "ready" ||
        this.topics.size === 0
      ) {
        return;
      }
      if (!tok?.ticket) {
        throw new Error("empty stream ticket");
      }
      const wsURL = this.resolveWSURL();
      const sock = this.createWebSocket(wsURL, [
        "fased.stream.v1",
        `fased.ticket.${tok.ticket}`,
      ]);
      this.ws = sock;
      let opened = false;

      sock.onopen = () => {
        if (gen !== this.runGeneration || this.ws !== sock) return;
        opened = true;
        this.wsFailCount = 0;
        this.retryAttempt = 0;
        this.setStatus("ws");
        const active = [...this.topics.keys()];
        if (active.length > 0) {
          this.safeSend({ op: "sub", topics: active });
        }
      };

      sock.onmessage = (ev) => {
        if (gen !== this.runGeneration || this.ws !== sock) return;
        try {
          const parsed = JSON.parse(ev.data) as StreamEnvelope;
          if (
            !parsed ||
            parsed.type === "ready" ||
            parsed.type === "pong" ||
            parsed.type === "ack"
          ) {
            return;
          }
          if (parsed.topic && parsed.type === "event") {
            this.enqueueFrame(parsed.topic, parsed);
          }
        } catch {
          /* ignore malformed frames */
        }
      };

      sock.onerror = () => {
        /* onclose handles reconnect / SSE fallback */
      };

      sock.onclose = () => {
        if (this.ws === sock) {
          this.ws = null;
        }
        if (gen !== this.runGeneration || this.disposed) return;
        if (!opened) {
          this.wsFailCount++;
        }
        if (this.topics.size === 0 || this.binding.mode !== "ready") {
          this.setStatus("idle");
          return;
        }
        if (this.shouldUseSSE()) {
          this.retryAttempt = 0;
          this.ensureConnected();
        } else {
          this.scheduleReconnect();
        }
      };
    } catch {
      if (gen !== this.runGeneration || this.disposed) return;
      this.ws = null;
      this.wsFailCount++;
      if (this.topics.size === 0 || this.binding.mode !== "ready") {
        this.setStatus("idle");
        return;
      }
      if (this.shouldUseSSE()) {
        this.retryAttempt = 0;
        this.ensureConnected();
      } else {
        this.scheduleReconnect();
      }
    }
  }

  private scheduleSSERestart(): void {
    if (this.sseDebounceTimer !== null) {
      clearTimeout(this.sseDebounceTimer);
    }
    this.sseDebounceTimer = setTimeout(() => {
      this.sseDebounceTimer = null;
      if (
        this.disposed ||
        this.binding.mode !== "ready" ||
        this.topics.size === 0
      ) {
        return;
      }
      if (this.sseAbort) {
        this.sseAbort.abort();
        this.sseAbort = null;
      }
      void this.startSSELoop();
    }, 40);
  }

  private buildMarketSSESubs(): string {
    const entries: string[] = [];
    const seen = new Set<string>();
    for (const topic of this.topics.keys()) {
      const parts = topic.split(":");
      if (parts.length < 3) continue;
      const kind = parts[0];
      let sub = "";
      if (kind === "candle") {
        sub = `${parts[1]}:${parts[2]}:${parts[3] ?? "1h"}:${parts[4] ?? "standard"}`;
      } else if (kind === "orderbook" || kind === "trade") {
        sub = `${parts[1]}:${parts[2]}`;
      } else if (kind === "footprint") {
        sub = `${parts[1]}:${parts[2]}:${parts[3] ?? "1h"}`;
      }
      if (sub && !seen.has(sub)) {
        seen.add(sub);
        entries.push(sub);
      }
      if (entries.length >= 16) break;
    }
    return entries.join(",");
  }

  private async startSSELoop(): Promise<void> {
    const subs = this.buildMarketSSESubs();
    if (!subs) {
      this.setStatus("idle");
      return;
    }
    const gen = ++this.runGeneration;
    const controller = new AbortController();
    this.sseAbort = controller;
    this.setStatus("sse");
    const qs = new URLSearchParams({ noSnapshot: "1", subs });

    try {
      await this.binding.streamGet(`market/stream?${qs.toString()}`, {
        signal: controller.signal,
        onEvent: (frame) => {
          if (gen !== this.runGeneration || controller.signal.aborted) return;
          if (
            !frame.data ||
            frame.event === "heartbeat" ||
            frame.event === "ready"
          ) {
            return;
          }
          try {
            const ev = JSON.parse(frame.data) as {
              type?: string;
              venue?: string;
              symbol?: string;
              interval?: string;
              style?: string;
              timestamp?: number;
              payload?: unknown;
            };
            const evType = ev.type ?? frame.event;
            if (!ev.venue || !ev.symbol || !evType) return;
            for (const topic of this.topics.keys()) {
              if (matchesTopicFromMarketSSE(topic, evType, ev)) {
                this.enqueueFrame(topic, {
                  type: "event",
                  topic,
                  event: evType,
                  ts: ev.timestamp,
                  payload: ev.payload,
                });
              }
            }
          } catch {
            /* ignore malformed SSE payload */
          }
        },
      });
    } catch {
      /* reconnect below if not aborted */
    } finally {
      if (this.sseAbort === controller) {
        this.sseAbort = null;
      }
    }

    if (
      gen !== this.runGeneration ||
      controller.signal.aborted ||
      this.disposed ||
      this.topics.size === 0 ||
      this.binding.mode !== "ready"
    ) {
      return;
    }
    this.scheduleReconnect();
  }

  private enqueueFrame(topic: string, msg: StreamEnvelope): void {
    if (!this.topics.has(topic)) return;
    if (topic.startsWith("trade:")) {
      let list = this.pendingTrades.get(topic);
      if (!list) {
        list = [];
        this.pendingTrades.set(topic, list);
      }
      list.push(msg);
      if (list.length > MAX_TRADE_BATCH_PER_FRAME) {
        list.splice(0, list.length - MAX_TRADE_BATCH_PER_FRAME);
      }
    } else {
      this.pendingLatest.set(topic, msg);
    }
    this.scheduleFlush();
  }

  private scheduleFlush(): void {
    if (this.flushTimer !== null) return;
    const isHidden =
      typeof document !== "undefined" && document.visibilityState === "hidden";
    if (!isHidden && typeof requestAnimationFrame === "function") {
      this.flushUsesRAF = true;
      this.flushTimer = requestAnimationFrame(() => {
        this.flushTimer = null;
        this.flush();
      });
    } else {
      this.flushUsesRAF = false;
      this.flushTimer = setTimeout(
        () => {
          this.flushTimer = null;
          this.flush();
        },
        isHidden ? 100 : 16,
      ) as unknown as number;
    }
  }

  private clearFlush(): void {
    if (this.flushTimer === null) return;
    if (this.flushUsesRAF && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(this.flushTimer);
    } else {
      clearTimeout(this.flushTimer);
    }
    this.flushTimer = null;
  }

  private flush(): void {
    if (this.disposed) return;

    const latestEntries = [...this.pendingLatest.entries()];
    this.pendingLatest.clear();
    const tradeEntries = [...this.pendingTrades.entries()];
    this.pendingTrades.clear();

    for (const [topic, msg] of latestEntries) {
      const listeners = this.topics.get(topic);
      if (!listeners) continue;
      for (const fn of [...listeners]) {
        fn(msg);
      }
    }

    for (const [topic, list] of tradeEntries) {
      const listeners = this.topics.get(topic);
      if (!listeners) continue;
      for (const msg of list) {
        for (const fn of [...listeners]) {
          fn(msg);
        }
      }
    }
  }
}

function matchesTopicFromMarketSSE(
  topic: string,
  evType: string,
  ev: { venue?: string; symbol?: string; interval?: string; style?: string },
): boolean {
  const parts = topic.split(":");
  if (parts.length < 3 || parts[0] !== evType) return false;
  if (parts[1] !== ev.venue) return false;
  if (parts[2].toUpperCase() !== (ev.symbol ?? "").toUpperCase()) return false;
  if (parts[0] === "candle") {
    if (parts[3] && ev.interval && parts[3] !== ev.interval) return false;
    if (parts[4] && ev.style && parts[4] !== ev.style) return false;
  } else if (parts[0] === "footprint") {
    if (parts[3] && ev.interval && parts[3] !== ev.interval) return false;
  }
  return true;
}

export function useStreamStatus(client: FasedStreamClient): StreamStatus {
  return useSyncExternalStore(
    client.subscribeStatus,
    client.getStatus,
    client.getStatus,
  );
}
