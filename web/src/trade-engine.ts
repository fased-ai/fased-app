import type { AgentV2CoreClient } from "./agents-v2";

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export type TradeMode = "manual" | "semi_auto" | "bot" | "ai_agent";

export type TradeStrategy =
  "market" | "limit" | "bracket" | "trailing_atr" | "grid" | "dca" | "twap";

export type TradeSide = "buy" | "sell";

export interface TradeOrderRequest {
  venue: string;
  symbol: string;
  interval?: string;
  mode?: TradeMode;
  strategy?: TradeStrategy;
  side: TradeSide;
  quantity: number;
  accountId?: string;
  executionMode?: "paper" | "live";
  entryPrice?: number;
  stopPrice?: number;
  targetPrice?: number;
  atrMultiplier?: number;
  gridLower?: number;
  gridUpper?: number;
  gridLevels?: number;
  slices?: number;
  intervalSeconds?: number;
  agentNote?: string;
}

export interface TradeOrderPlan {
  id: string;
  venue: string;
  symbol: string;
  interval: string;
  mode: TradeMode;
  strategy: TradeStrategy;
  side: TradeSide;
  status: string;
  simulated: boolean;
  accountId?: string;
  executionMode?: string;
  source?: string;
  botId?: string;
  agentId?: string;
  venueOrderId?: string;
  txSignature?: string;
  quantity: number;
  filledQty: number;
  avgFillPrice?: number;
  entryPrice?: number;
  stopPrice?: number;
  targetPrice?: number;
  trailingStopPrice?: number;
  atrMultiplier?: number;
  gridLower?: number;
  gridUpper?: number;
  gridLevels?: number;
  gridFills?: number;
  slices?: number;
  slicesFilled?: number;
  intervalSeconds?: number;
  markPrice?: number;
  unrealizedPnlUsd: number;
  realizedPnlUsd: number;
  agentNote?: string;
  lastError?: string;
  createdAt: number;
  updatedAt: number;
  lastEvaluatedAt?: number;
}

export interface TradeRiskPolicy {
  maxOrderNotionalUsd: number;
  maxDailyLossUsd: number;
  maxOpenPlans: number;
  dailyLossUsd: number;
  dailyLossDay?: string;
}

export interface TradeKillState {
  active: boolean;
  reason?: string;
  updatedBy?: string;
  updatedAt: number;
}

export interface TradeSnapshot {
  executionProfile: string;
  simulated: boolean;
  killSwitchActive: boolean;
  killReason?: string;
  policy: TradeRiskPolicy;
  activeCount: number;
  plans: TradeOrderPlan[];
}

export interface TradeEvaluateSummary {
  evaluated: number;
  updated: number;
  triggered: number;
  completed: number;
  killed: number;
  halted?: boolean;
}

export async function getTradeState(
  core: CoreRequestOnly,
): Promise<TradeSnapshot> {
  return core.request<TradeSnapshot>("trade/state");
}

export async function submitTradeOrder(
  core: CoreRequestOnly,
  req: TradeOrderRequest,
  stepUpToken?: string,
): Promise<TradeOrderPlan> {
  return core.request<TradeOrderPlan>(
    "trade/orders",
    "POST",
    req,
    undefined,
    stepUpToken ? { "X-Fased-Step-Up": stepUpToken } : undefined,
  );
}

export async function cancelTradeOrder(
  core: CoreRequestOnly,
  id: string,
): Promise<TradeOrderPlan> {
  return core.request<TradeOrderPlan>(
    `trade/orders/${encodeURIComponent(id)}/cancel`,
    "POST",
    {},
  );
}

export async function evaluateTradeOrders(
  core: CoreRequestOnly,
): Promise<TradeEvaluateSummary> {
  return core.request<TradeEvaluateSummary>("trade/evaluate", "POST", {});
}

export async function updateTradePolicy(
  core: CoreRequestOnly,
  policy: Partial<TradeRiskPolicy>,
): Promise<TradeRiskPolicy> {
  return core.request<TradeRiskPolicy>("trade/policy", "PUT", policy);
}

export async function setGlobalKillSwitch(
  core: CoreRequestOnly,
  active: boolean,
  reason = "",
): Promise<TradeKillState> {
  return core.request<TradeKillState>("trade/kill", "POST", {
    active,
    reason,
  });
}

export interface TradeAccountOption {
  id: string;
  label: string;
  kind: "paper" | "solana" | "exchange" | string;
  executionMode: "paper" | "live";
  address?: string;
  venue?: string;
  role?: string;
  custody?: string;
  cluster?: string;
}

export interface TradeBot {
  id: string;
  name: string;
  kind: "fasedscript" | "ai_agent";
  accountId: string;
  executionMode: "paper" | "live";
  venue: string;
  symbol: string;
  interval: string;
  scriptId?: string;
  agentId?: string;
  maxNotionalUsd: number;
  status: "running" | "paused" | "stopped";
  lastSignal?: string;
  lastError?: string;
  totalOrders: number;
  lastTickAt?: number;
  createdAt: number;
}

export async function getTradeAccounts(
  core: CoreRequestOnly,
): Promise<TradeAccountOption[]> {
  try {
    const res = await core.request<{ accounts?: TradeAccountOption[] }>(
      "trade/accounts",
    );
    return res.accounts ?? [];
  } catch {
    return [
      { id: "paper", label: "Paper", kind: "paper", executionMode: "paper" },
    ];
  }
}

export async function listTradeBots(
  core: CoreRequestOnly,
): Promise<TradeBot[]> {
  try {
    const res = await core.request<{ bots?: TradeBot[] }>("trade/bots");
    return res.bots ?? [];
  } catch {
    return [];
  }
}

export async function createTradeBot(
  core: CoreRequestOnly,
  req: {
    name: string;
    kind: "fasedscript" | "ai_agent";
    accountId: string;
    venue: string;
    symbol: string;
    interval: string;
    scriptId?: string;
    agentId?: string;
    maxNotionalUsd: number;
  },
): Promise<TradeBot> {
  return core.request<TradeBot>("trade/bots", "POST", req);
}

export async function setTradeBotStatus(
  core: CoreRequestOnly,
  id: string,
  action: "start" | "pause" | "stop",
): Promise<TradeBot> {
  return core.request<TradeBot>(
    `trade/bots/${encodeURIComponent(id)}/${action}`,
    "POST",
    {},
  );
}

export async function deleteTradeBot(
  core: CoreRequestOnly,
  id: string,
): Promise<void> {
  await core.request(`trade/bots/${encodeURIComponent(id)}`, "DELETE");
}
