import type { ReactElement } from "react";
export interface ResearchContext {
  source: "fixture";
  asset: "SAT";
  reference: "BTC-USD";
  view: "price" | "outlook" | "conditional";
  preset: string;
  weightsBps: number[];
  amountLamports: string | null;
  horizonHours: number;
  execution: "disabled";
}
export interface StrategyTerminalProps {
  readProvider?: {walletHex:(address:string)=>string;read:(input:any,signal:AbortSignal)=>Promise<any>;receipt:(input:any,signal:AbortSignal)=>Promise<any>;history?:(input:any,signal:AbortSignal)=>Promise<any>;treasuryReceipt?:(input:any,signal:AbortSignal)=>Promise<any>;operatingReceipt?:(input:any,signal:AbortSignal)=>Promise<any>};
  previewProvider?: {preview: (input: {phaseCase: "bootstrap"|"normal-empty-stake"|"normal-eligible"; amounts: string[]}, signal: AbortSignal) => Promise<any>; validate: (envelope: any, signal: AbortSignal) => Promise<unknown>};
  brand: "WEN" | "Fased";
  logoUrl?: string;
  initialView?: "discover" | "terminal";
  onViewChange?: (view: "discover" | "terminal") => void;
  onOpenResearch?: (context: ResearchContext) => void;
}
export declare function StrategyTerminal(
  props: StrategyTerminalProps,
): ReactElement;
export declare function compileAllocation(
  amount: string,
  weights: readonly number[],
): string[];

export type Scope = {
  market: string;
  terms: string;
  evidence: "paper" | "historical-replay" | "actual";
  from: string;
  to: string;
  dataset: string;
  valuation: string;
  costs: string;
  coverage: string;
};
export type HistoryRow = {
  id: string;
  cursor: number;
  strategyDigest: string;
  at: string;
  finality: "provisional" | "finalized" | "reverted";
  category: "mining" | "staking" | "prize" | "carry" | "transfer";
  committed: string;
  returned: string;
  fees: string;
  forfeitures: string;
  userCosts: string;
  btcSaleSOL: string;
  satSaleSOL: string;
  btcPending: string;
  btcReceived: string;
  satEarned: string;
  satClaimed: string;
  satSold: string;
};
export type Bundle = {
  schema: "wen-history-import/1";
  source: string;
  verified: false;
  scope: Scope;
  initialBankroll: string | null;
  skipped: number | null;
  pendingExposure: string | null;
  rows: HistoryRow[];
  gaps: string[];
};
export type Version = {
  digest: string;
  raw: string;
  name: string;
  createdAt: string;
  origin: string;
};
export type Library = {
  revision: number;
  versions: Version[];
  bundles: Bundle[];
};
export interface ResearchStorage {
  label: string;
  load: () => Promise<Library>;
  save: (revision: number, raw: string) => Promise<Library>;
  importHistory: (revision: number, raw: string) => Promise<Library>;
}

export type ResearchChatReview = {
  schema: "wen-research-chat-review/1";
  profileId: string;
  revision: number;
  expiresAt: number;
  text: string;
  source: string;
};

export declare function ResearchWorkspace(props: {
  storage: ResearchStorage;
  readResearch?: (lookback: number) => Promise<unknown>;
  onDiscussResearch?: (review: ResearchChatReview) => void;
  onDiscussSaved?: (selection: SavedChatSelection) => Promise<void>;
}): ReactElement;
export type SavedChatSelection = {
  libraryRevision: number;
  strategyDigest?: string;
  bundleIndex?: number;
};
export declare function browserStorage(namespace: string): ResearchStorage;
export declare function cloneStrategy(raw: string, name: string): string;
export declare function StrategyCompare(props: {
  versions: Version[];
  history: Bundle[];
}): ReactElement;

export declare function createMiningReadClient(options?:{fetcher?:typeof fetch;now?:()=>number}):NonNullable<StrategyTerminalProps["readProvider"]>;
