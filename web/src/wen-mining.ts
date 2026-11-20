import type { AgentRun, AgentTask } from "./agents";

export type WENMiningRequest = {
  accountId: string;
  proposalId?: string;
  strategyDigest: string;
  amountLamports: string;
  walletBudgetLamports: string;
  maxOperationLamports: string;
  policyExpiresAt: number;
};

export type WENMiningCommand = {
  id?: string;
  digest?: string;
  request?: WENMiningRequest;
};

export type WENMiningWalletPolicy = {
  accountId: string;
  network: string;
  publicKey: string;
  ownerAddress: string;
  rpcSha256: string;
  proposalId?: string;
  proposalDigest?: string;
  walletBudgetLamports: string;
  maxOperationLamports: string;
  policyExpiresAt: number;
};

export type WENMiningOperation = {
  id: string;
  profileId: string;
  instanceId: string;
  strategyDigest: string;
  strategyName: string;
  policy: WENMiningWalletPolicy;
  amountLamports: string;
  allocationsLamports: [string, string, string, string, string];
  weightsBps: [number, number, number, number, number];
  estimatedFeeLamports: string;
  slot: number;
  fixtureSource: string;
  fixtureSha256: string;
  createdAt: number;
  expiresAt: number;
};

export type WENMiningPrepared = {
  commitment: string;
  nonce: number;
  selection: number;
  instructions: {
    operation: string;
    requestSha256: string;
    templateSha256: string;
  }[];
};

export type WENMiningObservation = {
  state: string;
  slot: number;
  accountSha256?: string;
  receiptSha256?: string;
  receipt?: {
    amount: string | number;
    nonce: string | number;
    slot: string | number;
    settled: boolean;
    selection: number | null;
  };
};

export type WENMiningTaskPolicy = {
  version: string;
  operation: WENMiningOperation;
  prepared: WENMiningPrepared;
  digest: string;
};

export type WENMiningExecution = {
  version: string;
  operationDigest: string;
  bootId: string;
  state: string;
  attempted: boolean;
  observation?: WENMiningObservation;
  events: { state: string; at: number; previous: string; digest: string }[];
};

export type WENMiningReview = {
  operation: WENMiningOperation;
  prepared: WENMiningPrepared;
  revision: number;
  digest: string;
};

export type WENMiningRecovery = {
  taskId: string;
  status: string;
  cancel: boolean;
  reconcile: boolean;
  pause: boolean;
  resume: boolean;
};

export type WENMiningView = {
  version: string;
  available: boolean;
  miningActivation: boolean;
  liveExecution: boolean;
  profileId: string;
  revision: number;
  review?: WENMiningReview;
  recovery?: WENMiningRecovery[];
  tasks: AgentTask[];
  runs: AgentRun[];
};

export function lamportsToSOL(value: string | number): string {
  const s = String(value);
  if (!/^\d+$/.test(s)) return "—";
  const padded = s.padStart(10, "0");
  const frac = padded.slice(-9).replace(/0+$/, "");
  return `${padded.slice(0, -9)}${frac ? "." + frac : ""} SOL`;
}

export function solToLamports(value: string): string | undefined {
  const trimmed = value.trim();
  if (!/^\d+(\.\d+)?$/.test(trimmed) || trimmed.length > 32) return undefined;
  const [whole, frac = ""] = trimmed.split(".");
  if (frac.length > 9) return undefined;
  const n = BigInt(whole) * 1_000_000_000n + BigInt(frac.padEnd(9, "0"));
  if (n < 1n || n > 1_000_000_000_000_000n) return undefined;
  return String(n);
}

export function miningState(state: string): string {
  return (
    (
      {
        queued: "Queued",
        paused: "Paused",
        validating: "Checking",
        prepared: "Prepared",
        sending: "Submitting",
        submitted: "Submitted",
        unknown: "Unknown",
        "awaiting-receipt": "Awaiting receipt",
        confirmed: "Confirmed",
        failed: "Failed",
        expired: "Expired",
        "limit-held": "Limit held",
        "binding-held": "Wallet changed",
        "not-submitted": "Not submitted",
        cancelled: "Cancelled",
      } as Record<string, string>
    )[state] ?? "Unknown"
  );
}
