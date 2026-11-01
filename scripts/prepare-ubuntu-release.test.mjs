import { strict as assert } from "node:assert";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import {
  bindingOverlay,
  candidateInventory,
  expectedProvenance,
  releaseContext,
  validateConfig,
  verifiedCandidate,
} from "./prepare-ubuntu-release.mjs";

const rawConfig = JSON.parse(
  readFileSync(new URL("../release/ubuntu.json", import.meta.url)),
);
const sha = (v) => createHash("sha256").update(v).digest("hex");
const copy = (v) => structuredClone(v);
const complete = () => ({
  ...rawConfig,
  seedFile: "release/public/ubuntu-amd64.json",
  seedSha256: "a".repeat(64),
  custodyApprovalSha256: "b".repeat(64),
  initialTrustApprovalSha256: "c".repeat(64),
});
const source = {
  commit: "d".repeat(40),
  workflowCommit: "d".repeat(40),
  tree: "e".repeat(40),
  tagObject: "f".repeat(40),
  onMain: true,
  clean: true,
};
const env = {
  GITHUB_EVENT_NAME: "push",
  GITHUB_REF: "refs/tags/v0.1.0",
  GITHUB_REPOSITORY: rawConfig.repository,
  GITHUB_REPOSITORY_ID: rawConfig.repositoryId,
  GITHUB_REPOSITORY_OWNER_ID: rawConfig.ownerId,
  GITHUB_WORKFLOW_REF: `${rawConfig.repository}/.github/workflows/release.yml@refs/tags/v0.1.0`,
  GITHUB_SHA: source.commit,
  GITHUB_RUN_ID: "12",
  GITHUB_RUN_ATTEMPT: "1",
  RUNNER_OS: "Linux",
  RUNNER_ARCH: "X64",
};

test("unapproved production trust stays absent; configuration cannot redirect authority", () => {
  assert.deepEqual(validateConfig(rawConfig), [
    "seedFile",
    "seedSha256",
    "custodyApprovalSha256",
    "initialTrustApprovalSha256",
  ]);
  assert.deepEqual(validateConfig(complete()), []);
  for (const patch of [
    { version: "0.1.0-rc.1" },
    { version: "00.1.0" },
    { repository: "other/app" },
    { repositoryId: "7" },
    { ownerId: "3" },
    { workflow: "other.yml" },
    { target: { os: "linux", arch: "arm64", ubuntu: "24.04" } },
    { seedFile: "../../secret" },
    { seedSha256: "bad" },
    { privateKey: "never" },
    { target: { ...rawConfig.target, extra: true } },
  ])
    assert.throws(() => validateConfig({ ...complete(), ...patch }));
});

test("only the exact clean annotated main-merged tag-push can build", () => {
  assert.equal(releaseContext(complete(), env, source).tag, "v0.1.0");
  for (const patch of [
    { GITHUB_EVENT_NAME: "workflow_dispatch" },
    { GITHUB_EVENT_NAME: "pull_request" },
    { GITHUB_REF: "refs/heads/main" },
    { GITHUB_REF: "refs/tags/v0.1.1" },
    { GITHUB_REPOSITORY_ID: "7" },
    { GITHUB_REPOSITORY_OWNER_ID: "3" },
    { GITHUB_SHA: "a".repeat(40) },
    { GITHUB_RUN_ATTEMPT: "0" },
    { RUNNER_OS: "Windows" },
    { RUNNER_ARCH: "ARM64" },
    { GITHUB_WORKFLOW_REF: "other" },
  ])
    assert.throws(() =>
      releaseContext(complete(), { ...env, ...patch }, source),
    );
  for (const patch of [
    { onMain: false },
    { clean: false },
    { clean: "false" },
    { tagObject: "" },
    { workflowCommit: "a".repeat(40) },
  ])
    assert.throws(() =>
      releaseContext(complete(), env, { ...source, ...patch }),
    );
});

test("build overlay embeds only the exact approved public seed in both binaries", () => {
  const seed = Buffer.from('{"public":"test-only"}');
  const bindings = bindingOverlay(seed, sha(seed), "/fixture");
  assert.deepEqual(
    bindings.map((b) => b.target),
    [
      "/fixture/cmd/fased-app/publisher_seed_generated.go",
      "/fixture/cmd/fased-install/publisher_seed_generated.go",
    ],
  );
  for (const b of bindings) {
    assert.ok(b.source.includes(seed.toString("base64")));
    assert.ok(b.source.includes(sha(seed)));
    assert.match(b.source, /func init\(\)/);
    assert.doesNotMatch(b.source, /os\.Getenv|https:|privateKey/);
  }
  assert.throws(() => bindingOverlay(seed, "a".repeat(64), "/fixture"));
});

const archive = {
  file: "fased-app-linux-amd64.tar.gz",
  size: 12,
  sha256: "a".repeat(64),
};
const installer = {
  file: "fased-install-linux-amd64",
  size: 13,
  sha256: "b".repeat(64),
};
const input = {
  ...releaseContext(complete(), env, source),
  seedSha256: complete().seedSha256,
  custodyApprovalSha256: complete().custodyApprovalSha256,
  initialTrustApprovalSha256: complete().initialTrustApprovalSha256,
};
const report = {
  publicRelease: false,
  manifest: {
    schema: 2,
    unsignedEngineering: false,
    desktop: false,
    platform: "linux",
    arch: "amd64",
    version: input.version,
    source: input.sourceCommit,
  },
  identity: { size: archive.size, sha256: archive.sha256 },
  manifestIdentity: { sha256: "c".repeat(64) },
};

test("actual package identities and provenance expectations cannot drift apart", () => {
  assert.equal(
    candidateInventory(input, archive, installer, report).archive.sha256,
    archive.sha256,
  );
  const expected = expectedProvenance(input, archive);
  assert.equal(expected.archiveSha256, archive.sha256);
  assert.equal(expected.sourceCommit, input.sourceCommit);
  assert.equal(expected.runId, "12");
  for (const patch of [
    { version: "0.1.1" },
    { source: "b".repeat(40) },
    { schema: 1 },
    { unsignedEngineering: true },
    { desktop: true },
    { arch: "arm64" },
    { platform: "windows" },
  ])
    assert.throws(() =>
      candidateInventory(input, archive, installer, {
        ...report,
        manifest: { ...report.manifest, ...patch },
      }),
    );
  assert.throws(() =>
    candidateInventory(
      input,
      { ...archive, sha256: "d".repeat(64) },
      installer,
      report,
    ),
  );
  assert.throws(() =>
    candidateInventory(
      input,
      archive,
      { ...installer, file: archive.file },
      report,
    ),
  );
});

test("staging requires actual verifier timestamp enums, exact policy and tag identity", () => {
  const policy = {
    version: 1,
    repository: rawConfig.repository,
    repositoryId: rawConfig.repositoryId,
    ownerId: rawConfig.ownerId,
    workflow: rawConfig.workflow,
    trustedRoot: "public-fixture",
  };
  const proof = { sha256: "e".repeat(64) };
  const objects = candidateInventory(input, archive, installer, report);
  const decision = {
    BundleSHA256: proof.sha256,
    PolicySHA256: sha(JSON.stringify(policy)),
    Identity: `https://github.com/${rawConfig.repository}/${rawConfig.workflow}@refs/tags/v0.1.0`,
    TimestampSource: "Tlog",
    VerifiedAt: "2026-11-01T00:00:00Z",
  };
  for (const TimestampSource of ["Tlog", "TimestampAuthority"])
    assert.doesNotThrow(() =>
      verifiedCandidate(
        complete(),
        input,
        objects,
        { ...decision, TimestampSource },
        proof,
        policy,
      ),
    );
  for (const patch of [
    { BundleSHA256: "f".repeat(64) },
    { PolicySHA256: "f".repeat(64) },
    { Identity: decision.Identity.replace("v0.1.0", "v0.1.1") },
    { TimestampSource: "transparency-log" },
    { VerifiedAt: "invalid" },
  ])
    assert.throws(() =>
      verifiedCandidate(
        complete(),
        input,
        objects,
        { ...decision, ...patch },
        proof,
        policy,
      ),
    );
  assert.throws(() =>
    verifiedCandidate(rawConfig, input, objects, decision, proof, policy),
  );
  assert.throws(() =>
    verifiedCandidate(
      complete(),
      { ...input, seedSha256: "d".repeat(64) },
      objects,
      decision,
      proof,
      policy,
    ),
  );
  const altered = copy(policy);
  altered.trustedRoot = "substituted";
  assert.throws(() =>
    verifiedCandidate(complete(), input, objects, decision, proof, altered),
  );
});

test("dormant staging has one pinned Ubuntu build and no release or activation authority", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  assert.match(workflow, /push:\n\s+tags: \["v\*"\]/);
  assert.doesNotMatch(
    workflow,
    /workflow_dispatch|pull_request|contents: write|secrets\.|gh release|git push|sudo|activate/,
  );
  assert.equal((workflow.match(/runs-on:/g) ?? []).length, 1);
  assert.match(workflow, /runs-on: ubuntu-24\.04/);
  for (const line of workflow.split("\n").filter((s) => s.includes("uses:")))
    assert.match(line, /uses: actions\/[a-z-]+@[a-f0-9]{40}(?: |$)/);
  assert.ok(workflow.indexOf(".mjs prepare") < workflow.indexOf("npm ci"));
  assert.match(workflow, /GOFLAGS: -mod=readonly -overlay=/);
  assert.equal((workflow.match(/npm run build/g) ?? []).length, 1);
  assert.match(
    workflow,
    /subject-path: \.evidence\/ubuntu-release\/fased-app-linux-amd64.tar.gz/,
  );
  assert.match(workflow, /verify-provenance --policy-file/);
  assert.match(workflow, /create-storage-record: false/);
  assert.ok(
    workflow.indexOf("verify-provenance --policy-file") <
      workflow.indexOf(".mjs finish"),
  );
  assert.match(
    workflow,
    /if: \$\{\{ always\(\) && steps\.prepare\.outcome == 'success' \}\}\n\s+uses: actions\/upload-artifact@/,
  );
  const checks = readFileSync(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  assert.match(
    checks,
    /node --test --test-isolation=none scripts\/prepare-ubuntu-release.test.mjs/,
  );
  assert.match(
    checks,
    /go list [^\n]+\.\/cmd\/fased-release-candidate > \.evidence\/core-test-packages.txt/,
  );
});

// Single-owner publisher uses explicit injected fixture transport; no live API.
import {
  publicationInput,
  publishFinalized,
  stageCandidate,
  releaseCandidate,
  githubPublisher,
} from "./publish-ubuntu-release.mjs";
const psha = (b) => createHash("sha256").update(b).digest("hex");
function publisherFixture() {
  const descriptor = {
    version: 3,
    purpose: "fased-app-first-install-production:v1",
    developmentOnly: false,
    repository: "fased-ai/fased-app",
    channel: "stable",
    target: { os: "linux", arch: "amd64" },
    sequence: "7",
    productVersion: "0.1.0",
    source: "a".repeat(40),
    release: { tagObject: "9".repeat(40) },
    github: {
      repositoryId: "1426211857",
      releaseId: "11",
      assetId: "17",
      name: "fased-app-linux-amd64.tar.gz",
      size: 7,
      sha256: psha(Buffer.from("archive")),
    },
  };
  const encoded = (v) => Buffer.from(JSON.stringify(v)).toString("base64");
  const meta = (version = 1) => ({
    signed: { version, expires: new Date(Date.now() + 86400000).toISOString() },
  });
  const db = Buffer.from(JSON.stringify(descriptor));
  const publication = {
    version: 1,
    signingProfile: "single-owner-v1",
    repository: descriptor.repository,
    repositoryId: "1426211857",
    ownerId: "279582504",
    metadataBranch: "release-metadata",
    seedSha256: "b".repeat(64),
    planSha256: "c".repeat(64),
    descriptor,
    files: {
      "1.root.json": encoded(meta()),
      "1.targets.json": encoded(meta()),
      "1.snapshot.json": encoded(meta()),
      "timestamp.json": encoded(meta()),
      [`targets/production-bootstrap/${psha(db)}.descriptor.json`]:
        db.toString("base64"),
      [`targets/provenance/${"d".repeat(64)}.sigstore.json`]: encoded({
        fixture: true,
      }),
    },
  };
  const raw = Buffer.from(JSON.stringify(publication));
  const approval = { sha256: psha(raw), previousCommit: "1".repeat(40) };
  const calls = [],
    records = [];
  let head = approval.previousCommit;
  const candidate = {
    repository: publication.repository,
    repositoryId: publication.repositoryId,
    ownerId: publication.ownerId,
    version: "0.1.0",
    source: descriptor.source,
    tagObject: descriptor.release.tagObject,
    assets: [
      "fased-app-linux-amd64.tar.gz",
      "fased-install-linux-amd64",
      "fased-app-linux-amd64.provenance.sigstore.json",
      "install.sh",
    ].map((name, i) => ({
      name,
      size: i === 0 ? 7 : 1,
      sha256: i === 0 ? descriptor.github.sha256 : psha(Buffer.from("x")),
    })),
  };
  const assets = candidate.assets.map((a, i) => ({
    ...a,
    id: 17 + i,
    state: "uploaded",
    digest: `sha256:${a.sha256}`,
  }));
  let createdTree = [];
  const api = async (method, path, body) => {
    calls.push({ method, path, body });
    if (path.endsWith("/fased-app"))
      return {
        id: 1426211857,
        full_name: publication.repository,
        owner: { id: 279582504 },
      };
    if (path.endsWith("/releases/11"))
      return {
        id: 11,
        draft: false,
        prerelease: false,
        immutable: true,
        tag_name: "v0.1.0",
        assets,
      };
    if (path.includes("/git/ref/tags/"))
      return {
        ref: "refs/tags/v0.1.0",
        object: { type: "tag", sha: candidate.tagObject },
      };
    if (path.includes("/git/tags/"))
      return {
        sha: candidate.tagObject,
        tag: "v0.1.0",
        object: { type: "commit", sha: candidate.source },
      };
    if (path.includes("/git/ref/"))
      return {
        ref: "refs/heads/release-metadata",
        object: { type: "commit", sha: head },
      };
    if (method === "GET" && path.includes("/git/commits/")) {
      if (path.endsWith(approval.previousCommit))
        return { sha: approval.previousCommit, tree: { sha: "2".repeat(40) } };
      return {
        sha: "4".repeat(40),
        tree: { sha: "3".repeat(40) },
        parents: [{ sha: approval.previousCommit }],
      };
    }
    if (method === "GET" && path.includes("/git/trees/"))
      return {
        truncated: false,
        tree: path.includes("2".repeat(40)) ? [] : createdTree,
      };
    if (method === "POST" && path.endsWith("/git/blobs")) {
      const bytes = Buffer.from(body.content, "base64");
      return {
        sha: createHash("sha1")
          .update(`blob ${bytes.length}\0`)
          .update(bytes)
          .digest("hex"),
      };
    }
    if (method === "POST" && path.endsWith("/git/trees")) {
      createdTree = body.tree;
      return { sha: "3".repeat(40) };
    }
    if (method === "POST" && path.endsWith("/git/commits"))
      return { sha: "4".repeat(40) };
    if (method === "PATCH") {
      assert.equal(body.force, false);
      head = body.sha;
      return { object: { sha: head } };
    }
    throw Error("unexpected fixture request");
  };
  return {
    publication,
    raw,
    approval,
    calls,
    records,
    api,
    candidate,
    assets,
    record: async (x) => records.push(x),
  };
}
test("single owner publishes finalized metadata with non-force ref and readback", async () => {
  const f = publisherFixture();
  const r = await publishFinalized(f.raw, f.approval, f.api, f.record);
  assert.equal(r.activation, false);
  assert.equal(f.calls.filter((x) => x.method === "PATCH").length, 1);
  assert.equal(f.records.at(-1).phase, "metadata-published");
});
test("publisher refuses changed input, identity, expiry, draft, history and concurrent head", async () => {
  for (const variant of [
    "pin",
    "identity",
    "expired",
    "draft",
    "history",
    "race",
    "record",
  ]) {
    const f = publisherFixture();
    let raw = f.raw;
    let approval = f.approval;
    if (variant === "pin") raw = Buffer.concat([raw, Buffer.from(" ")]);
    if (variant === "expired") {
      f.publication.files["timestamp.json"] = Buffer.from(
        JSON.stringify({ signed: { expires: "2000-01-01T00:00:00Z" } }),
      ).toString("base64");
      raw = Buffer.from(JSON.stringify(f.publication));
      approval = { ...approval, sha256: psha(raw) };
    }
    let refs = 0;
    const api = async (m, p, b) => {
      const r = await f.api(m, p, b);
      if (variant === "identity" && p.endsWith("/fased-app")) r.id = 9;
      if (variant === "draft" && p.includes("/releases/")) r.draft = true;
      if (variant === "history" && p.includes("/git/trees/"))
        r.tree = [
          {
            path: "1.root.json",
            sha: "9".repeat(40),
            type: "blob",
            mode: "100644",
          },
        ];
      if (variant === "race" && p.includes("/git/ref/heads/") && ++refs === 2)
        r.object.sha = "9".repeat(40);
      return r;
    };
    await assert.rejects(
      publishFinalized(
        raw,
        approval,
        api,
        variant === "record"
          ? async () => {
              throw Error("disk full");
            }
          : f.record,
      ),
      variant,
    );
    assert.equal(
      f.calls.filter((x) => x.method === "PATCH").length,
      0,
      variant,
    );
  }
  assert.throws(() => githubPublisher(""));
});
test("candidate stages exact bytes as draft only; failed upload is not replayed", async () => {
  for (const failure of [false, true]) {
    const f = publisherFixture(),
      c = Buffer.from(JSON.stringify(f.candidate));
    let uploads = 0;
    const api = async (m, p, b) => {
      if (m === "GET") return f.api(m, p, b);
      if (m === "POST") return { id: 11, draft: true, tag_name: "v0.1.0" };
      if (m === "UPLOAD") {
        uploads++;
        if (failure) throw Error("connection lost");
        return f.assets.find((x) => p.endsWith(`name=${x.name}`));
      }
      throw Error("unexpected mutation");
    };
    const op = stageCandidate(
      c,
      psha(c),
      async (name) => Buffer.from(name.endsWith(".tar.gz") ? "archive" : "x"),
      api,
      f.record,
    );
    if (failure) {
      await assert.rejects(op);
      assert.equal(uploads, 1);
      assert.equal(f.records.at(-1).phase, "asset-upload-attempted");
    } else {
      const staged = await op;
      assert.equal(staged.published, false);
      assert.equal(staged.assets.length, 4);
    }
  }
});
test("only exact finalized complete draft may become immutable release", async () => {
  const f = publisherFixture(),
    raw = Buffer.from(JSON.stringify(f.candidate));
  let patches = 0;
  const api = async (m, p, b) => {
    if (m === "PATCH") {
      patches++;
      return { id: 11, draft: false, immutable: true };
    }
    if (p.endsWith("/releases/11"))
      return {
        id: 11,
        draft: true,
        prerelease: false,
        tag_name: "v0.1.0",
        assets: f.assets,
      };
    return f.api(m, p, b);
  };
  await releaseCandidate(raw, psha(raw), f.raw, f.approval, api, f.record);
  assert.equal(patches, 1);
  f.assets[1].digest = "sha256:" + "0".repeat(64);
  await assert.rejects(
    releaseCandidate(raw, psha(raw), f.raw, f.approval, api, f.record),
  );
  assert.equal(patches, 1);
});
test("publisher snapshots approvals and refuses wrong blobs/commits/tag before authority", async () => {
  for (const variant of ["approval-mutation", "blob", "commit", "tag"]) {
    const f = publisherFixture();
    const api = async (m, p, b) => {
      if (variant === "approval-mutation" && p.endsWith("/fased-app"))
        f.approval.previousCommit = "8".repeat(40);
      const r = await f.api(m, p, b);
      if (variant === "approval-mutation" && p.includes("/git/ref/heads/"))
        r.object.sha = "8".repeat(40);
      if (variant === "blob" && m === "POST" && p.endsWith("/git/blobs"))
        r.sha = "0".repeat(40);
      if (
        variant === "commit" &&
        m === "GET" &&
        p.endsWith("/git/commits/" + "4".repeat(40))
      )
        r.tree.sha = "0".repeat(40);
      if (variant === "tag" && p.includes("/git/ref/tags/"))
        r.object.sha = "0".repeat(40);
      return r;
    };
    await assert.rejects(publishFinalized(f.raw, f.approval, api, f.record));
    assert.equal(
      f.calls.filter((c) => c.method === "PATCH").length,
      0,
      variant,
    );
  }
});
test("stage refuses missing or moved canonical tag before creating a draft", async () => {
  const f = publisherFixture(),
    raw = Buffer.from(JSON.stringify(f.candidate));
  const api = async (m, p, b) => {
    const r = await f.api(m, p, b);
    if (p.includes("/git/tags/")) r.object.sha = "8".repeat(40);
    return r;
  };
  await assert.rejects(
    stageCandidate(
      raw,
      psha(raw),
      async (n) => Buffer.from(n.endsWith(".tar.gz") ? "archive" : "x"),
      api,
      f.record,
    ),
  );
  assert.equal(
    f.calls.some((c) => c.method !== "GET"),
    false,
  );
});
test("publisher rejects prior timestamp bytes that do not match tree blob identity", async () => {
  const f = publisherFixture();
  const truth = Buffer.from(JSON.stringify({ signed: { version: 99 } }));
  const id = createHash("sha1")
    .update(`blob ${truth.length}\0`)
    .update(truth)
    .digest("hex");
  const api = async (m, p, b) => {
    if (m === "GET" && p.endsWith(`/git/blobs/${id}`))
      return {
        sha: id,
        encoding: "base64",
        content: Buffer.from(
          JSON.stringify({ signed: { version: 0 } }),
        ).toString("base64"),
      };
    const r = await f.api(m, p, b);
    if (m === "GET" && p.includes("/git/trees/"))
      r.tree = [
        { path: "timestamp.json", type: "blob", mode: "100644", sha: id },
      ];
    return r;
  };
  await assert.rejects(
    publishFinalized(f.raw, f.approval, api, f.record),
    /Prior timestamp blob identity changed/,
  );
  assert.equal(
    f.calls.some((c) => c.method === "PATCH"),
    false,
  );
});

import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { resolve, join } from "node:path";
import { spawnSync } from "node:child_process";
import { runRecordedPublisher } from "./publish-ubuntu-release.mjs";
import {
  inspectPublisherJournal,
  openPublisherJournal,
} from "./publisher-journal.mjs";
import { runMaintainerPublisher } from "./prepare-ubuntu-release.mjs";

// Deterministic remote model: every mutation has a unique observable effect.
// A repeated create/upload/ref mutation fails; recovery must use reads instead.
function recoveryRemote(f, existing) {
  const state = existing ?? {
    release: null,
    mutations: [],
    head: "1".repeat(40),
    blobs: {},
    trees: { ["2".repeat(40)]: [] },
    commits: {
      ["1".repeat(40)]: { sha: "1".repeat(40), tree: { sha: "2".repeat(40) } },
    },
  };
  const api = async (m, p, b) => {
    const prefix = "/repos/fased-ai/fased-app";
    if (m !== "GET") state.mutations.push({ method: m, path: p });
    if (p === prefix)
      return {
        id: 1426211857,
        owner: { id: 279582504 },
        full_name: "fased-ai/fased-app",
      };
    if (p.includes("/git/ref/tags/"))
      return {
        ref: "refs/tags/v0.1.0",
        object: { type: "tag", sha: f.candidate.tagObject },
      };
    if (p.includes("/git/tags/"))
      return {
        sha: f.candidate.tagObject,
        tag: "v0.1.0",
        object: { type: "commit", sha: f.candidate.source },
      };
    if (m === "POST" && p === prefix + "/releases") {
      if (state.release) throw Error("DUPLICATE release creation");
      state.release = {
        id: 11,
        draft: true,
        prerelease: false,
        immutable: false,
        tag_name: "v0.1.0",
        assets: [],
      };
      return structuredClone(state.release);
    }
    if (m === "UPLOAD") {
      const name = p.split("?name=")[1];
      if (state.release.assets.some((a) => a.name === name))
        throw Error("DUPLICATE asset upload");
      const a = {
        id: 17 + state.release.assets.length,
        name,
        size: b.length,
        state: "uploaded",
        digest: `sha256:${psha(b)}`,
      };
      state.release.assets.push(a);
      return structuredClone(a);
    }
    if (p === prefix + "/releases/11") {
      if (!state.release)
        throw Object.assign(Error("not found"), { status: 404 });
      if (m === "PATCH") {
        if (!state.release.draft) throw Error("DUPLICATE release publication");
        state.release.draft = false;
        state.release.immutable = true;
      }
      return structuredClone(state.release);
    }
    if (m === "POST" && p.endsWith("/git/blobs")) {
      const bytes = Buffer.from(b.content, "base64"),
        id = createHash("sha1")
          .update(`blob ${bytes.length}\0`)
          .update(bytes)
          .digest("hex");
      state.blobs[id] = { sha: id, encoding: "base64", content: b.content };
      return { sha: id };
    }
    if (m === "POST" && p.endsWith("/git/trees")) {
      if (state.trees["3".repeat(40)]) throw Error("DUPLICATE tree creation");
      state.trees["3".repeat(40)] = b.tree;
      return { sha: "3".repeat(40) };
    }
    if (m === "POST" && p.endsWith("/git/commits")) {
      if (state.commits["4".repeat(40)])
        throw Error("DUPLICATE commit creation");
      state.commits["4".repeat(40)] = {
        sha: "4".repeat(40),
        tree: { sha: b.tree },
        parents: b.parents.map((sha) => ({ sha })),
        message: b.message,
      };
      return { sha: "4".repeat(40) };
    }
    if (p.includes("/git/refs/heads/")) {
      if (state.head !== "1".repeat(40)) throw Error("DUPLICATE ref update");
      state.head = b.sha;
      return { object: { type: "commit", sha: state.head } };
    }
    if (p.includes("/git/ref/heads/"))
      return {
        ref: "refs/heads/release-metadata",
        object: { type: "commit", sha: state.head },
      };
    for (const [kind, table] of [
      ["blobs", state.blobs],
      ["trees", state.trees],
      ["commits", state.commits],
    ]) {
      const marker = `/git/${kind}/`;
      if (m === "GET" && p.includes(marker)) {
        const id = p.split(marker)[1].split("?")[0],
          v = table[id];
        if (!v)
          throw Object.assign(Error("unknown original object"), {
            status: 404,
          });
        return structuredClone(
          kind === "trees" ? { sha: id, truncated: false, tree: v } : v,
        );
      }
    }
    throw Error("Unexpected fixture API route " + m + " " + p);
  };
  return { state, api };
}
function durableFixture(t, action) {
  const f = publisherFixture();
  const root = resolve(".evidence/publisher-recovery/test-state");
  mkdirSync(root, { recursive: true, mode: 0o700 });
  const parent = mkdtempSync(join(root, "case-"));
  chmodSync(parent, 0o700);
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const input = {
    action,
    mode: "start",
    directory: join(parent, "operation"),
    ...(action !== "metadata"
      ? {
          candidate: Buffer.from(JSON.stringify(f.candidate)),
          candidateSHA256: psha(Buffer.from(JSON.stringify(f.candidate))),
        }
      : {}),
    ...(action !== "stage" ? { publication: f.raw, approval: f.approval } : {}),
  };
  const remote = recoveryRemote(f);
  if (action !== "stage")
    remote.state.release = {
      id: 11,
      tag_name: "v0.1.0",
      draft: action === "release",
      prerelease: false,
      immutable: action === "metadata",
      assets: structuredClone(f.assets),
    };
  return {
    ...f,
    parent,
    input,
    remote,
    readArtifact: async (name) =>
      Buffer.from(name.endsWith(".tar.gz") ? "archive" : "x"),
  };
}
test("durable publisher completes and reopens each action as a verified no-op", async (t) => {
  for (const action of ["stage", "release", "metadata"]) {
    const f = durableFixture(t, action),
      deps = { api: f.remote.api, readArtifact: f.readArtifact };
    const first = await runRecordedPublisher(f.input, deps);
    assert.equal(first.noOp, false);
    const mutations = f.remote.state.mutations.length;
    const before = readFileSync(join(f.input.directory, "operation.json"));
    const again = await runRecordedPublisher(
      { ...f.input, mode: "resume" },
      deps,
    );
    assert.equal(again.noOp, true);
    assert.equal(f.remote.state.mutations.length, mutations);
    assert.deepEqual(
      readFileSync(join(f.input.directory, "operation.json")),
      before,
    );
    assert.equal(inspectPublisherJournal(f.input.directory).complete, true);
  }
});
test("durable upload loss is inspected and resumes with original release/asset IDs", async (t) => {
  const f = durableFixture(t, "stage");
  let dropped = false;
  const api = async (m, p, b) => {
    const r = await f.remote.api(m, p, b);
    if (m === "UPLOAD" && !dropped) {
      dropped = true;
      throw Error("response lost after upload");
    }
    return r;
  };
  await assert.rejects(
    runRecordedPublisher(f.input, { api, readArtifact: f.readArtifact }),
  );
  const original = inspectPublisherJournal(f.input.directory);
  assert.equal(original.pending.at(-1).pending, true);
  const result = await runRecordedPublisher(
    { ...f.input, mode: "resume" },
    { api: f.remote.api, readArtifact: f.readArtifact },
  );
  assert.equal(result.releaseId, "11");
  assert.equal(result.assets[0].id, "17");
  assert.equal(
    f.remote.state.mutations.filter((v) => v.method === "UPLOAD").length,
    4,
  );
  assert.equal(inspectPublisherJournal(f.input.directory).id, original.id);
});
test("uncertain draft holds until exact observed ID is explicitly bound; never creates twice", async (t) => {
  const f = durableFixture(t, "stage");
  let dropped = false;
  const api = async (m, p, b) => {
    const r = await f.remote.api(m, p, b);
    if (m === "POST" && p.endsWith("/releases") && !dropped) {
      dropped = true;
      throw Error("draft response lost");
    }
    return r;
  };
  await assert.rejects(
    runRecordedPublisher(f.input, { api, readArtifact: f.readArtifact }),
  );
  const observed = inspectPublisherJournal(f.input.directory).pending[0];
  await assert.rejects(
    runRecordedPublisher(
      { ...f.input, mode: "resume" },
      { api: f.remote.api, readArtifact: f.readArtifact },
    ),
    /Uncertain draft/,
  );
  const result = await runRecordedPublisher(
    {
      ...f.input,
      mode: "resume",
      resolutions: { 0: { requestSha256: observed.requestSha256, id: "11" } },
    },
    { api: f.remote.api, readArtifact: f.readArtifact },
  );
  assert.equal(result.releaseId, "11");
  assert.equal(
    f.remote.state.mutations.filter((v) => v.path.endsWith("/releases")).length,
    1,
  );
});
test("uncertain public transitions recover by readback with no second PATCH", async (t) => {
  for (const action of ["release", "metadata"]) {
    const f = durableFixture(t, action);
    let dropped = false;
    const api = async (m, p, b) => {
      const r = await f.remote.api(m, p, b);
      if (m === "PATCH" && !dropped) {
        dropped = true;
        throw Error("PATCH response lost");
      }
      return r;
    };
    await assert.rejects(runRecordedPublisher(f.input, { api }));
    const result = await runRecordedPublisher(
      { ...f.input, mode: "resume" },
      { api: f.remote.api },
    );
    assert.equal(result.activation, false);
    assert.equal(
      f.remote.state.mutations.filter((v) => v.method === "PATCH").length,
      1,
    );
    assert.equal(inspectPublisherJournal(f.input.directory).complete, true);
  }
});
test("uncertain absent effects and changed inputs/IDs refuse without mutations", async (t) => {
  const f = durableFixture(t, "stage");
  const api = async (m, p, b) => {
    if (m === "UPLOAD") throw Error("delivery unknown");
    return f.remote.api(m, p, b);
  };
  await assert.rejects(
    runRecordedPublisher(f.input, { api, readArtifact: f.readArtifact }),
  );
  const writes = f.remote.state.mutations.length;
  await assert.rejects(
    runRecordedPublisher(
      { ...f.input, mode: "resume" },
      { api: f.remote.api, readArtifact: f.readArtifact },
    ),
    /unresolved\/changed/,
  );
  assert.equal(f.remote.state.mutations.length, writes);
  const changed = { ...f.candidate, source: "8".repeat(40) },
    raw = Buffer.from(JSON.stringify(changed));
  await assert.rejects(
    runRecordedPublisher(
      {
        ...f.input,
        mode: "resume",
        candidate: raw,
        candidateSHA256: psha(raw),
      },
      { api: f.remote.api, readArtifact: f.readArtifact },
    ),
    /authority changed/,
  );
  assert.equal(f.remote.state.mutations.length, writes);
});
test("publisher state is private, exclusive, and corruption-preserving", async (t) => {
  const f = durableFixture(t, "stage"),
    dir = join(f.parent, "locked");
  const j = openPublisherJournal(dir, "a".repeat(64));
  assert.throws(
    () => openPublisherJournal(dir, "a".repeat(64), { resume: true }),
    /already running/,
  );
  j.close();
  const bytes = readFileSync(join(dir, "operation.json"));
  writeFileSync(
    join(dir, "operation.json"),
    Buffer.concat([bytes, Buffer.from("corrupt")]),
  );
  const bad = readFileSync(join(dir, "operation.json"));
  assert.throws(() =>
    openPublisherJournal(dir, "a".repeat(64), { resume: true }),
  );
  assert.deepEqual(readFileSync(join(dir, "operation.json")), bad);
  const publicDir = join(f.parent, "public");
  mkdirSync(publicDir, { mode: 0o755 });
  assert.throws(
    () => openPublisherJournal(publicDir, "a".repeat(64)),
    /Private owned/,
  );
});
test("production maintainer dispatch refuses missing trust before credential access", async () => {
  await assert.rejects(
    runMaintainerPublisher(rawConfig, { tokenFile: "/does-not-exist" }),
    /dispatch disabled/,
  );
});
test("actual SIGKILL restart recovers durable upload and released OS lock with original IDs", async (t) => {
  const f = durableFixture(t, "stage");
  const descriptor = join(f.parent, "child-input.json"),
    remoteFile = join(f.parent, "remote.json");
  writeFileSync(
    descriptor,
    JSON.stringify({
      candidate: f.candidate,
      assets: f.assets,
      options: { ...f.input, candidate: undefined },
      candidateBase64: f.input.candidate.toString("base64"),
      remoteFile,
    }),
    { mode: 0o600 },
  );
  const script = `import {readFileSync,writeFileSync,openSync,fsyncSync,closeSync} from 'node:fs';import {createHash} from 'node:crypto';import {runRecordedPublisher} from ${JSON.stringify(new URL("./publish-ubuntu-release.mjs", import.meta.url).href)};
 const psha=b=>createHash('sha256').update(b).digest('hex');const f=JSON.parse(readFileSync(process.argv[1]));const model=(${recoveryRemote.toString()})(f);const api=async(m,p,b)=>{const r=await model.api(m,p,b);writeFileSync(f.remoteFile,JSON.stringify(model.state),{mode:0o600});const fd=openSync(f.remoteFile,'r');fsyncSync(fd);closeSync(fd);if(m==='UPLOAD')process.kill(process.pid,'SIGKILL');return r;};await runRecordedPublisher({...f.options,candidate:Buffer.from(f.candidateBase64,'base64')},{api,readArtifact:async(n)=>Buffer.from(n.endsWith('.tar.gz')?'archive':'x')});`;
  const result = spawnSync(
    process.execPath,
    ["--input-type=module", "-e", script, descriptor],
    { timeout: 30000, maxBuffer: 65536 },
  );
  assert.equal(result.signal, "SIGKILL", result.stderr.toString());
  assert.equal(existsSync(join(f.input.directory, "writer.lock")), true);
  const remote = recoveryRemote(f, JSON.parse(readFileSync(remoteFile)));
  const recovered = await runRecordedPublisher(
    { ...f.input, mode: "resume" },
    { api: remote.api, readArtifact: f.readArtifact },
  );
  assert.equal(recovered.releaseId, "11");
  assert.equal(recovered.assets[0].id, "17");
  assert.equal(
    remote.state.mutations.filter((v) => v.method === "UPLOAD").length,
    4,
  );
  assert.equal(inspectPublisherJournal(f.input.directory).complete, true);
});
test("uncertain metadata object responses resume by exact identity without recreating objects", async (t) => {
  for (const kind of ["blobs", "trees", "commits"]) {
    const f = durableFixture(t, "metadata");
    let dropped = false;
    const api = async (m, p, b) => {
      const r = await f.remote.api(m, p, b);
      if (m === "POST" && p.endsWith(`/git/${kind}`) && !dropped) {
        dropped = true;
        throw Error("object response lost");
      }
      return r;
    };
    await assert.rejects(runRecordedPublisher(f.input, { api }));
    const last = inspectPublisherJournal(f.input.directory).pending.at(-1),
      count = f.remote.state.mutations.length;
    const resume = { ...f.input, mode: "resume" };
    if (kind !== "blobs") {
      await assert.rejects(
        runRecordedPublisher(resume, { api: f.remote.api }),
        /explicitly inspect/,
      );
      assert.equal(f.remote.state.mutations.length, count);
      resume.resolutions = {
        [last.index]: {
          requestSha256: last.requestSha256,
          id: (kind === "trees" ? "3" : "4").repeat(40),
        },
      };
    }
    const result = await runRecordedPublisher(resume, { api: f.remote.api });
    assert.equal(result.metadataCommit, "4".repeat(40));
    assert.equal(
      f.remote.state.mutations.filter((v) => v.path.endsWith(`/git/${kind}`))
        .length,
      kind === "blobs" ? 6 : 1,
    );
  }
});
test("completed recovery refuses changed remote asset IDs and metadata head", async (t) => {
  for (const action of ["stage", "metadata"]) {
    const f = durableFixture(t, action),
      deps = { api: f.remote.api, readArtifact: f.readArtifact };
    await runRecordedPublisher(f.input, deps);
    const count = f.remote.state.mutations.length;
    if (action === "stage") f.remote.state.release.assets[0].id = 99;
    else f.remote.state.head = "8".repeat(40);
    await assert.rejects(
      runRecordedPublisher({ ...f.input, mode: "resume" }, deps),
    );
    assert.equal(f.remote.state.mutations.length, count);
  }
});
test("journal write failure stops before any remote mutation", async (t) => {
  const f = durableFixture(t, "stage");
  const binding = psha(
    JSON.stringify({
      action: "stage",
      candidate: psha(f.input.candidate),
      publication: null,
      approval: null,
    }),
  );
  const j = openPublisherJournal(f.input.directory, binding);
  j.close();
  chmodSync(f.input.directory, 0o500);
  try {
    await assert.rejects(
      runRecordedPublisher(
        { ...f.input, mode: "resume" },
        { api: f.remote.api, readArtifact: f.readArtifact },
      ),
    );
    assert.equal(f.remote.state.mutations.length, 0);
  } finally {
    chmodSync(f.input.directory, 0o700);
  }
});
test("maintainer CLI inspects locally and blocks publishing before request/credential reads", () => {
  const inspect = spawnSync(
    process.execPath,
    ["scripts/prepare-ubuntu-release.mjs", "publisher-inspect"],
    { timeout: 10000 },
  );
  assert.equal(inspect.status, 0, inspect.stderr.toString());
  assert.deepEqual(
    JSON.parse(inspect.stdout).map((v) => v.action),
    ["stage", "release", "metadata"],
  );
  const run = spawnSync(
    process.execPath,
    ["scripts/prepare-ubuntu-release.mjs", "publisher"],
    { timeout: 10000 },
  );
  assert.equal(run.status, 1);
  assert.match(
    run.stderr.toString(),
    /Production trust missing; publisher dispatch disabled/,
  );
});
import fsBuiltin from "node:fs";
import { syncBuiltinESMExports } from "node:module";
test("publisher persists operation-directory parent before dispatch and sync failure holds", async (t) => {
  const original = fsBuiltin.fsyncSync;
  let target = null,
    saw = false,
    fail = false;
  fsBuiltin.fsyncSync = (fd) => {
    if (target && fsBuiltin.realpathSync(`/proc/self/fd/${fd}`) === target) {
      saw = true;
      if (fail) throw Error("fixture parent sync failed");
    }
    return original(fd);
  };
  syncBuiltinESMExports();
  try {
    const f = durableFixture(t, "stage");
    target = f.parent;
    await runRecordedPublisher(f.input, {
      api: async (m, p, b) => {
        if (m !== "GET") assert.equal(saw, true);
        return f.remote.api(m, p, b);
      },
      readArtifact: f.readArtifact,
    });
    assert.equal(saw, true);
    const held = durableFixture(t, "stage");
    target = held.parent;
    saw = false;
    fail = true;
    await assert.rejects(
      runRecordedPublisher(held.input, {
        api: held.remote.api,
        readArtifact: held.readArtifact,
      }),
      /parent sync failed/,
    );
    assert.equal(held.remote.state.mutations.length, 0);
    // Even the interrupted, now-existing directory must sync its parent again.
    fail = false;
    saw = false;
    await runRecordedPublisher(held.input, {
      api: async (m, p, b) => {
        if (m !== "GET") assert.equal(saw, true);
        return held.remote.api(m, p, b);
      },
      readArtifact: held.readArtifact,
    });
    assert.equal(saw, true);
  } finally {
    fsBuiltin.fsyncSync = original;
    syncBuiltinESMExports();
  }
});
import { publisherOperationDirectory } from "./prepare-ubuntu-release.mjs";
test("same-version metadata renewals retain distinct journals and identical input reuses its journal", () => {
  const first = publisherOperationDirectory(rawConfig, {
    action: "metadata",
    approval: { sha256: "a".repeat(64) },
  });
  const repeat = publisherOperationDirectory(rawConfig, {
    action: "metadata",
    approval: { sha256: "a".repeat(64) },
  });
  const renewed = publisherOperationDirectory(rawConfig, {
    action: "metadata",
    approval: { sha256: "b".repeat(64) },
  });
  assert.equal(first, repeat);
  assert.notEqual(first, renewed);
  assert.ok(first.endsWith("-" + "a".repeat(64)));
  assert.ok(renewed.endsWith("-" + "b".repeat(64)));
  assert.throws(() =>
    publisherOperationDirectory(rawConfig, {
      action: "metadata",
      approval: { sha256: "../reset" },
    }),
  );
});
