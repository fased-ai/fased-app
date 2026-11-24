import type { AgentV2CoreClient } from "./agents-v2";

export type DecisionStatus = {
  configured: boolean;
  model: string;
  endpoint: string;
  fallbackMode: string;
};

export type DecisionConfigUpdate = {
  apiKey?: string;
  clearKey?: boolean;
  model?: string;
};

export type DecisionQuestionSpec = {
  type: "noul" | "choice" | "score";
  instructions?: string;
  choices?: string[];
  rubric?: string[];
};

export type DecisionEvaluateRequest = {
  state: string;
  model?: string;
  questions: Record<string, DecisionQuestionSpec>;
};

export type DecisionAnswer = {
  type: "noul" | "choice" | "score";
  noul?: number;
  choice?: string;
  score?: number;
  probabilities?: Record<string, number>;
  legend?: string;
  confidence: number;
};

export type DecisionEvaluateResponse = {
  provider: string;
  model: string;
  answers: Record<string, DecisionAnswer>;
  usage: {
    input_tokens: number;
    output_tokens: number;
  };
  latencyMs: number;
  fallback: boolean;
};

export type ExternalMCPRemoteTool = {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
};

export type ExternalMCPRemoteResource = {
  uri: string;
  name: string;
  description?: string;
  mimeType?: string;
};

export type ExternalMCPServer = {
  id: string;
  name: string;
  endpoint: string;
  allowLocalhost: boolean;
  enabled: boolean;
  readOnly: boolean;
  hasAuthToken: boolean;
  allowedTools?: string[];
  lastSyncAt?: number;
  lastError?: string;
  toolCount: number;
  resourceCount: number;
  tools?: ExternalMCPRemoteTool[];
  resources?: ExternalMCPRemoteResource[];
};

export type ExternalMCPServerInput = {
  id: string;
  name: string;
  endpoint: string;
  allowLocalhost?: boolean;
  enabled?: boolean;
  readOnly?: boolean;
  authToken?: string;
  clearAuthToken?: boolean;
  allowedTools?: string[];
};

export type ExternalMCPCallResult = {
  serverId: string;
  tool: string;
  isError: boolean;
  untrusted: boolean;
  content: Record<string, unknown>[];
};

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export function parseAllowedToolsInput(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(",")) {
    const trimmed = part.trim();
    if (!trimmed || trimmed.length > 64 || seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
    if (out.length >= 64) break;
  }
  return out;
}

export function formatDecisionAnswerSummary(ans: DecisionAnswer): string {
  const confPct = Math.round(
    Math.max(0, Math.min(1, Number(ans.confidence || 0))) * 100,
  );
  if (ans.type === "noul" && typeof ans.noul === "number") {
    return `${ans.noul.toFixed(2)} (${confPct}%)`;
  }
  if (ans.type === "choice" && ans.choice) {
    return `${ans.choice} (${confPct}%)`;
  }
  if (ans.type === "score" && typeof ans.score === "number") {
    const label = ans.legend ? ` ${ans.legend}` : "";
    return `${ans.score}${label} (${confPct}%)`;
  }
  return `${ans.type} (${confPct}%)`;
}

export function getDecisionConfig(core: CoreRequestOnly) {
  return core.request<DecisionStatus>("decision/config");
}

export function saveDecisionConfig(
  core: CoreRequestOnly,
  update: DecisionConfigUpdate,
) {
  return core.request<DecisionStatus>("decision/config", "PUT", update);
}

export function evaluateDecision(
  core: CoreRequestOnly,
  req: DecisionEvaluateRequest,
) {
  return core.request<DecisionEvaluateResponse>(
    "decision/evaluate",
    "POST",
    req,
  );
}

export function listExternalMCPClients(core: CoreRequestOnly) {
  return core.request<{ clients: ExternalMCPServer[]; count: number }>(
    "mcp/clients",
  );
}

export function saveExternalMCPClient(
  core: CoreRequestOnly,
  input: ExternalMCPServerInput,
) {
  return core.request<ExternalMCPServer>("mcp/clients", "PUT", input);
}

export function deleteExternalMCPClient(core: CoreRequestOnly, id: string) {
  return core.request<{ deleted: boolean; id: string }>(
    `mcp/clients/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export function syncExternalMCPClient(core: CoreRequestOnly, id: string) {
  return core.request<ExternalMCPServer>(
    `mcp/clients/${encodeURIComponent(id)}/sync`,
    "POST",
  );
}

export function callExternalMCPTool(
  core: CoreRequestOnly,
  id: string,
  tool: string,
  args: Record<string, unknown> = {},
) {
  return core.request<ExternalMCPCallResult>(
    `mcp/clients/${encodeURIComponent(id)}/call`,
    "POST",
    { tool, arguments: args },
  );
}
