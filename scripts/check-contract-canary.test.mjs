import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
  EXPECTED_ENDPOINTS,
  runCassetteCheck,
  runLiveCanary,
  validateGoogleDiscovery,
  validateOpenAIDiscovery,
  validateSolanaGenesis,
} from "./check-contract-canary.mjs";

test("offline contract cassettes match Fased expected provider endpoints and schemas", async () => {
  const report = await runCassetteCheck();
  assert.equal(report.ok, true, JSON.stringify(report.results));
  assert.equal(report.results.length, 4);
});

test("canary validators detect drifted issuer, token endpoint, and genesis hash", () => {
  assert.ok(
    validateOpenAIDiscovery({
      issuer: "https://evil.example",
      jwks_uri: EXPECTED_ENDPOINTS.openaiJWKS,
    }),
  );
  assert.ok(
    validateGoogleDiscovery({
      issuer: EXPECTED_ENDPOINTS.googleIssuer,
      token_endpoint: "https://evil.example/token",
      jwks_uri: EXPECTED_ENDPOINTS.googleJWKS,
      revocation_endpoint: EXPECTED_ENDPOINTS.googleRevoke,
    }),
  );
  assert.ok(
    validateSolanaGenesis({
      jsonrpc: "2.0",
      result: "wrong-genesis",
    }),
  );
});

test("runLiveCanary enforces explicit timeouts and detects non-gating challenges vs contract drift", async () => {
  const mockFetch = async (url, init) => {
    assert.ok(init.signal instanceof AbortSignal);
    if (url === EXPECTED_ENDPOINTS.openaiDiscovery) {
      return new Response("cloudflare", {
        status: 403,
        headers: { "cf-ray": "ray-test-123" },
      });
    }
    if (url === EXPECTED_ENDPOINTS.googleDiscovery) {
      return Response.json({
        issuer: EXPECTED_ENDPOINTS.googleIssuer,
        token_endpoint: EXPECTED_ENDPOINTS.googleToken,
        jwks_uri: EXPECTED_ENDPOINTS.googleJWKS,
        revocation_endpoint: EXPECTED_ENDPOINTS.googleRevoke,
      });
    }
    return Response.json({
      jsonrpc: "2.0",
      id: 1,
      result: EXPECTED_ENDPOINTS.solanaMainnetGenesis,
    });
  };

  const tolerant = await runLiveCanary({
    fetchImpl: mockFetch,
    strictNetwork: false,
  });
  assert.equal(tolerant.ok, true, JSON.stringify(tolerant));
  assert.match(tolerant.results[0].detail, /blocked_challenge status=403/);

  const strict = await runLiveCanary({
    fetchImpl: mockFetch,
    strictNetwork: true,
  });
  assert.equal(strict.ok, false);

  const drifted = await runLiveCanary({
    fetchImpl: async (url) => {
      if (url === EXPECTED_ENDPOINTS.googleDiscovery) {
        return Response.json({ issuer: "https://drifted.example" });
      }
      return Response.json({
        jsonrpc: "2.0",
        result: EXPECTED_ENDPOINTS.solanaMainnetGenesis,
      });
    },
    strictNetwork: false,
  });
  assert.equal(drifted.ok, false);
});
