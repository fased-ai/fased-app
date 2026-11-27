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

export interface WalletTokenBalance {
  mint: string;
  amountRaw: string;
  decimals: number;
  amountHuman: string;
}

export interface WalletBalanceResult {
  walletId: string;
  publicKey: string;
  cluster: string;
  rpcHost: string;
  lamports: string;
  sol: string;
  tokens?: WalletTokenBalance[];
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

export interface WalletSecurityPosture {
  processIsolated: boolean;
  kekProtection: string;
  ownerAuthEnforced?: boolean;
  passkeyCount?: number;
  passkeyEnabled?: boolean;
  socketPath?: string;
  allowedPeerUid?: number;
}

export interface OwnerPasskey {
  credentialId: string;
  label: string;
  rpId: string;
  alg: number;
  signCount: number;
  createdAt: number;
  lastUsedAt?: number;
}

export interface PasskeyRegisterOptions {
  challenge: string;
  rpId: string;
  rpName: string;
  userId: string;
  userName: string;
  userDisplayName: string;
  pubKeyCredAlgs: number[];
  excludeCredentialIds?: string[];
  timeoutMs: number;
  userVerification: "required" | "preferred" | "discouraged";
  attestation: "none" | "indirect" | "direct";
  expiresAt: number;
}

export interface PasskeyAssertOptions {
  challenge: string;
  rpId: string;
  allowCredentialIds: string[];
  action: string;
  walletId: string;
  bindingDigest?: string;
  timeoutMs: number;
  userVerification: "required" | "preferred" | "discouraged";
  expiresAt: number;
}

export interface WalletsV2ListResponse {
  wallets: WalletV2[];
  count: number;
  socketReady?: boolean;
  signerPresent?: boolean;
  security?: WalletSecurityPosture;
  auditChainCount?: number;
  auditChainVerified?: boolean;
  ownerPasswordSet?: boolean;
}

function stepUpHeaders(
  stepUpToken?: string,
): Record<string, string> | undefined {
  return stepUpToken ? { "X-Fased-Step-Up": stepUpToken } : undefined;
}

export async function setLocalOwnerPassword(
  core: CoreRequestOnly,
  password: string,
): Promise<{ ok: boolean; hasPassword: boolean }> {
  return core.request("auth/password", "POST", { password });
}

export async function requestStepUpToken(
  core: CoreRequestOnly,
  password: string,
  action: string,
  walletId = "*",
): Promise<{ stepUpToken: string; expiresAt: number }> {
  return core.request("auth/step-up", "POST", { password, action, walletId });
}

function b64urlToBytes(b64url: string): Uint8Array {
  const pad =
    b64url.length % 4 === 0 ? "" : "=".repeat(4 - (b64url.length % 4));
  const raw = atob(b64url.replace(/-/g, "+").replace(/_/g, "/") + pad);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

function bytesToB64url(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

export function isWebAuthnAvailable(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.PublicKeyCredential !== "undefined" &&
    typeof navigator !== "undefined" &&
    typeof navigator.credentials !== "undefined"
  );
}

export async function listOwnerPasskeys(
  core: CoreRequestOnly,
): Promise<{ passkeys: OwnerPasskey[]; count: number }> {
  return core.request("auth/passkeys");
}

export async function registerOwnerPasskey(
  core: CoreRequestOnly,
  label: string,
  beginStepUpToken?: string,
  finishStepUpToken?: string,
): Promise<{ passkey: OwnerPasskey }> {
  if (!isWebAuthnAvailable()) {
    throw new Error("WebAuthn passkeys are not available in this browser");
  }
  const opts = await core.request<PasskeyRegisterOptions>(
    "auth/passkeys/register/begin",
    "POST",
    { label },
    undefined,
    stepUpHeaders(beginStepUpToken),
  );
  const cred = (await navigator.credentials.create({
    publicKey: {
      challenge: b64urlToBytes(opts.challenge) as BufferSource,
      rp: { id: opts.rpId, name: opts.rpName },
      user: {
        id: b64urlToBytes(opts.userId) as BufferSource,
        name: opts.userName,
        displayName: opts.userDisplayName,
      },
      pubKeyCredParams: opts.pubKeyCredAlgs.map((alg) => ({
        type: "public-key",
        alg,
      })),
      excludeCredentials: (opts.excludeCredentialIds ?? []).map((id) => ({
        type: "public-key",
        id: b64urlToBytes(id) as BufferSource,
      })),
      authenticatorSelection: {
        userVerification: opts.userVerification,
      },
      timeout: opts.timeoutMs,
      attestation: opts.attestation,
    },
  })) as PublicKeyCredential | null;
  if (!cred) {
    throw new Error("Passkey creation was cancelled");
  }
  const resp = cred.response as AuthenticatorAttestationResponse;
  return core.request<{ passkey: OwnerPasskey }>(
    "auth/passkeys/register/finish",
    "POST",
    {
      challenge: opts.challenge,
      label,
      credentialId: bytesToB64url(cred.rawId),
      clientDataJSON: bytesToB64url(resp.clientDataJSON),
      attestationObject: bytesToB64url(resp.attestationObject),
    },
    undefined,
    stepUpHeaders(finishStepUpToken),
  );
}

export async function deleteOwnerPasskey(
  core: CoreRequestOnly,
  credentialId: string,
  stepUpToken: string,
): Promise<{ deleted: boolean; credentialId: string }> {
  return core.request(
    `auth/passkeys/${encodeURIComponent(credentialId)}`,
    "DELETE",
    undefined,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function requestPasskeyStepUpToken(
  core: CoreRequestOnly,
  action: string,
  walletId = "*",
  bindingDigest = "",
): Promise<{
  stepUpToken: string;
  action: string;
  walletId: string;
  bindingDigest?: string;
  method: "passkey";
  expiresAt: number;
}> {
  if (!isWebAuthnAvailable()) {
    throw new Error("WebAuthn passkeys are not available in this browser");
  }
  const opts = await core.request<PasskeyAssertOptions>(
    "auth/step-up/passkey/begin",
    "POST",
    { action, walletId, bindingDigest },
  );
  const assertion = (await navigator.credentials.get({
    publicKey: {
      challenge: b64urlToBytes(opts.challenge) as BufferSource,
      rpId: opts.rpId,
      allowCredentials: opts.allowCredentialIds.map((id) => ({
        type: "public-key",
        id: b64urlToBytes(id) as BufferSource,
      })),
      userVerification: opts.userVerification,
      timeout: opts.timeoutMs,
    },
  })) as PublicKeyCredential | null;
  if (!assertion) {
    throw new Error("Passkey confirmation was cancelled");
  }
  const resp = assertion.response as AuthenticatorAssertionResponse;
  return core.request("auth/step-up/passkey/finish", "POST", {
    action,
    walletId,
    challenge: opts.challenge,
    credentialId: bytesToB64url(assertion.rawId),
    clientDataJSON: bytesToB64url(resp.clientDataJSON),
    authenticatorData: bytesToB64url(resp.authenticatorData),
    signature: bytesToB64url(resp.signature),
  });
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
  stepUpToken?: string,
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/policy`,
    "PUT",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function setWalletFreeze(
  core: CoreRequestOnly,
  walletId: string,
  frozen: boolean,
  reason = "",
  stepUpToken?: string,
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/freeze`,
    "POST",
    { frozen, reason },
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function updateWalletConnections(
  core: CoreRequestOnly,
  walletId: string,
  connections: WalletConnections,
  stepUpToken?: string,
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/connections`,
    "PUT",
    connections,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}

export async function updateWalletRPC(
  core: CoreRequestOnly,
  walletId: string,
  primaryRpcUrl: string,
  fallbackRpcUrl = "",
  stepUpToken?: string,
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    `wallets/${encodeURIComponent(walletId)}/rpc`,
    "PUT",
    { primaryRpcUrl, fallbackRpcUrl },
    undefined,
    stepUpHeaders(stepUpToken),
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
  stepUpToken?: string,
): Promise<{ archive: unknown; fingerprint: string; filename: string }> {
  return core.request<{
    archive: unknown;
    fingerprint: string;
    filename: string;
  }>(
    `wallets/${encodeURIComponent(walletId)}/backup`,
    "POST",
    { passphrase },
    undefined,
    stepUpHeaders(stepUpToken),
  );
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
  stepUpToken?: string,
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>(
    "wallets/restore",
    "POST",
    req,
    undefined,
    stepUpHeaders(stepUpToken),
  );
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
    mint?: string;
    decimals?: number;
    createDestAta?: boolean;
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
  stepUpToken?: string,
): Promise<{ entry: LedgerEntry; error?: string }> {
  return core.request(
    `wallets/${encodeURIComponent(walletId)}/reviews/${encodeURIComponent(reviewId)}/execute`,
    "POST",
    {},
    undefined,
    stepUpHeaders(stepUpToken),
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
  stepUpToken?: string,
): Promise<{ deleted: boolean; id: string }> {
  return core.request<{ deleted: boolean; id: string }>(
    `wallets/${encodeURIComponent(walletId)}`,
    "DELETE",
    undefined,
    undefined,
    stepUpHeaders(stepUpToken),
  );
}
