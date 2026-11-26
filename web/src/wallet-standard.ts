// Zero-dependency Solana Wallet Standard client for Hardware-backed & external browser wallets
// (Ledger via Phantom / Solflare / Backpack, Keystone, etc.).
// Keys remain strictly on the user's device; Fased receives public addresses & signed packets only.

export interface WalletStandardAccount {
  address: string;
  publicKey: Uint8Array;
  chains: readonly string[];
  features: readonly string[];
  label?: string;
}

export interface WalletStandardProvider {
  name: string;
  icon?: string;
  accounts: readonly WalletStandardAccount[];
  features: Record<string, any>;
}

declare global {
  interface Navigator {
    wallets?: {
      get?: () => readonly WalletStandardProvider[];
    };
  }
}

function getRegisteredWallets(): WalletStandardProvider[] {
  const seen = new Map<string, WalletStandardProvider>();
  try {
    const api = (window as any).navigator?.wallets;
    if (api && typeof api.get === "function") {
      for (const w of api.get() ?? []) {
        if (w && w.name) seen.set(w.name, w);
      }
    }
  } catch {
    // Ignore restricted navigator environments
  }

  // Trigger Wallet Standard discovery event for lazily injected wallets.
  try {
    const collected: WalletStandardProvider[] = [];
    const register = (wallet: WalletStandardProvider) => {
      if (wallet && wallet.name) collected.push(wallet);
      return () => {};
    };
    window.dispatchEvent(
      new CustomEvent("wallet-standard:app-ready", {
        detail: { register },
      }),
    );
    for (const w of collected) {
      if (!seen.has(w.name)) seen.set(w.name, w);
    }
  } catch {
    // No-op in headless/non-DOM environments
  }

  return [...seen.values()].filter(
    (w) =>
      w.features &&
      "standard:connect" in w.features &&
      "solana:signTransaction" in w.features,
  );
}

export function discoverSolanaWalletProviders(): WalletStandardProvider[] {
  return getRegisteredWallets();
}

export async function connectWalletStandardAccount(
  preferredName?: string,
  expectedAddress?: string,
): Promise<{
  provider: WalletStandardProvider;
  account: WalletStandardAccount;
}> {
  const providers = getRegisteredWallets();
  if (providers.length === 0) {
    throw new Error(
      "No Solana Wallet Standard provider detected (install Phantom, Solflare, or Backpack with Ledger connected)",
    );
  }
  let target = providers[0];
  if (expectedAddress) {
    const byAddr = providers.find((w) =>
      w.accounts?.some((a) => a.address === expectedAddress),
    );
    if (byAddr) target = byAddr;
  } else if (preferredName) {
    const byName = providers.find(
      (w) => w.name.toLowerCase() === preferredName.toLowerCase(),
    );
    if (byName) target = byName;
  }

  const connectFeat = target.features["standard:connect"];
  const res = await connectFeat.connect();
  const accounts: readonly WalletStandardAccount[] = (
    res?.accounts?.length ? res.accounts : target.accounts
  ).filter((a: WalletStandardAccount) =>
    a.chains?.some((c) => c.startsWith("solana:")),
  );
  if (accounts.length === 0) {
    throw new Error(`${target.name} did not expose a Solana account`);
  }
  const match = expectedAddress
    ? accounts.find((a) => a.address === expectedAddress)
    : accounts[0];
  if (!match) {
    throw new Error(
      `${target.name} is not connected to ${expectedAddress}; switch account in wallet`,
    );
  }
  return { provider: target, account: match };
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    bytes[i] = bin.charCodeAt(i);
  }
  return bytes;
}

function bytesToBase64(bytes: Uint8Array): string {
  let bin = "";
  const chunk = 8192;
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

export async function signWalletStandardTransaction(params: {
  unsignedTxBase64: string;
  expectedAddress: string;
  cluster?: "mainnet-beta" | "devnet" | "testnet" | string;
  preferredWalletName?: string;
}): Promise<string> {
  const { provider, account } = await connectWalletStandardAccount(
    params.preferredWalletName,
    params.expectedAddress,
  );
  const signFeat = provider.features["solana:signTransaction"];
  if (!signFeat || typeof signFeat.signTransaction !== "function") {
    throw new Error(`${provider.name} does not implement solana:signTransaction`);
  }
  const chain =
    params.cluster === "devnet"
      ? "solana:devnet"
      : params.cluster === "testnet"
        ? "solana:testnet"
        : "solana:mainnet";
  const txBytes = base64ToBytes(params.unsignedTxBase64);
  const outputs = await signFeat.signTransaction({
    account,
    transaction: txBytes,
    chain,
  });
  const first = Array.isArray(outputs) ? outputs[0] : outputs;
  const signedBytes: Uint8Array | undefined = first?.signedTransaction;
  if (!(signedBytes instanceof Uint8Array) || signedBytes.length < 66) {
    throw new Error("Hardware wallet returned an empty signed transaction");
  }
  return bytesToBase64(signedBytes);
}
