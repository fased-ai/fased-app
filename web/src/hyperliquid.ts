export type MarketMeta = {
  assetIndex: number;
  symbol: string;
  szDecimals: number;
  maxLeverage: number;
  onlyIsolated?: boolean;
  markPrice: string;
  oraclePrice: string;
  midPrice?: string;
  prevDayPrice?: string;
  dayNotionalVolume?: string;
  fundingRate: string;
  openInterest: string;
};

export type Position = {
  symbol: string;
  size: string;
  entryPrice: string;
  positionValue: string;
  unrealizedPnl: string;
  liquidationPrice?: string;
  leverage: number;
  marginMode: string;
};

export type OpenOrder = {
  symbol: string;
  side: string;
  limitPrice: string;
  size: string;
  orderId: number;
  timestamp: number;
  clientOrderId?: string;
};

export type AccountState = {
  address: string;
  accountValue: string;
  totalMarginUsed: string;
  withdrawable: string;
  positions: Position[];
  openOrders: OpenOrder[];
};

export type HyperliquidPolicy = {
  version: number;
  network: "testnet" | "mainnet";
  allowedAssets: string[];
  maxLeverage: number;
  maxNotionalMicroUsd: number;
  maxOrderPriceBpsDeviation: number;
  vaultAddress?: string;
  expiresAt: number;
};

export type OrderDraft = {
  asset: string;
  isBuy: boolean;
  limitPrice: string;
  size: string;
  leverage: number;
  reduceOnly: boolean;
  timeInForce: "Gtc" | "Ioc" | "Alo";
  clientOrderId?: string;
};

export type OrderPreview = {
  normalizedPrice: string;
  normalizedSize: string;
  referenceMarkPrice: string;
  notionalMicroUsd: number;
  initialMarginMicroUsd: number;
  estimatedFeeMicroUsd: number;
  priceDeviationBps: number;
  connectionId: string;
  typedDataDigest: string;
};

export type HyperliquidReview = {
  revision: number;
  expiresAt: number;
  digest: string;
  preview: OrderPreview;
};

export type HyperliquidOrderRecord = {
  id: string;
  orderId: number;
  asset: string;
  isBuy: boolean;
  limitPrice: string;
  size: string;
  leverage: number;
  reduceOnly: boolean;
  timeInForce: string;
  state: string;
  nonce: number;
  signerAddress: string;
  connectionId: string;
  typedDataDigest: string;
  signatureHex: string;
  filledSize?: string;
  avgPrice?: string;
  message?: string;
  updatedAt: number;
};

export type HyperliquidView = {
  version: string;
  available: boolean;
  activationEnabled: boolean;
  liveExecution: boolean;
  network: "testnet" | "mainnet";
  signerAddress: string;
  policy: HyperliquidPolicy;
  markets: MarketMeta[];
  account?: AccountState;
  review?: HyperliquidReview;
  orders: HyperliquidOrderRecord[];
};

export type HyperliquidCommand = {
  digest?: string;
  orderId?: number;
  request?: OrderDraft;
};

export function formatMicroUSD(micro: number | string | undefined): string {
  const n = typeof micro === "string" ? Number(micro) : (micro ?? 0);
  if (!Number.isFinite(n) || n < 0) return "$0.00";
  const dollars = n / 1_000_000;
  return `$${dollars.toLocaleString("en-US", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;
}

export function formatFundingRate(rate: string | undefined): string {
  const n = Number(rate ?? "");
  if (!Number.isFinite(n)) return "—";
  const pct = n * 100;
  const sign = pct > 0 ? "+" : "";
  return `${sign}${pct.toFixed(4)}%`;
}

export function hyperliquidOrderState(state: string): string {
  switch (state) {
    case "resting":
      return "Resting";
    case "filled":
      return "Filled";
    case "cancelled":
    case "canceled":
      return "Cancelled";
    case "rejected":
      return "Rejected";
    default:
      return "Status unknown";
  }
}

export function shortenHex(value: string, head = 6, tail = 4): string {
  if (!value) return "—";
  if (value.length <= head + tail + 3) return value;
  return `${value.slice(0, head + 2)}…${value.slice(-tail)}`;
}
