import type { AgentV2CoreClient } from "./agents-v2";

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export interface WalletV2 {
  id: string;
  name: string;
  kind: "solana" | "watch" | string;
  role: "personal" | "agent_vault" | "watch" | string;
  publicKey: string;
  hasMnemonic: boolean;
  accountIndex?: number;
  createdAt: number;
}

export interface SolanaRPCStatus {
  mainnetConfigured: boolean;
  mainnetHost?: string;
  devnetConfigured: boolean;
  devnetHost?: string;
}

export interface WalletsV2ListResponse {
  wallets: WalletV2[];
  count: number;
  rpc: SolanaRPCStatus;
}

export interface CreateWalletV2Result {
  wallet: WalletV2;
  mnemonic: string;
  publicKey: string;
}

export interface ExportWalletV2Result {
  mnemonic?: string;
  privateKeyBase58: string;
  keypairJson: string;
  publicKey: string;
}

export interface WalletV2BalanceResult {
  rpcRequired: boolean;
  cluster: string;
  rpcHost?: string;
  publicKey: string;
  lamports?: number;
  sol?: number;
  latencyMs?: number;
}

export async function listWalletsV2(
  core: CoreRequestOnly,
): Promise<WalletsV2ListResponse> {
  return core.request<WalletsV2ListResponse>("wallets");
}

export async function createWalletV2(
  core: CoreRequestOnly,
  input: {
    name: string;
    role: "personal" | "agent_vault";
    words: 12 | 24;
    passphrase: string;
  },
): Promise<CreateWalletV2Result> {
  return core.request<CreateWalletV2Result>("wallets/create", "POST", input);
}

export async function importWalletV2(
  core: CoreRequestOnly,
  input: {
    name: string;
    role?: string;
    mnemonic?: string;
    privateKey?: string;
    watchAddress?: string;
    accountIndex?: number;
    passphrase?: string;
  },
): Promise<{ wallet: WalletV2 }> {
  return core.request<{ wallet: WalletV2 }>("wallets/import", "POST", input);
}

export async function exportWalletV2(
  core: CoreRequestOnly,
  id: string,
  passphrase: string,
): Promise<ExportWalletV2Result> {
  return core.request<ExportWalletV2Result>(
    `wallets/${encodeURIComponent(id)}/export`,
    "POST",
    { passphrase },
  );
}

export async function checkWalletBalanceV2(
  core: CoreRequestOnly,
  id: string,
  cluster: "mainnet-beta" | "devnet",
): Promise<WalletV2BalanceResult> {
  return core.request<WalletV2BalanceResult>(
    `wallets/${encodeURIComponent(id)}/balance`,
    "POST",
    { cluster },
  );
}

export async function deleteWalletV2(
  core: CoreRequestOnly,
  id: string,
): Promise<void> {
  await core.request(`wallets/${encodeURIComponent(id)}`, "DELETE");
}
