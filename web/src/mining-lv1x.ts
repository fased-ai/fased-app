import type { AgentV2CoreClient } from "./agents-v2";
import { listWalletsV2, requestStepUpToken, type WalletV2 } from "./wallets-v2";

type CoreReq = Pick<AgentV2CoreClient, "request">;

export interface StrategyPreset {
  id: string;
  name: string;
  description: string;
  mode: string;
  bucketsBps: number[];
  curve: number[];
}

export interface RoundBucket {
  index: number;
  code: string;
  label: string;
  poolSol: number;
  crowdShare: number;
  modelProb: number;
  evMultiple: number;
}

export interface RoundTelemetry {
  cluster: string;
  programId: string;
  ledgerId: string;
  roundSlot: number;
  phase: string;
  endsAt: number;
  remainingSec: number;
  openPriceUsd: number;
  markPriceUsd: number;
  moveBps: number;
  winningRange?: number;
  innerBps: number;
  outerBps: number;
  totalPoolSol: number;
  minerCount: number;
  btcVaultSat?: number;
  buckets: RoundBucket[];
  source: string;
  bpfVerified: boolean;
  deployedSha256: string;
}

export interface StreakEntry {
  roundSlot: number;
  winningRange: number;
  code: string;
  moveBps: number;
  poolSol: number;
  userStaked?: boolean;
  userWon?: boolean;
  satReward?: number;
  settledAt: number;
}

export type MiningMode = "manual" | "semi_auto" | "auto";

export interface SessionRecord {
  id: string;
  walletId: string;
  walletHandle: string;
  cluster: string;
  strategyId: string;
  mode: MiningMode;
  status: string;
  perRoundSol: number;
  totalCommittedSol: number;
  totalClaimedSol: number;
  totalClaimedSat: number;
  roundsPlayed: number;
  autoClaim: boolean;
  stopOnReversal: boolean;
  curve: number[];
  sessionRef?: number;
  lastRoundSlot?: number;
  lastTxSignature?: string;
  lastDecision?: string;
  createdAt: number;
  updatedAt: number;
}

export interface CommitPreview {
  cluster: string;
  roundSlot: number;
  amountSol: number;
  amountLamports: string;
  dominantBucket: number;
  dominantCode: string;
  bucketWeightsBps: number[];
  expectedEvMultiple: number;
  mainnetBlocked: boolean;
  mainnetReason?: string;
}

export interface MiningStatus {
  cluster: string;
  mainnetEnabled: boolean;
  mainnetGateNote: string;
  round: RoundTelemetry;
  streak: StreakEntry[];
  presets: StrategyPreset[];
  sessions: SessionRecord[];
}

export interface CommitRequest {
  walletId: string;
  cluster?: string;
  amountSol: number;
  selection?: number;
  strategyId?: string;
  curve?: number[];
  shardId?: number;
  autonomous?: boolean;
  taskId?: string;
  dryRunPreview?: boolean;
}

export interface ClaimRequest {
  walletId: string;
  roundSlot: number;
  shardId?: number;
  page?: number;
}

export interface MiningPolicyInput {
  cluster: string;
  programId?: string;
  ledgerId?: string;
  expectedElfSha256?: string;
  maxSolPerRoundLamports: string;
  maxDailySolLamports: string;
  allowedStrategies: string[];
  autoClaim: boolean;
  allowMainnet?: boolean;
}

function stepUpHeaders(token?: string): Record<string, string> | undefined {
  return token ? { "X-Fased-Step-Up": token } : undefined;
}

export async function fetchMiningStatus(
  core: CoreReq,
  cluster = "devnet",
): Promise<MiningStatus> {
  return core.request<MiningStatus>(
    `mining/status?cluster=${encodeURIComponent(cluster)}`,
  );
}

export async function fetchMiningRound(
  core: CoreReq,
  cluster = "devnet",
): Promise<RoundTelemetry> {
  return core.request<RoundTelemetry>(
    `mining/round?cluster=${encodeURIComponent(cluster)}`,
  );
}

export async function fetchMiningHistory(
  core: CoreReq,
  limit = 24,
): Promise<StreakEntry[]> {
  const res = await core.request<{ streak: StreakEntry[] }>(
    `mining/history?limit=${limit}`,
  );
  return res.streak ?? [];
}

export async function previewMiningCommit(
  core: CoreReq,
  req: CommitRequest,
): Promise<CommitPreview> {
  return core.request<CommitPreview>("mining/preview", "POST", req);
}

export async function submitMiningCommit(
  core: CoreReq,
  req: CommitRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/commit",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningClaim(
  core: CoreReq,
  req: ClaimRequest,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request("mining/claim", "POST", req);
}

export async function listMiningSessions(
  core: CoreReq,
): Promise<SessionRecord[]> {
  const res = await core.request<{ sessions: SessionRecord[] }>(
    "mining/sessions",
  );
  return res.sessions ?? [];
}

export async function createMiningSession(
  core: CoreReq,
  session: Partial<SessionRecord>,
  stepUpToken?: string,
): Promise<SessionRecord> {
  const res = await core.request<{ session: SessionRecord }>(
    "mining/sessions",
    "POST",
    session,
    undefined,
    stepUpHeaders(stepUpToken),
  );
  return res.session;
}

export async function stopMiningSession(
  core: CoreReq,
  id: string,
): Promise<SessionRecord> {
  const res = await core.request<{ session: SessionRecord }>(
    `mining/sessions/${encodeURIComponent(id)}/stop`,
    "POST",
    {},
  );
  return res.session;
}

export async function closeMiningSession(
  core: CoreReq,
  id: string,
): Promise<SessionRecord> {
  const res = await core.request<{ session: SessionRecord }>(
    `mining/sessions/${encodeURIComponent(id)}/close`,
    "POST",
    {},
  );
  return res.session;
}

export async function fetchMiningPolicy(
  core: CoreReq,
  walletId: string,
): Promise<WalletV2> {
  const res = await core.request<{ wallet: WalletV2 }>(
    `mining/policy?walletId=${encodeURIComponent(walletId)}`,
  );
  return res.wallet;
}

export async function updateMiningPolicy(
  core: CoreReq,
  input: {
    walletId: string;
    approvalMode?: string;
    autoApproveMaxUsd?: number;
    maxPerTxLamports?: string;
    maxDailyLamports?: string;
    mining: MiningPolicyInput;
    confirmLoosen?: boolean;
    expectedPolicyHash?: string;
  },
  stepUpToken?: string,
): Promise<WalletV2> {
  const res = await core.request<{ wallet: WalletV2 }>(
    "mining/policy",
    "PUT",
    input,
    undefined,
    stepUpHeaders(stepUpToken),
  );
  return res.wallet;
}

export async function listRoleMiningWallets(
  core: CoreReq,
): Promise<WalletV2[]> {
  const res = await listWalletsV2(core);
  return (res.wallets ?? []).filter((w) => w.role === "mining");
}

export async function requestMiningStepUpToken(
  core: CoreReq,
  password: string,
  action: "mining-commit" | "mining-session" | "mining-policy",
  walletId: string,
): Promise<string> {
  const res = await requestStepUpToken(core, password, action, walletId || "*");
  return res.stepUpToken;
}

export const DEFAULT_PRESETS: StrategyPreset[] = [
  {
    id: "wide",
    name: "Wide",
    description: "Uniform 20% across A..E",
    mode: "neutral",
    bucketsBps: [2000, 2000, 2000, 2000, 2000],
    curve: [],
  },
  {
    id: "center",
    name: "Center",
    description: "Range-bound mean reversion",
    mode: "neutral",
    bucketsBps: [500, 1500, 6000, 1500, 500],
    curve: [],
  },
  {
    id: "trend-up",
    name: "Trend up",
    description: "Bullish breakout bias",
    mode: "directional",
    bucketsBps: [500, 1000, 1500, 4500, 2500],
    curve: [],
  },
  {
    id: "trend-down",
    name: "Trend down",
    description: "Bearish breakdown bias",
    mode: "directional",
    bucketsBps: [2500, 4500, 1500, 1000, 500],
    curve: [],
  },
  {
    id: "tails",
    name: "Tails",
    description: "Volatility breakout wings",
    mode: "defensive",
    bucketsBps: [4000, 1000, 0, 1000, 4000],
    curve: [],
  },
  {
    id: "custom",
    name: "Custom",
    description: "Direct pointer curve edit",
    mode: "neutral",
    bucketsBps: [2000, 2000, 2000, 2000, 2000],
    curve: [],
  },
];
