import { useCallback, useEffect, useRef, useState } from "react";

export type AgentThread = {
  id: string;
  profileId: string;
  title: string;
  connectionRef?: string;
  model?: string;
  summary?: string;
  archived: boolean;
  createdAt: number;
  updatedAt: number;
  messageCount?: number;
  pendingApprovals?: number;
};

export type AgentToolCall = {
  id: string;
  threadId: string;
  messageId: string;
  callId: string;
  toolName: string;
  tier: "read" | "action" | string;
  argsJson: string;
  status:
    | "pending"
    | "awaiting_approval"
    | "running"
    | "completed"
    | "rejected"
    | "error"
    | string;
  resultJson?: string;
  errorText?: string;
  createdAt: number;
  completedAt?: number;
};

export type AgentApproval = {
  id: string;
  threadId: string;
  messageId: string;
  toolCallId: string;
  toolName: string;
  title: string;
  summary: string;
  payloadJson: string;
  status: "pending" | "approved" | "rejected" | "expired" | string;
  decisionNote?: string;
  createdAt: number;
  decidedAt?: number;
};

export type AgentV2Message = {
  id: string;
  threadId: string;
  seq: number;
  role: "user" | "assistant" | "system" | "tool" | string;
  content: string;
  reasoning?: string;
  status: string;
  model?: string;
  provider?: string;
  callId?: string;
  toolName?: string;
  inputTokens?: number;
  outputTokens?: number;
  cachedTokens?: number;
  errorText?: string;
  createdAt: number;
  toolCalls?: AgentToolCall[];
  approvals?: AgentApproval[];
};

export type AgentV2Memory = {
  id: string;
  profileId?: string;
  scope: string;
  title?: string;
  content: string;
  tags?: string;
  pinned: boolean;
  source: string;
  createdAt: number;
  updatedAt: number;
};

export type AgentThreadDetail = {
  thread: AgentThread;
  messages: AgentV2Message[];
  pendingApprovals: AgentApproval[];
};

export type AgentStreamEvent = {
  type: string;
  threadId?: string;
  messageId?: string;
  provider?: string;
  model?: string;
  delta?: string;
  toolCall?: AgentToolCall;
  approval?: AgentApproval;
  message?: AgentV2Message;
  awaitingApproval?: boolean;
  error?: string;
};

export type SSEFrame = { event: string; data: string };

export function parseSSEChunkBuffer(buffer: string): {
  frames: SSEFrame[];
  rest: string;
} {
  const normalized = buffer.replace(/\r\n/g, "\n");
  const blocks = normalized.split("\n\n");
  const rest = blocks.pop() ?? "";
  const frames: SSEFrame[] = [];

  for (const block of blocks) {
    if (!block.trim()) continue;
    let event = "message";
    const dataLines: string[] = [];
    for (const line of block.split("\n")) {
      if (!line || line.startsWith(":")) continue;
      if (line.startsWith("event:")) {
        event = line.slice(6).trim() || "message";
      } else if (line.startsWith("data:")) {
        dataLines.push(line.slice(5).replace(/^ /, ""));
      }
    }
    if (dataLines.length > 0) {
      frames.push({ event, data: dataLines.join("\n") });
    }
  }
  return { frames, rest };
}

const toolLabels: Record<string, string> = {
  workspace_status: "Workspace status",
  list_wallets: "Wallets",
  get_wallet_balances: "Wallet balances",
  get_portfolio_summary: "Portfolio",
  fetch_web_page: "Web page",
  get_token_price: "Token price",
  get_swap_quote: "Swap quote",
  list_tasks: "Tasks",
  search_memories: "Memory",
  search_news: "News",
  list_strategies: "Strategies",
  save_memory: "Save memory",
  draft_task: "Draft task",
  draft_swap: "Swap draft",
  draft_strategy: "Strategy draft",
};

export function formatToolLabel(toolName: string): string {
  return toolLabels[toolName] ?? toolName.replace(/_/g, " ");
}

function formatUSDCompact(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "$0";
  if (value >= 1000) {
    return `$${Math.round(value).toLocaleString("en-US")}`;
  }
  if (value >= 1) {
    return `$${value.toFixed(2)}`;
  }
  return `$${Number(value.toPrecision(3))}`;
}

export function formatToolSummary(
  toolName: string,
  resultJson?: string,
): string {
  if (!resultJson) return "";
  let parsed: Record<string, unknown> | null = null;
  try {
    const raw = JSON.parse(resultJson);
    if (raw && typeof raw === "object" && !Array.isArray(raw)) {
      parsed = raw as Record<string, unknown>;
    }
  } catch {
    return "";
  }
  if (!parsed) return "";

  if (toolName === "get_token_price" && Array.isArray(parsed.prices)) {
    const parts = (parsed.prices as Array<Record<string, unknown>>)
      .slice(0, 2)
      .flatMap((p) => {
        const symbol = typeof p.symbol === "string" ? p.symbol : "";
        const price = typeof p.usdPrice === "number" ? p.usdPrice : 0;
        if (!symbol || !price) return [];
        const change =
          typeof p.change24hPct === "number" && Number.isFinite(p.change24hPct)
            ? ` (${p.change24hPct >= 0 ? "+" : ""}${p.change24hPct.toFixed(1)}%)`
            : "";
        return [`${symbol} ${formatUSDCompact(price)}${change}`];
      });
    return parts.join(" · ");
  }

  if (toolName === "get_swap_quote") {
    const inUi = typeof parsed.inUiAmount === "string" ? parsed.inUiAmount : "";
    const outUi =
      typeof parsed.outUiAmount === "string" ? parsed.outUiAmount : "";
    const fromSym =
      typeof parsed.fromSymbol === "string" ? parsed.fromSymbol : "";
    const toSym = typeof parsed.toSymbol === "string" ? parsed.toSymbol : "";
    if (!inUi || !outUi) return "";
    const outNum = Number(outUi);
    const outDisplay =
      Number.isFinite(outNum) && outNum >= 1
        ? outNum.toFixed(outNum >= 100 ? 2 : 4).replace(/\.?0+$/, "")
        : outUi;
    const base = `${inUi} ${fromSym} -> ${outDisplay} ${toSym}`.trim();
    const rawImpact = Number(parsed.priceImpactPct);
    if (Number.isFinite(rawImpact)) {
      return `${base} · ${(Math.abs(rawImpact) * 100).toFixed(2)}% impact`;
    }
    return base;
  }

  if (toolName === "get_wallet_balances") {
    if (parsed.count === 0) return "No wallets";
    if (parsed.status === "rpc_not_configured") return "Configure RPC";
    if (parsed.status === "rpc_passphrase_required") return "Vault passphrase";
    if (parsed.status === "rpc_locked") return "RPC locked";
    const solRaw = typeof parsed.sol === "string" ? parsed.sol : "";
    if (!solRaw) return "";
    const solNum = Number(solRaw);
    const solLabel =
      Number.isFinite(solNum) && solNum > 0
        ? solNum.toFixed(4).replace(/\.?0+$/, "")
        : solRaw;
    const count = Array.isArray(parsed.tokens) ? parsed.tokens.length : 0;
    return `${solLabel} SOL${count > 0 ? ` · ${count} ${count === 1 ? "token" : "tokens"}` : ""}`;
  }

  if (toolName === "get_portfolio_summary") {
    const wallets =
      typeof parsed.walletCount === "number" &&
      Number.isFinite(parsed.walletCount)
        ? parsed.walletCount
        : null;
    if (wallets === null) return "";
    const parts = [`${wallets} ${wallets === 1 ? "wallet" : "wallets"}`];
    if (
      typeof parsed.activeTasks === "number" &&
      Number.isFinite(parsed.activeTasks) &&
      parsed.activeTasks > 0
    ) {
      parts.push(
        `${parsed.activeTasks} ${parsed.activeTasks === 1 ? "task" : "tasks"}`,
      );
    }
    if (
      typeof parsed.pendingApprovals === "number" &&
      Number.isFinite(parsed.pendingApprovals) &&
      parsed.pendingApprovals > 0
    ) {
      parts.push(`${parsed.pendingApprovals} pending`);
    }
    return parts.join(" · ");
  }

  if (toolName === "fetch_web_page") {
    const title = typeof parsed.title === "string" ? parsed.title.trim() : "";
    if (title) {
      return title.length > 60 ? `${title.slice(0, 57)}...` : title;
    }
    const rawUrl =
      typeof parsed.finalUrl === "string"
        ? parsed.finalUrl
        : typeof parsed.url === "string"
          ? parsed.url
          : "";
    if (rawUrl) {
      try {
        const host = new URL(rawUrl).hostname;
        if (host) return host;
      } catch {
        // Ignore unparseable URL fallback.
      }
    }
  }

  return "";
}

export type ParsedTaskDraft = {
  id: string;
  name: string;
  description: string;
  intervalSeconds: number;
  createdAt: number;
};

export function parseTaskDraftMemory(m: AgentV2Memory): ParsedTaskDraft | null {
  const rawContent = (m.content || "").trim();
  const fallbackName = (m.title || "").replace(/^Task draft:\s*/i, "").trim();
  if (!rawContent && !fallbackName) return null;

  let name = fallbackName;
  let description = rawContent;
  let intervalSeconds = 300;
  try {
    const parsed = JSON.parse(rawContent);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const obj = parsed as Record<string, unknown>;
      if (typeof obj.name === "string" && obj.name.trim()) {
        name = obj.name.trim();
      }
      if (typeof obj.description === "string" && obj.description.trim()) {
        description = obj.description.trim();
      } else {
        description = name;
      }
      if (
        typeof obj.intervalSeconds === "number" &&
        Number.isFinite(obj.intervalSeconds)
      ) {
        const clamped = Math.round(obj.intervalSeconds);
        intervalSeconds = Math.min(86400, Math.max(60, clamped));
      }
    }
  } catch {
    if (!description) return null;
    if (!name) name = description.slice(0, 64);
  }
  if (!name || !description) return null;
  return {
    id: m.id,
    name,
    description,
    intervalSeconds,
    createdAt: m.createdAt,
  };
}

export type ApprovalField = { label: string; value: string };

export function formatApprovalFields(
  toolName: string,
  payloadJson: string,
): ApprovalField[] {
  let obj: Record<string, unknown> | null = null;
  try {
    const parsed = JSON.parse(payloadJson || "{}");
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      obj = parsed as Record<string, unknown>;
    }
  } catch {
    return payloadJson ? [{ label: "Payload", value: payloadJson }] : [];
  }
  if (!obj) return [];

  if (toolName === "draft_swap") {
    const from = String(obj.fromToken ?? "");
    const to = String(obj.toToken ?? "");
    const fields: ApprovalField[] = [];
    if (from || to) fields.push({ label: "Pair", value: `${from} -> ${to}` });
    if (obj.amount !== undefined)
      fields.push({
        label: "Amount",
        value: `${String(obj.amount)}${from ? ` ${from}` : ""}`,
      });
    if (obj.network)
      fields.push({ label: "Network", value: String(obj.network) });
    if (obj.maxSlippageBps !== undefined)
      fields.push({
        label: "Max slippage",
        value: `${String(obj.maxSlippageBps)} bps`,
      });
    if (obj.venue) fields.push({ label: "Venue", value: String(obj.venue) });
    if (obj.rationale)
      fields.push({ label: "Rationale", value: String(obj.rationale) });
    fields.push({ label: "Execution", value: "Review-only · No signing" });
    return fields;
  }

  if (toolName === "draft_strategy") {
    const fields: ApprovalField[] = [];
    if (obj.assetPair)
      fields.push({ label: "Pair", value: String(obj.assetPair) });
    if (obj.side) fields.push({ label: "Side", value: String(obj.side) });
    if (obj.entryRule)
      fields.push({ label: "Entry", value: String(obj.entryRule) });
    if (obj.exitRule)
      fields.push({ label: "Exit", value: String(obj.exitRule) });
    if (obj.maxPositionUsd !== undefined && Number(obj.maxPositionUsd) > 0)
      fields.push({
        label: "Max position",
        value: `$${String(obj.maxPositionUsd)}`,
      });
    if (obj.thesis) fields.push({ label: "Thesis", value: String(obj.thesis) });
    return fields;
  }

  if (toolName === "draft_task") {
    const fields: ApprovalField[] = [];
    if (obj.name) fields.push({ label: "Task", value: String(obj.name) });
    if (obj.intervalSeconds !== undefined)
      fields.push({
        label: "Interval",
        value: `${String(obj.intervalSeconds)}s`,
      });
    if (obj.description)
      fields.push({ label: "Details", value: String(obj.description) });
    return fields;
  }

  if (toolName === "save_memory") {
    const fields: ApprovalField[] = [];
    if (obj.title) fields.push({ label: "Title", value: String(obj.title) });
    if (obj.content)
      fields.push({ label: "Content", value: String(obj.content) });
    if (obj.tags) fields.push({ label: "Tags", value: String(obj.tags) });
    if (typeof obj.pinned === "boolean")
      fields.push({ label: "Pinned", value: obj.pinned ? "Yes" : "No" });
    return fields;
  }

  return Object.entries(obj).map(([k, v]) => ({
    label: k,
    value: typeof v === "string" ? v : JSON.stringify(v),
  }));
}

export type InlineSegment =
  | { kind: "text"; text: string }
  | { kind: "bold"; text: string }
  | { kind: "code"; text: string };

export type MarkdownBlock =
  | { kind: "code"; lang: string; code: string }
  | { kind: "list"; items: InlineSegment[][] }
  | { kind: "paragraph"; segments: InlineSegment[] };

export function parseInlineMarkdown(line: string): InlineSegment[] {
  const segments: InlineSegment[] = [];
  let rest = line;
  const token = /(\*\*[^*]+\*\*|`[^`]+`)/;
  while (rest.length > 0) {
    const m = token.exec(rest);
    if (!m || m.index === undefined) {
      segments.push({ kind: "text", text: rest });
      break;
    }
    if (m.index > 0) {
      segments.push({ kind: "text", text: rest.slice(0, m.index) });
    }
    const raw = m[0];
    if (raw.startsWith("**") && raw.endsWith("**")) {
      segments.push({ kind: "bold", text: raw.slice(2, -2) });
    } else if (raw.startsWith("`") && raw.endsWith("`")) {
      segments.push({ kind: "code", text: raw.slice(1, -1) });
    }
    rest = rest.slice(m.index + raw.length);
  }
  return segments;
}

export function parseMarkdownBlocks(text: string): MarkdownBlock[] {
  if (!text) return [];
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    if (line.startsWith("```")) {
      const lang = line.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith("```")) {
        codeLines.push(lines[i]);
        i++;
      }
      if (i < lines.length && lines[i].startsWith("```")) i++;
      blocks.push({ kind: "code", lang, code: codeLines.join("\n") });
      continue;
    }

    if (!line.trim()) {
      i++;
      continue;
    }

    if (/^\s*[-*]\s+/.test(line)) {
      const items: InlineSegment[][] = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        items.push(parseInlineMarkdown(lines[i].replace(/^\s*[-*]\s+/, "")));
        i++;
      }
      blocks.push({ kind: "list", items });
      continue;
    }

    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !lines[i].startsWith("```") &&
      !/^\s*[-*]\s+/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    blocks.push({
      kind: "paragraph",
      segments: parseInlineMarkdown(paraLines.join("\n")),
    });
  }
  return blocks;
}

export type AgentV2CoreClient = {
  mode: string;
  instanceId: string;
  request: <T>(
    route: string,
    method?: string,
    body?: unknown,
    signal?: AbortSignal,
    extraHeaders?: Record<string, string>,
  ) => Promise<T>;
  stream: (
    route: string,
    body: unknown,
    options: {
      signal?: AbortSignal;
      onEvent: (frame: SSEFrame) => void;
    },
  ) => Promise<void>;
};

export function listThreadsV2(
  core: Pick<AgentV2CoreClient, "request">,
  profileId: string,
  includeArchived = false,
) {
  const qs = new URLSearchParams();
  if (profileId) qs.set("profileId", profileId);
  if (includeArchived) qs.set("archived", "1");
  return core.request<{ threads: AgentThread[] }>(
    `agents/v2/threads?${qs.toString()}`,
  );
}

export function createThreadV2(
  core: Pick<AgentV2CoreClient, "request">,
  input: {
    profileId?: string;
    title?: string;
    connectionRef?: string;
    model?: string;
  },
) {
  return core.request<{ thread: AgentThread }>(
    "agents/v2/threads",
    "POST",
    input,
  );
}

export function getThreadV2(
  core: Pick<AgentV2CoreClient, "request">,
  threadId: string,
) {
  return core.request<AgentThreadDetail>(
    `agents/v2/threads/${encodeURIComponent(threadId)}`,
  );
}

export function updateThreadV2(
  core: Pick<AgentV2CoreClient, "request">,
  threadId: string,
  patch: {
    title?: string;
    archived?: boolean;
    connectionRef?: string;
    model?: string;
  },
) {
  return core.request<{ thread: AgentThread }>(
    `agents/v2/threads/${encodeURIComponent(threadId)}`,
    "PATCH",
    patch,
  );
}

export function deleteThreadV2(
  core: Pick<AgentV2CoreClient, "request">,
  threadId: string,
) {
  return core.request<{ deleted: boolean }>(
    `agents/v2/threads/${encodeURIComponent(threadId)}`,
    "DELETE",
  );
}

export function searchMemoriesV2(
  core: Pick<AgentV2CoreClient, "request">,
  params: { profileId?: string; q?: string; limit?: number } = {},
) {
  const qs = new URLSearchParams();
  if (params.profileId) qs.set("profileId", params.profileId);
  if (params.q) qs.set("q", params.q);
  if (params.limit) qs.set("limit", String(params.limit));
  return core.request<{ memories: AgentV2Memory[] }>(
    `agents/v2/memories?${qs.toString()}`,
  );
}

export function createMemoryV2(
  core: Pick<AgentV2CoreClient, "request">,
  input: {
    profileId?: string;
    scope?: string;
    title?: string;
    content: string;
    tags?: string;
    pinned?: boolean;
  },
) {
  return core.request<{ memory: AgentV2Memory }>(
    "agents/v2/memories",
    "POST",
    input,
  );
}

export function deleteMemoryV2(
  core: Pick<AgentV2CoreClient, "request">,
  id: string,
) {
  return core.request<{ deleted: boolean }>(
    `agents/v2/memories/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export type StreamDraft = {
  messageId: string;
  provider?: string;
  model?: string;
  content: string;
  reasoning: string;
  toolCalls: AgentToolCall[];
  approvals: AgentApproval[];
};

function mergeToolCall(list: AgentToolCall[], next: AgentToolCall) {
  const idx = list.findIndex((item) => item.id === next.id);
  if (idx === -1) return [...list, next];
  const copy = [...list];
  copy[idx] = next;
  return copy;
}

function mergeApproval(list: AgentApproval[], next: AgentApproval) {
  const idx = list.findIndex((item) => item.id === next.id);
  if (idx === -1) return [...list, next];
  const copy = [...list];
  copy[idx] = next;
  return copy;
}

export function useAgentsV2(core: AgentV2CoreClient, profileId: string) {
  const [threads, setThreads] = useState<AgentThread[]>([]);
  const [showArchived, setShowArchived] = useState(false);
  const [activeThreadId, setActiveThreadId] = useState("");
  const [detail, setDetail] = useState<AgentThreadDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [streaming, setStreaming] = useState(false);
  const [streamDraft, setStreamDraft] = useState<StreamDraft | null>(null);
  const [resolvingApprovalId, setResolvingApprovalId] = useState("");
  const [error, setError] = useState("");

  const generation = useRef(0);
  const abortController = useRef<AbortController | null>(null);

  const refreshThreads = useCallback(
    async (archived = showArchived) => {
      if (core.mode !== "ready") return [] as AgentThread[];
      const epoch = generation.current;
      const res = await listThreadsV2(core, profileId, archived);
      if (epoch === generation.current) {
        setThreads(res.threads ?? []);
      }
      return res.threads ?? [];
    },
    [core.mode, core.instanceId, core.request, profileId, showArchived],
  );

  const loadThread = useCallback(
    async (id: string) => {
      if (core.mode !== "ready" || !id) {
        setDetail(null);
        return null;
      }
      const epoch = generation.current;
      setLoading(true);
      try {
        const res = await getThreadV2(core, id);
        if (epoch === generation.current) {
          setDetail(res);
          setError("");
        }
        return res;
      } catch (e) {
        if (epoch === generation.current) {
          setError(e instanceof Error ? e.message : "Thread unavailable");
        }
        return null;
      } finally {
        if (epoch === generation.current) setLoading(false);
      }
    },
    [core.mode, core.request],
  );

  useEffect(() => {
    generation.current++;
    abortController.current?.abort();
    abortController.current = null;
    setThreads([]);
    setActiveThreadId("");
    setDetail(null);
    setStreamDraft(null);
    setStreaming(false);
    setError("");
    if (core.mode !== "ready") return;
    const epoch = generation.current;
    void listThreadsV2(core, profileId, showArchived)
      .then((res) => {
        if (epoch !== generation.current) return;
        const items = res.threads ?? [];
        setThreads(items);
        if (items.length > 0) {
          setActiveThreadId(items[0].id);
          void loadThread(items[0].id);
        }
      })
      .catch((e) => {
        if (epoch === generation.current) {
          setError(e instanceof Error ? e.message : "Threads unavailable");
        }
      });
    return () => {
      generation.current++;
    };
  }, [core.mode, core.instanceId, profileId, showArchived]);

  const selectThread = useCallback(
    async (id: string) => {
      abortController.current?.abort();
      setStreaming(false);
      setStreamDraft(null);
      setActiveThreadId(id);
      if (!id) {
        setDetail(null);
        return;
      }
      await loadThread(id);
    },
    [loadThread],
  );

  const startNewThread = useCallback(() => {
    abortController.current?.abort();
    setStreaming(false);
    setStreamDraft(null);
    setActiveThreadId("");
    setDetail(null);
    setError("");
  }, []);

  const stopStreaming = useCallback(() => {
    abortController.current?.abort();
    abortController.current = null;
  }, []);

  const applySSEFrame = useCallback((frame: SSEFrame) => {
    let ev: AgentStreamEvent;
    try {
      ev = JSON.parse(frame.data) as AgentStreamEvent;
    } catch {
      return;
    }
    const kind = ev.type || frame.event;
    if (kind === "error" && ev.error) {
      setError(ev.error);
      return;
    }
    if (kind === "run_started") {
      setStreamDraft({
        messageId: ev.messageId ?? "pending",
        provider: ev.provider,
        model: ev.model,
        content: "",
        reasoning: "",
        toolCalls: [],
        approvals: [],
      });
      return;
    }
    if (kind === "thinking_delta" && ev.delta) {
      setStreamDraft((cur) => ({
        messageId: ev.messageId || cur?.messageId || "pending",
        provider: cur?.provider,
        model: cur?.model,
        content: cur?.content ?? "",
        reasoning: (cur?.reasoning ?? "") + ev.delta,
        toolCalls: cur?.toolCalls ?? [],
        approvals: cur?.approvals ?? [],
      }));
      return;
    }
    if (kind === "text_delta" && ev.delta) {
      setStreamDraft((cur) => ({
        messageId: ev.messageId || cur?.messageId || "pending",
        provider: cur?.provider,
        model: cur?.model,
        content: (cur?.content ?? "") + ev.delta,
        reasoning: cur?.reasoning ?? "",
        toolCalls: cur?.toolCalls ?? [],
        approvals: cur?.approvals ?? [],
      }));
      return;
    }
    if ((kind === "tool_call" || kind === "tool_result") && ev.toolCall) {
      const tc = ev.toolCall;
      const updatedAp = ev.approval;
      if (updatedAp) {
        setDetail((cur) => {
          if (!cur || cur.thread.id !== updatedAp.threadId) return cur;
          return {
            ...cur,
            pendingApprovals: cur.pendingApprovals.filter(
              (a) => a.id !== updatedAp.id,
            ),
            messages: cur.messages.map((m) =>
              m.id !== updatedAp.messageId
                ? m
                : {
                    ...m,
                    toolCalls: m.toolCalls
                      ? mergeToolCall(m.toolCalls, tc)
                      : [tc],
                    approvals: m.approvals
                      ? mergeApproval(m.approvals, updatedAp)
                      : [updatedAp],
                  },
            ),
          };
        });
      }
      if (!updatedAp || kind === "tool_call") {
        setStreamDraft((cur) => ({
          messageId:
            ev.messageId || cur?.messageId || tc.messageId || "pending",
          provider: cur?.provider,
          model: cur?.model,
          content: cur?.content ?? "",
          reasoning: cur?.reasoning ?? "",
          toolCalls: mergeToolCall(cur?.toolCalls ?? [], tc),
          approvals:
            updatedAp && cur
              ? mergeApproval(cur.approvals, updatedAp)
              : (cur?.approvals ?? []),
        }));
      }
      return;
    }
    if (kind === "approval_required" && ev.approval) {
      const ap = ev.approval;
      setStreamDraft((cur) => ({
        messageId: ev.messageId || cur?.messageId || ap.messageId || "pending",
        provider: cur?.provider,
        model: cur?.model,
        content: cur?.content ?? "",
        reasoning: cur?.reasoning ?? "",
        toolCalls: cur?.toolCalls ?? [],
        approvals: mergeApproval(cur?.approvals ?? [], ap),
      }));
      return;
    }
    if (kind === "message_done" && ev.message) {
      const finished = ev.message;
      setDetail((cur) => {
        if (!cur || cur.thread.id !== finished.threadId) return cur;
        const msgs = cur.messages.filter((m) => m.id !== finished.id);
        return {
          ...cur,
          messages: [...msgs, finished],
        };
      });
      setStreamDraft(null);
    }
  }, []);

  const sendMessage = useCallback(
    async (input: {
      content: string;
      title?: string;
      forceNewThread?: boolean;
      connectionRef?: string;
      model?: string;
      toolsEnabled?: boolean;
    }) => {
      const trimmed = input.content.trim();
      if (!trimmed || core.mode !== "ready") return false;
      setError("");

      let threadId = input.forceNewThread ? "" : activeThreadId;
      let createdFresh = false;
      try {
        if (!threadId) {
          const created = await createThreadV2(core, {
            profileId,
            title: input.title,
            connectionRef: input.connectionRef || undefined,
            model: input.model || undefined,
          });
          threadId = created.thread.id;
          createdFresh = true;
          setActiveThreadId(threadId);
          setThreads((prev) => [created.thread, ...prev]);
          setDetail({
            thread: created.thread,
            messages: [],
            pendingApprovals: [],
          });
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : "Could not start thread");
        return false;
      }

      const optimisticUser: AgentV2Message = {
        id: `local-${Date.now()}`,
        threadId,
        seq: (detail?.messages.at(-1)?.seq ?? 0) + 1,
        role: "user",
        content: trimmed,
        status: "completed",
        createdAt: Math.floor(Date.now() / 1000),
      };
      setDetail((cur) =>
        cur && cur.thread.id === threadId
          ? { ...cur, messages: [...cur.messages, optimisticUser] }
          : cur,
      );

      const ctrl = new AbortController();
      abortController.current = ctrl;
      setStreaming(true);
      setStreamDraft(null);
      try {
        await core.stream(
          `agents/v2/threads/${encodeURIComponent(threadId)}/messages`,
          {
            content: trimmed,
            ...(input.connectionRef
              ? { connectionRef: input.connectionRef }
              : {}),
            ...(input.model ? { model: input.model } : {}),
            ...(input.toolsEnabled !== undefined
              ? { toolsEnabled: input.toolsEnabled }
              : {}),
          },
          { signal: ctrl.signal, onEvent: applySSEFrame },
        );
        return true;
      } catch (e) {
        if (e instanceof Error && e.name === "AbortError") {
          return true;
        }
        setError(e instanceof Error ? e.message : "Message failed");
        if (createdFresh) {
          await refreshThreads();
        }
        return false;
      } finally {
        if (abortController.current === ctrl) {
          abortController.current = null;
        }
        setStreaming(false);
        setStreamDraft(null);
        await Promise.allSettled([loadThread(threadId), refreshThreads()]);
      }
    },
    [
      activeThreadId,
      applySSEFrame,
      core,
      detail,
      loadThread,
      profileId,
      refreshThreads,
    ],
  );

  const decideApproval = useCallback(
    async (
      approvalId: string,
      decision: "approve" | "reject",
      note = "",
    ): Promise<boolean> => {
      if (core.mode !== "ready") return false;
      setError("");
      setResolvingApprovalId(approvalId);
      const ctrl = new AbortController();
      abortController.current = ctrl;
      setStreaming(true);
      try {
        await core.stream(
          `agents/v2/approvals/${encodeURIComponent(approvalId)}`,
          { decision, note: note.trim() || undefined, resume: true },
          { signal: ctrl.signal, onEvent: applySSEFrame },
        );
        return true;
      } catch (e) {
        if (!(e instanceof Error && e.name === "AbortError")) {
          setError(
            e instanceof Error ? e.message : "Could not update approval",
          );
        }
        return false;
      } finally {
        if (abortController.current === ctrl) {
          abortController.current = null;
        }
        setResolvingApprovalId("");
        setStreaming(false);
        setStreamDraft(null);
        if (activeThreadId) {
          await Promise.allSettled([
            loadThread(activeThreadId),
            refreshThreads(),
          ]);
        }
      }
    },
    [activeThreadId, applySSEFrame, core, loadThread, refreshThreads],
  );

  const renameThread = useCallback(
    async (threadId: string, title: string) => {
      const updated = await updateThreadV2(core, threadId, { title });
      setThreads((prev) =>
        prev.map((item) => (item.id === threadId ? updated.thread : item)),
      );
      setDetail((cur) =>
        cur && cur.thread.id === threadId
          ? { ...cur, thread: updated.thread }
          : cur,
      );
      return updated.thread;
    },
    [core],
  );

  const archiveThread = useCallback(
    async (threadId: string, archived: boolean) => {
      await updateThreadV2(core, threadId, { archived });
      if (threadId === activeThreadId && archived && !showArchived) {
        setActiveThreadId("");
        setDetail(null);
      }
      await refreshThreads();
    },
    [activeThreadId, core, refreshThreads, showArchived],
  );

  const removeThread = useCallback(
    async (threadId: string) => {
      await deleteThreadV2(core, threadId);
      if (threadId === activeThreadId) {
        setActiveThreadId("");
        setDetail(null);
      }
      await refreshThreads();
    },
    [activeThreadId, core, refreshThreads],
  );

  return {
    threads,
    showArchived,
    setShowArchived,
    activeThreadId,
    detail,
    loading,
    streaming,
    streamDraft,
    resolvingApprovalId,
    error,
    setError,
    refreshThreads,
    selectThread,
    startNewThread,
    sendMessage,
    stopStreaming,
    decideApproval,
    renameThread,
    archiveThread,
    removeThread,
  };
}
