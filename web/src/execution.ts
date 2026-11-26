import type { AgentV2CoreClient } from "./agents-v2";

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export interface ExecutionStatus {
  profile: string;
  mainnetLocked: boolean;
  mainnetCapped?: boolean;
  wenHeld: boolean;
  solanaDevnetReady?: boolean;
  solanaMainnetReady?: boolean;
  hyperliquidTestnet?: boolean;
  hyperliquidMainnet?: boolean;
  polymarketTestnetReady?: boolean;
  kalshiDemoReady?: boolean;
  lighterTestnetReady?: boolean;
  agentVaultId?: string;
  signerPublicKey?: string;
}

export interface AgentVaultRecord {
  id: string;
  label?: string;
  network?: string;
  allowedVenues?: string[];
  maxOrderMicroUsd?: number;
  dailyLossMicroUsd?: number;
}

export interface AgentVaultsResponse {
  vaults: AgentVaultRecord[];
  profile: string;
  activeVaultId?: string;
  mainnetLocked: boolean;
  wenHeld: boolean;
}

export async function getExecutionStatus(
  core: CoreRequestOnly,
): Promise<ExecutionStatus> {
  return core.request<ExecutionStatus>("execution/status");
}

export async function getAgentVaults(
  core: CoreRequestOnly,
): Promise<AgentVaultsResponse> {
  return core.request<AgentVaultsResponse>("vaults");
}
