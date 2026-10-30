import { expect, it } from "vitest";
import type { Account } from "../accounts";
import type { AgentStrategy } from "../agents";
import { translate } from "../ui-language";
import {
  parseStrategyUnits,
  paperOutcome,
  strategyPlan,
  strategyScheduleCommand,
  strategyUnits,
  strategyWallets,
  type StrategyDraft,
} from "./AgentStrategies";

const account: Account = {
  version: 2,
  id: "wallet-a",
  name: "Public wallet",
  mode: "watch",
  network: "devnet",
  publicKey: "public-reference",
};
const draft: StrategyDraft = {
  name: "Limit buy",
  accountId: account.id,
  quantity: "0.000000001",
  price: "150.123456",
  order: "20.000001",
  budget: "100",
};
const saved: AgentStrategy = {
  id: "strategy-a",
  profileId: "profile-a",
  name: draft.name,
  versions: [
    {
      version: 1,
      name: draft.name,
      createdAt: 1,
      plan: strategyPlan(draft, [account])!,
      binding: {
        accountId: account.id,
        network: account.network,
        publicKey: account.publicKey,
        referenceSha256: "digest",
      },
      digest: "version-digest",
      spentMicros: 0,
    },
  ],
};

it("converts SOL/USDC decimals exactly without rounding or changing the selected wallet", () => {
  const before = JSON.stringify({ draft, account });
  expect(strategyPlan(draft, [account])).toEqual({
    version: "paper-limit-buy-v1",
    market: "SOL/USDC",
    accountId: account.id,
    network: account.network,
    quantityAtoms: 1,
    limitPriceMicros: 150123456,
    orderBudgetMicros: 20000001,
    totalBudgetMicros: 100000000,
  });
  expect(strategyUnits(1, 9)).toBe("0.000000001");
  expect(strategyUnits(150123456, 6)).toBe("150.123456");
  expect(strategyUnits(100000000, 6)).toBe("100");
  expect(strategyUnits(0, 6)).toBe("0");
  expect(strategyUnits(1e15, 6)).toBe("1000000000");
  expect(JSON.stringify({ draft, account })).toBe(before);
});

it("rejects rounding, excess caps and nonfinite or unsafe monetary inputs", () => {
  for (const text of [
    "",
    "0",
    "-1",
    "1e6",
    "Infinity",
    "NaN",
    "1.0000001",
    "1000000001",
  ])
    expect(parseStrategyUnits(text, 6, 1e15)).toBeUndefined();
  expect(parseStrategyUnits("1000000000", 6, 1e15)).toBe(1e15);
  expect(
    strategyPlan({ ...draft, order: "100.000001" }, [account]),
  ).toBeUndefined();
  expect(
    strategyPlan({ ...draft, quantity: "1000.000000001" }, [account]),
  ).toBeUndefined();
  expect(
    strategyPlan({ ...draft, price: "1000000.000001" }, [account]),
  ).toBeUndefined();
  expect(
    strategyPlan({ ...draft, quantity: "0.0000000001" }, [account]),
  ).toBeUndefined();
});

it("does not offer old fixtures/manual accounts or silently replace a missing wallet", () => {
  const fixture = {
    ...account,
    id: "legacy",
    version: 1,
    network: "disposable-fixture:v1",
  };
  const manual: Account = { ...account, id: "manual", mode: "external" };
  const managed: Account = {
    ...account,
    id: "managed",
    version: 3,
    mode: "fixture-managed",
    network: "mainnet-beta",
  };
  expect(strategyWallets([fixture, manual, account, managed])).toEqual([
    account,
    managed,
  ]);
  expect(strategyPlan(draft, [managed])).toBeUndefined();
  expect(
    strategyPlan({ ...draft, accountId: manual.id }, [manual]),
  ).toBeUndefined();
});

it("reviews the latest saved version with finite timing and no provider/news authority", () => {
  const updated = {
    ...saved,
    versions: [
      ...saved.versions,
      { ...saved.versions[0], version: 2, digest: "new-version" },
    ],
  };
  const before = JSON.stringify(updated);
  expect(
    strategyScheduleCommand(updated, " Daily paper ", "1440", "7"),
  ).toEqual({
    action: "task-review",
    name: "Daily paper",
    strategyId: saved.id,
    strategyVersion: 2,
    intervalSeconds: 86400,
    maxRuns: 7,
  });
  for (const [minutes, runs] of [
    ["0", "1"],
    ["1441", "1"],
    ["1", "21"],
    ["1440", "8"],
    ["1.001", "1"],
    ["1", "0"],
  ])
    expect(
      strategyScheduleCommand(updated, "Paper", minutes, runs),
    ).toBeUndefined();
  expect(strategyScheduleCommand(updated, "", "1", "1")).toBeUndefined();
  expect(JSON.stringify(updated)).toBe(before);
});

it("names paper fills and every held result without presenting them as reminders", () => {
  expect(paperOutcome("paper-filled")).toBe("Filled (paper)");
  expect(paperOutcome("price-skipped")).toBe("Price skipped");
  expect(paperOutcome("price-held")).toBe("Price unavailable");
  expect(paperOutcome("version-held")).toBe("Version changed");
  expect(paperOutcome("binding-held")).toBe("Wallet changed");
  expect(paperOutcome("budget-held")).toBe("Budget held");
  expect(paperOutcome("order-cap-held")).toBe("Order cap reached");
  expect(paperOutcome("expired-held")).toBe("Expired");
  expect(paperOutcome("paper-unavailable")).toBe("Paper unavailable");
});

it("translates the strategy workflow and result states in all shipped languages", () => {
  for (const language of ["ru", "es", "zh", "ko"] as const)
    for (const key of [
      "New strategy",
      "Save version",
      "Schedule strategy",
      "Quantity (SOL)",
      "Order cap (USDC)",
      "Budget left",
      "Paper unavailable",
      "Price unavailable",
      "Version changed",
      "Review again",
    ])
      expect(translate(language, key)).not.toBe(key);
});
