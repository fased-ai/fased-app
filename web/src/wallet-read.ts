import type { Account } from "./accounts";
export type WalletSnapshot = {
  version: number;
  accountId: string;
  revision: number;
  binding: string;
  credentialRef: string;
  address: string;
  network: string;
  endpoint: string;
  commitment: string;
  slot: string;
  lamports: string;
  tokens: {
    program: string;
    mint: string;
    amount: string;
    decimals: number;
    slot: string;
  }[];
  activity: {
    signature: string;
    slot: string;
    blockTime: number | null;
    failed: boolean;
  }[];
  activityLimit: number;
  fetchedAt: number;
  expiresAt: number;
  fixtureOnly: boolean;
  liveRPC: boolean;
};
const uint = (value: unknown, digits = 20): value is string =>
  typeof value === "string" &&
  value.length <= digits &&
  /^(0|[1-9][0-9]*)$/.test(value);
const key = (value: unknown): value is string =>
  typeof value === "string" && /^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(value);
const programs = [
  "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
  "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb",
];
export function currentWalletSnapshot(
  value: WalletSnapshot,
  a: Account,
  revision: number,
  ref: string,
  address: string,
  now = Date.now() / 1000,
): boolean {
  return (
    !!value &&
    value.version === 1 &&
    value.accountId === a.id &&
    value.revision === revision &&
    value.address === address &&
    value.network === a.network &&
    value.endpoint === a.rpc?.endpoint &&
    value.credentialRef === ref &&
    /^[a-f0-9]{64}$/.test(value.binding) &&
    value.fixtureOnly === true &&
    value.liveRPC === false &&
    value.commitment === "finalized" &&
    uint(value.lamports) &&
    BigInt(value.lamports) <= 18446744073709551615n &&
    uint(value.slot) &&
    Number.isSafeInteger(value.fetchedAt) &&
    value.fetchedAt > 0 &&
    value.fetchedAt <= now + 5 &&
    value.expiresAt === value.fetchedAt + 60 &&
    now < value.expiresAt &&
    Array.isArray(value.tokens) &&
    value.tokens.length <= 128 &&
    value.tokens.every(
      (t) =>
        !!t &&
        typeof t === "object" &&
        programs.includes(t.program) &&
        key(t.mint) &&
        uint(t.amount, 23) &&
        Number.isInteger(t.decimals) &&
        t.decimals >= 0 &&
        t.decimals <= 255 &&
        uint(t.slot),
    ) &&
    value.activityLimit === 20 &&
    Array.isArray(value.activity) &&
    value.activity.length <= 20 &&
    value.activity.every(
      (v) =>
        !!v &&
        typeof v === "object" &&
        typeof v.signature === "string" &&
        /^[1-9A-HJ-NP-Za-km-z]{64,88}$/.test(v.signature) &&
        uint(v.slot) &&
        typeof v.failed === "boolean" &&
        (v.blockTime === null ||
          (Number.isSafeInteger(v.blockTime) && v.blockTime >= 0)),
    )
  );
}
export function walletAmount(amount: string, decimals: number): string {
  if (
    !uint(amount, 23) ||
    !Number.isInteger(decimals) ||
    decimals < 0 ||
    decimals > 255
  )
    return "—";
  if (decimals === 0) return amount;
  const padded = amount.padStart(decimals + 1, "0");
  const fraction = padded.slice(-decimals).replace(/0+$/, "");
  return padded.slice(0, -decimals) + (fraction ? "." + fraction : "");
}
