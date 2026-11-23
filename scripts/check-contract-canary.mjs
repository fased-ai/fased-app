import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export const DEFAULT_CASSETTE_DIR = resolve("internal/core/testdata/cassettes");

export const EXPECTED_ENDPOINTS = {
  openaiDiscovery: "https://auth.openai.com/.well-known/openid-configuration",
  openaiIssuer: "https://auth.openai.com",
  openaiJWKS: "https://auth.openai.com/.well-known/jwks.json",
  openaiRevoke: "https://auth.openai.com/oauth/revoke",
  googleDiscovery:
    "https://accounts.google.com/.well-known/openid-configuration",
  googleIssuer: "https://accounts.google.com",
  googleToken: "https://oauth2.googleapis.com/token",
  googleJWKS: "https://www.googleapis.com/oauth2/v3/certs",
  googleRevoke: "https://oauth2.googleapis.com/revoke",
  solanaMainnetRPC: "https://api.mainnet-beta.solana.com",
  solanaMainnetGenesis: "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d",
};

export function validateOpenAIDiscovery(doc) {
  if (!doc || typeof doc !== "object") {
    return "OpenAI discovery payload is not an object";
  }
  if (doc.issuer !== EXPECTED_ENDPOINTS.openaiIssuer) {
    return `OpenAI issuer drifted: ${String(doc.issuer)}`;
  }
  if (
    typeof doc.jwks_uri !== "string" ||
    !doc.jwks_uri.startsWith("https://auth.openai.com/")
  ) {
    return `OpenAI jwks_uri drifted: ${String(doc.jwks_uri)}`;
  }
  if (
    doc.revocation_endpoint &&
    !String(doc.revocation_endpoint).startsWith("https://auth.openai.com/")
  ) {
    return `OpenAI revocation_endpoint drifted: ${String(doc.revocation_endpoint)}`;
  }
  return null;
}

export function validateGoogleDiscovery(doc) {
  if (!doc || typeof doc !== "object") {
    return "Google discovery payload is not an object";
  }
  if (doc.issuer !== EXPECTED_ENDPOINTS.googleIssuer) {
    return `Google issuer drifted: ${String(doc.issuer)}`;
  }
  if (doc.token_endpoint !== EXPECTED_ENDPOINTS.googleToken) {
    return `Google token_endpoint drifted: ${String(doc.token_endpoint)}`;
  }
  if (doc.jwks_uri !== EXPECTED_ENDPOINTS.googleJWKS) {
    return `Google jwks_uri drifted: ${String(doc.jwks_uri)}`;
  }
  if (doc.revocation_endpoint !== EXPECTED_ENDPOINTS.googleRevoke) {
    return `Google revocation_endpoint drifted: ${String(doc.revocation_endpoint)}`;
  }
  return null;
}

export function validateSolanaGenesis(doc) {
  if (!doc || typeof doc !== "object") {
    return "Solana RPC genesis payload is not an object";
  }
  if (
    doc.jsonrpc !== "2.0" ||
    doc.result !== EXPECTED_ENDPOINTS.solanaMainnetGenesis
  ) {
    return `Solana mainnet genesis drifted: ${String(doc.result)}`;
  }
  return null;
}

export function validateOpenAISSECassette(text) {
  if (typeof text !== "string" || !text.includes("event: response.completed")) {
    return "OpenAI SSE cassette missing response.completed frame";
  }
  if (!text.includes('"sequence_number":0')) {
    return "OpenAI SSE cassette missing initial sequence_number 0";
  }
  return null;
}

export async function runCassetteCheck(cassetteDir = DEFAULT_CASSETTE_DIR) {
  const results = [];
  const openaiDoc = JSON.parse(
    await readFile(join(cassetteDir, "openai-oidc-discovery.json"), "utf8"),
  );
  const openaiErr = validateOpenAIDiscovery(openaiDoc);
  results.push({
    target: "cassette:openai-oidc",
    ok: !openaiErr,
    detail: openaiErr ?? "ok",
  });

  const googleDoc = JSON.parse(
    await readFile(join(cassetteDir, "google-oidc-discovery.json"), "utf8"),
  );
  const googleErr = validateGoogleDiscovery(googleDoc);
  results.push({
    target: "cassette:google-oidc",
    ok: !googleErr,
    detail: googleErr ?? "ok",
  });

  const solanaBatch = JSON.parse(
    await readFile(join(cassetteDir, "solana-mainnet-rpc-batch.json"), "utf8"),
  );
  const genesisErr = Array.isArray(solanaBatch)
    ? validateSolanaGenesis(solanaBatch[0])
    : "Solana cassette is not a batch array";
  results.push({
    target: "cassette:solana-mainnet-rpc",
    ok: !genesisErr,
    detail: genesisErr ?? "ok",
  });

  const sseText = await readFile(
    join(cassetteDir, "openai-responses-sse.txt"),
    "utf8",
  );
  const sseErr = validateOpenAISSECassette(sseText);
  results.push({
    target: "cassette:openai-responses-sse",
    ok: !sseErr,
    detail: sseErr ?? "ok",
  });

  return {
    ok: results.every((r) => r.ok),
    results,
  };
}

async function fetchWithTimeout(fetchImpl, url, init = {}, timeoutMs = 8000) {
  const controller = new AbortController();
  const timer = setTimeout(
    () => controller.abort(new Error("canary timeout")),
    timeoutMs,
  );
  try {
    return await fetchImpl(url, { ...init, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

export async function runLiveCanary({
  fetchImpl = globalThis.fetch,
  strictNetwork = false,
  timeoutMs = 8000,
} = {}) {
  if (typeof fetchImpl !== "function") {
    throw new Error("fetch implementation required");
  }
  const results = [];

  const checkEndpoint = async (name, url, init, validator, acceptChallenge) => {
    try {
      const res = await fetchWithTimeout(fetchImpl, url, init, timeoutMs);
      if (acceptChallenge && (res.status === 403 || res.status === 429)) {
        const cfRay = res.headers?.get?.("cf-ray") ?? "";
        const detail = `blocked_challenge status=${res.status}${cfRay ? ` cfRay=${cfRay}` : ""}`;
        results.push({
          target: name,
          ok: !strictNetwork,
          status: res.status,
          detail,
        });
        return;
      }
      if (!res.ok) {
        results.push({
          target: name,
          ok: false,
          status: res.status,
          detail: `unexpected status=${res.status}`,
        });
        return;
      }
      const body = await res.json();
      const err = validator(body);
      results.push({
        target: name,
        ok: !err,
        status: res.status,
        detail: err ?? "ok",
      });
    } catch (err) {
      results.push({
        target: name,
        ok: !strictNetwork,
        status: 0,
        detail: `network_error=${err instanceof Error ? err.message : String(err)}`,
      });
    }
  };

  await checkEndpoint(
    "live:openai-oidc",
    EXPECTED_ENDPOINTS.openaiDiscovery,
    { headers: { Accept: "application/json" } },
    validateOpenAIDiscovery,
    true,
  );
  await checkEndpoint(
    "live:google-oidc",
    EXPECTED_ENDPOINTS.googleDiscovery,
    { headers: { Accept: "application/json" } },
    validateGoogleDiscovery,
    false,
  );
  await checkEndpoint(
    "live:solana-mainnet-genesis",
    EXPECTED_ENDPOINTS.solanaMainnetRPC,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "getGenesisHash",
        params: [],
      }),
    },
    validateSolanaGenesis,
    true,
  );

  return {
    ok: results.every((r) => r.ok),
    results,
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const args = new Set(process.argv.slice(2));
  const live = args.has("--live");
  const strictNetwork = args.has("--strict");
  const cassetteReport = await runCassetteCheck();
  const report = { cassettes: cassetteReport };
  let ok = cassetteReport.ok;
  if (live) {
    report.live = await runLiveCanary({ strictNetwork });
    ok = ok && report.live.ok;
  }
  console.log(JSON.stringify(report, null, 2));
  if (!ok) {
    process.exitCode = 1;
  }
}
