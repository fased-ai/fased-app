import { describe, it, expect } from "vitest";
import {
  atomic,
  units,
  USDC,
  WRAPPED_SOL,
  onchainAccounts,
  tradeOwnerLink,
} from "./onchain";
import { translate } from "./ui-language";
describe("swap review amounts", () => {
  it("preserves atomic precision and refuses rounding or numeric coercion", () => {
    expect(atomic("12.5", USDC)).toBe("12500000");
    expect(atomic("0.000000001", WRAPPED_SOL)).toBe("1");
    expect(units("999999999999999", WRAPPED_SOL)).toBe("999999.999999999");
    for (const value of ["1e6", "0", "-1", "0.0000001", "NaN", "0x10"]) {
      expect(atomic(value, USDC)).toBeUndefined();
    }
  });
  it("translates swap controls in every supported interface language", () => {
    for (const lang of ["ru", "es", "zh", "ko"] as const) {
      for (const text of [
        "Swap",
        "Quote",
        "Minimum",
        "Reconcile",
        "Download approval",
        "Network cost",
        "Awaiting receipt",
      ]) {
        expect(translate(lang, text)).not.toBe(text);
      }
    }
  });
});

it("keeps installed ordinary wallets selectable without admitting unsupported networks", () => {
  const accounts = [
    { version: 4, network: "devnet", id: "ordinary" },
    { version: 3, network: "mainnet-beta", id: "managed" },
    { version: 1, network: "devnet", id: "old" },
    { version: 4, network: "unknown", id: "foreign" },
  ];
  expect(
    onchainAccounts(accounts as import("./accounts").Account[]).map(
      (a) => a.id,
    ),
  ).toEqual(["ordinary", "managed"]);
});

it("hands only the public exact trade to the private owner window", () => {
  const p = {
    version: "ordinary-controlled-swap-v1",
    account: { version: 3 },
    quote: {
      instanceId: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
      network: "devnet",
    },
    fixtureOnly: true,
    digest: "public",
  } as import("./onchain").SwapProposal;
  const link = tradeOwnerLink(p, "https://my-server.ts.net");
  const u = new URL(link);
  expect(u.searchParams.get("action")).toBe("approve-swap");
  const encoded = u.searchParams.get("proposal")!;
  expect(
    JSON.parse(atob(encoded.replaceAll("-", "+").replaceAll("_", "/"))),
  ).toEqual(p);
  expect([...u.searchParams.keys()]).toEqual([
    "action",
    "instance",
    "network",
    "origin",
    "proposal",
  ]);
  expect(tradeOwnerLink(p, "https://public.example")).toBe("");
  expect(tradeOwnerLink({ ...p, version: "unsupported" })).toBe("");
  expect(tradeOwnerLink({ ...p, digest: "x".repeat(13000) })).toBe("");
});
