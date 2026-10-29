import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { checkProviders, applyReviewed } from "./provider-models.mjs";
const raw = await readFile("internal/core/provider_local.json", "utf8");
const fullRegistry = JSON.parse(raw);
// Retained Ollama-only policy path; named-server formats require explicit review.
const registry = {
  ...fullRegistry,
  models: fullRegistry.models.filter((m) => !m.contract),
};
function sources(newer = false) {
  return async (url, opts) => {
    assert.equal(opts.redirect, "error");
    assert.equal(opts.headers.authorization, undefined);
    let text = registry.models.map((m) => m.id).join(" ");
    if (url === registry.sources.discovery)
      text = "/v1/models /v1/responses Streaming";
    if (url === registry.sources.families)
      text = newer ? "gemma4 gemma5" : "gemma4";
    return new Response(text, { status: 200 });
  };
}
test("local models use the existing reviewed source update path without inventing API prices", async () => {
  const p = await checkProviders(registry, raw, { fetcher: sources() });
  assert.equal(p.status, "review-required");
  assert.equal(p.provider, "local");
  assert.equal(p.candidateRegistry.models.length, 2);
  const updated = JSON.parse(await applyReviewed(p, raw));
  for (const m of updated.models) {
    assert.equal(m.input, 0);
    assert.equal(m.output, 0);
    assert.equal(m.cached, 0);
  }
  assert.ok(Object.keys(updated.sourceHashes).length >= 4);
});
test("new local family requires adapter review and withdraws old recommendations", async () => {
  const p = await checkProviders(registry, raw, { fetcher: sources(true) });
  assert.equal(p.status, "adapter-review-required");
  assert.deepEqual(p.newModelIDs, ["gemma5"]);
  assert.deepEqual(
    p.remove,
    registry.models.map((m) => m.id),
  );
  await assert.rejects(applyReviewed(p, raw));
});
test("missing local model or changed Responses contract cannot be renewed silently", async () => {
  await assert.rejects(
    checkProviders(registry, raw, {
      fetcher: async () => new Response("empty", { status: 200 }),
    }),
  );
});

test("named local-server sources share review but cannot silently renew policy", async () => {
  const proposal = await checkProviders(fullRegistry, raw, {
    fetcher: sources(),
  });
  assert.equal(proposal.status, "manual-policy-review-required");
  assert.deepEqual(proposal.candidateRegistry, fullRegistry);
  assert.equal(proposal.candidateRegistry.models.length, 7);
  await assert.rejects(applyReviewed(proposal, raw));
  const withoutOne = async (url, options) =>
    url === fullRegistry.sources.lmstudio
      ? new Response("", { status: 404 })
      : sources()(url, options);
  await assert.rejects(
    checkProviders(fullRegistry, raw, { fetcher: withoutOne }),
  );
});
