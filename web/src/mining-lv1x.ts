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

export interface SavedStrategy {
  id: string;
  name: string;
  description: string;
  mode: string;
  bucketsBps: number[];
  curve: number[];
  source: string;
  createdAt: number;
  updatedAt: number;
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
  openPriceUsd?: number;
  closePriceUsd?: number;
  innerBps?: number;
  outerBps?: number;
  minerSat?: number;
  userStaked?: boolean;
  userWon?: boolean;
  satReward?: number;
  settledAt: number;
}

export interface CandleBar {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  roundSlot?: number;
  moveBps?: number;
  winningRange?: number;
}

export interface CandleResponse {
  interval: string;
  symbol: string;
  source: string;
  candles: CandleBar[];
}

export type MiningMode = "manual" | "semi_auto" | "auto";
export type ScheduleMode = "every" | "choose" | "cycle" | "random";

export interface SessionRecord {
  id: string;
  walletId: string;
  walletHandle: string;
  cluster: string;
  strategyId: string;
  mode: MiningMode;
  status: string;
  scheduleMode?: ScheduleMode;
  horizonLabel?: string;
  startMode?: "now" | "next" | "plus2";
  selectedSlots?: number[];
  roundsCount?: number;
  perRoundSol: number;
  depositSol?: number;
  rollingBudget?: boolean;
  skipRiskWarning?: string;
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

export interface ProtocolStats {
  cluster: string;
  programId: string;
  ledgerId: string;
  runtimeAddress: string;
  mintAddress: string;
  btcMintAddress: string;
  poolAddress?: string;
  stakingAddress: string;
  treasuryAddress: string;
  phase: number;
  phaseName: string;
  paused: boolean;
  bootstrapWinddown: boolean;
  mintedSat: number;
  maxSupplySat: number;
  btcTreasurySat: number;
  totalStakedSat: number;
  stakedPct: number;
  volume24hSol: number;
  lpSol: number;
  lpSolMin: number;
  lpSat: number;
  lpSatMin: number;
  boostMultiplier: number;
  boostBudgetSat: number;
  boostAwardedSat: number;
  tgeBps: number;
  vestingDurationSec: number;
  graduatedAt: number;
  roundIntervalSec: number;
  lpBurnedSol?: number;
  treasuryBtcSolPending?: number;
  treasuryLpSolPending?: number;
  feeSplit?: number[];
  source: string;
  observedAt: number;
}

export interface LeaderboardMiner {
  address: string;
  shortAddress: string;
  status: "winner" | "active" | "missed";
  rounds: number;
  wins: number;
  committedSol: number;
  returnedSol: number;
  netSol: number;
  earnedSat: number;
  earnedBtc: number;
  lastRoundSlot: number;
}

export interface LeaderboardRound {
  roundSlot: number;
  phase: string;
  winningRange?: number;
  code: string;
  moveBps: number;
  poolSol: number;
  minerCount: number;
  minerSat: number;
  crowdShares: number[];
  settledAt: number;
}

export interface LeaderboardResponse {
  cluster: string;
  window: string;
  volumeSol: number;
  roundsCount: number;
  minersCount: number;
  satAwarded: number;
  miners: LeaderboardMiner[];
  rounds: LeaderboardRound[];
  source: string;
  observedAt: number;
}

export interface ClaimableRoundItem {
  roundSlot: number;
  shardId: number;
  page: number;
  entryRow: number;
  nonce?: number;
  entriesMask: number;
  selection: number;
  selectionCode: string;
  solLamports: string;
  satAtoms: string;
  vestedSatAtoms: string;
  btcAtoms: string;
  solClaimed: boolean;
  solEscape?: boolean;
  revealed?: boolean;
  nonceClosed?: boolean;
  btcClaimed: boolean;
  stakedSol: number;
}

export interface EntryHistoryItem {
  id: string;
  walletId: string;
  cluster: string;
  roundSlot: number;
  kind: string;
  selection?: number;
  strategyId?: string;
  amountSol?: number;
  amountSat?: number;
  amountBtc?: number;
  txSignature?: string;
  status: string;
  createdAt: number;
}

export interface PortfolioResponse {
  cluster: string;
  walletId: string;
  walletPublicKey: string;
  solBalanceSol: number;
  satBalance: number;
  btcBalance: number;
  claimableSol: number;
  claimableSat: number;
  vestedSat: number;
  claimableBtc: number;
  vestingProgressPct: number;
  vestingEndsAt: number;
  claimItems: ClaimableRoundItem[];
  history: EntryHistoryItem[];
  sessions: SessionRecord[];
  source: string;
  observedAt: number;
}

export interface StakingOverviewResponse {
  cluster: string;
  stakingAddress: string;
  positionAddress?: string;
  phase: number;
  paused: boolean;
  minStakeSat: number;
  totalActiveSat: number;
  totalPendingSat: number;
  totalBtcDistributed: number;
  streamRemainingBtc: number;
  streamEndTs: number;
  userActiveSat: number;
  userPendingSat: number;
  pendingMatureAt: number;
  canActivate: boolean;
  claimableBtc: number;
  walletSatBalance: number;
  walletBtcBalance: number;
  sharePct: number;
  source: string;
  observedAt: number;
}

export interface SimulateRequest {
  bucketsBps: number[];
  curve?: number[];
  perRoundSol: number;
  limit?: number;
}

export interface SimulateOutcome {
  roundSlot: number;
  winningRange?: number;
  kind: "exact" | "near" | "miss" | "void";
  returnPct: number;
}

export interface SimulateResponse {
  rounds: number;
  exactHitPct: number;
  avgCreditPct: number;
  retainedPct: number;
  netSol: number;
  estSat: number;
  totalCommitted: number;
  outcomes: SimulateOutcome[];
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
  savedStrategies?: SavedStrategy[];
  sessions: SessionRecord[];
}

export interface CommitRequest {
  walletId: string;
  cluster?: string;
  amountSol: number;
  selection?: number;
  strategyId?: string;
  bucketWeightsBps?: number[];
  curve?: number[];
  shardId?: number;
  autonomous?: boolean;
  taskId?: string;
  dryRunPreview?: boolean;
}

export interface RevealRequest {
  walletId: string;
  cluster?: string;
  roundSlot: number;
  shardId?: number;
  page?: number;
  entryRow?: number;
  selection: number;
  saltHex: string;
}

export interface ClaimRequest {
  walletId: string;
  cluster?: string;
  asset?: "sol" | "sat" | "btc";
  roundSlot: number;
  shardId?: number;
  page?: number;
  entryRow?: number;
  entriesMask?: number;
  batch?: boolean;
  solEscape?: boolean;
  createAta?: boolean;
}

export interface CloseNonceRequest {
  walletId: string;
  cluster?: string;
  roundSlot: number;
  nonce?: number;
}

export interface ManifestStatus {
  pinned: boolean;
  keyId?: string;
  sequence: number;
  status?: string;
  compatible: boolean;
  compatibilityError?: string;
  issuedAt?: number;
  expiresAt?: number;
  verifiedAt?: number;
  lastSyncAt?: number;
  lastSyncError?: string;
  mainnetStatus?: string;
  mainnetProgramId?: string;
  devnetProgramId: string;
}

export interface SelfKeeperStatus {
  running: boolean;
  cluster: string;
  walletId?: string;
  walletPublicKey?: string;
  autoClaim: boolean;
  autoStakeWensat: boolean;
  allowFallbackCrank: boolean;
  nextRevealAt?: number;
  activeRoundCommits: number;
  pendingClaimsCount: number;
  lastSweepAt?: number;
  lastSweepSummary?: string;
  fallbackGasUsedLamports: string;
  manifestSequence: number;
  manifestStatus?: string;
  manifestVerifiedAt?: number;
  manifestCompatible: boolean;
  manifestError?: string;
  lastManifestSyncAt?: number;
  lastError?: string;
}

export interface StakeActionRequest {
  walletId: string;
  cluster?: string;
  amountSat?: number;
  createAta?: boolean;
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
  autoStakeWensat?: boolean;
  allowFallbackCrank?: boolean;
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

export async function fetchMiningCandles(
  core: CoreReq,
  interval = "10m",
  limit = 120,
): Promise<CandleResponse> {
  return core.request<CandleResponse>(
    `mining/candles?interval=${encodeURIComponent(interval)}&limit=${limit}`,
  );
}

export async function fetchMiningProtocolStats(
  core: CoreReq,
  cluster = "devnet",
): Promise<ProtocolStats> {
  return core.request<ProtocolStats>(
    `mining/protocol-stats?cluster=${encodeURIComponent(cluster)}`,
  );
}

export async function fetchMiningLeaderboard(
  core: CoreReq,
  cluster = "devnet",
  window = "24h",
): Promise<LeaderboardResponse> {
  return core.request<LeaderboardResponse>(
    `mining/leaderboard?cluster=${encodeURIComponent(cluster)}&window=${encodeURIComponent(window)}`,
  );
}

export async function fetchMiningPortfolio(
  core: CoreReq,
  cluster = "devnet",
  walletId = "",
): Promise<PortfolioResponse> {
  return core.request<PortfolioResponse>(
    `mining/portfolio?cluster=${encodeURIComponent(cluster)}&walletId=${encodeURIComponent(walletId)}`,
  );
}

export async function fetchMiningStaking(
  core: CoreReq,
  cluster = "devnet",
  walletId = "",
): Promise<StakingOverviewResponse> {
  return core.request<StakingOverviewResponse>(
    `mining/staking?cluster=${encodeURIComponent(cluster)}&walletId=${encodeURIComponent(walletId)}`,
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

export async function listSavedMiningStrategies(
  core: CoreReq,
): Promise<{ strategies: SavedStrategy[]; presets: StrategyPreset[] }> {
  return core.request("mining/strategies");
}

export async function saveMiningStrategy(
  core: CoreReq,
  input: Partial<SavedStrategy>,
): Promise<SavedStrategy> {
  const res = await core.request<{ strategy: SavedStrategy }>(
    "mining/strategies",
    "POST",
    input,
  );
  return res.strategy;
}

export async function deleteMiningStrategy(
  core: CoreReq,
  id: string,
): Promise<void> {
  await core.request(`mining/strategies/${encodeURIComponent(id)}`, "DELETE");
}

export async function simulateMiningStrategy(
  core: CoreReq,
  req: SimulateRequest,
): Promise<SimulateResponse> {
  return core.request<SimulateResponse>(
    "mining/strategies/simulate",
    "POST",
    req,
  );
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

export async function submitMiningReveal(
  core: CoreReq,
  req: RevealRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/reveal",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningClaim(
  core: CoreReq,
  req: ClaimRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/claim",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningClaimAll(
  core: CoreReq,
  walletId: string,
  cluster = "devnet",
  assets: string[] = ["sol", "sat", "btc"],
  stepUpToken?: string,
): Promise<{ ledgers: unknown[]; count: number }> {
  return core.request(
    "mining/claim-all",
    "POST",
    {
      walletId,
      cluster,
      assets,
    },
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningStake(
  core: CoreReq,
  req: StakeActionRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/stake",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningActivateStake(
  core: CoreReq,
  req: StakeActionRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/activate-stake",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningUnstake(
  core: CoreReq,
  req: StakeActionRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/unstake",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function submitMiningClaimStaking(
  core: CoreReq,
  req: StakeActionRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/claim-staking",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
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

export async function depositMiningSession(
  core: CoreReq,
  id: string,
  depositSol: number,
): Promise<SessionRecord> {
  const res = await core.request<{ session: SessionRecord }>(
    `mining/sessions/${encodeURIComponent(id)}/deposit`,
    "POST",
    { depositSol },
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
  action: "policy" | "execute" | "mining-session" | "wallet",
  walletId: string,
  bindingDigest = "",
): Promise<string> {
  const res = await requestStepUpToken(
    core,
    password,
    action,
    walletId || "*",
    bindingDigest,
  );
  return res.stepUpToken;
}

export async function fetchMiningManifest(
  core: CoreReq,
): Promise<ManifestStatus> {
  return core.request<ManifestStatus>("mining/manifest");
}

export async function syncMiningManifest(
  core: CoreReq,
): Promise<{ manifest: ManifestStatus; synced: boolean; error: string }> {
  return core.request("mining/manifest/sync", "POST", {});
}

export async function fetchSelfKeeperStatus(
  core: CoreReq,
): Promise<SelfKeeperStatus> {
  return core.request<SelfKeeperStatus>("mining/self-keeper");
}

export async function triggerSelfKeeperSweep(
  core: CoreReq,
): Promise<SelfKeeperStatus> {
  return core.request<SelfKeeperStatus>("mining/self-keeper/sweep", "POST", {});
}

export async function submitMiningCloseNonce(
  core: CoreReq,
  req: CloseNonceRequest,
  stepUpToken?: string,
): Promise<{ review: unknown; ledger: unknown }> {
  return core.request(
    "mining/close-nonce",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
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
