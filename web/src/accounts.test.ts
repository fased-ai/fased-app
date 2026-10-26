import { expect, it } from "vitest";
import {
  accountAddress,
  fixtureNetwork,
  type Account,
  type AccountsView,
} from "./accounts";
it("uses a signer address only for a current disposable account", () => {
  const a: Account = {
    version: 1,
    id: "a",
    name: "Test",
    mode: "fixture-managed",
    network: fixtureNetwork,
    publicKey: "hex",
  };
  const v: AccountsView = {
    record: { version: 1, revision: 0, accounts: [a], proposals: [] },
    fixture: {
      available: true,
      status: {
        account: {
          version: 1,
          signerId: "s",
          walletId: "w",
          epoch: "e",
          network: fixtureNetwork,
          publicKey: "hex",
        },
        publicIdentity: {
          version: 1,
          chain: "solana",
          address: "address",
          network: fixtureNetwork,
          fixtureOnly: true,
        },
        locked: true,
        recoveryRequired: false,
        walletSigner: false,
        financialExecution: false,
      },
    },
    accountStates: { a: "current-fixture" },
    proposals: [],
  };
  expect(accountAddress(a, v)).toBe("address");
  v.accountStates.a = "stale";
  expect(accountAddress(a, v)).toBe("hex");
  v.accountStates.a = "current-fixture";
  v.fixture.status!.publicIdentity!.network = "mainnet-beta";
  expect(accountAddress(a, v)).toBe("hex");
  delete v.fixture.status!.publicIdentity;
  expect(accountAddress(a, v)).toBe("hex");
});
