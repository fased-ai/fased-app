import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const digest = (value) =>
  createHash("sha256").update(value).digest("hex");
export function validateRegistry(r) {
  assert.equal(r.version, 1);
  assert.equal(r.policy, "latest-only");
  assert.ok(["openai", "anthropic", "xai", "local"].includes(r.provider));
  assert.ok(
    r.expiresAt > r.reviewedAt && r.expiresAt - r.reviewedAt <= 31 * 86400,
  );
  assert.ok(r.models.length > 0 && r.models.length <= 16);
  const ids = new Set();
  for (const m of r.models) {
    assert.match(
      m.id,
      r.provider === "local"
        ? /^gemma\d+:e[24]b$/
        : r.provider === "openai"
          ? /^gpt-\d+(?:\.\d+)?-(?:sol|terra|astra)$/
          : r.provider === "anthropic"
            ? /^claude-(?:haiku|sonnet|opus|fable)-\d+-\d+$/
            : /^grok-\d+(?:\.\d+)?$/,
    );
    if (r.provider !== "openai") assert.equal(m.provider, r.provider);
    if (r.provider === "xai")
      assert.equal(m.billableOutputTokens, m.contextTokens);
    assert.ok(!ids.has(m.id));
    ids.add(m.id);
    assert.ok(
      m.contextTokens >= 32000 &&
        m.outputTokens >= 1024 &&
        (r.provider === "local"
          ? m.input === 0 && m.output === 0 && m.cached === 0
          : m.input > 0 && m.output > 0) &&
        m.cached >= 0 &&
        m.cached <= m.input,
    );
    assert.ok(["none", "low"].includes(m.reasoning));
    if (r.provider === "openai")
      assert.equal(
        m.source,
        "https://developers.openai.com/api/docs/models/" + m.id + ".md",
      );
    sourceAllowed(r.provider, m.source);
  }
  for (const source of Object.values(r.sources))
    sourceAllowed(r.provider, source);
}
function sourceAllowed(provider, source) {
  const u = new URL(source);
  assert.ok(!u.username && !u.password && !u.search && !u.hash);
  if (provider === "local") {
    assert.ok(
      (u.origin === "https://ollama.com" &&
        (u.pathname.startsWith("/library/") || u.pathname === "/search")) ||
        (u.origin === "https://docs.ollama.com" &&
          u.pathname === "/api/openai-compatibility"),
    );
    return;
  }
  const origin = {
    openai: "https://developers.openai.com",
    anthropic: "https://platform.claude.com",
    xai: "https://docs.x.ai",
  }[provider];
  assert.equal(u.origin, origin);
  assert.ok(
    u.pathname.startsWith(
      provider === "openai"
        ? "/api/"
        : provider === "anthropic"
          ? "/docs/en/"
          : "/developers/",
    ),
  );
}
function modelParts(provider, id) {
  if (provider === "anthropic") {
    const [, family, major, minor] = /^claude-([a-z]+)-(\d+)-(\d+)$/.exec(id);
    return { family, generation: [+major, +minor] };
  }
  if (provider === "xai")
    return { family: "grok", generation: id.slice(5).split(".").map(Number) };
  return {
    family: id.split("-").at(-1),
    generation: id.split("-")[1].split(".").map(Number),
  };
}
const tokens = (s) => {
  const m = /([\d,.]+)\s*([MK])?/i.exec(s);
  assert.ok(m, "Token limit unavailable");
  return (
    Number(m[1].replaceAll(",", "")) *
    (m[2]?.toUpperCase() === "M" ? 1e6 : m[2]?.toUpperCase() === "K" ? 1e3 : 1)
  );
};
function nativePolicy(r, m, snapshots) {
  if (r.provider === "anthropic") {
    const overview = snapshots[r.sources.models].body;
    const row = (label) =>
      overview.split("\n").find((s) => s.startsWith("| " + label));
    const api = row("Claude API ID");
    assert.ok(api, "Claude API IDs unavailable");
    const index = api
      .split("|")
      .findIndex((cell) => cell.includes("`" + m.id + "`"));
    assert.ok(index > 0, "Claude identity unavailable");
    const context = row("[Context window]") || row("Context window"),
      output = row("Max output");
    assert.ok(
      context &&
        output &&
        /^\s*Adaptive(?: \(always on\))?\s*$/.test(
          (row("[Thinking]") || row("Thinking") || "").split("|")[index] || "",
        ),
      "Messages limits/thinking changed",
    );
    m.contextTokens = tokens(context.split("|")[index]);
    m.outputTokens = tokens(output.split("|")[index]);
    const tariff = snapshots[r.sources.pricing].body
      .split("\n")
      .find((s) => s.startsWith("| " + m.name + " "));
    assert.ok(tariff, "Claude tariff unavailable");
    const prices = [...tariff.matchAll(/\$([\d.]+)/g)].map((x) =>
      Math.round(Number(x[1]) * 1e6),
    );
    assert.equal(prices.length, 5, "Claude tariff columns changed");
    [m.input, , , m.cached, m.output] = prices;
    assert.ok(
      /structured_outputs|json_schema/.test(
        snapshots[r.sources.contract].body,
      ) && /adaptive/.test(snapshots[r.sources.contract].body),
      "Messages contract changed",
    );
  } else {
    const doc = snapshots[m.source].body;
    assert.ok(
      doc.includes("`" + m.id + "`") &&
        /Structured outputs:\*\* Yes/.test(doc) &&
        /Reasoning efforts[^\n]*`low`/.test(doc),
      "Grok capabilities changed",
    );
    const context = /Context window:\*\* ([\d,]+)/.exec(doc);
    assert.ok(context, "Grok context unavailable");
    m.contextTokens = tokens(context[1]);
    m.outputTokens = m.contextTokens;
    m.billableOutputTokens = m.contextTokens;
    const price = (name) => {
      const row = doc.split("\n").find((s) => s.startsWith("| " + name + " |"));
      assert.ok(row, "Grok tariff unavailable");
      const match = /\$([\d.]+)/.exec(row);
      assert.ok(match);
      return Math.round(Number(match[1]) * 1e6);
    };
    m.input = price("Input");
    m.cached = price("Cached input");
    m.output = price("Output");
    assert.ok(
      /reasoning_tokens/.test(snapshots[r.sources.contract].body) &&
        /max_output_tokens/.test(snapshots[r.sources.contract].body),
      "Responses accounting contract changed",
    );
  }
}

export async function checkProviders(
  r,
  raw,
  { fetcher = fetch, now = Date.now() } = {},
) {
  validateRegistry(r);
  if (r.provider === "local") return checkLocalModels(r, raw, { fetcher, now });
  const snapshots = {};
  for (const url of new Set([
    ...Object.values(r.sources),
    ...r.models.filter(() => r.provider !== "anthropic").map((m) => m.source),
  ])) {
    // Native documentation publishes its machine-readable page at the official .md link.
    const retrievedFrom =
      r.provider !== "openai" && !url.endsWith(".md") ? url + ".md" : url;
    sourceAllowed(r.provider, retrievedFrom);
    let response;
    try {
      response = await fetcher(retrievedFrom, {
        redirect: "error",
        signal: AbortSignal.timeout(10000),
        headers: { accept: "text/markdown" },
      });
    } catch (error) {
      throw new Error(
        "Official source fetch failed: " +
          url +
          " (" +
          (error.cause?.code || error.name) +
          ")",
      );
    }
    assert.equal(response.status, 200, "Official source unavailable: " + url);
    const reader = response.body.getReader();
    const chunks = [];
    let size = 0;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.length;
        assert.ok(size <= 1048576, "Official source too large");
        chunks.push(value);
      }
    } finally {
      await reader.cancel();
    }
    const body = Buffer.concat(chunks).toString("utf8");
    snapshots[url] = { sha256: digest(body), body, retrievedFrom };
  }
  const catalogue = snapshots[r.sources.models].body;
  const listed = [
    ...new Set(
      catalogue.match(
        r.provider === "openai"
          ? /gpt-\d+(?:\.\d+)?-(?:sol|terra|astra)\b/g
          : r.provider === "anthropic"
            ? /claude-(?:haiku|sonnet|opus|fable)-\d+-\d+\b/g
            : /grok-\d+(?:\.\d+)?\b/g,
      ) ?? [],
    ),
  ];
  assert.ok(
    listed.length > 0,
    "Current model catalogue is empty or unrecognized",
  );
  const withdrawn = r.models
    .filter((m) => !listed.includes(m.id))
    .map((m) => m.id);
  const generation = (id) => modelParts(r.provider, id).generation;
  const newerThan = (a, b) => {
    const x = generation(a),
      y = generation(b);
    return x[0] > y[0] || (x[0] === y[0] && (x[1] ?? 0) > (y[1] ?? 0));
  };
  const family = (id) => modelParts(r.provider, id).family;
  const newer = listed.filter((id) =>
    r.models.some((m) => family(m.id) === family(id) && newerThan(id, m.id)),
  );
  const removals = r.models
    .filter(
      (m) =>
        withdrawn.includes(m.id) ||
        newer.some((id) => family(id) === family(m.id)),
    )
    .map((m) => m.id);
  const candidate = structuredClone(r);
  candidate.reviewedAt = Math.floor(now / 86400000) * 86400;
  candidate.expiresAt = candidate.reviewedAt + 30 * 86400;
  candidate.models = candidate.models.filter((m) => !removals.includes(m.id));
  const pricing = snapshots[r.sources.pricing].body;
  for (const m of candidate.models) {
    if (r.provider !== "openai") {
      nativePolicy(r, m, snapshots);
      continue;
    }
    const doc = snapshots[m.source].body;
    assert.ok(
      doc.replaceAll("`", "").includes("Model ID: " + m.id),
      "Model identity changed: " + m.id,
    );
    assert.ok(
      /## Supported features[\s\S]*?- structured_outputs/.test(doc) &&
        /## Supported features[\s\S]*?- streaming/.test(doc) &&
        /\| Responses \| `v1\/responses` \| Supported \|/.test(doc),
      "Adapter capabilities changed: " + m.id,
    );
    const context =
        doc.match(/Context window:\s*([\d,]+)/) ??
        doc.match(/([\d,]+) context window/),
      output =
        doc.match(/Max output:\s*([\d,]+)/) ??
        doc.match(/([\d,]+) max output tokens/);
    assert.ok(context && output, "Model limits unavailable: " + m.id);
    m.contextTokens = Number(context[1].replaceAll(",", ""));
    m.outputTokens = Number(output[1].replaceAll(",", ""));
    const row = pricing
      .split("\n")
      .find((l) => l.startsWith("| " + m.id + " |"));
    assert.ok(row, "Standard tariff unavailable: " + m.id);
    const prices = [...row.matchAll(/\$([\d.]+)/g)].map((x) =>
      Math.round(Number(x[1]) * 1000000),
    );
    assert.ok(prices.length >= 3, "Standard tariff malformed: " + m.id);
    [m.input, m.output, m.cached] = prices;
  }
  const sources = Object.fromEntries(
    Object.entries(snapshots).map(([url, s]) => [
      url,
      {
        sha256: s.sha256,
        retrievedFrom: s.retrievedFrom,
        changed: r.sourceHashes?.[url] !== s.sha256,
      },
    ]),
  );
  candidate.sourceHashes = Object.fromEntries(
    Object.entries(sources).map(([url, s]) => [url, s.sha256]),
  );
  return {
    version: 1,
    provider: r.provider,
    policy: r.policy,
    beforeSHA256: digest(raw),
    checkedAt: Math.floor(now / 1000),
    status:
      newer.length || withdrawn.length
        ? "adapter-review-required"
        : "review-required",
    newModelIDs: newer,
    remove: removals,
    sources,
    candidateRegistry: candidate,
  };
}
export async function checkLocalModels(
  r,
  raw,
  { fetcher = fetch, now = Date.now() } = {},
) {
  validateRegistry(r);
  const snapshots = {};
  for (const url of new Set([
    ...Object.values(r.sources),
    ...r.models.map((m) => m.source),
  ])) {
    sourceAllowed("local", url);
    const response = await fetcher(url, {
      redirect: "error",
      signal: AbortSignal.timeout(10000),
      headers: { accept: "text/html,text/markdown" },
    });
    assert.equal(response.status, 200, "Local model source unavailable");
    const reader = response.body.getReader();
    const parts = [];
    let size = 0;
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.length;
        assert.ok(size <= 1048576, "Local model source too large");
        parts.push(value);
      }
    } finally {
      await reader.cancel();
    }
    const body = Buffer.concat(parts).toString("utf8");
    snapshots[url] = { body, sha256: digest(body), retrievedFrom: url };
  }
  const doc = snapshots[r.sources.discovery].body;
  assert.ok(
    doc.includes("/v1/responses") &&
      doc.includes("/v1/models") &&
      /streaming/i.test(doc),
    "Local adapter contract changed",
  );
  const catalogue = snapshots[r.sources.models].body;
  const newer = [
    ...new Set(snapshots[r.sources.families].body.match(/gemma\d+/g) ?? []),
  ].filter((id) =>
    r.models.some(
      (m) => Number(id.slice(5)) > Number(m.id.match(/^gemma(\d+)/)[1]),
    ),
  );
  const removals = r.models
    .filter((m) => !catalogue.includes(m.id) || newer.length)
    .map((m) => m.id);
  const candidate = structuredClone(r);
  candidate.reviewedAt = Math.floor(now / 86400000) * 86400;
  candidate.expiresAt = candidate.reviewedAt + 30 * 86400;
  candidate.models = candidate.models.filter((m) => !removals.includes(m.id));
  for (const m of candidate.models) {
    assert.ok(
      snapshots[m.source].body.includes(m.id),
      "Local model identity changed",
    );
    // Never invent a remote API price or increase installed-server limits.
    assert.equal(m.input, 0);
    assert.equal(m.output, 0);
    assert.equal(m.cached, 0);
  }
  const sources = Object.fromEntries(
    Object.entries(snapshots).map(([url, s]) => [
      url,
      {
        sha256: s.sha256,
        retrievedFrom: s.retrievedFrom,
        changed: r.sourceHashes?.[url] !== s.sha256,
      },
    ]),
  );
  candidate.sourceHashes = Object.fromEntries(
    Object.entries(sources).map(([url, s]) => [url, s.sha256]),
  );
  return {
    version: 1,
    provider: "local",
    policy: r.policy,
    beforeSHA256: digest(raw),
    checkedAt: Math.floor(now / 1000),
    status:
      newer.length || removals.length
        ? "adapter-review-required"
        : "review-required",
    newModelIDs: newer,
    remove: removals,
    sources,
    candidateRegistry: candidate,
  };
}
export async function applyReviewed(proposal, raw) {
  assert.equal(proposal.version, 1);
  assert.equal(
    proposal.beforeSHA256,
    digest(raw),
    "Registry changed since review",
  );
  assert.equal(proposal.status, "review-required");
  assert.equal(
    proposal.newModelIDs.length,
    0,
    "New models need adapter review",
  );
  assert.ok(
    Math.abs(Date.now() / 1000 - proposal.checkedAt) < 86400,
    "Source review expired",
  );
  validateRegistry(proposal.candidateRegistry);
  return JSON.stringify(proposal.candidateRegistry, null, 2) + "\n";
}
const registryPaths = {
  openai: "internal/core/provider_models.json",
  anthropic: "internal/core/provider_anthropic.json",
  xai: "internal/core/provider_xai.json",
  local: "internal/core/provider_local.json",
};
async function main() {
  const records = await Promise.all(
    Object.entries(registryPaths).map(async ([provider, file]) => {
      const raw = await readFile(path.resolve(file), "utf8");
      return { provider, file, raw, r: JSON.parse(raw) };
    }),
  );
  if (process.argv[2] === "--apply-reviewed") {
    const proposal = JSON.parse(await readFile(process.argv[3], "utf8"));
    const proposals = proposal.providers || [proposal];
    assert.equal(
      new Set(proposals.map((p) => p.provider)).size,
      proposals.length,
    );
    const edits = [];
    // Validate the whole review against current source before any source write.
    for (const p of proposals) {
      const record = records.find((r) => r.provider === p.provider);
      assert.ok(record, "Unknown provider");
      assert.equal(p.candidateRegistry.provider, record.provider);
      edits.push({
        file: record.file,
        next: await applyReviewed(p, record.raw),
      });
    }
    for (const edit of edits)
      await writeFile(path.resolve(edit.file), edit.next, { mode: 0o644 });
    process.stdout.write(
      "Provider registries updated locally. Review the diff and affected checks before delivery.\n",
    );
    return;
  }
  assert.ok(
    process.argv.length === 2 || process.argv[2] === "--check",
    "Use --check or --apply-reviewed <proposal>",
  );
  const providers = [];
  for (const record of records)
    providers.push(await checkProviders(record.r, record.raw));
  const proposal = { version: 1, providers };
  const out = path.resolve(
    ".evidence/provider-models/provider-update-proposal.json",
  );
  await mkdir(path.dirname(out), { recursive: true });
  await writeFile(out, JSON.stringify(proposal, null, 2) + "\n", {
    mode: 0o600,
  });
  process.stdout.write(
    JSON.stringify({
      proposal: out,
      providers: providers.map(({ provider, status, newModelIDs, remove }) => ({
        provider,
        status,
        newModelIDs,
        remove,
      })),
    }) + "\n",
  );
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main().catch((error) => {
    process.stderr.write(
      "Provider check refused: " +
        String(error.message).replace(/\s+/g, " ").slice(0, 512) +
        "\n",
    );
    process.exitCode = 1;
  });
}
