import { walletOwnerLink, type Account } from "./accounts";
import type { AgentTask, AgentRun } from "./agents";
export const WRAPPED_SOL = "So11111111111111111111111111111111111111112";
export const USDC = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v";
export type SwapQuote = {
  version: string;
  id: string;
  profileId: string;
  instanceId: string;
  accountId: string;
  network: string;
  owner: string;
  rpcSha256: string;
  inputMint: string;
  outputMint: string;
  amount: string;
  output: string;
  minimumOutput: string;
  slippageBps: number;
  maxFeeLamports: string;
  feeLamports: string;
  rentLamports: string;
  blockhash: string;
  lastValidHeight: string;
  observedSlot: string;
  createdAt: number;
  expiresAt: number;
  instructions: unknown[];
  transactionSha256: string;
  digest: string;
};
export type SwapProposal = {
  receiptRPC?: { version: number; ref: string; origin: string };
  version: string;
  account: unknown;
  quote: SwapQuote;
  fixtureOnly: boolean;
  digest: string;
};
export type SwapReview = {
  proposal: SwapProposal;
  revision: number;
  digest: string;
  approval: string;
};
export type SwapTaskPolicy = {
  credentialLease?: string;
  version: string;
  proposal: SwapProposal;
  digest: string;
};
export type SwapExecution = {
  version: string;
  proposalDigest: string;
  bootId: string;
  state: string;
  signed?: string;
  signature?: string;
  attempted: boolean;
  observation?: {
    state: string;
    slot: string;
    input?: string;
    output?: string;
  };
  events: { state: string; at: number; previous: string; digest: string }[];
};
export type SwapCommand = {
  id?: string;
  digest?: string;
  request?: {
    accountId: string;
    inputMint: string;
    outputMint: string;
    amount: string;
    slippageBps: number;
    maxFeeLamports: string;
  };
};
export type SwapRecovery = {
  taskId: string;
  status: string;
  cancel: boolean;
  reconcile: boolean;
  pause: boolean;
  resume: boolean;
};
export type OnchainView = {
  recovery?: SwapRecovery[];
  version: string;
  available: boolean;
  liveExecution: boolean;
  profileId: string;
  revision: number;
  review?: SwapReview;
  tasks: AgentTask[];
  runs: AgentRun[];
};
export function symbol(mint: string) {
  return mint === WRAPPED_SOL ? "wSOL" : "USDC";
}
export function units(value: string, mint: string) {
  if (!/^\d+$/.test(value)) return "—";
  const decimals = mint === WRAPPED_SOL ? 9 : 6;
  const s = value.padStart(decimals + 1, "0");
  const fraction = s.slice(-decimals).replace(/0+$/, "");
  return s.slice(0, -decimals) + (fraction ? "." + fraction : "");
}
export function atomic(value: string, mint: string) {
  const decimals = mint === WRAPPED_SOL ? 9 : 6;
  if (!/^\d+(\.\d+)?$/.test(value.trim()) || value.length > 40) return;
  const [whole, fraction = ""] = value.trim().split(".");
  if (fraction.length > decimals) return;
  const n =
    BigInt(whole) * 10n ** BigInt(decimals) +
    BigInt(fraction.padEnd(decimals, "0"));
  if (n < 1n || n > 1000000000000000n) return;
  return String(n);
}
export function swapState(state: string) {
  return (
    (
      {
        queued: "Queued",
        validating: "Checking",
        signing: "Signing",
        signed: "Signed",
        sending: "Submitting",
        submitted: "Submitted",
        confirmed: "Confirmed",
        failed: "Failed",
        unknown: "Unknown",
        "awaiting-receipt": "Awaiting receipt",
        expired: "Expired",
        cancelled: "Cancelled",
        paused: "Paused",
        "not-submitted": "Not submitted",
        "simulation-failed": "Simulation failed",
      } as Record<string, string>
    )[state] ?? "Unknown"
  );
}

export function onchainAccounts(accounts: Account[]) {
  return accounts.filter(
    (a) =>
      [2, 3, 4].includes(a.version) &&
      ["mainnet-beta", "devnet"].includes(a.network),
  );
}

// Public exact intent only; credentials are collected by the isolated owner window.
export function tradeOwnerLink(p: SwapProposal, origin = ""): string {
  const a = p.account as { version?: number };
  if (p.version !== "ordinary-controlled-swap-v1" || a?.version !== 3)
    return "";
  const base = walletOwnerLink(
    p.quote.instanceId,
    p.quote.network,
    "setup",
    origin,
  );
  if (!base) return "";
  const encoded = btoa(
    Array.from(new TextEncoder().encode(JSON.stringify(p)), (b) =>
      String.fromCharCode(b),
    ).join(""),
  )
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/, "");
  if (encoded.length > 12 * 1024) return "";
  const u = new URL(base);
  u.searchParams.set("action", "approve-swap");
  u.searchParams.set("proposal", encoded);
  u.searchParams.sort();
  return u.toString();
}
