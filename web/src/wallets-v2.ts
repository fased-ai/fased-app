import type { AgentV2CoreClient } from "./agents-v2";

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export type CustodyMode = "native_signer" | "hardware_standard" | "watch_only";
export type WalletRole = "vault" | "trading" | "agent" | "mining" | "watch";
export type ApprovalMode = "manual_review" | "semi_auto" | "bounded_auto";

export interface AssetCap {
  asset: string;
  mint?: string;
  symbol?: string;
  decimals: number;
  maxPerTxRaw: string;
  maxDailyRaw: string;
}

export interface SkillGrant {
  skillId: string;
  actions: string[];
  allowedMints?: string[];
  maxPerTxUsd?: number;
  maxSlippageBps?: number;
  scheduledAllowed: boolean;
}

export interface WalletPolicy {
  version: number;
  policyHash: string;
  role: WalletRole;
  frozen: boolean;
  freezeReason?: string;
  approvalMode: ApprovalMode;
  autoApproveMaxUsd?: number;
  operations: string[];
  programs: string[];
  destinations: string[];
  solCap: AssetCap;
  tokenCaps: AssetCap[];
  maxSlippageBps: number;
  dailyLossCapUsd?: number;
  expiresAt?: number;
}

export interface WalletConnections {
  agentIds: string[];
  defaultAgentWallet: boolean;
  allowTradeManual: boolean;
  allowTradeBots: boolean;
  allowedVenues: string[];
  skillGrants: SkillGrant[];
  sweepToWalletId?: string;
}

export interface WalletRPCStatus {
  configured: boolean;
  host?: string;
  cluster?: string;
  genesisHash?: string;
  witnessHost?: string;
  witnessStatus?: string;
  fallbackHost?: string;
  latencyMs?: number;
  version: number;
}

export interface WalletV2 {
  id: string;
  handle: string;
  name: string;
  custody: CustodyMode;
  role: WalletRole;
  publicKey: string;
  policy: WalletPolicy;
  policyAcknowledged: boolean;
  connections: WalletConnections;
  rpc: WalletRPCStatus;
  dailySpentLamports: string;
  dailyReservedLamports: string;
  dayUtc?: string;
  createdAt: number;
  updatedAt: number;
}

export interface WalletBalanceResult {
  walletId: string;
  publicKey: string;
  cluster: string;
  rpcHost: string;
  lamports: string;
  sol: string;
  latencyMs: number;
}

export interface WalletReview {
  id: string;
  walletId: string;
  role: WalletRole;
  custody: CustodyMode;
  operation: string;
  programs: string[];
  destination: string;
  mint?: string;
  symbol: string;
  amountRaw: string;
  amountHuman: string;
  estimatedFeeLamports: string;
  policyVersion: number;
  policyHash: string;
  txDigest: string;
  unsignedTxBase64: string;
  unsignedMsgBase64: string;
  simulationOk: boolean;
  simulationError?: string;
  expiresAt: number;
  status: string;
  createdAt: number;
}

export interface LedgerEntry {
  id: string;
  reviewId: string;
  walletId: string;
  operation: string;
  destination: string;
  asset: string;
  amountRaw: string;
  state: "reserved" | "broadcast" | "confirmed" | "failed" | "unknown";
  txSignature?: string;
  policyHash: string;
  error?: string;
  createdAt: number;
  updatedAt: number;
}

export interface AuditEntry {
  id: string;
  walletId: string;
  action: string;
  actor: string;
  summary: string;
  policyHash?: string;
  timestamp: number;
}

export interface WalletsV2ListResponse {
  wallets: WalletV2[];
  count: number;
  socketReady?: boolean;
  signerPresent?: boolean;
}

export async function listWalletsV2(
  core: CoreRequestOnly,
): Promise<WalletsV2ListResponse> {
  return core.request<WalletsV2ListResponse>("wallets");
}

export async function createNativeWallet(
  core: CoreRequestOnly,
  req: { name: string; role: WalletRole; rpcUrl: string },
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>("wallets/create", "POST", req);
}

export async function attachExternalWallet(
  core: CoreRequestOnly,
  req: {
    custody: "hardware_standard" | "watch_only";
    name: string;
    role: WalletRole;
    publicKey: string;
    rpcUrl: string;
  },
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>("wallets/attach", "POST", req);
}

export async function updateWalletPolicy(
  core: CoreRequestOnly,
  walletId: string,
  req: {
    policy: WalletPolicy;
    confirmLoosen?: boolean;
    expectedPolicyHash?: string;
  },
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/policy`,
    "PUT",
    req,
  );
}

export async function setWalletFreeze(
  core: CoreRequestOnly,
  walletId: string,
  frozen: boolean,
  reason = "",
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/freeze`,
    "POST",
    { frozen, reason },
  );
}

export async function updateWalletConnections(
  core: CoreRequestOnly,
  walletId: string,
  connections: WalletConnections,
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/connections`,
    "PUT",
    connections,
  );
}

export async function updateWalletRPC(
  core: CoreRequestOnly,
  walletId: string,
  primaryRpcUrl: string,
  fallbackRpcUrl = "",
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/rpc`,
    "PUT",
    { primaryRpcUrl, fallbackRpcUrl },
  );
}

export async function checkWalletBalanceV2(
  core: CoreRequestOnly,
  walletId: string,
): Promise<WalletBalanceResult> {
  return core.request<WalletBalanceResult>(
    `wallets/${encodeURIComponent(walletId)}/balance`,
    "POST",
    {},
  );
}

export async function exportEncryptedBackupV2(
  core: CoreRequestOnly,
  walletId: string,
  passphrase: string,
): Promise<{ archive: unknown; fingerprint: string; filename: string }> {
  return core.request<{
    archive: unknown;
    fingerprint: string;
    filename: string;
  }>(`wallets/${encodeURIComponent(walletId)}/backup`, "POST", { passphrase });
}

export async function restoreEncryptedBackupV2(
  core: CoreRequestOnly,
  req: {
    archive: unknown;
    passphrase: string;
    name?: string;
    role?: WalletRole;
    rpcUrl: string;
  },
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>("wallets/restore", "POST", req);
}

export async function listWalletActivity(
  core: CoreRequestOnly,
  walletId: string,
): Promise<{
  reviews: WalletReview[];
  ledger: LedgerEntry[];
  audit: AuditEntry[];
}> {
  return core.request(`wallets/${encodeURIComponent(walletId)}/activity`);
}

export async function prepareWalletReview(
  core: CoreRequestOnly,
  walletId: string,
  req: {
    operation: string;
    destination: string;
    amountHuman: string;
    isBotOrAuto?: boolean;
  },
): Promise<{ review: WalletReview }> {
  return core.request<{ review: WalletReview }>(
    `wallets/${encodeURIComponent(walletId)}/reviews`,
    "POST",
    req,
  );
}

export async function executeNativeWalletReview(
  core: CoreRequestOnly,
  walletId: string,
  reviewId: string,
): Promise<{ entry: LedgerEntry; error?: string }> {
  return core.request(
    `wallets/${encodeURIComponent(walletId)}/reviews/${encodeURIComponent(reviewId)}/execute`,
    "POST",
    {},
  );
}

export async function submitHardwareSignedReview(
  core: CoreRequestOnly,
  walletId: string,
  reviewId: string,
  signedTxBase64: string,
): Promise<{ entry: LedgerEntry; error?: string }> {
  return core.request(
    `wallets/${encodeURIComponent(walletId)}/reviews/${encodeURIComponent(reviewId)}/submit-signed`,
    "POST",
    { signedTxBase64 },
  );
}

export async function reconcileWalletLedger(
  core: CoreRequestOnly,
  walletId: string,
  entryId: string,
): Promise<{ entry: LedgerEntry }> {
  return core.request(
    `wallets/${encodeURIComponent(walletId)}/ledger/${encodeURIComponent(entryId)}/reconcile`,
    "POST",
    {},
  );
}

export async function deleteWalletV2(
  core: CoreRequestOnly,
  walletId: string,
): Promise<{ deleted: boolean; id: string }> {
  return core.request<{ deleted: boolean; id: string }>(
    `wallets/${encodeURIComponent(walletId)}`,
    "DELETE",
  );
}
