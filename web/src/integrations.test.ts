import { describe, expect, it, vi } from "vitest";
import {
  callExternalMCPTool,
  deleteExternalMCPClient,
  evaluateDecision,
  formatDecisionAnswerSummary,
  getDecisionConfig,
  listExternalMCPClients,
  parseAllowedToolsInput,
  saveDecisionConfig,
  saveExternalMCPClient,
  syncExternalMCPClient,
} from "./integrations";

describe("integrations helpers and Core API wrappers", () => {
  it("parses and deduplicates comma-separated allowed tool names", () => {
    expect(
      parseAllowedToolsInput(" quote_lookup , metrics_read, quote_lookup,  "),
    ).toEqual(["quote_lookup", "metrics_read"]);
    expect(parseAllowedToolsInput("")).toEqual([]);
  });

  it("formats typed System-1 answers concisely", () => {
    expect(
      formatDecisionAnswerSummary({
        type: "noul",
        noul: 0.824,
        confidence: 0.76,
      }),
    ).toBe("0.82 (76%)");
    expect(
      formatDecisionAnswerSummary({
        type: "choice",
        choice: "bullish",
        confidence: 0.91,
      }),
    ).toBe("bullish (91%)");
    expect(
      formatDecisionAnswerSummary({
        type: "score",
        score: 4,
        legend: "High",
        confidence: 0.65,
      }),
    ).toBe("4 High (65%)");
  });

  it("routes Jev and External MCP client calls to the expected Core routes", async () => {
    const request = vi.fn().mockResolvedValue({ ok: true });
    const core = { request };

    await getDecisionConfig(core);
    await saveDecisionConfig(core, { apiKey: "jev-k", model: "jev-latest" });
    await evaluateDecision(core, {
      state: "SOL breakout",
      questions: { bias: { type: "choice", choices: ["bullish", "bearish"] } },
    });
    await listExternalMCPClients(core);
    await saveExternalMCPClient(core, {
      id: "lab py",
      name: "Lab Py",
      endpoint: "https://lab.example.com/mcp",
    });
    await syncExternalMCPClient(core, "lab py");
    await callExternalMCPTool(core, "lab py", "quote_lookup", {
      symbol: "SOL",
    });
    await deleteExternalMCPClient(core, "lab py");

    expect(request.mock.calls).toEqual([
      ["decision/config"],
      ["decision/config", "PUT", { apiKey: "jev-k", model: "jev-latest" }],
      [
        "decision/evaluate",
        "POST",
        {
          state: "SOL breakout",
          questions: {
            bias: { type: "choice", choices: ["bullish", "bearish"] },
          },
        },
      ],
      ["mcp/clients"],
      [
        "mcp/clients",
        "PUT",
        {
          id: "lab py",
          name: "Lab Py",
          endpoint: "https://lab.example.com/mcp",
        },
      ],
      ["mcp/clients/lab%20py/sync", "POST"],
      [
        "mcp/clients/lab%20py/call",
        "POST",
        { tool: "quote_lookup", arguments: { symbol: "SOL" } },
      ],
      ["mcp/clients/lab%20py", "DELETE"],
    ]);
  });
});
