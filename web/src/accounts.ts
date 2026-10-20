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
