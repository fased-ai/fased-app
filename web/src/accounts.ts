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
  mode: "watch" | "external" | "fixture-managed" | "managed";
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
    custodyClass?: "ordinary-custody:v1";
    admissionHeld?: boolean;
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
    fixtureOnly: boolean;
    networkWatchEnabled: boolean;
    ownerSupported: boolean;
  };
  addresses?: Record<string, string>;
  record: AccountsRecord;
  fixture: {
    available: boolean;
    status?: {
      fixtureOnly?: boolean;
      custody?: {
        version: number;
        class: string;
        profile: string;
        origin: "" | "generated" | "imported";
        provenance: string;
      };
      admissionHeld?: boolean;
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
    !status ||
    !ref ||
    !identity ||
    !["locked", "unlocked", "recovery"].includes(setup.state) ||
    !view.fixture.available ||
    !walletNetwork(setup.network) ||
    ref.network !== setup.network ||
    identity.version !== ref.version ||
    identity.network !== ref.network ||
    identity.chain !== "solana" ||
    !identity.address ||
    status.walletSigner ||
    status.financialExecution
  )
    return;
  if (ref.version === 2) {
    if (!setup.fixtureOnly || !identity.fixtureOnly || setup.custodyClass)
      return;
  } else if (ref.version === 3) {
    const admission = status.custody;
    if (
      setup.custodyClass !== "ordinary-custody:v1" ||
      !setup.admissionHeld ||
      !status.admissionHeld ||
      admission?.version !== 1 ||
      admission.class !== setup.custodyClass ||
      !["generated", "imported"].includes(admission.origin)
    )
      return;
    const controlled =
      setup.fixtureOnly &&
      identity.fixtureOnly &&
      admission.provenance === "controlled-disposable:v1" &&
      ["controlled-disposable:v1", "owner-authorized:v1"].includes(
        admission.profile,
      );
    const owner =
      !setup.fixtureOnly &&
      !identity.fixtureOnly &&
      admission.profile === "owner-authorized:v1" &&
      admission.provenance === "owner-input:v1";
    if (!controlled && !owner) return;
  } else return;

  return ref;
}
export function walletOwnerCommands(
  view: AccountsView,
  mode: Exclude<WalletMode, "watch">,
  selectedNetwork: string,
): string[] {
  const setup = view.setup;
  if (
    !setup?.ownerSupported ||
    !setup.fixtureOnly ||
    (setup.custodyClass && setup.custodyClass !== "ordinary-custody:v1")
  )
    return [];
  if (setup.custodyClass) {
    const status = view.fixture.status;
    const custody = status?.custody;
    if (
      setup.state !== "empty" ||
      !view.fixture.available ||
      !status?.fixtureOnly ||
      status.account ||
      status.walletSigner ||
      status.financialExecution ||
      !status.admissionHeld ||
      !setup.admissionHeld ||
      custody?.version !== 1 ||
      custody.class !== setup.custodyClass ||
      custody.profile !== "owner-authorized:v1" ||
      custody.provenance !== "controlled-disposable:v1" ||
      custody.origin !== ""
    )
      return [];
  }
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
