export type AccountRef = {
  version: number;
  signerId: string;
  walletId: string;
  epoch: string;
  network: string;
  publicKey: string;
};
export type Account = {
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
  const status = view.fixture.status;
  const identity = status?.publicIdentity;
  return a.mode === "fixture-managed" &&
    view.accountStates[a.id] === "current-fixture" &&
    status?.account?.publicKey === a.publicKey &&
    identity?.version === 1 &&
    identity.chain === "solana" &&
    identity.fixtureOnly &&
    identity.network === fixtureNetwork
    ? identity.address
    : a.publicKey;
}
