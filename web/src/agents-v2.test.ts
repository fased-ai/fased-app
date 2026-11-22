import { describe, expect, it, vi } from "vitest";
import {
  buildNewsChatPrompt,
  buildTaskRunChatPrompt,
  compactThreadV2,
  formatApprovalFields,
  formatCompactTokens,
  formatDraftMemorySummary,
  formatToolLabel,
  formatToolSummary,
  formatUsageCostUSD,
  getUsageV2,
  parseChatSlashCommand,
  parseMarkdownBlocks,
  parseSSEChunkBuffer,
  parseTaskDraftMemory,
  updateMemoryV2,
} from "./agents-v2";

describe("parseSSEChunkBuffer", () => {
  it("parses split chunks, CRLF, comments, multi-line data, and retains incomplete trailing buffer", () => {
    const first = parseSSEChunkBuffer(
      ': keepalive\r\nevent: text_delta\r\ndata: {"type":"text_delta"}\r\n\r\nevent: tool_call\ndata: {"line":1}\ndata: {"line":2}\n\nevent: partial',
    );
    expect(first.frames).toEqual([
      { event: "text_delta", data: '{"type":"text_delta"}' },
      { event: "tool_call", data: '{"line":1}\n{"line":2}' },
    ]);
    expect(first.rest).toBe("event: partial");

    const second = parseSSEChunkBuffer(
      `${first.rest}\ndata: {"type":"message_done"}\n\n`,
    );
    expect(second.frames).toEqual([
      { event: "partial", data: '{"type":"message_done"}' },
    ]);
    expect(second.rest).toBe("");
  });
});

describe("formatToolLabel", () => {
  it("maps known workspace & trading tools and formats unknown names", () => {
    expect(formatToolLabel("workspace_status")).toBe("Workspace status");
    expect(formatToolLabel("list_wallets")).toBe("Wallets");
    expect(formatToolLabel("get_wallet_balances")).toBe("Wallet balances");
    expect(formatToolLabel("get_portfolio_summary")).toBe("Portfolio");
    expect(formatToolLabel("fetch_web_page")).toBe("Web page");
    expect(formatToolLabel("get_token_price")).toBe("Token price");
    expect(formatToolLabel("get_swap_quote")).toBe("Swap quote");
    expect(formatToolLabel("list_tasks")).toBe("Tasks");
    expect(formatToolLabel("search_memories")).toBe("Memory");
    expect(formatToolLabel("search_news")).toBe("News");
    expect(formatToolLabel("list_strategies")).toBe("Strategies");
    expect(formatToolLabel("save_memory")).toBe("Save memory");
    expect(formatToolLabel("draft_task")).toBe("Draft task");
    expect(formatToolLabel("draft_swap")).toBe("Swap draft");
    expect(formatToolLabel("draft_strategy")).toBe("Strategy draft");
    expect(formatToolLabel("custom_alpha_scan")).toBe("custom alpha scan");
  });
});

describe("formatToolSummary", () => {
  it("formats market price, swap quote, wallet balance summaries, and handles invalid inputs", () => {
    expect(
      formatToolSummary(
        "get_token_price",
        JSON.stringify({
          prices: [
            { symbol: "SOL", usdPrice: 70.007, change24hPct: 1.84 },
            { symbol: "USDC", usdPrice: 1.0, change24hPct: -0.01 },
          ],
        }),
      ),
    ).toBe("SOL $70.01 (+1.8%) · USDC $1.00 (-0.0%)");

    expect(
      formatToolSummary(
        "get_swap_quote",
        JSON.stringify({
          inUiAmount: "1",
          outUiAmount: "70.059912",
          fromSymbol: "SOL",
          toSymbol: "USDC",
          priceImpactPct: "0.0000224",
        }),
      ),
    ).toBe("1 SOL -> 70.0599 USDC · 0.00% impact");

    expect(
      formatToolSummary(
        "get_wallet_balances",
        JSON.stringify({
          sol: "2.450000000",
          tokens: [{ mint: "EPjF", uiAmount: "120" }],
        }),
      ),
    ).toBe("2.45 SOL · 1 token");

    expect(
      formatToolSummary("get_wallet_balances", JSON.stringify({ count: 0 })),
    ).toBe("No wallets");
    expect(
      formatToolSummary(
        "get_wallet_balances",
        JSON.stringify({ status: "rpc_not_configured" }),
      ),
    ).toBe("Configure RPC");
    expect(
      formatToolSummary(
        "get_portfolio_summary",
        JSON.stringify({ walletCount: 2, activeTasks: 1, pendingApprovals: 3 }),
      ),
    ).toBe("2 wallets · 1 task · 3 pending");
    expect(
      formatToolSummary(
        "get_portfolio_summary",
        JSON.stringify({ walletCount: 0, activeTasks: 0, pendingApprovals: 0 }),
      ),
    ).toBe("0 wallets");
    expect(
      formatToolSummary(
        "fetch_web_page",
        JSON.stringify({
          title: "Solana Foundation Network Status",
          finalUrl: "https://status.solana.com/",
        }),
      ),
    ).toBe("Solana Foundation Network Status");
    expect(
      formatToolSummary(
        "fetch_web_page",
        JSON.stringify({
          title: "",
          finalUrl: "https://docs.jupiter.ag/api",
        }),
      ),
    ).toBe("docs.jupiter.ag");
    expect(formatToolSummary("get_token_price", "{broken")).toBe("");
    expect(formatToolSummary("workspace_status", "{}")).toBe("");
  });
});

describe("parseTaskDraftMemory", () => {
  it("parses JSON task_draft payloads, clamps intervals, and supports fallback/invalid inputs", () => {
    const parsed = parseTaskDraftMemory({
      id: "mem-1",
      scope: "task_draft",
      title: "Task draft: SOL Watch",
      content: JSON.stringify({
        name: "SOL Volatility Watch",
        description: "Check SOL/USDC spread",
        intervalSeconds: 30,
      }),
      pinned: false,
      source: "agent",
      createdAt: 1000,
      updatedAt: 1000,
    });
    expect(parsed).toEqual({
      id: "mem-1",
      name: "SOL Volatility Watch",
      description: "Check SOL/USDC spread",
      intervalSeconds: 60,
      createdAt: 1000,
    });

    const fallback = parseTaskDraftMemory({
      id: "mem-2",
      scope: "task_draft",
      title: "Task draft: Daily digest",
      content: "Summarize overnight RSS feeds",
      pinned: false,
      source: "agent",
      createdAt: 2000,
      updatedAt: 2000,
    });
    expect(fallback).toEqual({
      id: "mem-2",
      name: "Daily digest",
      description: "Summarize overnight RSS feeds",
      intervalSeconds: 300,
      createdAt: 2000,
    });

    expect(
      parseTaskDraftMemory({
        id: "mem-3",
        scope: "task_draft",
        content: "",
        pinned: false,
        source: "agent",
        createdAt: 3000,
        updatedAt: 3000,
      }),
    ).toBeNull();
  });
});

describe("formatApprovalFields", () => {
  it("formats draft_swap, draft_strategy, draft_task, save_memory, and malformed JSON", () => {
    expect(
      formatApprovalFields(
        "draft_swap",
        JSON.stringify({
          fromToken: "USDC",
          toToken: "SOL",
          amount: "25",
          network: "solana-mainnet",
          maxSlippageBps: 50,
          venue: "Jupiter",
          rationale: "Rebalance",
        }),
      ),
    ).toEqual([
      { label: "Pair", value: "USDC -> SOL" },
      { label: "Amount", value: "25 USDC" },
      { label: "Network", value: "solana-mainnet" },
      { label: "Max slippage", value: "50 bps" },
      { label: "Venue", value: "Jupiter" },
      { label: "Rationale", value: "Rebalance" },
      { label: "Execution", value: "Review-only · No signing" },
    ]);

    expect(
      formatApprovalFields(
        "draft_strategy",
        JSON.stringify({
          assetPair: "SOL/USDC",
          side: "buy",
          entryRule: "RSI < 30",
          exitRule: "RSI > 60",
          maxPositionUsd: 500,
          thesis: "Range mean reversion",
        }),
      ),
    ).toEqual([
      { label: "Pair", value: "SOL/USDC" },
      { label: "Side", value: "buy" },
      { label: "Entry", value: "RSI < 30" },
      { label: "Exit", value: "RSI > 60" },
      { label: "Max position", value: "$500" },
      { label: "Thesis", value: "Range mean reversion" },
    ]);

    expect(
      formatApprovalFields(
        "draft_task",
        JSON.stringify({
          name: "SOL Alert",
          intervalSeconds: 300,
          description: "Monitor spread",
        }),
      ),
    ).toEqual([
      { label: "Task", value: "SOL Alert" },
      { label: "Interval", value: "300s" },
      { label: "Details", value: "Monitor spread" },
    ]);

    expect(
      formatApprovalFields(
        "save_memory",
        JSON.stringify({
          title: "Slippage",
          content: "Cap at 50 bps",
          tags: "risk",
          pinned: true,
        }),
      ),
    ).toEqual([
      { label: "Title", value: "Slippage" },
      { label: "Content", value: "Cap at 50 bps" },
      { label: "Tags", value: "risk" },
      { label: "Pinned", value: "Yes" },
    ]);

    expect(formatApprovalFields("draft_swap", "{invalid")).toEqual([
      { label: "Payload", value: "{invalid" },
    ]);
  });
});

describe("parseMarkdownBlocks", () => {
  it("parses fenced code, inline bold/code, lists, and preserves plain HTML tags safely", () => {
    const blocks = parseMarkdownBlocks(
      "Use **limit** orders with `<script>alert(1)</script>`:\n\n- Check `USDC`\n- Check **SOL**\n\n```ts\nconst x = 1;\n```",
    );
    expect(blocks).toEqual([
      {
        kind: "paragraph",
        segments: [
          { kind: "text", text: "Use " },
          { kind: "bold", text: "limit" },
          { kind: "text", text: " orders with " },
          { kind: "code", text: "<script>alert(1)</script>" },
          { kind: "text", text: ":" },
        ],
      },
      {
        kind: "list",
        items: [
          [
            { kind: "text", text: "Check " },
            { kind: "code", text: "USDC" },
          ],
          [
            { kind: "text", text: "Check " },
            { kind: "bold", text: "SOL" },
          ],
        ],
      },
      {
        kind: "code",
        lang: "ts",
        code: "const x = 1;",
      },
    ]);
  });
});

describe("formatDraftMemorySummary & updateMemoryV2", () => {
  it("formats swap, task, and strategy drafts cleanly while leaving non-drafts and invalid JSON untouched", () => {
    expect(
      formatDraftMemorySummary({
        scope: "swap_draft",
        title: "Swap draft: 10 USDC -> SOL",
        content: JSON.stringify({
          amount: "10",
          fromToken: "USDC",
          toToken: "SOL",
          maxSlippageBps: 50,
          venue: "Jupiter",
          rationale: "Rebalance core SOL position",
        }),
      }),
    ).toBe(
      "10 USDC -> SOL (slippage 50 bps) · Jupiter · Rebalance core SOL position",
    );

    expect(
      formatDraftMemorySummary({
        scope: "task_draft",
        title: "Task draft: SOL Volatility Watch",
        content: JSON.stringify({
          name: "SOL Volatility Watch",
          description: "Alert on 3% intraday swing",
          intervalSeconds: 300,
        }),
      }),
    ).toBe("Alert on 3% intraday swing · every 5m");

    expect(
      formatDraftMemorySummary({
        scope: "strategy_draft",
        title: "Strategy draft: SOL Grid",
        content: JSON.stringify({
          assetPair: "SOL/USDC",
          side: "buy",
          thesis: "Accumulate pullbacks above support",
          entryRule: "RSI < 38",
          maxPositionUsd: 500,
        }),
      }),
    ).toBe(
      "SOL/USDC (buy) · Accumulate pullbacks above support · RSI < 38 · max $500",
    );

    expect(
      formatDraftMemorySummary({
        scope: "workspace",
        title: "Rule",
        content: '{"raw":"kept verbatim"}',
      }),
    ).toBe('{"raw":"kept verbatim"}');

    expect(
      formatDraftMemorySummary({
        scope: "swap_draft",
        title: "Broken",
        content: "{not-json",
      }),
    ).toBe("{not-json");
  });

  it("sends PATCH request from updateMemoryV2", async () => {
    const req = vi.fn().mockResolvedValue({
      memory: {
        id: "mem_1",
        scope: "workspace",
        title: "Risk",
        content: "Cap slippage at 25 bps",
        pinned: true,
        source: "user",
        createdAt: 1,
        updatedAt: 2,
      },
    });
    await updateMemoryV2({ request: req }, "mem 1", {
      content: "Cap slippage at 25 bps",
      pinned: true,
    });
    expect(req).toHaveBeenCalledWith("agents/v2/memories/mem%201", "PATCH", {
      content: "Cap slippage at 25 bps",
      pinned: true,
    });
  });
});

describe("buildNewsChatPrompt & buildTaskRunChatPrompt", () => {
  it("formats news items as untrusted data and includes scheduled run details", () => {
    const newsPrompt = buildNewsChatPrompt("Solana News", {
      title: "SOL throughput hits new peak",
      url: "https://example.invalid/sol",
    });
    expect(newsPrompt).toContain("untrusted external data");
    expect(newsPrompt).toContain("Solana News");
    expect(newsPrompt).toContain("SOL throughput hits new peak");
    expect(newsPrompt).toContain("https://example.invalid/sol");

    const runPrompt = buildTaskRunChatPrompt(
      { name: "Morning Risk", text: "Check open risk caps" },
      {
        id: "task-1:1",
        outcome: "analyzed",
        analysis: { state: "completed" },
        watch: { newIds: ["a", "b"] },
      },
      "Spread widened to 18 bps.",
    );
    expect(runPrompt).toContain("Morning Risk");
    expect(runPrompt).toContain("task-1:1");
    expect(runPrompt).toContain("New matches: 2");
    expect(runPrompt).toContain("Spread widened to 18 bps.");
  });
});

describe("Phase 8B-1 slash commands, usage formatters, and API helpers", () => {
  it("parses /compact and /side slash commands cleanly while keeping regular prompts", () => {
    expect(parseChatSlashCommand("  /compact  ")).toEqual({ kind: "compact" });
    expect(parseChatSlashCommand("/side Why did fees spike?")).toEqual({
      kind: "side",
      question: "Why did fees spike?",
    });
    expect(parseChatSlashCommand("/side")).toEqual({
      kind: "side",
      question: "",
    });
    expect(parseChatSlashCommand("Check my balances")).toEqual({
      kind: "message",
      content: "Check my balances",
    });
  });

  it("formats token counts and USD costs without inventing unpriced values", () => {
    expect(formatCompactTokens(420)).toBe("420");
    expect(formatCompactTokens(1500)).toBe("1.5k");
    expect(formatCompactTokens(2_450_000)).toBe("2.5M");
    expect(formatUsageCostUSD(0, false)).toBe("—");
    expect(formatUsageCostUSD(0, true)).toBe("$0.00");
    expect(formatUsageCostUSD(1200, true)).toBe("$0.0012");
    expect(formatUsageCostUSD(2_310_000, true)).toBe("$2.31");
  });

  it("calls compactThreadV2 and getUsageV2 (including days=0)", async () => {
    const req = vi.fn().mockResolvedValue({});
    await compactThreadV2({ request: req }, "th 1");
    expect(req).toHaveBeenCalledWith(
      "agents/v2/threads/th%201/compact",
      "POST",
      {},
    );

    await compactThreadV2({ request: req }, "th_2", 4);
    expect(req).toHaveBeenCalledWith("agents/v2/threads/th_2/compact", "POST", {
      keepRecent: 4,
    });

    await getUsageV2({ request: req }, { profileId: "wen-operator", days: 0 });
    expect(req).toHaveBeenCalledWith(
      "agents/v2/usage?profileId=wen-operator&days=0",
    );
  });
});
