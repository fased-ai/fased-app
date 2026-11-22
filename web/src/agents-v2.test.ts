import { describe, expect, it } from "vitest";
import {
  formatApprovalFields,
  formatToolLabel,
  formatToolSummary,
  parseMarkdownBlocks,
  parseSSEChunkBuffer,
  parseTaskDraftMemory,
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
