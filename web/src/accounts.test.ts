import { expect, it } from "vitest";
import {
  accountAddress,
  fixtureNetwork,
  rpcEndpoint,
  walletOwnerCommands,
  walletSetupLabel,
  walletSetupReference,
  type Account,
  type AccountsView,
} from "./accounts";
import { translate } from "./ui-language";
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

function setupView(): AccountsView {
  return {
    setup: {
      version: 1,
      instanceId: "a".repeat(43),
      source: "local",
      network: "devnet",
      state: "locked",
      fixtureOnly: true,
      networkWatchEnabled: false,
      ownerSupported: true,
    },
    record: { version: 1, revision: 0, accounts: [], proposals: [] },
    fixture: {
      available: true,
      status: {
        account: {
          version: 2,
          signerId: "s",
          walletId: "w",
          epoch: "e",
          network: "devnet",
          publicKey: "hex",
        },
        publicIdentity: {
          version: 2,
          chain: "solana",
          address: "11111111111111111111111111111111",
          network: "devnet",
          fixtureOnly: true,
        },
        locked: true,
        recoveryRequired: true,
        walletSigner: false,
        financialExecution: false,
      },
    },
    accountStates: {},
    proposals: [],
  };
}

it("allows public attachment of locked/recovery keys without changing their holds", () => {
  const view = setupView();
  for (const state of ["locked", "recovery"] as const) {
    view.setup!.state = state;
    const before = JSON.stringify(view);
    expect(walletSetupReference(view)).toEqual(view.fixture.status!.account);
    expect(walletSetupLabel(view)).toBe(
      state === "locked" ? "Locked" : "Recovery required",
    );
    expect(JSON.stringify(view)).toBe(before);
    expect(walletOwnerCommands(view, "import", "mainnet-beta")).toEqual([]);
  }
});

it("refuses unavailable, held, legacy or mismatched signer identities", () => {
  for (const state of [
    "not-installed",
    "unavailable",
    "empty",
    "update-held",
    "legacy",
  ] as const) {
    const view = setupView();
    view.setup!.state = state;
    expect(walletSetupReference(view)).toBeUndefined();
  }
  const view = setupView();
  view.fixture.status!.publicIdentity!.network = "mainnet-beta";
  expect(walletSetupReference(view)).toBeUndefined();
  view.fixture.status!.publicIdentity!.network = "devnet";
  view.fixture.status!.account!.version = 1;
  expect(walletSetupReference(view)).toBeUndefined();
  view.fixture.status!.account!.version = 2;
  view.fixture.available = false;
  expect(walletSetupReference(view)).toBeUndefined();
});

it("keeps network choice on install and private file placeholders on protected commands", () => {
  const view = setupView();
  view.setup!.state = "not-installed";
  const [install, status] = walletOwnerCommands(view, "create", "mainnet-beta");
  expect(install).toBe(
    `fased-app custody enroll --source local --expected-instance ${"a".repeat(43)} --network mainnet-beta --signer-binary /absolute/path/to/fased-signer --disposable-fixture --prepare-host`,
  );
  expect(status).toBe("fased-app custody status --disposable-fixture");
  expect(walletOwnerCommands(view, "import", "mainnet-beta")).toEqual([
    install,
    status,
  ]);
  expect(
    walletOwnerCommands(view, "create", "mainnet-beta").join(" "),
  ).not.toMatch(/custody (setup|import)/);
  view.setup!.state = "empty";
  expect(walletOwnerCommands(view, "create", "mainnet-beta")).toEqual([
    "fased-app custody setup --disposable-fixture --backup-file /absolute/private/wallet-backup.json",
  ]);
  const commands = walletOwnerCommands(view, "import", "mainnet-beta");
  expect(commands).toEqual([
    "fased-app custody import --disposable-fixture --backup-file /absolute/private/wallet-backup.json --seed-file /absolute/private/wallet-seed.bin",
  ]);
  expect(commands.join(" ")).not.toMatch(
    /--network|--prepare-host|--password|--key|--seed\s/,
  );
  view.setup!.ownerSupported = false;
  expect(walletOwnerCommands(view, "create", "devnet")).toEqual([]);
  view.setup!.ownerSupported = true;
  view.setup!.state = "not-installed";
  expect(walletOwnerCommands(view, "create", "devnet; anything")).toEqual([]);
});

it("leaves network RPC empty and retains server-resolved public addresses when signer is unavailable", () => {
  const view = setupView();
  const a: Account = {
    version: 3,
    id: "a",
    name: "Wallet",
    mode: "watch",
    network: "devnet",
    publicKey: "hex",
  };
  expect(rpcEndpoint(a)).toBe("");
  expect(rpcEndpoint({ ...a, network: "mainnet-beta" })).toBe("");
  expect(rpcEndpoint({ ...a, network: fixtureNetwork })).toBe(
    "https://rpc.fased.invalid/solana",
  );
  a.rpc = {
    version: 1,
    endpoint: "https://controlled.example/solana",
    network: "devnet",
  };
  expect(rpcEndpoint(a)).toBe(a.rpc.endpoint);
  view.addresses = { a: "11111111111111111111111111111111" };
  view.fixture.available = false;
  view.setup!.state = "unavailable";
  expect(accountAddress(a, view)).toBe(view.addresses.a);
});

it("translates the wallet setup actions and held states in all shipped languages", () => {
  const keys = [
    "Add wallet",
    "Wallet name",
    "Public address",
    "Attach",
    "Watch unavailable",
    "Instance terminal",
    "Refresh status",
    "Recovery required",
    "Update held",
    "Already added",
    "Verified (test)",
  ];
  for (const language of ["ru", "es", "zh", "ko"] as const)
    for (const key of keys) {
      expect(translate(language, key)).not.toBe(key);
      expect(translate(language, key).length).toBeGreaterThan(0);
    }
});

it("enrolls the exact installed source and never creates a second blank instance", () => {
  const view = setupView();
  view.setup!.state = "not-installed";
  view.setup!.source = "remote";
  expect(walletOwnerCommands(view, "create", "devnet")[0]).toContain(
    "--source remote --expected-instance " + "a".repeat(43),
  );
  expect(walletOwnerCommands(view, "create", "devnet").join(" ")).not.toContain(
    "custody install",
  );
  for (const id of [
    undefined,
    "",
    "a".repeat(42),
    "x;$(id)",
    "a".repeat(43) + "\n",
  ]) {
    view.setup!.instanceId = id;
    expect(walletOwnerCommands(view, "create", "devnet")).toEqual([]);
  }
  view.setup!.instanceId = "a".repeat(43);
  delete view.setup!.source;
  expect(walletOwnerCommands(view, "create", "devnet")).toEqual([]);
  expect(walletSetupLabel(view)).toBe("Install app");
});
