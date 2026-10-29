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
      if (url.startsWith("https://auth.x.ai"))
        assert.equal(url, "https://auth.x.ai/.well-known/openid-configuration");
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

test("compatible providers share source review without silently renewing policy or converting currency", async () => {
  for (const provider of [
    "moonshot",
    "google",
    "openrouter",
    "qwen",
    "opencode",
  ]) {
    const raw = await readFile(
      `internal/core/provider_${provider}.json`,
      "utf8",
    );
    const registry = JSON.parse(raw);
    validateRegistry(registry);
    const proposal = await checkProviders(registry, raw, {
      fetcher: async (_url, options) => {
        assert.equal(options.redirect, "error");
        assert.equal(options.headers.authorization, undefined);
        return new Response("Official reference fixture", { status: 200 });
      },
    });
    assert.equal(proposal.status, "manual-policy-review-required");
    assert.deepEqual(proposal.candidateRegistry, registry);
    assert.ok(Object.keys(proposal.sources).length >= 1);
    await assert.rejects(applyReviewed(proposal, raw));
    assert.throws(() =>
      validateRegistry({
        ...registry,
        sources: { models: "https://unreviewed.invalid/model" },
      }),
    );
  }
});

test("Qwen authentication review is restricted to the official document", async () => {
  const raw = await readFile("internal/core/provider_qwen.json", "utf8");
  const registry = JSON.parse(raw);
  const auth =
    "https://raw.githubusercontent.com/QwenLM/qwen-code/main/docs/users/configuration/auth.md";
  assert.equal(registry.sources.authentication, auth);
  const seen = [];
  const proposal = await checkProviders(registry, raw, {
    fetcher: async (url) => {
      seen.push(url);
      return new Response("Official controlled source", { status: 200 });
    },
  });
  assert.ok(seen.includes(auth));
  assert.ok(seen.includes(registry.sources.discovery));
  assert.ok(seen.includes(registry.sources.subscription));
  assert.ok(seen.includes(registry.sources.teamSubscription));
  assert.equal(proposal.status, "manual-policy-review-required");
  await assert.rejects(applyReviewed(proposal, raw));
  for (const url of [
    auth.replace("QwenLM/qwen-code", "unreviewed/provider"),
    auth + "?redirect=1",
    auth.replace("https:", "http:"),
  ])
    assert.throws(() =>
      validateRegistry({
        ...registry,
        sources: { ...registry.sources, authentication: url },
      }),
    );
});

test("Kimi international source review preserves regional currency and cache tariff identity", async () => {
  const raw = await readFile(
    "internal/core/provider_moonshot_intl.json",
    "utf8",
  );
  const r = JSON.parse(raw);
  validateRegistry(r);
  const proposal = await checkProviders(r, raw, {
    fetcher: async () =>
      new Response("Controlled official reference", { status: 200 }),
  });
  assert.equal(proposal.status, "manual-policy-review-required");
  assert.deepEqual(proposal.candidateRegistry, r);
  await assert.rejects(applyReviewed(proposal, raw));
  for (const model of [
    { ...r.models[0], currency: "CNY" },
    { ...r.models[0], cacheWrite: 4000000 },
    { ...r.models[0], id: "kimi-k3.1-pro" },
  ])
    assert.throws(() => validateRegistry({ ...r, models: [model] }));
  assert.throws(() =>
    validateRegistry({
      ...r,
      sources: {
        ...r.sources,
        pricing: "https://platform.kimi.com/docs/pricing/chat",
      },
    }),
  );
});

test("Google auth source maintenance includes official API OAuth and CLI restriction", async () => {
  const raw = await readFile("internal/core/provider_google.json", "utf8"),
    r = JSON.parse(raw),
    seen = [];
  const p = await checkProviders(r, raw, {
    fetcher: async (url) => {
      seen.push(url);
      return new Response("Official controlled source", { status: 200 });
    },
  });
  assert.ok(seen.includes("https://ai.google.dev/gemini-api/docs/oauth"));
  assert.ok(seen.includes("https://geminicli.com/docs/resources/tos-privacy/"));
  assert.equal(p.status, "manual-policy-review-required");
  await assert.rejects(applyReviewed(p, raw));
});

test("xAI auth review pins official metadata and CLI/proxy scope without authorizing login", async () => {
  const raw = await readFile("internal/core/provider_xai.json", "utf8"),
    r = JSON.parse(raw);
  validateRegistry(r);
  assert.equal(
    r.sources.oauthMetadata,
    "https://auth.x.ai/.well-known/openid-configuration",
  );
  assert.equal(r.sources.signIn, "https://docs.x.ai/build/enterprise");
  const auth = r.sources.cliAuthentication;
  for (const bad of [
    auth.replace("xai-org/grok-build", "unreviewed/client"),
    r.sources.oauthMetadata + "?redirect=1",
    "https://auth.x.ai/oauth2/register",
    "https://docs.x.ai/build/fabricated-client-registration",
  ]) {
    assert.throws(() =>
      validateRegistry({ ...r, sources: { ...r.sources, signIn: bad } }),
    );
  }
});

test("Kimi Code source review preserves interactive membership without invented tariffs", async () => {
  const raw = await readFile("internal/core/provider_kimi_code.json", "utf8"),
    r = JSON.parse(raw);
  validateRegistry(r);
  const proposal = await checkProviders(r, raw, {
    fetcher: async (_url, options) => {
      assert.equal(options.redirect, "error");
      assert.equal(options.headers.authorization, undefined);
      return new Response("Official controlled reference", { status: 200 });
    },
  });
  assert.equal(proposal.status, "manual-policy-review-required");
  assert.deepEqual(proposal.candidateRegistry, r);
  assert.equal(
    r.sources.terms,
    "https://www.kimi.com/en/help/kimi-code/community-guidelines",
  );
  assert.deepEqual(
    r.models.map((m) => m.id),
    ["kimi-code/k3.1-pro", "kimi-code/k3.1-pro-highspeed"],
  );
  await assert.rejects(applyReviewed(proposal, raw));
  assert.throws(() =>
    validateRegistry({ ...r, models: [{ ...r.models[0], input: 1 }] }),
  );
  assert.throws(() =>
    validateRegistry({
      ...r,
      sources: { models: "https://unreviewed.invalid/models" },
    }),
  );
});

test("Anthropic workspace and subscription sources stay reviewed without instance changes", async () => {
  const raw = await readFile("internal/core/provider_anthropic.json", "utf8"),
    r = JSON.parse(raw);
  validateRegistry(r);
  assert.equal(
    r.sources.workspaceAuthentication,
    "https://platform.claude.com/docs/en/manage-claude/authentication",
  );
  assert.equal(
    r.sources.subscriptionTerms,
    "https://code.claude.com/docs/en/legal-and-compliance",
  );
  assert.throws(() =>
    validateRegistry({
      ...r,
      sources: {
        ...r.sources,
        subscriptionTerms: "https://untrusted.invalid/claude-login",
      },
    }),
  );
});

test("Ollama Cloud source review keeps quota and no-structured-output contract", async () => {
  const raw = await readFile(
      "internal/core/provider_ollama_cloud.json",
      "utf8",
    ),
    r = JSON.parse(raw);
  validateRegistry(r);
  const p = await checkProviders(r, raw, {
    fetcher: async () =>
      new Response("Controlled official reference", { status: 200 }),
  });
  assert.equal(p.status, "manual-policy-review-required");
  assert.deepEqual(p.candidateRegistry, r);
  assert.equal(
    r.sources.structuredOutputs,
    "https://docs.ollama.com/capabilities/structured-outputs",
  );
  await assert.rejects(applyReviewed(p, raw));
  assert.throws(() =>
    validateRegistry({ ...r, models: [{ ...r.models[0], input: 1 }] }),
  );
});
