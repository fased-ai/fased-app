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

it("binds a network key address to its current versioned public view", () => {
  const ref = {
    version: 2,
    signerId: "s",
    walletId: "w",
    epoch: "e",
    network: "devnet",
    publicKey: "hex",
  };
  const a: Account = {
    version: 3,
    id: "a",
    name: "Wallet",
    mode: "fixture-managed",
    network: "devnet",
    publicKey: "hex",
    signer: ref,
  };
  const view: AccountsView = {
    record: { version: 1, revision: 0, accounts: [a], proposals: [] },
    fixture: {
      available: true,
      status: {
        account: ref,
        publicIdentity: {
          version: 2,
          chain: "solana",
          address: "address",
          network: "devnet",
          fixtureOnly: true,
        },
        locked: true,
        recoveryRequired: true,
        walletSigner: false,
        financialExecution: false,
      },
    },
    accountStates: { a: "current-fixture" },
    proposals: [],
  };
  expect(accountAddress(a, view)).toBe("address");
  view.fixture.status!.publicIdentity!.network = "mainnet-beta";
  expect(accountAddress(a, view)).toBe("hex");
  view.fixture.status!.publicIdentity!.network = "devnet";
  view.accountStates.a = "stale";
  expect(accountAddress(a, view)).toBe("hex");
});
