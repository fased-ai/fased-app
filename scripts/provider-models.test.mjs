import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import {
  checkProviders,
  applyReviewed,
  validateRegistry,
} from "./provider-models.mjs";
const raw = await readFile("internal/core/provider_models.json", "utf8"),
  r = JSON.parse(raw);
const fixtures = () =>
  new Map([
    ...Object.values(r.sources).map((url) => [
      url,
      url === r.sources.models
        ? r.models.map((m) => m.id).join("\n")
        : url === r.sources.pricing
          ? r.models
              .map(
                (m) =>
                  `| ${m.id} | $${m.input / 1e6} | $${m.output / 1e6} | $${m.cached / 1e6} |`,
              )
              .join("\n")
          : "Official contract",
    ]),
    ...r.models.map((m) => [
      m.source,
      `Model ID: ${m.id}\nContext window: ${m.contextTokens}\nMax output: ${m.outputTokens}\n## Supported features\n- structured_outputs\n- streaming\n\n| Responses | \`v1/responses\` | Supported |`,
    ]),
  ]);
test("one reviewed registry updates source/tariff metadata without touching instances", async () => {
  const docs = fixtures();
  const p = await checkProviders(r, raw, {
    fetcher: async (url) => new Response(docs.get(url), { status: 200 }),
  });
  assert.equal(p.status, "review-required");
  assert.equal(p.newModelIDs.length, 0);
  const next = JSON.parse(await applyReviewed(p, raw));
  assert.equal(next.policy, "latest-only");
  assert.equal(next.models.length, 3);
  await assert.rejects(applyReviewed(p, raw + " "), /Registry changed/);
});
test("new generations remove old recommendations in proposal and require adapter review", async () => {
  const docs = fixtures();
  docs.set(r.sources.models, docs.get(r.sources.models) + " gpt-7-terra");
  const p = await checkProviders(r, raw, {
    fetcher: async (url) => new Response(docs.get(url), { status: 200 }),
  });
  assert.equal(p.status, "adapter-review-required");
  assert.deepEqual(p.remove, ["gpt-6.1-terra"]);
  await assert.rejects(applyReviewed(p, raw));
});
test("provider source/capability failures cannot propose a usable update", async () => {
  await assert.rejects(
    checkProviders(r, raw, {
      fetcher: async () => new Response("", { status: 404 }),
    }),
  );
  const docs = fixtures();
  docs.set(r.models[0].source, "model no longer supported");
  await assert.rejects(
    checkProviders(r, raw, {
      fetcher: async (url) => new Response(docs.get(url), { status: 200 }),
    }),
  );
  assert.throws(() =>
    validateRegistry({
      ...r,
      models: [{ ...r.models[0], id: "gpt-4.1-mini" }],
    }),
  );
});

test("empty or incomplete current catalogue cannot renew withdrawn models", async () => {
  const docs = fixtures();
  docs.set(r.sources.models, "");
  await assert.rejects(
    checkProviders(r, raw, {
      fetcher: async (url) => new Response(docs.get(url), { status: 200 }),
    }),
    /empty/,
  );
  docs.set(
    r.sources.models,
    r.models
      .slice(1)
      .map((m) => m.id)
      .join("\n"),
  );
  const p = await checkProviders(r, raw, {
    fetcher: async (url) => new Response(docs.get(url), { status: 200 }),
  });
  assert.equal(p.status, "adapter-review-required");
  assert.deepEqual(p.remove, [r.models[0].id]);
  await assert.rejects(applyReviewed(p, raw));
});
for (const provider of ["anthropic", "xai"]) {
  test(`${provider} uses the same reviewed update path and refuses capability/tariff drift`, async () => {
    const raw = await readFile(
        `internal/core/provider_${provider}.json`,
        "utf8",
      ),
      registry = JSON.parse(raw),
      docs = new Map(
        Object.values(registry.sources).map((s) => [s, "Official contract"]),
      );
    if (provider === "anthropic") {
      docs.set(
        registry.sources.models,
        `| Claude API ID | ${registry.models.map((m) => "`" + m.id + "`").join(" | ")} |\n| Context window | ${registry.models.map(() => "1M tokens").join(" | ")} |\n| Max output | ${registry.models.map(() => "128K tokens").join(" | ")} |\n| Thinking | ${registry.models.map(() => "Adaptive").join(" | ")} |`,
      );
      docs.set(
        registry.sources.pricing,
        registry.models
          .map(
            (m) =>
              `| ${m.name} | $${m.input / 1e6} | $1 | $2 | $${m.cached / 1e6} | $${m.output / 1e6} |`,
          )
          .join("\n"),
      );
      docs.set(registry.sources.contract, "json_schema adaptive");
    } else {
      docs.set(
        registry.sources.models,
        registry.models.map((m) => m.id).join("\n"),
      );
      docs.set(registry.sources.contract, "reasoning_tokens max_output_tokens");
      for (const m of registry.models)
        docs.set(
          m.source,
          `Model name: \`${m.id}\`\nContext window:** 500,000 tokens\nStructured outputs:** Yes\nReasoning efforts (supported): \`low\`\n| Input | $${m.input / 1e6} |\n| Cached input | $${m.cached / 1e6} |\n| Output | $${m.output / 1e6} |`,
        );
    }
    const fetcher = async (url) => {
      const source = docs.has(url) ? url : url.replace(/\.md$/, "");
      return new Response(docs.get(source), {
        status: docs.has(source) ? 200 : 404,
      });
    };
    const proposal = await checkProviders(registry, raw, { fetcher });
    assert.equal(proposal.status, "review-required");
    const next = JSON.parse(await applyReviewed(proposal, raw));
    assert.deepEqual(next.models, registry.models);
    if (provider === "anthropic") {
      const original = docs.get(registry.sources.models);
      docs.set(
        registry.sources.models,
        original.replace("| Thinking | Adaptive", "| Thinking | No"),
      );
      await assert.rejects(
        checkProviders(registry, raw, { fetcher }),
        /thinking changed/,
      );
      docs.set(
        registry.sources.models,
        original.replace(/\| Thinking \|[^\n]*/, "| Thinking | Adaptive |"),
      );
      await assert.rejects(
        checkProviders(registry, raw, { fetcher }),
        /thinking changed/,
      );
      docs.set(registry.sources.models, original);
    }
    docs.set(
      registry.sources.models,
      docs.get(registry.sources.models) +
        (provider === "anthropic" ? " claude-sonnet-6-0" : " grok-5.0"),
    );
    const newer = await checkProviders(registry, raw, { fetcher });
    assert.equal(newer.status, "adapter-review-required");
    assert.ok(newer.remove.length > 0);
    await assert.rejects(applyReviewed(newer, raw));
    docs.set(
      registry.sources.models,
      provider === "xai"
        ? "grok-4.7"
        : docs.get(registry.sources.models).replace(" claude-sonnet-6-0", ""),
    );
    docs.set(
      registry.sources.contract,
      "Contract no longer supports this adapter",
    );
    await assert.rejects(checkProviders(registry, raw, { fetcher }));
  });
}
