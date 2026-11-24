import type { AgentV2CoreClient } from "./agents-v2";

export type ScriptInputSpec = {
  name: string;
  default: number;
  min: number;
  max: number;
  step: number;
};

export type LabScript = {
  id: string;
  name: string;
  category: string;
  description?: string;
  source: string;
  builtIn: boolean;
  inputs?: ScriptInputSpec[];
  createdAt: number;
  updatedAt: number;
};

export type LabScriptInput = {
  id: string;
  name: string;
  category?: string;
  description?: string;
  source: string;
};

export type BacktestConfig = {
  venue?: string;
  symbol?: string;
  interval?: string;
  limit?: number;
  initialCapital?: number;
  feeBps?: number;
  slippageBps?: number;
  params?: Record<string, number>;
  seed?: number;
  permutations?: number;
  wfPermutations?: number;
  holdoutPct?: number;
};

export type BacktestMetrics = {
  totalReturnPct: number;
  annualizedSharpe: number;
  profitFactor: number;
  winRatePct: number;
  maxDrawdownPct: number;
  tradeCount: number;
  avgTradePct: number;
  totalFeesUsd: number;
  finalEquity: number;
  expectancyUsd: number;
};

export type EquityPoint = {
  time: number;
  equity: number;
  drawdownPct: number;
};

export type TradeFill = {
  side: string;
  entryTime: number;
  exitTime: number;
  entryPrice: number;
  exitPrice: number;
  pnlPct: number;
  pnlUsd: number;
  feeUsd: number;
  exitReason: string;
};

export type BacktestResult = {
  scriptId?: string;
  config: BacktestConfig;
  metrics: BacktestMetrics;
  trades: TradeFill[];
  equity: EquityPoint[];
  params: Record<string, number>;
  barCount: number;
};

export type HistogramBucket = {
  from: number;
  to: number;
  count: number;
};

export type PlateauCell = {
  params: Record<string, number>;
  sharpe: number;
  returnPct: number;
  trades: number;
  passed: boolean;
};

export type WalkForwardFold = {
  fold: number;
  trainBars: number;
  testBars: number;
  chosenParams: Record<string, number>;
  trainSharpe: number;
  oosSharpe: number;
  oosReturnPct: number;
  oosTrades: number;
};

export type FourGateReport = {
  grade: string;
  gatesPassed: number;
  promotionReady: boolean;
  executionPolicy: string;
  warnings?: string[];
  gate1: {
    passed: boolean;
    testedCells: number;
    passingCells: number;
    plateauRatio: number;
    bestSharpe: number;
    bestParams: Record<string, number>;
    cells: PlateauCell[];
  };
  gate2: {
    passed: boolean;
    iterations: number;
    realSharpe: number;
    meanPerm: number;
    p95Perm: number;
    pValue: number;
    beatCount: number;
    histogram: HistogramBucket[];
  };
  gate3: {
    passed: boolean;
    folds: WalkForwardFold[];
    oosSharpe: number;
    oosReturnPct: number;
    holdoutBars: number;
    holdoutSharpe: number;
    holdoutReturnPct: number;
    holdoutTrades: number;
    frozenParams: Record<string, number>;
  };
  gate4: {
    passed: boolean;
    iterations: number;
    realSharpe: number;
    meanPerm: number;
    p90Perm: number;
    pValue: number;
    beatCount: number;
    histogram: HistogramBucket[];
  };
  baselineMetrics: BacktestMetrics;
  equity: EquityPoint[];
  trades: TradeFill[];
};

export type ValidationRunRecord = {
  id: string;
  scriptId: string;
  scriptName: string;
  config: BacktestConfig;
  report: FourGateReport;
  durationMs: number;
  createdAt: number;
};

export type ValidationRunSummary = {
  id: string;
  scriptId: string;
  scriptName: string;
  venue: string;
  symbol: string;
  interval: string;
  grade: string;
  gatesPassed: number;
  promotionReady: boolean;
  totalReturnPct: number;
  sharpe: number;
  durationMs: number;
  createdAt: number;
};

export type DeskMode = "shadow" | "paper";

export type DeskSeatPolicy = {
  id: string;
  title: string;
  role: string;
  allowedTools: string[];
  canExecutePaper: boolean;
  canModifyLimits: boolean;
  canProposeTrades: boolean;
};

export type DeskRiskLimits = {
  mode: DeskMode;
  initialCapitalUsd: number;
  riskPerTradePct: number;
  maxPositionPct: number;
  maxOpenPositions: number;
  maxDailyLossPct: number;
  maxConsecutiveLosses: number;
  cooldownHours: number;
  maxSlippageBps: number;
  feeBps: number;
  minChartScore: number;
  minRiskReward: number;
  staleDataMaxAgeSec: number;
  requirePmTap: boolean;
  requireFourGatePass: boolean;
};

export type DeskControlState = {
  paused: boolean;
  killSwitchActive: boolean;
  externalKillActive: boolean;
  effectiveHalted: boolean;
  reason?: string;
  updatedBy?: string;
  updatedAt: number;
};

export type PaperAccount = {
  initialCapitalUsd: number;
  cashUsd: number;
  equityUsd: number;
  realizedPnlUsd: number;
  unrealizedPnlUsd: number;
  dailyPnlUsd: number;
  dailyLossPct: number;
  totalFeesUsd: number;
  consecutiveLosses: number;
  openPositions: number;
  pendingTickets: number;
  updatedAt: number;
};

export type SeatArtifact = {
  seat: string;
  verdict: string;
  summary: string;
  score?: number;
  evidence?: string[];
  timestamp: number;
};

export type OrderTicket = {
  id: string;
  cycleId: string;
  venue: string;
  symbol: string;
  interval: string;
  scriptId: string;
  side: string;
  mode: DeskMode | string;
  entryPrice: number;
  stopPrice: number;
  targetPrice: number;
  quantity: number;
  notionalUsd: number;
  riskUsd: number;
  riskReward: number;
  expectedAvgR: number;
  chartScore: number;
  status: string;
  reason?: string;
  createdAt: number;
  updatedAt: number;
};

export type PaperPosition = {
  id: string;
  ticketId: string;
  venue: string;
  symbol: string;
  scriptId: string;
  side: string;
  quantity: number;
  entryPrice: number;
  markPrice: number;
  stopPrice: number;
  targetPrice: number;
  initialRiskUsd: number;
  entryFeeUsd: number;
  exitPrice?: number;
  exitFeeUsd?: number;
  unrealizedPnlUsd: number;
  realizedPnlUsd: number;
  pnlR: number;
  status: string;
  exitReason?: string;
  openedAt: number;
  closedAt?: number;
};

export type BenchEntry = {
  id: string;
  scopeKey: string;
  reason: string;
  losses: number;
  benchedUntil: number;
  createdAt: number;
};

export type JournalEntry = {
  id: string;
  cycleId: string;
  ticketId?: string;
  seat: string;
  venue: string;
  symbol: string;
  scriptId: string;
  side: string;
  mode: string;
  outcome: string;
  score: number;
  entryPrice: number;
  stopPrice: number;
  targetPrice: number;
  exitPrice?: number;
  quantity: number;
  riskUsd: number;
  pnlUsd: number;
  pnlR: number;
  expectedAvgR: number;
  riskCheck: string;
  skepticVerdict: string;
  reason?: string;
  lesson?: string;
  createdAt: number;
  updatedAt: number;
};

export type LessonRecord = {
  id: string;
  category: string;
  symbol?: string;
  rule: string;
  sourceTradeId?: string;
  active: boolean;
  createdAt: number;
  updatedAt: number;
};

export type LessonInput = {
  id?: string;
  category?: string;
  symbol?: string;
  rule: string;
  sourceTradeId?: string;
  active?: boolean;
};

export type DecayReport = {
  closedTrades: number;
  paperAvgR: number;
  backtestAvgR: number;
  winRatePct: number;
  decaying: boolean;
  status: "WARMING_UP" | "HEALTHY" | "DECAYING" | string;
  skepticKills: number;
  riskRejections: number;
  shadowCount: number;
  summary: string;
};

export type CycleRequest = {
  venue?: string;
  symbol?: string;
  interval?: string;
  scriptId?: string;
  mode?: DeskMode;
  limit?: number;
};

export type RiskEvaluation = {
  approved: boolean;
  violatedRules?: string[];
  computedQty: number;
  notionalUsd: number;
  riskUsd: number;
  riskReward: number;
  dailyLossPct: number;
  summary: string;
};

export type CycleResult = {
  id: string;
  venue: string;
  symbol: string;
  interval: string;
  scriptId: string;
  mode: string;
  finalStatus: string;
  artifacts: SeatArtifact[];
  ticket?: OrderTicket;
  position?: PaperPosition;
  riskCheck: RiskEvaluation;
  createdAt: number;
};

export type DeskSnapshot = {
  control: DeskControlState;
  limits: DeskRiskLimits;
  seats: DeskSeatPolicy[];
  account: PaperAccount;
  positions: PaperPosition[];
  tickets: OrderTicket[];
  bench: BenchEntry[];
  decay: DecayReport;
  lessonsCount: number;
  updatedAt: number;
};

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export function slugifyScriptId(name: string): string {
  const slug = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
  return slug || "custom-script";
}

export function formatCurrencyUSD(val: number): string {
  if (!Number.isFinite(val)) return "$0.00";
  const sign = val < 0 ? "-" : "";
  return `${sign}$${Math.abs(val).toLocaleString("en-US", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;
}

export function formatSignedPct(val: number): string {
  if (!Number.isFinite(val)) return "0.00%";
  const sign = val > 0 ? "+" : "";
  return `${sign}${val.toFixed(2)}%`;
}

export function formatRMultiple(val: number): string {
  if (!Number.isFinite(val)) return "0.00R";
  const sign = val > 0 ? "+" : "";
  return `${sign}${val.toFixed(2)}R`;
}

export function deskStatusLabel(
  ctrl?: Pick<
    DeskControlState,
    "killSwitchActive" | "externalKillActive" | "paused"
  > | null,
): "Kill switch" | "Paused" | "Active" {
  if (ctrl?.killSwitchActive || ctrl?.externalKillActive) return "Kill switch";
  if (ctrl?.paused) return "Paused";
  return "Active";
}

export function normalizeHistogramHeights(
  buckets: HistogramBucket[],
): number[] {
  if (!buckets.length) return [];
  const max = Math.max(...buckets.map((b) => Math.max(0, b.count)), 0);
  if (max <= 0) return buckets.map(() => 0);
  return buckets.map(
    (b) => Math.round((Math.max(0, b.count) / max) * 1000) / 10,
  );
}

export function buildEquityPolyline(
  points: EquityPoint[],
  width = 360,
  height = 84,
): string {
  if (!points.length) return "";
  const valid = points.filter((p) => Number.isFinite(p.equity));
  if (!valid.length) return "";
  if (valid.length === 1) {
    const midY = (height / 2).toFixed(1);
    return `0,${midY} ${width},${midY}`;
  }
  const vals = valid.map((p) => p.equity);
  const min = Math.min(...vals);
  const max = Math.max(...vals);
  const span = max - min;
  const pad = 6;
  const innerH = Math.max(1, height - pad * 2);
  return valid
    .map((p, idx) => {
      const x = ((idx / (valid.length - 1)) * width).toFixed(1);
      const norm = span > 0 ? (p.equity - min) / span : 0.5;
      const y = (height - pad - norm * innerH).toFixed(1);
      return `${x},${y}`;
    })
    .join(" ");
}

export function listLabScripts(core: CoreRequestOnly) {
  return core.request<{ scripts: LabScript[]; count: number }>("lab/scripts");
}

export function saveLabScript(core: CoreRequestOnly, input: LabScriptInput) {
  return core.request<LabScript>("lab/scripts", "PUT", input);
}

export function deleteLabScript(core: CoreRequestOnly, id: string) {
  return core.request<{ deleted: boolean; id: string }>(
    `lab/scripts/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export function runLabBacktest(
  core: CoreRequestOnly,
  payload: { scriptId?: string; source?: string; config: BacktestConfig },
) {
  return core.request<BacktestResult>("lab/backtest", "POST", payload);
}

export function runLabValidation(
  core: CoreRequestOnly,
  payload: { scriptId?: string; source?: string; config: BacktestConfig },
) {
  return core.request<ValidationRunRecord>("lab/validate", "POST", payload);
}

export function listLabRuns(core: CoreRequestOnly, limit = 15) {
  return core.request<{ runs: ValidationRunSummary[]; count: number }>(
    `lab/runs?limit=${encodeURIComponent(String(limit))}`,
  );
}

export function getLabRun(core: CoreRequestOnly, id: string) {
  return core.request<ValidationRunRecord>(
    `lab/runs/${encodeURIComponent(id)}`,
  );
}

export function getDeskState(core: CoreRequestOnly) {
  return core.request<DeskSnapshot>("desk/state");
}

export function updateDeskLimits(
  core: CoreRequestOnly,
  limits: DeskRiskLimits,
) {
  return core.request<DeskRiskLimits>("desk/limits", "PUT", limits);
}

export function setDeskControl(
  core: CoreRequestOnly,
  payload: {
    action: "pause" | "resume" | "kill" | "unkill" | "reset_paper";
    confirm?: string;
    reason?: string;
  },
) {
  return core.request<DeskControlState>("desk/control", "POST", payload);
}

export function runDeskCycle(core: CoreRequestOnly, req: CycleRequest) {
  return core.request<CycleResult>("desk/cycle", "POST", req);
}

export function listDeskCycles(core: CoreRequestOnly, limit = 20) {
  return core.request<{ cycles: CycleResult[]; count: number }>(
    `desk/cycles?limit=${encodeURIComponent(String(limit))}`,
  );
}

export function approveDeskTicket(core: CoreRequestOnly, id: string) {
  return core.request<PaperPosition>(
    `desk/tickets/${encodeURIComponent(id)}/approve`,
    "POST",
  );
}

export function rejectDeskTicket(
  core: CoreRequestOnly,
  id: string,
  reason?: string,
) {
  return core.request<OrderTicket>(
    `desk/tickets/${encodeURIComponent(id)}/reject`,
    "POST",
    reason ? { reason } : undefined,
  );
}

export function markDeskPositions(core: CoreRequestOnly) {
  return core.request<{ positions: PaperPosition[]; count: number }>(
    "desk/positions/mark",
    "POST",
  );
}

export function closeDeskPosition(
  core: CoreRequestOnly,
  id: string,
  payload?: { exitPrice?: number; reason?: string },
) {
  return core.request<PaperPosition>(
    `desk/positions/${encodeURIComponent(id)}/close`,
    "POST",
    payload,
  );
}

export function listDeskJournal(
  core: CoreRequestOnly,
  params: { symbol?: string; outcome?: string; limit?: number } = {},
) {
  const q = new URLSearchParams();
  if (params.symbol) q.set("symbol", params.symbol);
  if (params.outcome) q.set("outcome", params.outcome);
  q.set("limit", String(params.limit ?? 50));
  return core.request<{ journal: JournalEntry[]; count: number }>(
    `desk/journal?${q.toString()}`,
  );
}

export function listDeskLessons(core: CoreRequestOnly, limit = 50) {
  return core.request<{ lessons: LessonRecord[]; count: number }>(
    `desk/lessons?limit=${encodeURIComponent(String(limit))}`,
  );
}

export function saveDeskLesson(core: CoreRequestOnly, input: LessonInput) {
  return core.request<LessonRecord>("desk/lessons", "PUT", input);
}

export function deleteDeskLesson(core: CoreRequestOnly, id: string) {
  return core.request<{ deleted: boolean; id: string }>(
    `desk/lessons/${encodeURIComponent(id)}`,
    "DELETE",
  );
}
