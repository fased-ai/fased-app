import { describe, expect, it } from "vitest";
import {
  formatApprovalFields,
  formatToolLabel,
  parseMarkdownBlocks,
  parseSSEChunkBuffer,
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
