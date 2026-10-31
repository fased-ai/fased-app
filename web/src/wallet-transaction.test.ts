import { describe, it, expect } from "vitest";
import {
  currentWalletTransaction,
  walletSignedAmount,
  type WalletSnapshot,
  type WalletTransaction,
} from "./wallet-read";
const key = "11111111111111111111111111111111";
const activity = {
  signature: "1".repeat(64),
  slot: "99",
  blockTime: null,
  failed: false,
};
const snapshot = { expiresAt: 160, activity: [activity] } as WalletSnapshot;
const detail = (): WalletTransaction => ({
  ...activity,
  feeLamports: "5000",
  payer: key,
  nativeBefore: "9007199254740993",
  nativeAfter: "9007199254740893",
  nativeChange: "-100",
  tokens: [
    {
      account: key,
      program: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
      mint: key,
      decimals: 6,
      before: "9007199254740993",
      after: "9007199254741000",
      change: "7",
    },
  ],
  tokenChangesComplete: true,
});
describe("transaction observations", () => {
  it("preserves exact signed amounts and separately displays payer fee", () => {
    expect(currentWalletTransaction(detail(), snapshot, activity, 100)).toBe(
      true,
    );
    expect(walletSignedAmount("-100", 9)).toBe("-0.0000001");
    expect(walletSignedAmount("7", 6)).toBe("+0.000007");
    expect(walletSignedAmount("0", 9)).toBe("0");
    expect(walletSignedAmount("-0", 9)).toBe("—");
    expect(walletSignedAmount("1e3", 9)).toBe("—");
  });
  it("keeps partial observations distinct", () => {
    const v = detail();
    v.tokenChangesComplete = false;
    v.tokens = [];
    expect(currentWalletTransaction(v, snapshot, activity, 100)).toBe(true);
  });
  it.each([
    "signature",
    "slot",
    "failed",
    "fee",
    "native",
    "tokens",
    "duplicate",
    "expired",
  ])("rejects %s", (kind) => {
    const v = detail();
    let now = 100;
    if (kind === "signature") v.signature = "2".repeat(64);
    if (kind === "slot") v.slot = "100";
    if (kind === "failed") v.failed = true;
    if (kind === "fee") v.feeLamports = "-1";
    if (kind === "native") v.nativeChange = "-99";
    if (kind === "tokens") v.tokens = [null as never];
    if (kind === "duplicate") v.tokens.push({ ...v.tokens[0] });
    if (kind === "expired") now = 160;
    expect(currentWalletTransaction(v, snapshot, activity, now)).toBe(false);
  });
});
