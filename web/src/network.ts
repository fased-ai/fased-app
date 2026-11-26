import type { AgentV2CoreClient } from "./agents-v2";

export type SATBondMetadata = {
  amountSat: number;
  status: string;
};

export type HubPackage = {
  id: string;
  name: string;
  kind: "fasedscript" | "desk_blueprint" | "skill" | "watchlist_pack" | string;
  version: string;
  authorPubKeyHex: string;
  payload: string;
  payloadSha256: string;
  signatureHex: string;
  satBond: SATBondMetadata;
  verified: boolean;
  installed: boolean;
  deployedTarget?: string;
  deployedSummary?: string;
  createdAt: number;
};

export type HubListView = {
  wenHeld: boolean;
  satStatus: string;
  packages: HubPackage[];
  fixtureMode: boolean;
};

export type SwarmConfig = {
  enabled: boolean;
  copyMode: "shadow" | "paper" | string;
  localPeerId: string;
  localPubKeyHex: string;
  liveCopyLocked: boolean;
  updatedAt: number;
};

export type SwarmPeer = {
  peerId: string;
  alias: string;
  pubKeyHex: string;
  endpoint?: string;
  status: string;
  addedAt: number;
};

export type SwarmRelayResult = {
  peerId: string;
  endpoint: string;
  delivered: boolean;
  statusCode?: number;
  error?: string;
};

export type SwarmEnvelope = {
  envelopeId: string;
  nonce: string;
  senderPubKeyHex: string;
  kind: string;
  symbol: string;
  direction: "LONG" | "SHORT" | "NEUTRAL" | string;
  thesis: string;
  confidence: number;
  timestamp: number;
  signatureHex: string;
  untrusted: boolean;
  verifiedAt?: number;
  relayResults?: SwarmRelayResult[];
};

export type SwarmStatusView = {
  config: SwarmConfig;
  liveCopyLocked: boolean;
  wenHeld: boolean;
  peerCount: number;
  signalCount: number;
  peers: SwarmPeer[];
  recentSignals: SwarmEnvelope[];
  fixtureMode: boolean;
};

export type CEXVenueSpec = {
  venue: string;
  displayName: string;
  authScheme: string;
  testnetOrigin: string;
  tradeOnlyEnforced: boolean;
};

export type CEXCredentialStatus = {
  venue: string;
  label: string;
  environment: string;
  keyFingerprint: string;
  permissions: string[];
  withdrawalDisabled: boolean;
  updatedAt: number;
};

export type CEXOrderReceipt = {
  id: string;
  venue: string;
  symbol: string;
  side: string;
  orderType: string;
  quantity: number;
  price?: number;
  environment: string;
  status: string;
  simulated: boolean;
  digest: string;
  message: string;
  createdAt: number;
};

export type CEXStatusView = {
  activationEnabled: boolean;
  mainnetLocked: boolean;
  supportedVenues: CEXVenueSpec[];
  credentials: CEXCredentialStatus[];
  recentOrders: CEXOrderReceipt[];
  fixtureMode: boolean;
};

export type OptionContract = {
  occSymbol: string;
  underlying: string;
  expiration: string;
  right: "C" | "P" | string;
  strike: number;
  bid: number;
  ask: number;
  impliedVolatility: number;
  delta: number;
  openInterest: number;
};

export type OptionsChainView = {
  underlying: string;
  spotPrice: number;
  contracts: OptionContract[];
  source: string;
  fixture: boolean;
  updatedAt: number;
};

export type EquitiesOrderReceipt = {
  id: string;
  venue: string;
  accountId?: string;
  symbol: string;
  isOption: boolean;
  side: string;
  orderType: string;
  quantity: number;
  limitPrice?: number;
  environment: string;
  status: string;
  simulated: boolean;
  message: string;
  createdAt: number;
};

export type EquitiesStatusView = {
  activationEnabled: boolean;
  liveLocked: boolean;
  alpacaPaperOrigin: string;
  ibkrLocalOrigin: string;
  recentOrders: EquitiesOrderReceipt[];
  fixtureMode: boolean;
};

export function previewHubList(): HubListView {
  return {
    wenHeld: true,
    satStatus: "deferred_wen_hold",
    fixtureMode: true,
    packages: [
      {
        id: "hub-momentum-rsi",
        name: "Momentum RSI Filter",
        kind: "fasedscript",
        version: "1.0.0",
        authorPubKeyHex: "demo-ed25519-author",
        payload: 'const r = rsi(close, 14);\nsignal("long", r > 52);',
        payloadSha256: "demo-sha256",
        signatureHex: "demo-sig",
        satBond: { amountSat: 250, status: "deferred_wen_hold" },
        verified: true,
        installed: false,
        createdAt: 1795600000,
      },
    ],
  };
}

export function previewSwarmStatus(): SwarmStatusView {
  return {
    liveCopyLocked: true,
    wenHeld: true,
    peerCount: 1,
    signalCount: 0,
    fixtureMode: true,
    config: {
      enabled: false,
      copyMode: "shadow",
      localPeerId: "peer-local",
      localPubKeyHex: "demo-ed25519",
      liveCopyLocked: true,
      updatedAt: 1795600000,
    },
    peers: [
      {
        peerId: "peer-desk-alpha",
        alias: "Desk Alpha Observer",
        pubKeyHex: "demo-peer-pub",
        status: "verified",
        addedAt: 1795600000,
      },
    ],
    recentSignals: [],
  };
}

export function previewCEXStatus(): CEXStatusView {
  return {
    activationEnabled: false,
    mainnetLocked: true,
    fixtureMode: true,
    supportedVenues: [
      {
        venue: "binance",
        displayName: "Binance",
        authScheme: "hmac-sha256",
        testnetOrigin: "https://testnet.binance.vision",
        tradeOnlyEnforced: true,
      },
      {
        venue: "bybit",
        displayName: "Bybit",
        authScheme: "hmac-sha256-v5",
        testnetOrigin: "https://api-testnet.bybit.com",
        tradeOnlyEnforced: true,
      },
      {
        venue: "okx",
        displayName: "OKX",
        authScheme: "hmac-sha256-base64",
        testnetOrigin: "https://www.okx.com",
        tradeOnlyEnforced: true,
      },
      {
        venue: "kraken",
        displayName: "Kraken",
        authScheme: "hmac-sha512",
        testnetOrigin: "https://demo-futures.kraken.com",
        tradeOnlyEnforced: true,
      },
      {
        venue: "coinbase",
        displayName: "Coinbase Advanced",
        authScheme: "hmac-sha256 / ed25519",
        testnetOrigin: "https://api-sandbox.exchange.coinbase.com",
        tradeOnlyEnforced: true,
      },
      {
        venue: "deribit",
        displayName: "Deribit",
        authScheme: "ed25519",
        testnetOrigin: "https://test.deribit.com",
        tradeOnlyEnforced: true,
      },
    ],
    credentials: [],
    recentOrders: [],
  };
}

export function previewEquitiesStatus(): EquitiesStatusView {
  return {
    activationEnabled: false,
    liveLocked: true,
    alpacaPaperOrigin: "https://paper-api.alpaca.markets",
    ibkrLocalOrigin: "https://127.0.0.1:5000",
    recentOrders: [],
    fixtureMode: true,
  };
}

export async function listHubPackages(
  core: AgentV2CoreClient,
): Promise<HubListView> {
  return core.request<HubListView>("hub/packages");
}

export async function installHubPackage(
  core: AgentV2CoreClient,
  id: string,
): Promise<HubPackage> {
  return core.request<HubPackage>("hub/install", "POST", { id });
}

export async function getSwarmStatus(
  core: AgentV2CoreClient,
): Promise<SwarmStatusView> {
  return core.request<SwarmStatusView>("swarm/status");
}

export async function updateSwarmConfig(
  core: AgentV2CoreClient,
  patch: { enabled?: boolean; copyMode?: "shadow" | "paper" },
): Promise<SwarmConfig> {
  return core.request<SwarmConfig>("swarm/config", "PUT", patch);
}

export async function broadcastSwarmSignal(
  core: AgentV2CoreClient,
  signal: {
    kind?: "signal" | "chat";
    symbol: string;
    direction: "LONG" | "SHORT" | "NEUTRAL";
    thesis: string;
    confidence: number;
  },
): Promise<SwarmEnvelope> {
  return core.request<SwarmEnvelope>("swarm/signals", "POST", signal);
}

export async function addSwarmPeer(
  core: AgentV2CoreClient,
  peer: {
    peerId: string;
    alias: string;
    pubKeyHex: string;
    endpoint?: string;
  },
): Promise<SwarmPeer> {
  return core.request<SwarmPeer>("swarm/peers", "POST", peer);
}

export async function getCEXStatus(
  core: AgentV2CoreClient,
): Promise<CEXStatusView> {
  return core.request<CEXStatusView>("cex/status");
}

export async function submitCEXOrder(
  core: AgentV2CoreClient,
  order: {
    venue: string;
    symbol: string;
    side: "BUY" | "SELL";
    orderType: "LIMIT" | "MARKET";
    quantity: number;
    price?: number;
    environment?: "paper" | "testnet" | "sandbox";
    simulateOnly?: boolean;
  },
): Promise<CEXOrderReceipt> {
  return core.request<CEXOrderReceipt>("cex/orders", "POST", order);
}

export async function getEquitiesStatus(
  core: AgentV2CoreClient,
): Promise<EquitiesStatusView> {
  return core.request<EquitiesStatusView>("equities/status");
}

export async function getOptionsChain(
  core: AgentV2CoreClient,
  symbol: string,
): Promise<OptionsChainView> {
  return core.request<OptionsChainView>(
    `equities/options?symbol=${encodeURIComponent(symbol.trim().toUpperCase() || "SPY")}`,
  );
}

export async function submitEquitiesOrder(
  core: AgentV2CoreClient,
  order: {
    venue: "alpaca" | "ibkr";
    accountId?: string;
    symbol: string;
    side: "BUY" | "SELL";
    orderType: "LIMIT" | "MARKET";
    quantity: number;
    limitPrice?: number;
    simulateOnly?: boolean;
  },
): Promise<EquitiesOrderReceipt> {
  return core.request<EquitiesOrderReceipt>("equities/orders", "POST", order);
}
