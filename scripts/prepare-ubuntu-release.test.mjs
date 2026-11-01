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
