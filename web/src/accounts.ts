export type AccountRef = {
  version: number;
  signerId: string;
  walletId: string;
  epoch: string;
  network: string;
  publicKey: string;
};
export type Account = {
  rpc?: { version: number; endpoint: string; network: string };
  version: number;
  id: string;
  name: string;
  mode: "watch" | "external" | "fixture-managed";
  network: string;
  publicKey: string;
  signer?: AccountRef;
};
type Policy = {
  account: AccountRef;
  asset: string;
  budget: string;
  maxOperation: string;
  destinations: string[];
  expiresAt: number;
};
export type Proposal = {
  version: number;
  id: string;
  policy: Policy;
  grant: Policy & { id: string; botId: string; revoked: boolean };
};
export type AccountsRecord = {
  version: number;
  revision: number;
  accounts: Account[];
  proposals: { accountId: string; proposal: Proposal }[];
};
export type AccountsView = {
  setup?: {
    version: 1;
    instanceId?: string;
    source?: "local" | "remote";
    network: string;
    state:
      | "not-installed"
      | "unavailable"
      | "empty"
      | "locked"
      | "unlocked"
      | "recovery"
      | "update-held"
      | "legacy";
    fixtureOnly: true;
    networkWatchEnabled: boolean;
    ownerSupported: boolean;
  };
  addresses?: Record<string, string>;
  record: AccountsRecord;
  fixture: {
    available: boolean;
    status?: {
      account?: AccountRef;
      publicIdentity?: {
        version: number;
        chain: string;
        address: string;
        network: string;
        fixtureOnly: boolean;
      };
      locked: boolean;
      recoveryRequired: boolean;
      walletSigner: boolean;
      financialExecution: boolean;
    };
  };
  accountStates: Record<string, string>;
  proposals: {
    id: string;
    digest: string;
    receipt: { digest: string; state: string };
  }[];
};
export const fixtureNetwork = "disposable-fixture:v1";
export const publicID = () => crypto.randomUUID().replaceAll("-", "");

export function accountAddress(a: Account, view: AccountsView): string {
  if (view.addresses?.[a.id]) return view.addresses[a.id];
  const status = view.fixture.status;
  const identity = status?.publicIdentity;
  return a.mode === "fixture-managed" &&
    view.accountStates[a.id] === "current-fixture" &&
    status?.account?.publicKey === a.publicKey &&
    identity &&
    ((a.version === 1 &&
      identity.version === 1 &&
      identity.network === fixtureNetwork) ||
      (a.version === 3 &&
        a.signer?.version === 2 &&
        identity.version === 2 &&
        identity.network === a.network &&
        ["devnet", "mainnet-beta"].includes(a.network))) &&
    identity.chain === "solana" &&
    identity.fixtureOnly
    ? identity.address
    : a.publicKey;
}

export type WalletMode = "watch" | "create" | "import";
export const walletNetworks = ["devnet", "mainnet-beta"] as const;
export function walletNetwork(network: string): boolean {
  return walletNetworks.some((n) => n === network);
}
export function rpcEndpoint(account: Account): string {
  return (
    account.rpc?.endpoint ??
    (account.network === fixtureNetwork
      ? "https://rpc.fased.invalid/solana"
      : "")
  );
}
export function walletSetupLabel(view: AccountsView): string {
  const labels = {
    "not-installed": "Install signer",
    unavailable: "Signer unavailable",
    empty: "No wallet",
    locked: "Locked",
    unlocked: "Unlocked",
    recovery: "Recovery required",
    "update-held": "Update held",
    legacy: "Upgrade signer",
  };
  if (view.setup?.state === "not-installed" && !view.setup.source)
    return "Install app";
  return view.setup ? labels[view.setup.state] : "Signer unavailable";
}
// Only the exact public identity reported by the current v2 signer may attach.
// Locked/recovery-held keys remain locked/held: this grants no authority.
export function walletSetupReference(
  view: AccountsView,
): AccountRef | undefined {
  const { setup } = view;
  const status = view.fixture.status;
  const ref = status?.account;
  const identity = status?.publicIdentity;
  if (
    !setup ||
    !["locked", "unlocked", "recovery"].includes(setup.state) ||
    !setup.fixtureOnly ||
    !view.fixture.available ||
    !walletNetwork(setup.network) ||
    ref?.version !== 2 ||
    ref.network !== setup.network ||
    identity?.version !== 2 ||
    identity.network !== ref.network ||
    identity.chain !== "solana" ||
    !identity.fixtureOnly ||
    !identity.address
  )
    return;
  return ref;
}
export function walletOwnerCommands(
  view: AccountsView,
  mode: Exclude<WalletMode, "watch">,
  selectedNetwork: string,
): string[] {
  const setup = view.setup;
  if (!setup?.ownerSupported || !setup.fixtureOnly) return [];
  const network =
    setup.state === "not-installed" ? selectedNetwork : setup.network;
  if (
    !walletNetwork(network) ||
    !["not-installed", "empty"].includes(setup.state)
  )
    return [];
  if (setup.state === "not-installed") {
    if (
      !["local", "remote"].includes(setup.source ?? "") ||
      !/^[A-Za-z0-9_-]{43}$/.test(setup.instanceId ?? "")
    )
      return [];
    return [
      `fased-app custody enroll --source ${setup.source} --expected-instance ${setup.instanceId} --network ${network} --signer-binary /absolute/path/to/fased-signer --disposable-fixture --prepare-host`,
      "fased-app custody status --disposable-fixture",
    ];
  }
  return [
    `fased-app custody ${mode === "create" ? "setup" : "import"} --disposable-fixture --backup-file /absolute/private/wallet-backup.json${mode === "import" ? " --seed-file /absolute/private/wallet-seed.bin" : ""}`,
  ];
}
