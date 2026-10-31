import { describe, expect, it } from "vitest";
import type { Account } from "./accounts";
import {
  currentWalletSnapshot,
  walletAmount,
  type WalletSnapshot,
} from "./wallet-read";
const a: Account = {
  version: 2,
  id: "a".repeat(32),
  name: "Wallet",
  mode: "watch",
  network: "devnet",
  publicKey: "02".repeat(32),
  rpc: {
    version: 2,
    network: "devnet",
    endpoint: "https://devnet.helius-rpc.com",
  },
};
const address = "11111111111111111111111111111111";
const snapshot = (): WalletSnapshot => ({
  version: 1,
  accountId: a.id,
  revision: 3,
  binding: "b".repeat(64),
  credentialRef: "ref",
  address,
  network: "devnet",
  endpoint: a.rpc!.endpoint,
  commitment: "finalized",
  slot: "100",
  lamports: "9007199254740993",
  tokens: [],
  activity: [],
  activityLimit: 20,
  fetchedAt: 1000,
  expiresAt: 1060,
  fixtureOnly: true,
  liveRPC: false,
});
describe("wallet read disclosure", () => {
  it("keeps integer precision and true zero", () => {
    expect(walletAmount("9007199254740993", 9)).toBe("9007199.254740993");
    expect(walletAmount("9007199254741000", 6)).toBe("9007199254.741");
    expect(walletAmount("0", 9)).toBe("0");
    expect(walletAmount("100", 0)).toBe("100");
    expect(walletAmount("1e9", 9)).toBe("—");
  });
  it("requires exact account/network/revision/address/vault and freshness", () => {
    expect(currentWalletSnapshot(snapshot(), a, 3, "ref", address, 1001)).toBe(
      true,
    );
    for (const patch of [
      { accountId: "other" },
      { revision: 4 },
      { credentialRef: "rotated" },
      { address: "other" },
      { network: "mainnet-beta" },
      { endpoint: "https://other.invalid" },
      { liveRPC: true },
      { fixtureOnly: false },
      { commitment: "processed" },
      { lamports: "1e9" },
      { lamports: "18446744073709551616" },
      { activityLimit: 1000 },
      { expiresAt: 9999 },
    ]) {
      expect(
        currentWalletSnapshot(
          { ...snapshot(), ...patch },
          a,
          3,
          "ref",
          address,
          1001,
        ),
      ).toBe(false);
    }
    expect(currentWalletSnapshot(snapshot(), a, 3, "ref", address, 1060)).toBe(
      false,
    );
    expect(
      currentWalletSnapshot(
        { ...snapshot(), lamports: "0" },
        a,
        3,
        "ref",
        address,
        1001,
      ),
    ).toBe(true);
  });
  it("rejects malformed and unbounded arrays", () => {
    const token = {
      program: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
      mint: address,
      amount: "1",
      decimals: 6,
      slot: "100",
    };
    expect(
      currentWalletSnapshot(
        { ...snapshot(), tokens: [token] },
        a,
        3,
        "ref",
        address,
        1001,
      ),
    ).toBe(true);
    expect(
      currentWalletSnapshot(
        { ...snapshot(), tokens: [{ ...token, decimals: -1 }] },
        a,
        3,
        "ref",
        address,
        1001,
      ),
    ).toBe(false);
    expect(
      currentWalletSnapshot(
        { ...snapshot(), tokens: Array(129).fill(token) },
        a,
        3,
        "ref",
        address,
        1001,
      ),
    ).toBe(false);
    expect(
      currentWalletSnapshot(
        {
          ...snapshot(),
          tokens: [null] as unknown as WalletSnapshot["tokens"],
        },
        a,
        3,
        "ref",
        address,
        1001,
      ),
    ).toBe(false);
  });
});
